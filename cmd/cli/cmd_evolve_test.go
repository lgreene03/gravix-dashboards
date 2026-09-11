// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/evolve"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// seedEvolveFixture writes a few days of facts and builds their partitions, so an
// evolution has real history to act on. It returns the window, ending yesterday so
// every day is inside retention.
func seedEvolveFixture(t *testing.T, days int) (from, to time.Time) {
	t.Helper()
	t.Chdir(t.TempDir())

	store, err := storage.NewLocalStore(dataRoot)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()

	now := time.Now().UTC()
	last := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	first := last.AddDate(0, 0, -(days - 1))

	agents := []string{"Chrome", "Firefox", "Safari"}
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		var buf bytes.Buffer
		for minute := 0; minute < 4; minute++ {
			for i, agent := range agents {
				id, err := uuid.NewV7()
				if err != nil {
					t.Fatalf("uuid: %v", err)
				}
				fact := &gravixv1.RequestFact{
					EventId:         id.String(),
					EventTime:       timestamppb.New(d.Add(time.Duration(minute)*time.Minute + 9*time.Hour)),
					Service:         "api",
					Method:          "GET",
					PathTemplate:    "/users/{id}",
					StatusCode:      200,
					LatencyMs:       int32(10 + i*7 + minute),
					UserAgentFamily: agent,
				}
				data, err := protojson.Marshal(fact)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				buf.Write(data)
				buf.WriteByte('\n')
			}
		}
		key := fmt.Sprintf("raw/request_facts/%s/09/batch.jsonl", d.Format("2006-01-02"))
		if err := store.Put(ctx, key, bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	to = last.AddDate(0, 0, 1)
	if _, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: "./data/raw", OutputDir: "./data/warehouse",
		Window: recompute.Window{From: first, To: to},
	}); err != nil {
		t.Fatalf("build fixture partitions: %v", err)
	}
	return first, to
}

func runEvolveCLI(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := evolveMain(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func ymd(t time.Time) string { return t.Format("2006-01-02") }

// ─── AC-12: confirmation is required ───

func TestEvolveRequiresConfirmation(t *testing.T) {
	from, to := seedEvolveFixture(t, 3)

	// An empty answer declines. So does anything that is not exactly "yes".
	for _, answer := range []string{"", "\n", "y\n", "Y\n", "no\n", "YES\n"} {
		t.Run(fmt.Sprintf("answer=%q", answer), func(t *testing.T) {
			code, stdout, stderr := runEvolveCLI(t, answer,
				"add-percentile", "--quantile", "0.999", "--from", ymd(from), "--to", ymd(to))

			if code != evolveExitDeclined {
				t.Fatalf("exit = %d, want %d: %s", code, evolveExitDeclined, stderr)
			}
			if !strings.Contains(stderr, "evolve: aborted") {
				t.Errorf("stderr = %q, want the abort message", stderr)
			}
			// The plan must still have been shown — the user needs to see what they
			// are declining.
			if !strings.Contains(stdout, "plan:") {
				t.Errorf("stdout = %q, want the plan printed before the prompt", stdout)
			}
			if !strings.Contains(stdout, "Type 'yes' to continue") {
				t.Errorf("stdout = %q, want the confirmation prompt", stdout)
			}
		})
	}
}

func TestEvolveProceedsOnYes(t *testing.T) {
	from, to := seedEvolveFixture(t, 3)

	code, stdout, stderr := runEvolveCLI(t, "yes\n",
		"add-percentile", "--quantile", "0.999", "--from", ymd(from), "--to", ymd(to))
	if code != evolveExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	for _, want := range []string{"evolve: percentile p99.9", "partitions:", "fact re-read: no", "duration:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestEvolveYesFlagSkipsPrompt(t *testing.T) {
	from, to := seedEvolveFixture(t, 2)

	code, stdout, stderr := runEvolveCLI(t, "",
		"add-percentile", "--quantile", "0.99", "--from", ymd(from), "--to", ymd(to), "--yes")
	if code != evolveExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	if strings.Contains(stdout, "Type 'yes' to continue") {
		t.Error("--yes still prompted")
	}
}

// ─── AC-11: a dry run writes nothing and does not prompt ───

func TestEvolveDryRunWritesNothing(t *testing.T) {
	from, to := seedEvolveFixture(t, 3)

	before := treeDigest(t, filepath.Join(dataRoot, "warehouse"))

	code, stdout, stderr := runEvolveCLI(t, "",
		"add-dimension", "--field", "user_agent_family", "--from", ymd(from), "--to", ymd(to), "--dry-run")
	if code != evolveExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	if strings.Contains(stdout, "Type 'yes' to continue") {
		t.Error("a dry run prompted for confirmation; it writes nothing to confirm")
	}
	if !strings.Contains(stdout, "fact re-read: yes") {
		t.Errorf("stdout = %q, want it to say a dimension needs a fact re-read", stdout)
	}

	if after := treeDigest(t, filepath.Join(dataRoot, "warehouse")); after != before {
		t.Error("the dry run changed the warehouse")
	}
}

// treeDigest summarises a directory tree by name, size and modification time.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		fmt.Fprintf(&b, "%s|%d\n", path, info.Size())
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return b.String()
}

// ─── refusals reach the exit code ───

func TestEvolveRefusesDeniedDimension(t *testing.T) {
	from, to := seedEvolveFixture(t, 2)

	code, _, stderr := runEvolveCLI(t, "",
		"add-dimension", "--field", "user_id", "--from", ymd(from), "--to", ymd(to), "--yes")

	if code != evolveExitRefused {
		t.Fatalf("exit = %d, want %d", code, evolveExitRefused)
	}
	if !strings.Contains(stderr, "unbounded") {
		t.Errorf("stderr = %q, want it to say the dimension is unbounded", stderr)
	}
	if !strings.Contains(stderr, "docs/04-non-goals.md §5") {
		t.Errorf("stderr = %q, want it to cite the non-goal", stderr)
	}
}

func TestEvolveRefusesBadQuantile(t *testing.T) {
	from, to := seedEvolveFixture(t, 2)

	for _, q := range []string{"0", "1", "1.5"} {
		code, _, stderr := runEvolveCLI(t, "",
			"add-percentile", "--quantile", q, "--from", ymd(from), "--to", ymd(to), "--yes")
		if code != evolveExitRefused {
			t.Errorf("q=%s: exit = %d, want %d", q, code, evolveExitRefused)
		}
		if !strings.Contains(stderr, "strictly between 0 and 1") {
			t.Errorf("q=%s: stderr = %q", q, stderr)
		}
	}
}

func TestEvolveRejectsUnknownSubcommand(t *testing.T) {
	code, _, stderr := runEvolveCLI(t, "", "add-everything", "--from", "2026-09-01", "--to", "2026-09-02")
	if code != evolveExitRefused {
		t.Errorf("exit = %d, want %d", code, evolveExitRefused)
	}
	if !strings.Contains(stderr, "unknown subcommand") {
		t.Errorf("stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "Usage: gravix evolve") {
		t.Errorf("stderr = %q, want the usage text", stderr)
	}
}

func TestEvolveWithNoSubcommandShowsUsage(t *testing.T) {
	code, _, stderr := runEvolveCLI(t, "")
	if code != evolveExitRefused {
		t.Errorf("exit = %d, want %d", code, evolveExitRefused)
	}
	if !strings.Contains(stderr, "Usage: gravix evolve") {
		t.Errorf("stderr = %q, want the usage text", stderr)
	}
}

func TestEvolveRequiresWindow(t *testing.T) {
	seedEvolveFixture(t, 2)

	code, _, stderr := runEvolveCLI(t, "", "add-percentile", "--quantile", "0.99", "--yes")
	if code != evolveExitRefused {
		t.Errorf("exit = %d, want %d", code, evolveExitRefused)
	}
	if !strings.Contains(stderr, "--from and --to are required") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestEvolveRefusedClassification(t *testing.T) {
	// evolveRefused decides which failures are the user's input rather than the
	// system's fault; every sentinel the planner can return must be covered.
	for _, err := range []error{
		fmt.Errorf("wrapped: %w", evolve.ErrUnboundedDimension),
		fmt.Errorf("wrapped: %w", evolve.ErrUnknownField),
		fmt.Errorf("wrapped: %w", evolve.ErrQuantileRange),
		fmt.Errorf("wrapped: %w", evolve.ErrBeyondRetention),
		fmt.Errorf("wrapped: %w", evolve.ErrNoSketch),
		fmt.Errorf("wrapped: %w", evolve.ErrUnsupportedDimension),
	} {
		if !evolveRefused(err) {
			t.Errorf("%v was not classified as a refusal", err)
		}
	}
	if evolveRefused(fmt.Errorf("disk on fire")) {
		t.Error("an unrelated error was classified as a refusal")
	}
}
