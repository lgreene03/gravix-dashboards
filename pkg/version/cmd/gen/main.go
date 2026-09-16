// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command gen regenerates SECURITY.md's supported-versions table from the
// release record.
//
// The table is generated rather than written because a security policy that
// claims a support window the maintainers do not honour is worse than no
// policy at all.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/version"
)

func main() {
	var (
		register = flag.String("register", version.RegisterPath, "path to the release record")
		doc      = flag.String("doc", "SECURITY.md", "path to the document holding the generated table")
		check    = flag.Bool("check", false, "exit non-zero if the document is stale instead of rewriting it")
	)
	flag.Parse()

	releases, err := version.LoadRegister(*register)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supported-versions: %v\n", err)
		os.Exit(1)
	}

	raw, err := os.ReadFile(*doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supported-versions: reading %s: %v\n", *doc, err)
		os.Exit(1)
	}

	table := version.SecurityTable(time.Now().UTC(), releases)
	updated, err := version.ReplaceTable(string(raw), table)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supported-versions: %v\n", err)
		os.Exit(1)
	}

	if *check {
		if updated != string(raw) {
			fmt.Fprintf(os.Stderr,
				"supported-versions: %s is stale; run `make supported-versions`\n", *doc)
			os.Exit(1)
		}
		fmt.Printf("supported-versions: %s is current (%d release(s) in the record)\n",
			*doc, len(releases))
		return
	}

	if err := os.WriteFile(*doc, []byte(updated), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "supported-versions: writing %s: %v\n", *doc, err)
		os.Exit(1)
	}
	fmt.Printf("supported-versions: wrote %s (%d release(s) in the record)\n", *doc, len(releases))
}
