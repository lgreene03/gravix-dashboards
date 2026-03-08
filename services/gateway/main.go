// Gateway service provides tenant management, user login, API key management,
// and Stripe billing for multi-tenant Gravix deployments.
//
// Endpoints:
//
//	POST /api/gateway/login            — Authenticate user, return JWT
//	POST /api/gateway/register         — Create tenant + user + Stripe customer
//	GET  /api/gateway/me               — Get current user info (JWT required)
//	POST /api/gateway/api-keys         — Create API key (JWT required, admin only)
//	GET  /api/gateway/api-keys         — List API keys (JWT required)
//	DELETE /api/gateway/api-keys/:id   — Revoke API key (JWT required, admin only)
//	POST /api/gateway/webhooks/stripe  — Handle Stripe webhooks (no auth, signature verified)
//	POST /api/gateway/billing/portal   — Get Stripe Customer Portal URL (JWT required)
//	GET  /api/gateway/billing/usage    — Get current usage stats (JWT required)
//	GET  /live                         — Health check
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/billing"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"golang.org/x/crypto/bcrypt"
)

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]interface{}{"error": msg, "code": code})
}

func main() {
	port := flag.Int("port", 8091, "HTTP port")
	tenantDBPath := flag.String("tenant-db", "", "Path to tenant SQLite database")
	jwtSecret := flag.String("jwt-secret", "", "JWT signing secret")
	flag.Parse()

	if *tenantDBPath == "" {
		*tenantDBPath = os.Getenv("TENANT_DB_PATH")
	}
	if *jwtSecret == "" {
		*jwtSecret = os.Getenv("JWT_SECRET")
	}

	if *tenantDBPath == "" {
		log.Fatal("FATAL: TENANT_DB_PATH is required")
	}
	if *jwtSecret == "" {
		log.Fatal("FATAL: JWT_SECRET is required")
	}

	db, err := tenantdb.Open(*tenantDBPath)
	if err != nil {
		log.Fatalf("Failed to open tenant database: %v", err)
	}
	defer db.Close()

	tokens := auth.NewTokenService(*jwtSecret, 24*time.Hour)

	gw := &gateway{db: db, tokens: tokens}

	// Initialize Stripe billing if configured
	stripeKey := os.Getenv("STRIPE_SECRET_KEY")
	if stripeKey != "" {
		plans := billing.DefaultPlans(
			os.Getenv("STRIPE_PRICE_FREE"),
			os.Getenv("STRIPE_PRICE_STARTER"),
			os.Getenv("STRIPE_PRICE_PRO"),
		)
		gw.billing = billing.NewStripeService(
			stripeKey,
			os.Getenv("STRIPE_WEBHOOK_SECRET"),
			plans,
		)
		log.Println("Stripe billing enabled")

		// Start background usage metering (hourly)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go gw.usageMeteringLoop(ctx)
	} else {
		log.Println("Stripe billing disabled (STRIPE_SECRET_KEY not set)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/gateway/login", gw.handleLogin)
	mux.HandleFunc("/api/gateway/register", gw.handleRegister)
	mux.HandleFunc("/api/gateway/me", gw.requireAuth(gw.handleMe))
	mux.HandleFunc("/api/gateway/api-keys", gw.requireAuth(gw.handleAPIKeys))
	mux.HandleFunc("/api/gateway/webhooks/stripe", gw.handleStripeWebhook)
	mux.HandleFunc("/api/gateway/billing/portal", gw.requireAuth(gw.handleBillingPortal))
	mux.HandleFunc("/api/gateway/billing/usage", gw.requireAuth(gw.handleBillingUsage))
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("up"))
	})

	// CORS middleware for dashboard access
	handler := corsMiddleware(mux)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Gateway service starting on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type gateway struct {
	db      tenantdb.DB
	tokens  *auth.TokenService
	billing billing.Service // nil if Stripe is not configured
}

// handleLogin authenticates a user with email/password and returns a JWT.
func (gw *gateway) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := gw.db.Users().GetByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Get tenant info for the token
	tenant, err := gw.db.Tenants().GetByID(r.Context(), user.TenantID)
	if err != nil || tenant.Status != "active" {
		writeError(w, http.StatusForbidden, "tenant is not active")
		return
	}

	token, err := gw.tokens.Generate(user.TenantID, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":     token,
		"tenant_id": user.TenantID,
		"user_id":   user.ID,
		"email":     user.Email,
		"role":      user.Role,
		"plan":      tenant.Plan,
	})
}

// handleRegister creates a new tenant, admin user, initial API key, and
// optionally a Stripe customer with a Free subscription.
func (gw *gateway) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Name == "" || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "name, email, and password are required")
		return
	}

	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	ctx := r.Context()

	// Check if email already exists
	if _, err := gw.db.Tenants().GetByEmail(ctx, req.Email); err == nil {
		writeError(w, http.StatusConflict, "email already registered")
		return
	}

	// Create tenant
	tenant := &tenantdb.Tenant{
		Name:   req.Name,
		Email:  req.Email,
		Plan:   "free",
		Status: "active",
	}
	if err := gw.db.Tenants().Create(ctx, tenant); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create tenant")
		return
	}

	// Create Stripe customer + subscription if billing is enabled
	if gw.billing != nil {
		customerID, err := gw.billing.CreateCustomer(ctx, req.Name, req.Email, tenant.ID)
		if err != nil {
			log.Printf("Warning: failed to create Stripe customer for tenant %s: %v", tenant.ID, err)
		} else {
			subID, err := gw.billing.CreateSubscription(ctx, customerID, gw.billing.FreePriceID())
			if err != nil {
				log.Printf("Warning: failed to create Stripe subscription for tenant %s: %v", tenant.ID, err)
			}
			if err := gw.db.Tenants().UpdateStripe(ctx, tenant.ID, customerID, subID); err != nil {
				log.Printf("Warning: failed to store Stripe IDs for tenant %s: %v", tenant.ID, err)
			}
		}
	}

	// Create admin user
	user := &tenantdb.User{
		TenantID:     tenant.ID,
		Email:        req.Email,
		PasswordHash: req.Password, // Will be hashed by the repo
		Role:         "admin",
	}
	if err := gw.db.Users().Create(ctx, user); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	// Create initial API key
	plainKey, apiKey, err := gw.db.APIKeys().Create(ctx, tenant.ID, "default")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create API key")
		return
	}

	// Generate JWT for immediate login
	token, err := gw.tokens.Generate(tenant.ID, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"token":      token,
		"tenant_id":  tenant.ID,
		"user_id":    user.ID,
		"email":      user.Email,
		"role":       user.Role,
		"plan":       tenant.Plan,
		"api_key":    plainKey,
		"key_prefix": apiKey.KeyPrefix,
	})
}

// requireAuth wraps a handler to require a valid JWT in the Authorization header.
func (gw *gateway) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := gw.tokens.Validate(tokenStr)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		// Store claims in request context
		r = r.WithContext(auth.WithClaims(r.Context(), claims))
		next(w, r)
	}
}

// handleMe returns current user and tenant info.
func (gw *gateway) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":     claims.UserID,
		"email":       claims.Email,
		"role":        claims.Role,
		"tenant_id":   claims.TenantID,
		"tenant_name": tenant.Name,
		"plan":        tenant.Plan,
	})
}

// handleAPIKeys handles CRUD for API keys.
func (gw *gateway) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		keys, err := gw.db.APIKeys().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list keys")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"keys": keys})

	case http.MethodPost:
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}

		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}

		plain, key, err := gw.db.APIKeys().Create(r.Context(), claims.TenantID, req.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create key")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"key":        plain,
			"key_id":     key.ID,
			"key_prefix": key.KeyPrefix,
			"name":       key.Name,
		})

	case http.MethodDelete:
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}

		// Extract key ID from path: /api/gateway/api-keys/<id>
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 5 || parts[4] == "" {
			writeError(w, http.StatusBadRequest, "key ID required in path")
			return
		}
		keyID := parts[4]

		if err := gw.db.APIKeys().Revoke(r.Context(), keyID); err != nil {
			writeError(w, http.StatusNotFound, "key not found or already revoked")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

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
		log.Printf("Webhook signature verification failed: %v", err)
		writeError(w, http.StatusBadRequest, "invalid webhook signature")
		return
	}

	ctx := r.Context()
	log.Printf("Stripe webhook: type=%s customer=%s subscription=%s status=%s plan=%s",
		event.Type, event.CustomerID, event.SubscriptionID, event.Status, event.PlanName)

	// Find tenant by Stripe customer ID
	tenants, err := gw.db.Tenants().List(ctx)
	if err != nil {
		log.Printf("Webhook error: failed to list tenants: %v", err)
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
		log.Printf("Webhook: no tenant found for Stripe customer %s", event.CustomerID)
		// Return 200 to acknowledge — Stripe retries on non-2xx
		w.WriteHeader(http.StatusOK)
		return
	}

	switch event.Type {
	case "customer.subscription.created", "customer.subscription.updated":
		// Update plan based on price ID
		if event.PlanName != "" && event.PlanName != "unknown" {
			if err := gw.db.Tenants().UpdatePlan(ctx, tenant.ID, event.PlanName); err != nil {
				log.Printf("Webhook error: failed to update plan for tenant %s: %v", tenant.ID, err)
			} else {
				log.Printf("Webhook: updated tenant %s plan to %s", tenant.ID, event.PlanName)
			}
		}
		// Update subscription ID
		if err := gw.db.Tenants().UpdateStripe(ctx, tenant.ID, event.CustomerID, event.SubscriptionID); err != nil {
			log.Printf("Webhook error: failed to update stripe IDs for tenant %s: %v", tenant.ID, err)
		}
		// Reactivate if previously suspended
		if event.Status == "active" && tenant.Status != "active" {
			if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "active"); err != nil {
				log.Printf("Webhook error: failed to reactivate tenant %s: %v", tenant.ID, err)
			}
		}

	case "customer.subscription.deleted":
		// Subscription canceled — mark tenant as churned
		if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "churned"); err != nil {
			log.Printf("Webhook error: failed to mark tenant %s as churned: %v", tenant.ID, err)
		} else {
			log.Printf("Webhook: marked tenant %s as churned", tenant.ID)
		}

	case "invoice.payment_failed":
		// Payment failed — suspend tenant
		if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "suspended"); err != nil {
			log.Printf("Webhook error: failed to suspend tenant %s: %v", tenant.ID, err)
		} else {
			log.Printf("Webhook: suspended tenant %s due to payment failure", tenant.ID)
		}
	}

	// Always return 200 to acknowledge receipt
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
		log.Printf("Failed to create portal session for tenant %s: %v", tenant.ID, err)
		writeError(w, http.StatusInternalServerError, "failed to create billing portal session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleBillingUsage returns current event usage for the tenant.
func (gw *gateway) handleBillingUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	// Get today's count and this month's total
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":   claims.TenantID,
		"today":       todayCount,
		"month_total": monthTotal,
		"period":      fmt.Sprintf("%d-%02d", year, month),
	})
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

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reportUsageToStripe reads event counters and reports to Stripe for all active tenants.
func (gw *gateway) reportUsageToStripe(ctx context.Context) {
	if gw.billing == nil {
		return
	}

	tenants, err := gw.db.Tenants().List(ctx)
	if err != nil {
		log.Printf("Usage metering: failed to list tenants: %v", err)
		return
	}

	today := time.Now().UTC().Format("2006-01-02")

	for _, tenant := range tenants {
		if tenant.Status != "active" || tenant.StripeSubscriptionID == "" {
			continue
		}

		count, err := gw.db.EventCounters().GetCount(ctx, tenant.ID, today)
		if err != nil {
			log.Printf("Usage metering: failed to get count for tenant %s: %v", tenant.ID, err)
			continue
		}

		if count == 0 {
			continue
		}

		if err := gw.billing.ReportUsage(ctx, tenant.StripeSubscriptionID, count, time.Now().UTC()); err != nil {
			log.Printf("Usage metering: failed to report usage for tenant %s: %v", tenant.ID, err)
		} else {
			log.Printf("Usage metering: reported %d events for tenant %s", count, tenant.ID)
		}
	}
}
