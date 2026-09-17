// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command gen validates the RFCs and renders the decision log.
//
// It is invoked by `make rfc-index` to regenerate the committed index, and by
// `make rfc-check` to render into a scratch file that is then diffed against
// the committed one — the same pattern as `make contracts-check`. The second
// form is what stops the decision log drifting from the decisions it claims to
// record.
//
// Validation runs in both forms. An RFC that breaks a rule fails the build
// rather than being quietly indexed, because the rules charter §6 cares about —
// the length of the comment window, the number of approvals, whether an
// entrenched clause was named — are exactly the ones nobody re-checks by hand.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lgreene/gravix-dashboards/pkg/rfc"
)

func main() {
	in := flag.String("in", "docs/oss/rfcs", "directory of RFC files")
	out := flag.String("out", "docs/oss/rfcs/index.md", "index file to write")
	flag.Parse()

	rfcs, err := rfc.Load(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rfc: %v\n", err)
		os.Exit(1)
	}

	if errs := rfc.ValidateAll(rfcs); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "%v\n", e)
		}
		fmt.Fprintf(os.Stderr, "\n%d rule violation(s) across %d RFC(s)\n", len(errs), len(rfcs))
		os.Exit(1)
	}

	if err := os.WriteFile(*out, []byte(rfc.RenderIndex(rfcs)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "rfc: writing %s: %v\n", *out, err)
		os.Exit(1)
	}
}
