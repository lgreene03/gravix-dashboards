package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/billing"
	"github.com/lgreene/gravix-dashboards/pkg/ratelimit"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// handleStripeWebhook processes Stripe webhook events.
// No JWT auth — signature is verified by the billing service.
func (gw *gateway) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	if gw.billing == nil {
		writeError(w, http.StatusServiceUnavailable, "billing not configured")
		return
	}

	// Read body (Stripe recommends max 65536 bytes)
	payload, err := io.ReadAll(io.LimitReader(r.Body, 65536))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	sigHeader := r.Header.Get("Stripe-Signature")
	event, err := gw.billing.ParseWebhook(payload, sigHeader)
	if err != nil {
		slog.Error("webhook signature verification failed", "error", err)
		gatewayBillingWebhookTotal.WithLabelValues("unknown", "error").Inc()
		writeError(w, http.StatusBadRequest, "invalid webhook signature")
		return
	}

	ctx := r.Context()
	slog.Info("stripe webhook received", "type", event.Type, "customer_id", event.CustomerID, "subscription_id", event.SubscriptionID, "status", event.Status, "plan", event.PlanName)

	// Find tenant by Stripe customer ID
	tenants, err := gw.db.Tenants().List(ctx)
	if err != nil {
		slog.Error("webhook failed to list tenants", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var tenant *tenantdb.Tenant
	for _, t := range tenants {
		if t.StripeCustomerID == event.CustomerID {
			tenant = t
			break
		}
	}

	if tenant == nil {
		slog.Warn("webhook no tenant found for stripe customer", "customer_id", event.CustomerID)
		// Return 200 to acknowledge — Stripe retries on non-2xx
		w.WriteHeader(http.StatusOK)
		return
	}

	switch event.Type {
	case "customer.subscription.created", "customer.subscription.updated":
		// Update plan based on price ID
		oldPlan := tenant.Plan
		if event.PlanName != "" && event.PlanName != "unknown" {
			if err := gw.db.Tenants().UpdatePlan(ctx, tenant.ID, event.PlanName); err != nil {
				slog.Error("webhook failed to update plan", "tenant_id", tenant.ID, "error", err)
			} else {
				slog.Info("webhook updated tenant plan", "tenant_id", tenant.ID, "plan", event.PlanName)
				gw.auditDirect(tenant.ID, "", "tenant.plan_change", "tenant", tenant.ID,
					fmt.Sprintf(`{"old":%q,"new":%q}`, oldPlan, event.PlanName), r.RemoteAddr)
			}
		}
		// Update subscription ID
		if err := gw.db.Tenants().UpdateStripe(ctx, tenant.ID, event.CustomerID, event.SubscriptionID); err != nil {
			slog.Error("webhook failed to update stripe IDs", "tenant_id", tenant.ID, "error", err)
		}
		// Reactivate if previously suspended
		if event.Status == "active" && tenant.Status != "active" {
			if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "active"); err != nil {
				slog.Error("webhook failed to reactivate tenant", "tenant_id", tenant.ID, "error", err)
			}
		}

	case "customer.subscription.deleted":
		// Subscription canceled — mark tenant as churned
		if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "churned"); err != nil {
			slog.Error("webhook failed to mark tenant as churned", "tenant_id", tenant.ID, "error", err)
		} else {
			slog.Info("webhook marked tenant as churned", "tenant_id", tenant.ID)
			gw.auditDirect(tenant.ID, "", "tenant.churned", "tenant", tenant.ID, "", r.RemoteAddr)
		}

	case "invoice.payment_failed":
		// Payment failed — suspend tenant
		if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "suspended"); err != nil {
			slog.Error("webhook failed to suspend tenant", "tenant_id", tenant.ID, "error", err)
		} else {
			slog.Info("webhook suspended tenant due to payment failure", "tenant_id", tenant.ID)
			gw.auditDirect(tenant.ID, "", "tenant.suspended", "tenant", tenant.ID, "payment_failed", r.RemoteAddr)
		}
	}

	// Always return 200 to acknowledge receipt
	gatewayBillingWebhookTotal.WithLabelValues(event.Type, "success").Inc()
	w.WriteHeader(http.StatusOK)
}

// handleBillingPortal returns a Stripe Customer Portal URL for self-service billing.
func (gw *gateway) handleBillingPortal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	if gw.billing == nil {
		writeError(w, http.StatusServiceUnavailable, "billing not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}

	if tenant.StripeCustomerID == "" {
		writeError(w, http.StatusBadRequest, "no billing account found — contact support")
		return
	}

	var req struct {
		ReturnURL string `json:"return_url"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.ReturnURL == "" {
		req.ReturnURL = "/"
	}

	url, err := gw.billing.CreatePortalSession(r.Context(), tenant.StripeCustomerID, req.ReturnURL)
	if err != nil {
		slog.Error("failed to create portal session", "tenant_id", tenant.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create billing portal session")
		return
	}

	gatewayBillingPortalRequestsTotal.Inc()
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (gw *gateway) handleBillingCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	if gw.billing == nil {
		writeError(w, http.StatusServiceUnavailable, "billing not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}

	tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}

	if tenant.StripeCustomerID == "" {
		writeError(w, http.StatusBadRequest, "no billing account found")
		return
	}

	var req struct {
		PriceID       string `json:"price_id"`
		BillingPeriod string `json:"billing_period"` // "monthly" (default) or "annual"
		SuccessURL    string `json:"success_url"`
		CancelURL     string `json:"cancel_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.PriceID == "" {
		writeError(w, http.StatusBadRequest, "price_id required")
		return
	}
	if req.BillingPeriod == "" {
		req.BillingPeriod = "monthly"
	}
	if req.BillingPeriod != "monthly" && req.BillingPeriod != "annual" {
		writeError(w, http.StatusBadRequest, "billing_period must be 'monthly' or 'annual'")
		return
	}
	if req.SuccessURL == "" {
		req.SuccessURL = "/"
	}
	if req.CancelURL == "" {
		req.CancelURL = "/"
	}

	svc, ok := gw.billing.(*billing.StripeService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "checkout not available")
		return
	}

	// Resolve the correct price ID based on billing period
	checkoutPriceID := req.PriceID
	if req.BillingPeriod == "annual" {
		if annualID := svc.AnnualPriceIDFor(req.PriceID); annualID != "" {
			checkoutPriceID = annualID
		} else {
			writeError(w, http.StatusBadRequest, "annual billing not available for this plan")
			return
		}
	}

	// Determine trial days for the target plan
	planName := gw.billing.PlanForPriceID(checkoutPriceID)
	trialDays := billing.DefaultTrialDays(planName)

	url, err := svc.CreateCheckoutSession(r.Context(), billing.CheckoutParams{
		CustomerID: tenant.StripeCustomerID,
		PriceID:    checkoutPriceID,
		SuccessURL: req.SuccessURL,
		CancelURL:  req.CancelURL,
		TrialDays:  trialDays,
	})
	if err != nil {
		slog.Error("failed to create checkout session", "tenant_id", tenant.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create checkout session")
		return
	}

	gatewayBillingCheckoutTotal.WithLabelValues(planName, req.BillingPeriod).Inc()
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleBillingUsage returns current event usage for the tenant, including plan info and daily breakdown.
func (gw *gateway) handleBillingUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	// Look up tenant for plan info
	tenant, err := gw.db.Tenants().GetByID(ctx, claims.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up tenant")
		return
	}

	// Get today's count
	today := time.Now().UTC().Format("2006-01-02")
	todayCount, _ := gw.db.EventCounters().GetCount(ctx, claims.TenantID, today)

	// Sum the current month's counts
	year, month, _ := time.Now().UTC().Date()
	var monthTotal int64
	for day := 1; day <= 31; day++ {
		d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		if d.Month() != month {
			break
		}
		dayStr := d.Format("2006-01-02")
		count, _ := gw.db.EventCounters().GetCount(ctx, claims.TenantID, dayStr)
		monthTotal += count
	}

	// Daily counts for last 30 days
	type dailyCount struct {
		Day   string `json:"day"`
		Count int64  `json:"count"`
	}
	dailyCounts := make([]dailyCount, 0, 30)
	for i := 29; i >= 0; i-- {
		d := time.Now().UTC().AddDate(0, 0, -i)
		dayStr := d.Format("2006-01-02")
		count, _ := gw.db.EventCounters().GetCount(ctx, claims.TenantID, dayStr)
		dailyCounts = append(dailyCounts, dailyCount{Day: dayStr, Count: count})
	}

	// Plan limits
	var eventLimit int64
	switch tenant.Plan {
	case "pro":
		eventLimit = 50_000_000
	case "starter":
		eventLimit = 10_000_000
	default:
		eventLimit = 1_000_000
	}

	rateLimitRPS, _ := ratelimit.PlanRateLimit(tenant.Plan)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":      claims.TenantID,
		"today":          todayCount,
		"month_total":    monthTotal,
		"period":         fmt.Sprintf("%d-%02d", year, month),
		"plan":           tenant.Plan,
		"event_limit":    eventLimit,
		"rate_limit_rps": rateLimitRPS,
		"daily_counts":   dailyCounts,
	})
}

// handleBillingInvoices returns Stripe invoice list for the tenant.
func (gw *gateway) handleBillingInvoices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	tenant, err := gw.db.Tenants().GetByID(ctx, claims.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up tenant")
		return
	}

	if tenant.StripeCustomerID == "" || gw.billing == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"invoices": []interface{}{}})
		return
	}

	invoices, err := gw.billing.ListInvoices(ctx, tenant.StripeCustomerID)
	if err != nil {
		slog.Error("failed to list invoices", "tenant_id", claims.TenantID, "error", err)
		writeJSON(w, http.StatusOK, map[string]interface{}{"invoices": []interface{}{}})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"invoices": invoices})
}

// handleBillingUsageHistory returns monthly usage summaries for the tenant.
func (gw *gateway) handleBillingUsageHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	history, err := gw.db.MonthlyUsage().GetByTenant(ctx, claims.TenantID, 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get usage history")
		return
	}

	type monthEntry struct {
		Month string `json:"month"`
		Count int64  `json:"count"`
		Plan  string `json:"plan"`
	}
	entries := make([]monthEntry, 0, len(history))
	for _, h := range history {
		entries = append(entries, monthEntry{Month: h.Month, Count: h.Count, Plan: h.Plan})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"history": entries})
}

// usageMeteringLoop reports tenant event usage to Stripe every hour.
func (gw *gateway) usageMeteringLoop(ctx context.Context) {
	// Wait 5 minutes after startup before first run
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		gw.reportUsageToStripe(ctx)

		// Clean up expired revoked tokens and SSO states
		_ = gw.db.RevokedTokens().Cleanup(ctx)
		_ = gw.db.SSOStates().Cleanup(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
// reportUsageToStripe reads event counters, calculates overage, and reports to Stripe.
func (gw *gateway) reportUsageToStripe(ctx context.Context) {
	if gw.billing == nil {
		return
	}

	tenants, err := gw.db.Tenants().List(ctx)
	if err != nil {
		slog.Error("usage metering failed to list tenants", "error", err)
		return
	}

	now := time.Now().UTC()
	year, month, _ := now.Date()

	for _, tenant := range tenants {
		if tenant.Status != "active" || tenant.StripeSubscriptionID == "" {
			continue
		}

		// Calculate month-to-date total for overage
		var monthTotal int64
		for day := 1; day <= now.Day(); day++ {
			d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
			if d.Month() != month {
				break
			}
			dayStr := d.Format("2006-01-02")
			count, err := gw.db.EventCounters().GetCount(ctx, tenant.ID, dayStr)
			if err != nil {
				continue
			}
			monthTotal += count
		}

		if monthTotal == 0 {
			continue
		}

		// Report raw usage to Stripe (for metered billing)
		if err := gw.billing.ReportUsage(ctx, tenant.StripeSubscriptionID, monthTotal, now); err != nil {
			slog.Error("usage metering failed to report usage", "tenant_id", tenant.ID, "error", err)
		} else {
			slog.Info("usage metering reported events", "tenant_id", tenant.ID, "count", monthTotal)
		}

		// Log overage for paid plans with overage allowed
		if tenant.OverageAllowed {
			result := billing.CalculateOverage(tenant.Plan, monthTotal)
			if result.Overage > 0 {
				slog.Info("tenant overage detected",
					"tenant_id", tenant.ID,
					"plan", tenant.Plan,
					"limit", result.EventLimit,
					"usage", monthTotal,
					"overage", result.Overage,
					"overage_cost_cents", billing.OverageCostCents(result.Overage),
				)
			}
		}
	}

	// Monthly snapshot: on the 1st of each month, snapshot previous month's total
	if now.Day() == 1 {
		prevMonth := now.AddDate(0, -1, 0)
		prevYear, prevMon, _ := prevMonth.Date()
		monthStr := fmt.Sprintf("%d-%02d", prevYear, prevMon)

		for _, tenant := range tenants {
			if tenant.Status != "active" {
				continue
			}
			var total int64
			for day := 1; day <= 31; day++ {
				d := time.Date(prevYear, prevMon, day, 0, 0, 0, 0, time.UTC)
				if d.Month() != prevMon {
					break
				}
				dayStr := d.Format("2006-01-02")
				c, _ := gw.db.EventCounters().GetCount(ctx, tenant.ID, dayStr)
				total += c
			}
			if total > 0 {
				if err := gw.db.MonthlyUsage().Snapshot(ctx, tenant.ID, monthStr, total, tenant.Plan); err != nil {
					slog.Error("monthly snapshot failed", "tenant_id", tenant.ID, "month", monthStr, "error", err)
				} else {
					slog.Info("monthly usage snapshot", "tenant_id", tenant.ID, "month", monthStr, "count", total)
				}
			}
		}
	}
}
