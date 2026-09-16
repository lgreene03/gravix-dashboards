// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command eligible reports whether a change of a given kind is backported to
// an LTS branch.
//
// It exists so scripts/backport.sh and docs/oss/lts-policy.md cannot disagree
// about what is backported: both derive from pkg/version's Eligible.
//
// It prints a verdict token on the first line and always exits 0, rather than
// encoding the verdict in the exit status. `go run` collapses every non-zero
// exit to 1, so a caller that read the exit code could not tell "this change
// is not eligible" from "nobody has said what kind of change this is" — and
// those need different answers: one is a decision, the other is a question.
//
//	eligible: yes     — backport it
//	eligible: no      — do not
//	eligible: unknown — classify it first
package main

import (
	"flag"
	"fmt"

	"github.com/lgreene/gravix-dashboards/pkg/version"
)

func main() {
	kind := flag.String("kind", "", "the kind of change: security, data-correctness, data-loss, crash, performance, feature, dependency")
	flag.Parse()

	k := version.BackportKind(*kind)

	if *kind == "" || !version.KnownKind(k) {
		fmt.Println("eligible: unknown")
		if *kind == "" {
			fmt.Println("--kind is required: security, data-correctness, data-loss, crash, performance, feature, dependency")
			return
		}
		_, reason := version.Eligible(k)
		fmt.Println(reason)
		return
	}

	ok, reason := version.Eligible(k)
	if ok {
		fmt.Println("eligible: yes")
	} else {
		fmt.Println("eligible: no")
	}
	fmt.Println(reason)
}
