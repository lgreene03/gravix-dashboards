// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command incidentaudit reports incidents that have not closed the loop.
//
// It reports. It never auto-dispositions and never auto-closes — a machine
// deciding that an incident produced no learning would defeat the purpose of
// the loop, which exists precisely because that is the decision people are
// tempted to skip.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/incident"
)

const (
	exitClean       = 0
	exitEnforcement = 1
	exitUnreadable  = 2
)

func main() {
	dir := flag.String("dir", "docs/oss/incidents", "directory holding the incident records")
	flag.Parse()

	incidents, err := incident.Load(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "incident-audit: %v\n", err)
		os.Exit(exitUnreadable)
	}

	report := incident.Audit(incidents, time.Now().UTC())
	report.Render(os.Stdout)

	if !report.OK() {
		os.Exit(exitEnforcement)
	}
	os.Exit(exitClean)
}
