// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import "context"

// RequestFact is the fact shape an adapter produces.
//
// It mirrors the wire fields of the RequestFact protobuf rather than aliasing
// the generated type. The ABI must not change because a generated file did,
// and a plugin author should not need Gravix's protobuf toolchain to write an
// adapter.
type RequestFact struct {
	EventID         string `json:"event_id"`
	EventTime       string `json:"event_time"` // RFC3339
	Service         string `json:"service"`
	Method          string `json:"method"`
	PathTemplate    string `json:"path_template"`
	StatusCode      int32  `json:"status_code"`
	LatencyMs       int32  `json:"latency_ms"`
	UserAgentFamily string `json:"user_agent_family"`
	TenantID        string `json:"tenant_id"`
}

// Adapter converts a foreign payload into Gravix facts.
type Adapter interface {
	// Convert turns a raw payload into validated facts. It must reject anything
	// that would violate the cardinality budget rather than passing it through:
	// a plugin is not an exemption from docs/04-non-goals.md §5.
	Convert(ctx context.Context, payload []byte, contentType string) ([]RequestFact, error)
}
