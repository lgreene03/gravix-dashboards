// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"time"
)

// Alert is one alert delivered to a notifier.
//
// It is a plain JSON struct rather than an alias of an internal type: it
// crosses the ABI, so its shape is a published contract that changes only with
// ABIVersion.
type Alert struct {
	// AlertID identifies this firing. Notify must be idempotent on it.
	AlertID       string    `json:"alert_id"`
	RuleName      string    `json:"rule_name"`
	Metric        string    `json:"metric"`
	Operator      string    `json:"operator"`
	Threshold     float64   `json:"threshold"`
	ActualValue   float64   `json:"actual_value"`
	WindowMinutes int       `json:"window_minutes"`
	Service       string    `json:"service"`
	PathTemplate  string    `json:"path_template"`
	FiredAt       time.Time `json:"fired_at"`
	DashboardURL  string    `json:"dashboard_url"`
	Severity      string    `json:"severity"`
}

// Notifier delivers an alert to a destination.
type Notifier interface {
	// Notify delivers one alert. It must be idempotent on AlertID: Gravix may
	// retry, and a duplicate page is worse than a late one.
	Notify(ctx context.Context, alert Alert) error
}
