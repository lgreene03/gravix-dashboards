// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// TestEvaluatorFiresRulesOnALogChannel guards the one line GRVX-904 changed in
// this file, and it exists because mutation testing showed nothing else did.
//
// The evaluator looks up a rule's notification channel and parses its config
// before firing. The plain parser requires a webhook URL, so a "log" channel —
// the kind GRVX-904 auto-creates so a self-hoster can arm a rule without first
// configuring Slack — fails it, and the evaluator `continue`s. The rule never
// fires, nothing lands in alert history, and the only trace is one log line.
// Every auto-armed rule would be silently inert.
//
// Reverting `ParseChannelConfigForType(ch.Type, ch.Config)` to
// `ParseChannelConfig(ch.Config)` must fail this test.
func TestEvaluatorFiresRulesOnALogChannel(t *testing.T) {
	gw := newTestGateway(t)
	ctx := context.Background()

	// Cube reports an error rate well above the threshold, so the rule triggers
	// and the evaluator proceeds to channel dispatch — which is the step under
	// test.
	cube := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"RequestMetricsMinute.errorRate": 0.42}},
		})
	}))
	defer cube.Close()
	gw.cubeAPIURL = cube.URL

	tenant := &tenantdb.Tenant{Name: "Local", Email: "local@gravix.invalid", Plan: "free", Status: "active"}
	if err := gw.db.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// Exactly what GRVX-904's arm endpoint creates.
	channel := &tenantdb.NotificationChannel{
		TenantID: tenant.ID, Name: "Local (dashboard only)", Type: "log",
		Config: "{}", Status: "active",
	}
	if err := gw.db.NotificationChannels().Create(ctx, channel); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	rule := &tenantdb.AlertRule{
		TenantID: tenant.ID, Name: "api: Elevated error rate (auto-proposed)",
		Metric: "error_rate", Operator: "gt", Threshold: 0.05, WindowMinutes: 15,
		Service: "api", ChannelID: channel.ID, CooldownMinutes: 30, Status: "active",
	}
	if err := gw.db.AlertRules().Create(ctx, rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	gw.evaluateAlerts(ctx)

	history, err := gw.db.AlertHistory().ListByRule(ctx, rule.ID, 10)
	if err != nil {
		t.Fatalf("list alert history: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("a rule on a log channel did not fire. The evaluator parses the channel config " +
			"before dispatching, and the type-blind parser rejects a log channel for having no " +
			"webhook URL — so every rule armed by GRVX-904 would be silently skipped, with no " +
			"history row and nothing for the dashboard to show")
	}
	if history[0].Status == "" {
		t.Errorf("the history entry has no status: %+v", history[0])
	}

	// A webhook channel with a genuinely invalid config must still be rejected:
	// the fix must not have turned config validation off for everyone.
	broken := &tenantdb.NotificationChannel{
		TenantID: tenant.ID, Name: "Broken webhook", Type: "webhook",
		Config: "{}", Status: "active",
	}
	if err := gw.db.NotificationChannels().Create(ctx, broken); err != nil {
		t.Fatalf("create broken channel: %v", err)
	}
	brokenRule := &tenantdb.AlertRule{
		TenantID: tenant.ID, Name: "api: broken channel",
		Metric: "error_rate", Operator: "gt", Threshold: 0.05, WindowMinutes: 15,
		Service: "api", ChannelID: broken.ID, CooldownMinutes: 30, Status: "active",
	}
	if err := gw.db.AlertRules().Create(ctx, brokenRule); err != nil {
		t.Fatalf("create broken rule: %v", err)
	}

	gw.evaluateAlerts(ctx)

	brokenHistory, err := gw.db.AlertHistory().ListByRule(ctx, brokenRule.ID, 10)
	if err != nil {
		t.Fatalf("list broken history: %v", err)
	}
	if len(brokenHistory) != 0 {
		t.Error("a webhook channel with no webhook_url was dispatched to. ParseChannelConfigForType " +
			"must relax validation only for the log type")
	}
}
