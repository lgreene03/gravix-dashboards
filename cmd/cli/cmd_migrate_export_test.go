// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return d
}

// tarGZ builds a gzip+tar archive whose entry names are the map's keys, which
// is what the gateway streams: hdr.Name is the object-store key verbatim.
func tarGZ(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Size: int64(len(content)), Mode: 0o644, ModTime: time.Now(),
		}); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar body %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("closing gzip: %v", err)
	}
	return buf.Bytes()
}

// AC-1
func TestDateWindowsSplitsLongRange(t *testing.T) {
	since := mustDate(t, "2026-01-01")
	until := mustDate(t, "2026-03-06") // 65 days inclusive

	got := dateWindows(since, until, 30)
	if len(got) != 3 {
		t.Fatalf("got %d windows, want 3: %+v", len(got), got)
	}

	// No gap and no overlap: window n+1 starts the day after window n ends.
	for i, w := range got {
		days := int(w.End.Sub(w.Start).Hours()/24) + 1
		if days > 30 {
			t.Errorf("window %d spans %d days, over the gateway's 30-day cap", i, days)
		}
		if days < 1 {
			t.Errorf("window %d is empty: %s..%s", i, w.startString(), w.endString())
		}
		if i > 0 {
			wantStart := got[i-1].End.AddDate(0, 0, 1)
			if !w.Start.Equal(wantStart) {
				t.Errorf("window %d starts %s; window %d ended %s, so there is a %s",
					i, w.startString(), i-1, got[i-1].endString(),
					gapOrOverlap(w.Start, wantStart))
			}
		}
	}

	if !got[0].Start.Equal(since) {
		t.Errorf("first window starts %s, want %s", got[0].startString(), since.Format("2006-01-02"))
	}
	if !got[len(got)-1].End.Equal(until) {
		t.Errorf("last window ends %s, want %s", got[len(got)-1].endString(), until.Format("2006-01-02"))
	}
}

func gapOrOverlap(got, want time.Time) string {
	if got.After(want) {
		return "gap"
	}
	return "overlap"
}

func TestDateWindowsEdgeCases(t *testing.T) {
	d := func(s string) time.Time { return mustDate(t, s) }

	t.Run("single day", func(t *testing.T) {
		got := dateWindows(d("2026-01-01"), d("2026-01-01"), 30)
		if len(got) != 1 || !got[0].Start.Equal(got[0].End) {
			t.Fatalf("got %+v, want one single-day window", got)
		}
	})

	t.Run("exactly the cap", func(t *testing.T) {
		got := dateWindows(d("2026-01-01"), d("2026-01-30"), 30)
		if len(got) != 1 {
			t.Fatalf("a 30-day range produced %d windows, want 1: %+v", len(got), got)
		}
	})

	t.Run("one day over the cap", func(t *testing.T) {
		got := dateWindows(d("2026-01-01"), d("2026-01-31"), 30)
		if len(got) != 2 {
			t.Fatalf("a 31-day range produced %d windows, want 2: %+v", len(got), got)
		}
		if !got[1].Start.Equal(got[1].End) {
			t.Errorf("the remainder window is %s..%s, want a single day",
				got[1].startString(), got[1].endString())
		}
	})

	t.Run("until before since", func(t *testing.T) {
		if got := dateWindows(d("2026-02-01"), d("2026-01-01"), 30); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("nonsense cap", func(t *testing.T) {
		if got := dateWindows(d("2026-01-01"), d("2026-02-01"), 0); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	// A year of history is the case the whole command exists for. Every day in
	// the range must appear in exactly one window.
	t.Run("a full year is covered exactly once", func(t *testing.T) {
		since, until := d("2026-01-01"), d("2026-12-31")
		seen := map[string]int{}
		for _, w := range dateWindows(since, until, 30) {
			for day := w.Start; !day.After(w.End); day = day.AddDate(0, 0, 1) {
				seen[day.Format("2006-01-02")]++
			}
		}
		for day := since; !day.After(until); day = day.AddDate(0, 0, 1) {
			if n := seen[day.Format("2006-01-02")]; n != 1 {
				t.Fatalf("%s appears in %d windows, want exactly 1", day.Format("2006-01-02"), n)
			}
		}
		if len(seen) != 365 {
			t.Errorf("the windows cover %d days, want 365", len(seen))
		}
	})
}

// AC-2 — the layout claim the whole migration rests on. The gateway writes the
// object-store key into hdr.Name and transforms nothing, so extracting at that
// same relative path produces a directory a LocalStore reads with the identical
// keys. If this ever stops holding, the self-hosted rollup finds no input and
// the exported data is inert.
func TestFetchExportWindowPreservesRawKeyLayout(t *testing.T) {
	const (
		keyA = "raw/ten_abc123/request_facts/2026-01-01/09/0193f8a0-0000-7000-8000-000000000001.jsonl"
		keyB = "raw/ten_abc123/request_facts/2026-01-02/14/0193f8a0-0000-7000-8000-000000000002.jsonl"
	)
	archive := tarGZ(t, map[string]string{keyA: `{"a":1}`, keyB: `{"b":2}`})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		if body["start_date"] != "2026-01-01" || body["end_date"] != "2026-01-30" ||
			body["data_type"] != "request_facts" {
			t.Errorf("request body = %v", body)
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(archive)
	}))
	defer srv.Close()

	outDir := t.TempDir()
	n, err := fetchExportWindow(context.Background(), srv.Client(), srv.URL, "tok", "request_facts",
		dateWindow{Start: mustDate(t, "2026-01-01"), End: mustDate(t, "2026-01-30")}, outDir)
	if err != nil {
		t.Fatalf("fetchExportWindow: %v", err)
	}
	if n != 2 {
		t.Errorf("wrote %d files, want 2", n)
	}

	for key, want := range map[string]string{keyA: `{"a":1}`, keyB: `{"b":2}`} {
		got, err := os.ReadFile(filepath.Join(outDir, filepath.FromSlash(key)))
		if err != nil {
			t.Errorf("the key layout was not preserved: %v", err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// AC-4
func TestFetchExportWindowRetriesOnRateLimit(t *testing.T) {
	prev := rateLimitBackoff
	rateLimitBackoff = time.Millisecond
	defer func() { rateLimitBackoff = prev }()

	archive := tarGZ(t, map[string]string{"raw/ten_abc123/request_facts/2026-01-01/09/a.jsonl": "{}"})

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"an export is already in progress for this tenant"}`)
			return
		}
		w.Write(archive)
	}))
	defer srv.Close()

	n, err := fetchExportWindow(context.Background(), srv.Client(), srv.URL, "tok", "request_facts",
		dateWindow{Start: mustDate(t, "2026-01-01"), End: mustDate(t, "2026-01-01")}, t.TempDir())
	if err != nil {
		t.Fatalf("fetchExportWindow: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d files, want 1", n)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("made %d requests, want exactly 2 (one retry)", got)
	}
}

// TestFetchExportWindowGivesUpAfterASecondRateLimit: the gateway's concurrency
// limit is per tenant, so a second 429 means somebody else is mid-export.
// Retrying harder would only make both slower.
func TestFetchExportWindowGivesUpAfterASecondRateLimit(t *testing.T) {
	prev := rateLimitBackoff
	rateLimitBackoff = time.Millisecond
	defer func() { rateLimitBackoff = prev }()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := fetchExportWindow(context.Background(), srv.Client(), srv.URL, "tok", "request_facts",
		dateWindow{Start: mustDate(t, "2026-01-01"), End: mustDate(t, "2026-01-01")}, t.TempDir())
	if !errors.Is(err, ErrExportFailed) {
		t.Fatalf("got %v, want ErrExportFailed", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("made %d requests, want exactly 2", got)
	}
}

// AC-5 — a tenant exporting a year of history has mostly empty windows. A 404
// on one of them is the normal case, not a failure.
func TestFetchExportWindowSkipsEmptyWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"no data found for the specified date range"}`)
	}))
	defer srv.Close()

	n, err := fetchExportWindow(context.Background(), srv.Client(), srv.URL, "tok", "request_facts",
		dateWindow{Start: mustDate(t, "2026-01-01"), End: mustDate(t, "2026-01-30")}, t.TempDir())
	if err != nil {
		t.Fatalf("a 404 was treated as an error: %v", err)
	}
	if n != 0 {
		t.Errorf("wrote %d files, want 0", n)
	}
}

func TestFetchExportWindowSurfacesTheGatewaysOwnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"export range cannot exceed 30 days"}`)
	}))
	defer srv.Close()

	_, err := fetchExportWindow(context.Background(), srv.Client(), srv.URL, "tok", "request_facts",
		dateWindow{Start: mustDate(t, "2026-01-01"), End: mustDate(t, "2026-03-01")}, t.TempDir())
	if !errors.Is(err, ErrExportFailed) {
		t.Fatalf("got %v, want ErrExportFailed", err)
	}
	// An operator debugging a failed migration needs the server's words.
	if !strings.Contains(err.Error(), "export range cannot exceed 30 days") {
		t.Errorf("the gateway's message was swallowed: %v", err)
	}
}

// TestFetchExportWindowRefusesArchiveEntriesOutsideOutDir: this command runs
// against a server the customer is in the process of leaving. An entry named
// "../../.ssh/authorized_keys" must land nowhere.
func TestFetchExportWindowRefusesArchiveEntriesOutsideOutDir(t *testing.T) {
	for _, name := range []string{
		"../escaped.jsonl",
		"raw/../../escaped.jsonl",
		"/etc/cron.d/escaped",
	} {
		t.Run(name, func(t *testing.T) {
			archive := tarGZ(t, map[string]string{name: "pwned"})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write(archive)
			}))
			defer srv.Close()

			parent := t.TempDir()
			outDir := filepath.Join(parent, "out")
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			_, err := fetchExportWindow(context.Background(), srv.Client(), srv.URL, "tok", "request_facts",
				dateWindow{Start: mustDate(t, "2026-01-01"), End: mustDate(t, "2026-01-01")}, outDir)

			escaped := filepath.Join(parent, "escaped.jsonl")
			if _, statErr := os.Stat(escaped); statErr == nil {
				t.Fatalf("an archive entry escaped the output directory to %s", escaped)
			}
			if name == "/etc/cron.d/escaped" {
				// An absolute path is cleaned to a relative one and lands
				// inside outDir, which is safe; it must not reach /etc.
				if _, statErr := os.Stat(name); statErr == nil {
					t.Fatalf("an archive entry was written to %s", name)
				}
				return
			}
			if err == nil {
				t.Errorf("a %q entry was accepted", name)
			}
		})
	}
}

// AC-3
func TestMigrateExportRequiresTenantID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := migrateExportCloud(context.Background(),
		[]string{"--since", "2026-01-01", "--gateway-token", "tok"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), migrateExportUsage) {
		t.Errorf("stderr = %q, want the usage line", stderr.String())
	}
}

func TestMigrateExportFlagFailures(t *testing.T) {
	t.Setenv("GRAVIX_GATEWAY_TOKEN", "")

	cases := []struct {
		name    string
		args    []string
		wantOut string
	}{
		{
			name:    "missing since",
			args:    []string{"--tenant-id", "ten_abc123", "--gateway-token", "tok"},
			wantOut: migrateExportUsage,
		},
		{
			name:    "missing token",
			args:    []string{"--tenant-id", "ten_abc123", "--since", "2026-01-01"},
			wantOut: ErrMissingToken.Error(),
		},
		{
			name: "until before since",
			args: []string{"--tenant-id", "ten_abc123", "--since", "2026-02-01",
				"--until", "2026-01-01", "--gateway-token", "tok"},
			wantOut: ErrInvalidDateRange.Error(),
		},
		{
			name: "unparseable since",
			args: []string{"--tenant-id", "ten_abc123", "--since", "01/01/2026",
				"--gateway-token", "tok"},
			wantOut: "--since must be YYYY-MM-DD",
		},
		{
			name: "empty data types",
			args: []string{"--tenant-id", "ten_abc123", "--since", "2026-01-01",
				"--gateway-token", "tok", "--data-types", " , "},
			wantOut: "--data-types must name at least one data type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := migrateExportCloud(context.Background(), tc.args, &stdout, &stderr); code != 2 {
				t.Fatalf("exit code %d, want 2 (stderr: %s)", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.wantOut) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantOut)
			}
		})
	}
}

// TestMigrateExportReadsTheTokenFromTheEnvironment: a JWT on a command line
// ends up in shell history and in `ps`.
func TestMigrateExportReadsTheTokenFromTheEnvironment(t *testing.T) {
	var seen atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	t.Setenv("GRAVIX_GATEWAY_TOKEN", "env-token")

	var stdout, stderr bytes.Buffer
	code := migrateExportCloud(context.Background(), []string{
		"--tenant-id", "ten_abc123", "--since", "2026-01-01", "--until", "2026-01-01",
		"--gateway-endpoint", srv.URL, "--out-dir", t.TempDir(),
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if got, _ := seen.Load().(string); got != "Bearer env-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer env-token")
	}
}

// AC-6
func TestMigrateExportWritesManifest(t *testing.T) {
	archive := tarGZ(t, map[string]string{
		"raw/ten_abc123/request_facts/2026-01-05/09/a.jsonl": "{}",
	})

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["data_type"] == "request_facts" {
			w.Write(archive)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	outDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := migrateExportCloud(context.Background(), []string{
		"--tenant-id", "ten_abc123",
		"--since", "2026-01-01",
		"--until", "2026-01-31",
		"--gateway-endpoint", srv.URL,
		"--gateway-token", "tok",
		"--out-dir", outDir,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr.String())
	}

	raw, err := os.ReadFile(filepath.Join(outDir, "MIGRATION_MANIFEST.json"))
	if err != nil {
		t.Fatalf("no manifest was written: %v", err)
	}
	var got migrationManifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the manifest is not valid JSON: %v", err)
	}

	if got.TenantID != "ten_abc123" {
		t.Errorf("tenant_id = %q, want %q", got.TenantID, "ten_abc123")
	}
	if got.Since != "2026-01-01" {
		t.Errorf("since = %q, want %q", got.Since, "2026-01-01")
	}
	if got.Until != "2026-01-31" {
		t.Errorf("until = %q, want %q", got.Until, "2026-01-31")
	}
	// The manifest records what was ASKED FOR, not only what arrived, so a
	// short export is visibly short. Two data types over a 31-day range is two
	// windows each.
	if len(got.DataTypes) != 2 {
		t.Errorf("data_types = %v, want both defaults", got.DataTypes)
	}
	if got.FilesExported != 2 {
		t.Errorf("files_exported = %d, want 2 (one file in each of two windows)", got.FilesExported)
	}
	if got.ExportedAt.IsZero() {
		t.Error("exported_at is zero")
	}
	if n := requests.Load(); n != 4 {
		t.Errorf("made %d requests, want 4 (2 data types x 2 windows)", n)
	}

	if !strings.Contains(stdout.String(), "exported 2 files across 4 requests") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestMigrateExportExitsOneOnGatewayFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"failed to list files"}`)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := migrateExportCloud(context.Background(), []string{
		"--tenant-id", "ten_abc123", "--since", "2026-01-01", "--until", "2026-01-01",
		"--gateway-endpoint", srv.URL, "--gateway-token", "tok", "--out-dir", t.TempDir(),
	}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "failed to list files") {
		t.Errorf("stderr = %q, want the gateway's message", stderr.String())
	}
}

// TestMigrateExportErrorsAreTheOnesTheContractNames pins the sentinel text, so
// a rename shows up here rather than in somebody's runbook.
func TestMigrateExportErrorsAreTheOnesTheContractNames(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{ErrMissingTenantID, "migrate export-cloud: --tenant-id is required"},
		{ErrMissingSince, "migrate export-cloud: --since is required"},
		{ErrMissingToken, "migrate export-cloud: --gateway-token is required (or set GRAVIX_GATEWAY_TOKEN)"},
		{ErrInvalidDateRange, "migrate export-cloud: --until must not be before --since"},
		{ErrExportFailed, "migrate export-cloud: gateway export request failed"},
	} {
		if tc.err.Error() != tc.want {
			t.Errorf("got %q, want %q", tc.err.Error(), tc.want)
		}
	}
}

// TestMigrateExportIsListedInUsage: an exit path nobody can find is not an
// exit path.
func TestMigrateExportIsListedInUsage(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	body := string(raw)
	for _, want := range []string{`case "migrate":`, "gravix migrate", "export-cloud"} {
		if !strings.Contains(body, want) {
			t.Errorf("cmd/cli/main.go does not contain %q", want)
		}
	}
}
