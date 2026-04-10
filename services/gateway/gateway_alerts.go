package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/notify"
	"github.com/lgreene/gravix-dashboards/pkg/pagination"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/montanaflynn/stats"
)

// --- Notification Channel handlers ---

func (gw *gateway) handleChannels(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		channels, err := gw.db.NotificationChannels().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list channels")
			return
		}
		if channels == nil {
			channels = []*tenantdb.NotificationChannel{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"channels": channels})

	case http.MethodPost:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}

		var req struct {
			Name   string          `json:"name"`
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Name == "" || req.Type == "" {
			writeError(w, http.StatusBadRequest, "name and type are required")
			return
		}
		if req.Type != "slack" && req.Type != "webhook" {
			writeError(w, http.StatusBadRequest, "type must be slack or webhook")
			return
		}

		configStr := "{}"
		if req.Config != nil {
			configStr = string(req.Config)
		}
		// Validate config
		if _, err := notify.ParseChannelConfig(configStr); err != nil {
			writeError(w, http.StatusBadRequest, "invalid config: "+err.Error())
			return
		}

		ch := &tenantdb.NotificationChannel{
			TenantID: claims.TenantID,
			Name:     req.Name,
			Type:     req.Type,
			Config:   configStr,
		}
		if err := gw.db.NotificationChannels().Create(r.Context(), ch); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create channel")
			return
		}
		gw.audit(r, "channel.create", "channel", ch.ID, req.Name)
		writeJSON(w, http.StatusCreated, ch)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (gw *gateway) handleChannelByID(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	// Extract ID from path: /api/gateway/channels/<id>[/test]
	parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
	if len(parts) < 5 || parts[4] == "" {
		writeError(w, http.StatusBadRequest, "channel ID required")
		return
	}
	channelID := parts[4]

	// Check for /test suffix
	isTest := len(parts) >= 6 && parts[5] == "test"

	if isTest && r.Method == http.MethodPost {
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}
		ch, err := gw.db.NotificationChannels().GetByID(r.Context(), channelID)
		if err != nil {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		if ch.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		cfg, err := notify.ParseChannelConfig(ch.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid channel config: "+err.Error())
			return
		}
		if err := gw.notifier.SendTest(r.Context(), ch.Type, cfg); err != nil {
			writeError(w, http.StatusBadGateway, "test failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}
		ch, err := gw.db.NotificationChannels().GetByID(r.Context(), channelID)
		if err != nil {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		if ch.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		if err := gw.db.NotificationChannels().Delete(r.Context(), channelID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete channel")
			return
		}
		gw.audit(r, "channel.delete", "channel", channelID, ch.Name)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// --- Alert Rule handlers ---

var validMetrics = map[string]bool{
	"error_rate":  true,
	"p50_latency": true,
	"p95_latency": true,
	"p99_latency": true,
	"throughput":  true,
}

func (gw *gateway) handleAlertRules(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		rules, err := gw.db.AlertRules().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list rules")
			return
		}
		if rules == nil {
			rules = []*tenantdb.AlertRule{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"rules": rules})

	case http.MethodPost:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}

		var req struct {
			Name            string  `json:"name"`
			Metric          string  `json:"metric"`
			Operator        string  `json:"operator"`
			Threshold       float64 `json:"threshold"`
			WindowMinutes   int     `json:"window_minutes"`
			Service         string  `json:"service"`
			PathTemplate    string  `json:"path_template"`
			ChannelID       string  `json:"channel_id"`
			CooldownMinutes int     `json:"cooldown_minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		if errMsg := validateAlertRule(req.Name, req.Metric, req.Operator, req.Threshold,
			req.WindowMinutes, req.CooldownMinutes, req.ChannelID); errMsg != "" {
			writeError(w, http.StatusBadRequest, errMsg)
			return
		}

		// Verify channel belongs to this tenant
		ch, err := gw.db.NotificationChannels().GetByID(r.Context(), req.ChannelID)
		if err != nil || ch.TenantID != claims.TenantID {
			writeError(w, http.StatusBadRequest, "channel not found")
			return
		}

		rule := &tenantdb.AlertRule{
			TenantID:        claims.TenantID,
			Name:            req.Name,
			Metric:          req.Metric,
			Operator:        req.Operator,
			Threshold:       req.Threshold,
			WindowMinutes:   req.WindowMinutes,
			Service:         req.Service,
			PathTemplate:    req.PathTemplate,
			ChannelID:       req.ChannelID,
			CooldownMinutes: req.CooldownMinutes,
		}
		if err := gw.db.AlertRules().Create(r.Context(), rule); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create rule")
			return
		}
		gw.audit(r, "alert_rule.create", "alert_rule", rule.ID, req.Name)
		writeJSON(w, http.StatusCreated, rule)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (gw *gateway) handleAlertRuleByID(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
	if len(parts) < 5 || parts[4] == "" {
		writeError(w, http.StatusBadRequest, "rule ID required")
		return
	}
	ruleID := parts[4]

	switch r.Method {
	case http.MethodPut:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}

		existing, err := gw.db.AlertRules().GetByID(r.Context(), ruleID)
		if err != nil {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		if existing.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}

		var req struct {
			Name            *string  `json:"name"`
			Metric          *string  `json:"metric"`
			Operator        *string  `json:"operator"`
			Threshold       *float64 `json:"threshold"`
			WindowMinutes   *int     `json:"window_minutes"`
			Service         *string  `json:"service"`
			PathTemplate    *string  `json:"path_template"`
			ChannelID       *string  `json:"channel_id"`
			CooldownMinutes *int     `json:"cooldown_minutes"`
			Status          *string  `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		// Apply only the provided fields to the existing rule
		if req.Name != nil {
			existing.Name = *req.Name
		}
		if req.Metric != nil {
			existing.Metric = *req.Metric
		}
		if req.Operator != nil {
			existing.Operator = *req.Operator
		}
		if req.Threshold != nil {
			existing.Threshold = *req.Threshold
		}
		if req.WindowMinutes != nil {
			existing.WindowMinutes = *req.WindowMinutes
		}
		if req.Service != nil {
			existing.Service = *req.Service
		}
		if req.PathTemplate != nil {
			existing.PathTemplate = *req.PathTemplate
		}
		if req.ChannelID != nil {
			existing.ChannelID = *req.ChannelID
		}
		if req.CooldownMinutes != nil {
			existing.CooldownMinutes = *req.CooldownMinutes
		}
		if req.Status != nil {
			if *req.Status != "active" && *req.Status != "paused" {
				writeError(w, http.StatusBadRequest, "status must be active or paused")
				return
			}
			existing.Status = *req.Status
		}

		// Validate the merged rule
		if errMsg := validateAlertRule(existing.Name, existing.Metric, existing.Operator, existing.Threshold,
			existing.WindowMinutes, existing.CooldownMinutes, existing.ChannelID); errMsg != "" {
			writeError(w, http.StatusBadRequest, errMsg)
			return
		}

		// Verify channel belongs to tenant
		ch, err := gw.db.NotificationChannels().GetByID(r.Context(), existing.ChannelID)
		if err != nil || ch.TenantID != claims.TenantID {
			writeError(w, http.StatusBadRequest, "channel not found")
			return
		}

		if err := gw.db.AlertRules().Update(r.Context(), existing); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update rule")
			return
		}
		gw.audit(r, "alert_rule.update", "alert_rule", ruleID,
			fmt.Sprintf(`{"name":%q,"status":%q}`, existing.Name, existing.Status))
		writeJSON(w, http.StatusOK, existing)

	case http.MethodDelete:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}

		existing, err := gw.db.AlertRules().GetByID(r.Context(), ruleID)
		if err != nil {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		if existing.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}

		if err := gw.db.AlertRules().Delete(r.Context(), ruleID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete rule")
			return
		}
		gw.audit(r, "alert_rule.delete", "alert_rule", ruleID, existing.Name)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func validateAlertRule(name, metric, operator string, threshold float64, windowMin, cooldownMin int, channelID string) string {
	if name == "" {
		return "name is required"
	}
	if !validMetrics[metric] {
		return "metric must be one of: error_rate, p50_latency, p95_latency, p99_latency, throughput"
	}
	if operator != "gt" && operator != "lt" && operator != "anomaly" {
		return "operator must be gt, lt, or anomaly"
	}
	if operator == "anomaly" {
		if threshold <= 0 {
			return "threshold (stddev multiplier) must be > 0 for anomaly rules"
		}
		if windowMin < 1 || windowMin > 30 {
			return "window_minutes (lookback days) must be 1-30 for anomaly rules"
		}
	} else {
		if threshold < 0 {
			return "threshold must be >= 0"
		}
		if windowMin < 1 || windowMin > 60 {
			return "window_minutes must be 1-60"
		}
	}
	if cooldownMin < 1 || cooldownMin > 1440 {
		return "cooldown_minutes must be 1-1440"
	}
	if channelID == "" {
		return "channel_id is required"
	}
	return ""
}

// --- Alert History handler ---

func (gw *gateway) handleAlertHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())

	pg := pagination.FromRequest(r)
	ruleID := r.URL.Query().Get("rule_id")
	var entries []*tenantdb.AlertHistoryEntry
	var err error

	if ruleID != "" {
		// Verify rule belongs to tenant
		rule, rerr := gw.db.AlertRules().GetByID(r.Context(), ruleID)
		if rerr != nil || rule.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		entries, err = gw.db.AlertHistory().ListByRule(r.Context(), ruleID, pg.Limit)
	} else {
		entries, err = gw.db.AlertHistory().ListByTenant(r.Context(), claims.TenantID, pg.Limit)
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list alert history")
		return
	}
	if entries == nil {
		entries = []*tenantdb.AlertHistoryEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       entries,
		"pagination": pagination.NewResponse(pg, len(entries)),
	})
}

// --- Alert Evaluator ---

var metricToCubeMeasure = map[string]string{
	"error_rate":  "RequestMetricsMinute.errorRate",
	"p50_latency": "RequestMetricsMinute.p50Latency",
	"p95_latency": "RequestMetricsMinute.p95Latency",
	"p99_latency": "RequestMetricsMinute.p99Latency",
	"throughput":  "RequestMetricsMinute.requestCount",
}

func (gw *gateway) alertEvaluatorLoop(ctx context.Context) {
	// Wait 2 minutes after startup before first evaluation
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		gw.evaluateAlerts(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (gw *gateway) evaluateAlerts(ctx context.Context) {
	rules, err := gw.db.AlertRules().ListActive(ctx)
	if err != nil {
		slog.Error("alert evaluator failed to list active rules", "error", err)
		return
	}

	if len(rules) == 0 {
		return
	}

	// Group rules by tenant for JWT reuse
	byTenant := map[string][]*tenantdb.AlertRule{}
	for _, rule := range rules {
		byTenant[rule.TenantID] = append(byTenant[rule.TenantID], rule)
	}

	now := time.Now().UTC()

	for tenantID, tenantRules := range byTenant {
		// Generate a short-lived token for Cube.js queries
		token, err := gw.tokens.Generate(tenantID, "evaluator", "evaluator@system", "admin")
		if err != nil {
			slog.Error("alert evaluator failed to generate token", "tenant_id", tenantID, "error", err)
			continue
		}

		for _, rule := range tenantRules {
			// Check cooldown
			if rule.LastTriggeredAt != nil {
				cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
				if now.Sub(*rule.LastTriggeredAt) < cooldown {
					continue
				}
			}

			var value float64
			var triggered bool
			var message string
			var anomalyRes *anomalyResult

			if rule.Operator == "anomaly" {
				// Anomaly detection: compare current value against historical baseline
				var err error
				anomalyRes, err = gw.evaluateAnomalyRule(ctx, token, rule)
				if err != nil {
					slog.Error("alert evaluator anomaly error", "tenant_id", tenantID, "rule_id", rule.ID, "error", err)
					continue
				}
				if anomalyRes == nil {
					continue // not enough data or no anomaly
				}
				value = anomalyRes.currentValue
				triggered = anomalyRes.triggered
				message = fmt.Sprintf("%s: %s anomaly detected — value %.4f deviates from mean %.4f by %.1fσ (threshold %.1fσ)",
					rule.Name, rule.Metric, anomalyRes.currentValue,
					anomalyRes.mean, anomalyRes.deviationSigma, rule.Threshold)
			} else {
				var err error
				value, err = gw.queryCubeMetric(ctx, token, rule)
				if err != nil {
					slog.Error("alert evaluator error querying metric", "tenant_id", tenantID, "rule_id", rule.ID, "error", err)
					gatewayAlertEvalErrorsTotal.Inc()
					continue
				}

				if rule.Operator == "gt" && value > rule.Threshold {
					triggered = true
				} else if rule.Operator == "lt" && value < rule.Threshold {
					triggered = true
				}

				operatorText := ">"
				if rule.Operator == "lt" {
					operatorText = "<"
				}
				message = fmt.Sprintf("%s: %s is %.4f (%s %.4f) over %d min window",
					rule.Name, rule.Metric, value, operatorText, rule.Threshold, rule.WindowMinutes)
			}

			if !triggered {
				continue
			}

			slog.Warn("alert evaluator triggered", "tenant_id", tenantID, "rule_name", rule.Name, "metric", rule.Metric, "value", value)

			// Dispatch notification
			ch, err := gw.db.NotificationChannels().GetByID(ctx, rule.ChannelID)
			if err != nil {
				slog.Error("alert evaluator channel not found", "channel_id", rule.ChannelID, "rule_id", rule.ID, "error", err)
				continue
			}

			cfg, err := notify.ParseChannelConfig(ch.Config)
			if err != nil {
				slog.Error("alert evaluator invalid config for channel", "channel_id", ch.ID, "error", err)
				continue
			}

			// Build dashboard deep-link URL
			dashURL := gw.baseURL + "/index.html?"
			if rule.Service != "" {
				dashURL += "service=" + url.QueryEscape(rule.Service) + "&"
			}
			if rule.PathTemplate != "" {
				dashURL += "path=" + url.QueryEscape(rule.PathTemplate) + "&"
			}
			from := now.Add(-time.Duration(rule.WindowMinutes) * time.Minute).Format(time.RFC3339)
			dashURL += "from=" + url.QueryEscape(from) + "&to=" + url.QueryEscape(now.Format(time.RFC3339))

			payload := notify.AlertPayload{
				RuleName:      rule.Name,
				Metric:        rule.Metric,
				Operator:      rule.Operator,
				Threshold:     rule.Threshold,
				ActualValue:   value,
				WindowMinutes: rule.WindowMinutes,
				Service:       rule.Service,
				PathTemplate:  rule.PathTemplate,
				FiredAt:       now,
				DashboardURL:  dashURL,
			}

			// Set anomaly-specific fields if applicable
			if rule.Operator == "anomaly" && anomalyRes != nil {
				payload.Mean = anomalyRes.mean
				payload.Stddev = anomalyRes.stddev
				payload.DeviationSigma = anomalyRes.deviationSigma
			}

			status := "fired"
			if err := gw.notifier.Send(ctx, ch.Type, cfg, payload); err != nil {
				slog.Error("alert evaluator failed to send notification", "rule_id", rule.ID, "error", err)
				gatewayAlertNotificationErrorsTotal.WithLabelValues(ch.Type).Inc()
				status = "error"
				message += " (notification failed: " + err.Error() + ")"
			}

			// Record in history
			entry := &tenantdb.AlertHistoryEntry{
				RuleID:       rule.ID,
				TenantID:     tenantID,
				Metric:       rule.Metric,
				Threshold:    rule.Threshold,
				ActualValue:  value,
				Service:      rule.Service,
				PathTemplate: rule.PathTemplate,
				Status:       status,
				Message:      message,
			}
			if err := gw.db.AlertHistory().Create(ctx, entry); err != nil {
				slog.Error("alert evaluator failed to record history", "rule_id", rule.ID, "error", err)
			}

			// Update last triggered
			if err := gw.db.AlertRules().UpdateLastTriggered(ctx, rule.ID, now); err != nil {
				slog.Error("alert evaluator failed to update last_triggered", "rule_id", rule.ID, "error", err)
			}
		}
	}
}

// anomalyResult holds the result of an anomaly evaluation.
type anomalyResult struct {
	currentValue   float64
	mean           float64
	stddev         float64
	deviationSigma float64
	triggered      bool
}

// evaluateAnomalyRule compares the current metric value against a historical
// baseline for the same hour-of-day and day-of-week. Returns nil if there
// isn't enough historical data (< 3 data points).
func (gw *gateway) evaluateAnomalyRule(ctx context.Context, token string, rule *tenantdb.AlertRule) (*anomalyResult, error) {
	measure, ok := metricToCubeMeasure[rule.Metric]
	if !ok {
		return nil, fmt.Errorf("unknown metric: %s", rule.Metric)
	}

	// WindowMinutes is repurposed as lookback days for anomaly rules
	lookbackDays := rule.WindowMinutes
	if lookbackDays < 1 {
		lookbackDays = 7
	}

	// Build filters for historical data query
	filters := []map[string]interface{}{}
	if rule.Service != "" {
		filters = append(filters, map[string]interface{}{
			"member":   "RequestMetricsMinute.service",
			"operator": "equals",
			"values":   []string{rule.Service},
		})
	}
	if rule.PathTemplate != "" {
		filters = append(filters, map[string]interface{}{
			"member":   "RequestMetricsMinute.pathTemplate",
			"operator": "equals",
			"values":   []string{rule.PathTemplate},
		})
	}

	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -lookbackDays)

	// Query historical data at hourly granularity
	query := map[string]interface{}{
		"measures": []string{measure},
		"timeDimensions": []map[string]interface{}{{
			"dimension":   "RequestMetricsMinute.bucketStart",
			"granularity": "hour",
			"dateRange":   []string{cutoff.Format("2006-01-02"), now.Format("2006-01-02")},
		}},
		"filters": filters,
		"order":   map[string]string{"RequestMetricsMinute.bucketStart": "asc"},
	}

	body, err := json.Marshal(map[string]interface{}{"query": query})
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", gw.cubeAPIURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cube query: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("cube returned %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Data []map[string]interface{} `json:"data"`
		} `json:"results"`
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	var data []map[string]interface{}
	if len(result.Results) > 0 {
		data = result.Results[0].Data
	} else {
		data = result.Data
	}

	if len(data) == 0 {
		return nil, nil // No data at all
	}

	// Find the time key in the response
	timeKey := "RequestMetricsMinute.bucketStart"
	if _, ok := data[0]["RequestMetricsMinute.bucketStart.hour"]; ok {
		timeKey = "RequestMetricsMinute.bucketStart.hour"
	}

	// Current hour and day of week for filtering
	currentHour := now.Hour()
	currentDow := now.Weekday()

	// Filter historical data points matching same hour-of-day and day-of-week
	var historicalValues []float64
	var currentValue float64
	currentFound := false

	for _, row := range data {
		tsRaw, ok := row[timeKey]
		if !ok {
			continue
		}
		tsStr, ok := tsRaw.(string)
		if !ok {
			continue
		}

		// Parse timestamp
		var ts time.Time
		for _, layout := range []string{
			"2006-01-02T15:04:05.000",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
		} {
			ts, err = time.Parse(layout, tsStr)
			if err == nil {
				break
			}
		}
		if ts.IsZero() {
			continue
		}

		// Extract metric value
		val := extractFloat(row, measure)

		// Check if this is the current hour (most recent matching)
		if ts.Hour() == currentHour && ts.Weekday() == currentDow {
			// Check if this is today's data point
			if ts.Year() == now.Year() && ts.YearDay() == now.YearDay() {
				currentValue = val
				currentFound = true
			} else {
				// Historical data point matching same hour + DOW
				historicalValues = append(historicalValues, val)
			}
		}
	}

	// If we didn't find a current value, get it from the most recent data point
	if !currentFound {
		currentValue, err = gw.queryCubeMetric(ctx, token, rule)
		if err != nil {
			return nil, fmt.Errorf("query current value: %w", err)
		}
		currentFound = true
	}

	// Need at least 3 historical data points for meaningful statistics
	if len(historicalValues) < 3 {
		slog.Info("alert evaluator anomaly rule skipped insufficient data", "rule_id", rule.ID, "data_points", len(historicalValues))
		return nil, nil
	}

	mean, err := stats.Mean(historicalValues)
	if err != nil {
		return nil, fmt.Errorf("compute mean: %w", err)
	}

	stddev, err := stats.StandardDeviationSample(historicalValues)
	if err != nil {
		return nil, fmt.Errorf("compute stddev: %w", err)
	}

	// Avoid division by zero — if stddev is 0, any deviation is infinite
	var deviationSigma float64
	if stddev > 0 {
		deviationSigma = math.Abs(currentValue-mean) / stddev
	} else if currentValue != mean {
		deviationSigma = math.Inf(1) // Any deviation from a flat baseline is infinite sigma
	}

	triggered := deviationSigma > rule.Threshold

	return &anomalyResult{
		currentValue:   currentValue,
		mean:           mean,
		stddev:         stddev,
		deviationSigma: deviationSigma,
		triggered:      triggered,
	}, nil
}

// extractFloat extracts a float64 value from a Cube.js data row.
func extractFloat(row map[string]interface{}, key string) float64 {
	val, ok := row[key]
	if !ok {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return v
	case string:
		var f float64
		fmt.Sscanf(v, "%f", &f)
		return f
	default:
		return 0
	}
}

func (gw *gateway) queryCubeMetric(ctx context.Context, token string, rule *tenantdb.AlertRule) (float64, error) {
	measure, ok := metricToCubeMeasure[rule.Metric]
	if !ok {
		return 0, fmt.Errorf("unknown metric: %s", rule.Metric)
	}

	filters := []map[string]interface{}{}
	if rule.Service != "" {
		filters = append(filters, map[string]interface{}{
			"member":   "RequestMetricsMinute.service",
			"operator": "equals",
			"values":   []string{rule.Service},
		})
	}
	if rule.PathTemplate != "" {
		filters = append(filters, map[string]interface{}{
			"member":   "RequestMetricsMinute.pathTemplate",
			"operator": "equals",
			"values":   []string{rule.PathTemplate},
		})
	}

	cutoff := time.Now().UTC().Add(-time.Duration(rule.WindowMinutes) * time.Minute)
	filters = append(filters, map[string]interface{}{
		"member":   "RequestMetricsMinute.bucketStart",
		"operator": "gte",
		"values":   []string{cutoff.Format("2006-01-02T15:04:05")},
	})

	query := map[string]interface{}{
		"measures": []string{measure},
		"filters":  filters,
	}

	body, err := json.Marshal(map[string]interface{}{"query": query})
	if err != nil {
		return 0, fmt.Errorf("marshal query: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", gw.cubeAPIURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("cube query: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("cube returned %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Data []map[string]interface{} `json:"data"`
		} `json:"results"`
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	var data []map[string]interface{}
	if len(result.Results) > 0 {
		data = result.Results[0].Data
	} else {
		data = result.Data
	}

	if len(data) == 0 {
		return 0, nil // No data for this window
	}

	val, ok := data[0][measure]
	if !ok {
		return 0, nil
	}

	switch v := val.(type) {
	case float64:
		return v, nil
	case string:
		var f float64
		fmt.Sscanf(v, "%f", &f)
		return f, nil
	default:
		return 0, fmt.Errorf("unexpected value type for %s: %T", measure, val)
	}
}

