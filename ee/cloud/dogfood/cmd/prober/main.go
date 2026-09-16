// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Command prober polls Gravix Cloud's own health endpoints and reports each
// result as an ordinary RequestFact into the tenant Cloud runs on itself.
//
// It is an ingestion client and nothing else. The facts it writes are read by
// the same rollup, the same warehouse and the same Public Metrics API every
// customer's facts go through — which is the point. Cloud's published uptime
// number is a Gravix query, so if Gravix's aggregation is wrong, the number
// Cloud pays credits against is wrong too.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/cloud/dogfood"
)

const (
	usageTargets = "prober: --targets is required"
	usageAPIKey  = "prober: --api-key is required (or set GRAVIX_DOGFOOD_API_KEY)"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run is main with the process exit and the output streams lifted out, so a
// test can assert the exit codes in §6.1 rather than describe them.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prober", flag.ExitOnError)
	endpoint := fs.String("ingestion-endpoint", "http://localhost:8090", "Base URL of the dogfood tenant's ingestion API")
	apiKey := fs.String("api-key", "", "Dogfood tenant API key; falls back to GRAVIX_DOGFOOD_API_KEY env var")
	targets := fs.String("targets", "", "Comma-separated name=url pairs to probe (required), same format as cmd/status_page's STATUS_ENDPOINTS")
	service := fs.String("service", "gravix-cloud-dogfood", "RequestFact.service value recorded for every probe")
	interval := fs.Duration("interval", 30*time.Second, "Poll interval between cycles when not run with --once")
	once := fs.Bool("once", false, "Run exactly one poll cycle across all targets, then exit 0")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	checks, err := dogfood.ParseTargets(*targets)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 2
	}
	if len(checks) == 0 {
		fmt.Fprintln(stderr, usageTargets)
		return 2
	}

	key := *apiKey
	if key == "" {
		key = os.Getenv("GRAVIX_DOGFOOD_API_KEY")
	}
	if key == "" {
		fmt.Fprintln(stderr, usageAPIKey)
		return 2
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := &http.Client{}

	for {
		cycle(ctx, client, *endpoint, key, *service, checks, stdout, stderr)
		if *once {
			return 0
		}

		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "prober: stopping")
			return 0
		case <-time.After(*interval):
		}
	}
}

// cycle probes every target once and reports each result.
//
// A reporting failure is logged and the cycle continues. A monitoring process
// that stops monitoring because one write failed has turned a partial outage
// into a total loss of visibility, and the missing facts are exactly the ones
// that would have explained it.
func cycle(ctx context.Context, client *http.Client, endpoint, apiKey, service string,
	checks []dogfood.HealthCheck, stdout, stderr io.Writer) {

	for _, check := range checks {
		result := dogfood.Probe(ctx, client, check)
		if err := dogfood.SendFact(ctx, client, endpoint, apiKey, result, service); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			continue
		}
		fmt.Fprintf(stdout, "%s status=%d latency_ms=%d\n", check.Name, result.StatusCode, result.LatencyMs)
	}
}
