// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command gen_facts writes a deterministic dataset of RequestFacts to disk.
//
// It exists so scripts/prove_it.sh can produce a week of history without a
// running ingestion service, a container, or a network — a demonstration that
// needs any of those is a demonstration most skeptics will not run.
//
// The generator is tests/correctness/fixtures, the same one the correctness
// suite uses. Sharing it is the point: the data the demo runs on is the data the
// property tests run on, so nobody has to wonder whether the demo was given an
// easier dataset.
//
//	go run ./cmd/gen_facts -dir ./data/raw/request_facts -days 7 -seed 42
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
)

func main() {
	var (
		dir      = flag.String("dir", "./data/raw/request_facts", "directory to write JSONL fact files into")
		days     = flag.Int("days", 7, "days of history to generate")
		seed     = flag.Int64("seed", 42, "random seed; the same seed always produces the same facts")
		services = flag.Int("services", 3, "number of distinct services")
		paths    = flag.Int("paths", 4, "path templates per service")
		perMin   = flag.Int("per-minute", 25, "facts per minute")
		minutes  = flag.Int("minutes-per-day", 0, "minutes of each day to populate; 0 means all 1440")
		dist     = flag.String("dist", "pareto", "latency distribution: uniform|normal|lognormal|bimodal|pareto")
		errRate  = flag.Float64("error-rate", 0.008, "fraction of requests that fail")
		lateFrac = flag.Float64("late-fraction", 0, "fraction of facts arriving after their bucket")
		quiet    = flag.Bool("quiet", false, "print only the fact count")
		origin   = flag.String("origin", "", "first day of the dataset, YYYY-MM-DD; default is the fixture's fixed origin")
	)
	flag.Parse()

	// A dataset's dates matter to more than readability: `gravix evolve` refuses a
	// window older than fact retention, quite rightly, so a demonstration has to
	// generate history that is actually still within it.
	if *origin != "" {
		t, err := time.Parse("2006-01-02", *origin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen_facts: -origin %q: %v\n", *origin, err)
			os.Exit(2)
		}
		fixtures.Origin = t.UTC()
	}

	spec := fixtures.Spec{
		Seed:            *seed,
		Days:            *days,
		ServicesCount:   *services,
		PathsPerService: *paths,
		FactsPerMinute:  *perMin,
		MinutesPerDay:   *minutes,
		LatencyDist:     *dist,
		ErrorRate:       *errRate,
		LateFraction:    *lateFrac,
		MaxLateness:     30 * time.Minute,
		// A bounded, low-cardinality field, so there is something real for a
		// retroactive dimension to split rows by.
		UserAgents: []string{"Chrome", "Firefox", "Safari", "curl"},
	}

	facts, err := fixtures.Generate(*dir, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen_facts: %v\n", err)
		os.Exit(1)
	}

	if *quiet {
		fmt.Println(len(facts))
		return
	}
	fmt.Printf("%d facts written to %s\n", len(facts), *dir)
	fmt.Printf("  %d days from %s, %d services, %s latency, %.1f%% errors\n",
		spec.Days, fixtures.Origin.Format("2006-01-02"), spec.ServicesCount,
		spec.LatencyDist, spec.ErrorRate*100)
	fmt.Printf("  seed %d — the same seed always produces exactly these facts\n", spec.Seed)
}
