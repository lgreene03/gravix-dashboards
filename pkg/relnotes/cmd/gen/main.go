// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command gen assembles release notes for a version range.
//
// It is invoked by `make relnotes` and by the release workflow. It writes
// nothing unless every rule holds: a summary written by a person, an upgrade
// note for every breaking change, a version that matches the bump the changes
// require, and no email address anywhere in the output.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/lgreene/gravix-dashboards/pkg/relnotes"
)

func main() {
	repo := flag.String("repo", ".", "repository path")
	prev := flag.String("prev", "", "previous tag; the range starts here")
	head := flag.String("head", "HEAD", "head ref")
	version := flag.String("version", "", "version being released, e.g. v1.1.0")
	tmplPath := flag.String("template", "pkg/relnotes/template.md", "output template")
	out := flag.String("out", "", "file to write; stdout when empty")
	flag.Parse()

	if *version == "" {
		fmt.Fprintln(os.Stderr, "relnotes: -version is required")
		os.Exit(2)
	}

	notes, err := relnotes.Generate(context.Background(), *repo, *prev, *head, *version)
	if err != nil {
		report(err)
		os.Exit(1)
	}

	tmpl, err := os.ReadFile(*tmplPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relnotes: read %s: %v\n", *tmplPath, err)
		os.Exit(1)
	}

	rendered, err := relnotes.Render(notes, string(tmpl))
	if err != nil {
		report(err)
		os.Exit(1)
	}

	if *out == "" {
		fmt.Print(rendered)
		return
	}
	if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "relnotes: write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "relnotes: wrote %s — %d changes, %d contributors, %d first-time\n",
		*out, len(notes.Changes), len(notes.Contributors), len(notes.FirstTimers))
}

// report prints the error, with a pointer to the fix for the ones a person hits.
func report(err error) {
	fmt.Fprintf(os.Stderr, "%v\n", err)
	switch {
	case errors.Is(err, relnotes.ErrNoSummary):
		fmt.Fprintf(os.Stderr, "\nWrite %s. One paragraph: what changed, and why somebody should care.\n"+
			"The machine assembles everything else; it does not get to invent the narrative.\n",
			relnotes.SummaryFile)
	case errors.Is(err, relnotes.ErrBreakingNoUpgrade):
		fmt.Fprintln(os.Stderr, "\nAdd an `Upgrade-Note:` trailer to that commit saying what an operator must do.")
	case errors.Is(err, relnotes.ErrEmailLeak):
		fmt.Fprintln(os.Stderr, "\nPRIVACY DEFECT. These addresses were given for DCO compliance, not publication.")
	}
}
