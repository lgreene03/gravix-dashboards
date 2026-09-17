// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command gen renders the metric contract registry into Markdown documentation.
//
// It is invoked by `make contracts` to regenerate the committed document, and by
// `make contracts-check` to render into a scratch file that is then diffed against
// the committed one. The second form is what stops the document drifting from the
// contracts it claims to describe.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lgreene/gravix-dashboards/pkg/metriccontract"
)

func main() {
	in := flag.String("in", "contracts", "directory of contract YAML files")
	out := flag.String("out", "docs/02-derived-metrics.md", "file to write")
	flag.Parse()

	reg, err := metriccontract.Load(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "contracts: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*out, []byte(metriccontract.Render(reg)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "contracts: writing %s: %v\n", *out, err)
		os.Exit(1)
	}
}
