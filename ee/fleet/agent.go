// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// AgentConfig configures the self-report. Its zero value is off, which is what
// every installation gets until somebody deliberately turns it on.
type AgentConfig struct {
	// Enabled is the local operator's kill switch. One flag, no console
	// involvement, effective immediately: an operator who wants this process to
	// stop talking to anyone does not have to ask the people it talks to.
	Enabled bool

	// ConsoleURL is where the report is POSTed. Outbound only.
	ConsoleURL string

	// Interval is how often to report. Zero means DefaultInterval.
	Interval time.Duration
}

// DefaultInterval is how often the self-report is sent. It is minutes rather
// than seconds because nothing waits on it.
const DefaultInterval = time.Minute

// Agent sends one process's self-report to a console.
//
// It is not the thing docs/04-non-goals.md §3 forbids, and the difference is
// structural rather than semantic:
//
//   - It reports only this Gravix process's own version, health-check result
//     and config hash — the three fields of Report, which has no others.
//   - It collects nothing about the host. No CPU, memory, disk, network,
//     process list or anything else. There is no code here that could.
//   - It is outbound only. It opens no listener, so a console cannot connect to
//     an installation even if it wanted to.
//   - It is absent by default and disabled with one local flag.
//
// A self-report from one process about itself is not a host daemon. If this ever
// drifts toward collecting something about the machine, §10 of the spec says to
// cut scope rather than argue about the word, and TestAgentCollectsNoHostMetrics
// is what notices.
type Agent struct {
	cfg    AgentConfig
	client *http.Client

	mu     sync.Mutex
	queued []Report
	// Observe is called after each attempt, for tests and for a local log.
	Observe func(sent bool, err error)
}

// NewAgent returns an agent. A disabled config yields an agent whose Run
// returns immediately and whose Send does nothing.
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	return &Agent{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled reports whether this agent will do anything at all.
func (a *Agent) Enabled() bool { return a.cfg.Enabled && a.cfg.ConsoleURL != "" }

// SelfReport builds the report for this process. It takes everything it reports
// as an argument: the agent has no way to find out anything on its own.
func SelfReport(installID, version, configHash string, healthy bool, now time.Time) Report {
	return Report{
		InstallID:  installID,
		Version:    version,
		Healthy:    healthy,
		ConfigHash: configHash,
		ReportedAt: now.UTC(),
	}
}

// Send posts one report, queueing it locally if the console cannot be reached.
//
// A failure here is not an error condition for the installation. The console
// being down means the console is down; the install carries on, and the reports
// it could not deliver wait in memory to be sent with the next one.
func (a *Agent) Send(ctx context.Context, rep Report) error {
	if !a.Enabled() {
		return nil
	}

	a.mu.Lock()
	batch := append(append([]Report(nil), a.queued...), rep)
	a.mu.Unlock()

	err := a.post(ctx, batch)
	a.mu.Lock()
	if err != nil {
		a.queued = batch
		if len(a.queued) > maxQueued {
			// Oldest first: a report from an hour ago says less than the one
			// from a minute ago, and an unbounded queue in a monitoring agent
			// is how the monitoring becomes the outage.
			a.queued = a.queued[len(a.queued)-maxQueued:]
		}
	} else {
		a.queued = nil
	}
	queued := len(a.queued)
	a.mu.Unlock()

	if a.Observe != nil {
		a.Observe(err == nil, err)
	}
	if err != nil {
		return fmt.Errorf("fleet: console unreachable; local operation unaffected (%d report(s) queued): %w",
			queued, err)
	}
	return nil
}

// maxQueued bounds the in-memory backlog.
const maxQueued = 60

func (a *Agent) post(ctx context.Context, batch []Report) error {
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.ConsoleURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("console returned %d", resp.StatusCode)
	}
	return nil
}

// Queued reports how many undelivered reports are waiting.
func (a *Agent) Queued() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.queued)
}

// Run reports on an interval until ctx is cancelled. It returns immediately
// when the agent is disabled, which is the default.
func (a *Agent) Run(ctx context.Context, build func(time.Time) Report) error {
	if !a.Enabled() {
		return nil
	}
	ticker := time.NewTicker(a.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case t := <-ticker.C:
			// An error here is logged by Observe and otherwise ignored. There
			// is nothing for an installation to do about a console being down.
			_ = a.Send(ctx, build(t))
		}
	}
}
