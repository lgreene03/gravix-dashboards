// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command evidence computes the annual charter review's measured record and
// writes it as JSON.
//
// Every field it emits is measured. A field it cannot measure is listed under
// "unmeasurable" rather than estimated, because a plausible-looking proxy in a
// transparency report is worse than an admitted gap.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/charterreview"
)

func main() {
	var (
		repo   = flag.String("repo", ".", "path to the repository")
		period = flag.String("period", fmt.Sprint(time.Now().UTC().Year()), "the review period, a year")
		out    = flag.String("out", "", "write JSON here instead of stdout")
	)
	flag.Parse()

	e, err := charterreview.Collect(context.Background(), *repo, *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charter-evidence: %v\n", err)
		os.Exit(1)
	}

	raw, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "charter-evidence: %v\n", err)
		os.Exit(1)
	}
	raw = append(raw, '\n')

	if *out != "" {
		if err := os.WriteFile(*out, raw, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "charter-evidence: writing %s: %v\n", *out, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "charter-evidence: wrote %s\n", *out)
	} else {
		os.Stdout.Write(raw)
	}

	// A re-gated capability is reported on stderr and exits non-zero, so the
	// finding cannot wait for somebody to read the JSON. §10: escalate before
	// publication, not at it.
	if err := e.RegatingError(); err != nil {
		fmt.Fprintf(os.Stderr, "\n%v\n", err)
		os.Exit(2)
	}
	if msg := e.CanaryMessage(); msg != "" {
		fmt.Fprintf(os.Stderr, "\n%s\n", msg)
		os.Exit(3)
	}
}
