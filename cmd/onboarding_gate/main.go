// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command onboarding_gate applies the Phase 9 ten-minute onboarding budget
// (docs/oss/12-goal-tree.md G3.1) to a measured elapsed duration and prints the
// exact PASS/FAIL contract scripts/timed_onboarding_test.sh depends on.
//
// The split is deliberate and follows cmd/checkboundary: everything that can be
// decided without Docker lives here, under unit test, and only the parts that
// genuinely need a container runtime — booting the stack, polling Cube, reading
// a wall clock — live in the shell script. A gate whose pass/fail logic can only
// be exercised by a twenty-minute CI job is a gate nobody checks.
package main

import (
	"flag"
	"fmt"
	"os"
)

// BudgetSeconds is the maximum number of seconds from `docker compose up` to a
// populated dashboard. Changing this value is a product decision, not an
// implementation detail — it requires the same review as any other regression
// threshold in this repository.
const BudgetSeconds = 600

const usage = "usage: onboarding_gate -elapsed-seconds <n> [-timed-out]"

// evaluate applies the pass/fail decision and returns the exact message
// (verbatim, newline-free) and exit code scripts/timed_onboarding_test.sh must
// print and propagate.
//
// timedOut takes precedence over elapsedSeconds: polling that gave up without
// ever seeing data has no meaningful elapsed time, and reporting its small
// stopwatch reading as a pass would turn the gate into a rubber stamp for the
// one failure it exists to catch.
func evaluate(elapsedSeconds int, timedOut bool) (message string, exitCode int) {
	if timedOut {
		return fmt.Sprintf(
			"FAIL: no populated dashboard within %ds (timed out waiting for RequestMetricsMinute data)",
			BudgetSeconds,
		), 1
	}
	if elapsedSeconds > BudgetSeconds {
		return fmt.Sprintf(
			"FAIL: onboarding budget of %ds regressed to %ds",
			BudgetSeconds, elapsedSeconds,
		), 1
	}
	return fmt.Sprintf(
		"PASS: time to populated dashboard %ds (budget: %ds)",
		elapsedSeconds, BudgetSeconds,
	), 0
}

func main() {
	fs := flag.NewFlagSet("onboarding_gate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	elapsed := fs.Int("elapsed-seconds", -1,
		"measured wall-clock seconds from `docker compose up` to a populated dashboard")
	timedOut := fs.Bool("timed-out", false,
		"set when polling exhausted its own timeout without ever observing a populated dashboard")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, usage) }

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	// -1 is the sentinel for "not supplied". A negative measured duration is
	// not a thing a stopwatch produces, so it is rejected the same way rather
	// than being allowed through as a very fast pass.
	if *elapsed < 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	message, code := evaluate(*elapsed, *timedOut)
	fmt.Println(message)
	os.Exit(code)
}
