// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a plugin subprocess. Re-executing ourselves with
// GRAVIX_PLUGIN_MODE set gives a real process speaking real JSON-RPC over real
// pipes, which is the only way to test crash isolation and timeouts honestly —
// a mocked transport cannot hang or die.
const pluginModeEnv = "GRAVIX_PLUGIN_MODE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(pluginModeEnv); mode != "" {
		runFakePlugin(mode)
		return
	}
	os.Exit(m.Run())
}

// runFakePlugin serves one behaviour named by mode, then exits.
func runFakePlugin(mode string) {
	in := bufio.NewReader(os.Stdin)
	out := os.Stdout

	respond := func(id int, result any) {
		body, _ := json.Marshal(rpcResponse{JSONRPC: jsonrpcVersion, ID: id, Result: mustRaw(result)})
		fmt.Fprintf(out, "%s\n", body)
	}

	manifest := Manifest{
		Name: "fake-notifier", Version: "1.0.0", ABIVersion: ABIVersion,
		Kind: KindNotifier, Description: "a test plugin", License: "Apache-2.0",
		ConfigSchema: map[string]ConfigField{
			"webhook_url": {Type: ConfigTypeString, Required: true},
			"token":       {Type: ConfigTypeSecret, Required: true},
		},
	}

	switch mode {
	case "abi-mismatch":
		manifest.ABIVersion = "999"
	case "bad-manifest":
		manifest.Name = "Not Kebab Case"
	case "exporter":
		manifest.Name, manifest.Kind = "fake-exporter", KindExporter
	case "adapter":
		manifest.Name, manifest.Kind = "fake-adapter", KindAdapter
	case "crash-on-start":
		os.Exit(1)
	}

	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}

		switch req.Method {
		case describeMethod:
			respond(req.ID, manifest)

		case notifyMethod:
			switch mode {
			case "crash-on-call":
				os.Exit(2)
			case "hang":
				// A sleep, not `select {}`: the runtime's deadlock detector
				// would abort a blocked-forever process, and the host would
				// see EOF instead of the hang we are trying to test.
				time.Sleep(10 * time.Minute)
			case "always-fail":
				body, _ := json.Marshal(rpcResponse{
					JSONRPC: jsonrpcVersion, ID: req.ID,
					Error: &rpcError{Code: -32000, Message: "delivery refused"},
				})
				fmt.Fprintf(out, "%s\n", body)
			case "malformed":
				fmt.Fprintln(out, "this is not json")
			default:
				respond(req.ID, map[string]any{"delivered": true})
			}

		case exportMethod:
			respond(req.ID, ExportResult{RowsWritten: 42, Destination: "s3://example/prefix"})

		case convertMethod:
			var p convertParams
			_ = json.Unmarshal(mustRaw(req.Params), &p)
			respond(req.ID, []RequestFact{
				{EventID: "e1", EventTime: "2026-01-01T00:00:00Z", Service: "checkout", Method: "GET", PathTemplate: "/orders/{id}", StatusCode: 200, LatencyMs: 3},
				{EventID: "e2", EventTime: "2026-01-01T00:00:01Z", Service: "checkout", Method: "GET", PathTemplate: "/users/9f3a2b1c-8d7e-6f5a", StatusCode: 200, LatencyMs: 4},
			})

		default:
			body, _ := json.Marshal(rpcResponse{
				JSONRPC: jsonrpcVersion, ID: req.ID,
				Error: &rpcError{Code: -32601, Message: "method not found"},
			})
			fmt.Fprintf(out, "%s\n", body)
		}
	}
}

func mustRaw(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

// newTestHost starts the test binary as a plugin in the given mode.
func newTestHost(t *testing.T, mode string, tune func(*HostOptions)) *Host {
	t.Helper()

	opts := HostOptions{
		Command: os.Args[0],
		Config:  map[string]any{"webhook_url": "https://example.com/hook", "token": "super-secret-token"},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if tune != nil {
		tune(&opts)
	}

	h := NewHost(opts)
	// exec.Command does not inherit a modified Env unless we set it, and the
	// mode is how the child knows which behaviour to serve.
	h.opts.Command = os.Args[0]
	t.Setenv(pluginModeEnv, mode)
	t.Cleanup(func() { h.Close() })
	return h
}

// AC-1
func TestPluginLifecycle(t *testing.T) {
	h := newTestHost(t, "ok", nil)

	m, err := h.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m.Name != "fake-notifier" || m.Kind != KindNotifier {
		t.Fatalf("manifest = %+v", m)
	}

	impl, err := h.Impl(nil)
	if err != nil {
		t.Fatalf("Impl: %v", err)
	}
	notifier, ok := impl.(Notifier)
	if !ok {
		t.Fatalf("a notifier plugin did not yield a Notifier")
	}

	if err := notifier.Notify(context.Background(), Alert{AlertID: "a-1", RuleName: "checkout errors"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got := h.Failures(); got != 0 {
		t.Errorf("failures = %d after a successful call", got)
	}
}

// AC-2: an incompatible plugin is refused at the handshake, not attempted.
func TestABIMismatchRefused(t *testing.T) {
	h := newTestHost(t, "abi-mismatch", nil)

	_, err := h.Start(context.Background())
	if !errors.Is(err, ErrABIMismatch) {
		t.Fatalf("err = %v, want ErrABIMismatch", err)
	}
	// The message must name both versions, or an operator cannot tell which
	// build of the plugin to fetch.
	for _, want := range []string{"fake-notifier", "999", ABIVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestBadManifestRefused(t *testing.T) {
	h := newTestHost(t, "bad-manifest", nil)

	if _, err := h.Start(context.Background()); !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("err = %v, want ErrManifestInvalid", err)
	}
}

// AC-3: a plugin that dies does not take the host with it.
func TestCrashIsolation(t *testing.T) {
	h := newTestHost(t, "crash-on-call", nil)

	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	impl, _ := h.Impl(nil)
	err := impl.(Notifier).Notify(context.Background(), Alert{AlertID: "a-1"})
	if err == nil {
		t.Fatal("a crashing plugin reported success")
	}
	if !errors.Is(err, ErrCrashed) {
		t.Errorf("err = %v, want ErrCrashed", err)
	}

	// The host is still usable: it counted a failure and is ready to back off
	// and retry rather than having panicked or deadlocked.
	if got := h.Failures(); got != 1 {
		t.Errorf("failures = %d, want 1", got)
	}
	if h.Disabled() {
		t.Error("one crash disabled the plugin; the budget is five")
	}
}

// AC-4: a plugin that never answers is killed at the timeout.
func TestTimeoutKillsPlugin(t *testing.T) {
	h := newTestHost(t, "hang", func(o *HostOptions) {
		o.CallTimeout = 250 * time.Millisecond
	})

	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	impl, _ := h.Impl(nil)
	start := time.Now()
	err := impl.(Notifier).Notify(context.Background(), Alert{AlertID: "a-1"})
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	// The point of the timeout is that the call returns. A host that waited
	// for a hung plugin would stall alerting indefinitely.
	if elapsed > 5*time.Second {
		t.Errorf("the call took %s; the timeout did not bound it", elapsed)
	}
	if !strings.Contains(err.Error(), "fake-notifier") {
		t.Errorf("error %q does not name the plugin", err)
	}
}

// AC-5
func TestFailureBudgetDisables(t *testing.T) {
	h := newTestHost(t, "always-fail", func(o *HostOptions) {
		o.CallTimeout = time.Second
		// Backoff must not gate the test: the subject is the failure count,
		// not the restart schedule.
		o.now = func() time.Time { return time.Now().Add(time.Hour) }
	})

	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	impl, _ := h.Impl(nil)
	notifier := impl.(Notifier)

	for i := 0; i < FailureBudget; i++ {
		if err := notifier.Notify(context.Background(), Alert{AlertID: fmt.Sprintf("a-%d", i)}); err == nil {
			t.Fatalf("call %d succeeded against an always-failing plugin", i)
		}
	}

	if !h.Disabled() {
		t.Fatalf("plugin not disabled after %d consecutive failures", FailureBudget)
	}

	err := notifier.Notify(context.Background(), Alert{AlertID: "after"})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d consecutive failures", FailureBudget)) {
		t.Errorf("error %q does not say why it is disabled", err)
	}
}

// AC-6: a disabled plugin returns promptly rather than blocking whatever was
// calling it.
func TestDisabledPluginDoesNotBlockPipeline(t *testing.T) {
	h := newTestHost(t, "always-fail", func(o *HostOptions) {
		o.CallTimeout = time.Second
		o.now = func() time.Time { return time.Now().Add(time.Hour) }
	})

	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	impl, _ := h.Impl(nil)
	notifier := impl.(Notifier)

	for i := 0; i < FailureBudget; i++ {
		_ = notifier.Notify(context.Background(), Alert{AlertID: "x"})
	}

	// Once disabled, the answer must be immediate — no process start, no
	// timeout wait. Alerting continues down its other channels.
	start := time.Now()
	for i := 0; i < 50; i++ {
		if err := notifier.Notify(context.Background(), Alert{AlertID: "y"}); !errors.Is(err, ErrDisabled) {
			t.Fatalf("call %d returned %v, want ErrDisabled", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("50 calls to a disabled plugin took %s; a disabled plugin must not cost anything", elapsed)
	}
}

// AC-7: an adapter cannot put unbounded dimensions into the fact table by
// promising not to.
func TestAdapterCannotExceedCardinality(t *testing.T) {
	h := newTestHost(t, "adapter", nil)

	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The fake adapter returns two facts: one templated path, one carrying a
	// raw UUID. Only the first is within budget.
	admit := func(f RequestFact) bool {
		return !strings.Contains(f.PathTemplate, "-")
	}

	impl, err := h.Impl(admit)
	if err != nil {
		t.Fatalf("Impl: %v", err)
	}

	facts, err := impl.(Adapter).Convert(context.Background(), []byte("{}"), "application/json")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("kept %d facts, want 1 — the over-cardinality fact was not rejected", len(facts))
	}
	if facts[0].PathTemplate != "/orders/{id}" {
		t.Errorf("kept the wrong fact: %+v", facts[0])
	}
}

// An adapter host cannot even be constructed without a budget check, so the
// enforcement cannot be skipped by forgetting to pass one.
func TestAdapterRequiresACardinalityCheck(t *testing.T) {
	h := newTestHost(t, "adapter", nil)
	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := h.Impl(nil); !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("err = %v, want an adapter without a cardinality check to be refused", err)
	}
}

// AC-9: a secret config value never appears in a log line or an error.
func TestSecretConfigNeverLogged(t *testing.T) {
	var logged strings.Builder
	h := newTestHost(t, "always-fail", func(o *HostOptions) {
		o.CallTimeout = time.Second
		o.now = func() time.Time { return time.Now().Add(time.Hour) }
		o.Logger = slog.New(slog.NewTextHandler(&logged, nil))
	})

	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	impl, _ := h.Impl(nil)
	notifier := impl.(Notifier)

	var errorText strings.Builder
	for i := 0; i < FailureBudget+1; i++ {
		if err := notifier.Notify(context.Background(), Alert{AlertID: "x"}); err != nil {
			errorText.WriteString(err.Error())
		}
	}

	const secret = "super-secret-token"
	if strings.Contains(logged.String(), secret) {
		t.Errorf("a secret reached the log:\n%s", logged.String())
	}
	if strings.Contains(errorText.String(), secret) {
		t.Errorf("a secret reached an error message:\n%s", errorText.String())
	}
	// The redaction must be visible, not achieved by omitting the config
	// entirely — an operator still needs to see which plugin and what it was
	// configured with.
	if !strings.Contains(logged.String(), RedactedValue) {
		t.Errorf("the disabled-plugin log did not report a redacted config:\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), "webhook_url") {
		t.Errorf("the non-secret config was dropped instead of kept:\n%s", logged.String())
	}
}

func TestExporterRoundTrip(t *testing.T) {
	h := newTestHost(t, "exporter", nil)

	m, err := h.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m.Kind != KindExporter {
		t.Fatalf("kind = %q", m.Kind)
	}

	impl, err := h.Impl(nil)
	if err != nil {
		t.Fatalf("Impl: %v", err)
	}
	res, err := impl.(Exporter).Export(context.Background(), Batch{
		Dataset: "metrics", EventDay: "2026-01-01",
		Rows: []map[string]any{{"request_count": 3}},
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.RowsWritten != 42 || res.Destination != "s3://example/prefix" {
		t.Errorf("result = %+v", res)
	}
}

func TestMalformedResponseIsAnError(t *testing.T) {
	h := newTestHost(t, "malformed", nil)
	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	impl, _ := h.Impl(nil)
	if err := impl.(Notifier).Notify(context.Background(), Alert{AlertID: "a"}); err == nil {
		t.Fatal("malformed JSON-RPC was accepted")
	}
}

func TestPluginThatDiesOnStartIsReported(t *testing.T) {
	h := newTestHost(t, "crash-on-start", func(o *HostOptions) {
		o.CallTimeout = 2 * time.Second
	})

	if _, err := h.Start(context.Background()); err == nil {
		t.Fatal("a plugin that exits immediately was accepted")
	}
}

// A cancelled context must return promptly rather than waiting out the call
// timeout.
func TestContextCancellationReturnsPromptly(t *testing.T) {
	h := newTestHost(t, "hang", func(o *HostOptions) {
		o.CallTimeout = time.Minute
	})
	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	impl, _ := h.Impl(nil)
	start := time.Now()
	if err := impl.(Notifier).Notify(ctx, Alert{AlertID: "a"}); err == nil {
		t.Fatal("a cancelled call reported success")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

// AC-11: the guide's worked example builds and runs.
//
// The example is extracted from the published Markdown, compiled, and driven
// as a real plugin. A guide whose example does not work is worse than no
// guide, because a reader will assume their own code is at fault.
func TestPluginGuideExampleWorks(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	guidePath, err := filepath.Abs("../../docs-site/docs/writing-a-plugin.md")
	if err != nil {
		t.Fatalf("resolve guide path: %v", err)
	}
	source, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatalf("read %s: %v", guidePath, err)
	}

	example, ok := firstGoCodeBlock(string(source))
	if !ok {
		t.Fatalf("%s publishes no Go example", guidePath)
	}
	if !strings.Contains(example, "gravix.describe") || !strings.Contains(example, "gravix.notify") {
		t.Fatalf("the example does not implement the handshake and a call")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(example), 0o644); err != nil {
		t.Fatalf("write example: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fileNotifier\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	bin := filepath.Join(dir, "file-notifier")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = dir
	// GOFLAGS from the parent can carry -mod=vendor, which this throwaway
	// module has no vendor directory for.
	build.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the guide's example does not compile: %v\n%s", err, out)
	}

	// The example writes alerts.log to its working directory, which a plugin
	// inherits from the host (§5.4). Run the test from the temp directory so
	// it lands there and not in the repository.
	t.Chdir(dir)

	h := NewHost(HostOptions{
		Command:     bin,
		CallTimeout: 10 * time.Second,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer h.Close()

	m, err := h.Start(context.Background())
	if err != nil {
		t.Fatalf("the guide's example failed the handshake: %v", err)
	}
	if m.Name != "file-notifier" || m.Kind != KindNotifier {
		t.Fatalf("manifest = %+v", m)
	}

	impl, err := h.Impl(nil)
	if err != nil {
		t.Fatalf("Impl: %v", err)
	}
	if err := impl.(Notifier).Notify(context.Background(), Alert{AlertID: "a-1", RuleName: "checkout errors"}); err != nil {
		t.Fatalf("the guide's example failed a notify call: %v", err)
	}
}

// firstGoCodeBlock returns the contents of the first ```go fenced block.
func firstGoCodeBlock(markdown string) (string, bool) {
	const fence = "```go\n"
	start := strings.Index(markdown, fence)
	if start < 0 {
		return "", false
	}
	start += len(fence)
	end := strings.Index(markdown[start:], "```")
	if end < 0 {
		return "", false
	}
	return markdown[start : start+end], true
}

// The guide must state the network trust boundary plainly — §9 requires it,
// and an operator deciding whether to install third-party code needs to read
// it there rather than infer it.
func TestGuideStatesTheTrustBoundary(t *testing.T) {
	source, err := os.ReadFile("../../docs-site/docs/writing-a-plugin.md")
	if err != nil {
		t.Fatalf("read guide: %v", err)
	}
	text := string(source)

	for _, want := range []string{
		"third-party code with unrestricted network access",
		"Read a plugin's source before you install it",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the guide does not say: %q", want)
		}
	}

	// And it must publish the isolation numbers the host actually enforces.
	for _, want := range []string{"30s", "256 MB", "5 consecutive failures"} {
		if !strings.Contains(text, want) {
			t.Errorf("the guide does not publish the isolation rule %q", want)
		}
	}
}

// A restart backs off, and the backoff is capped rather than growing without
// bound.
func TestRestartBackoffIsCapped(t *testing.T) {
	h := NewHost(HostOptions{Command: "/nonexistent", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	for i := 0; i < 20; i++ {
		h.restartLocked()
	}
	wait := time.Until(h.nextRestart)
	if wait > MaxRestartBackoff+time.Second {
		t.Fatalf("backoff grew to %s, above the %s cap", wait, MaxRestartBackoff)
	}
	if wait < time.Minute {
		t.Fatalf("backoff after 20 restarts is only %s; it should have grown", wait)
	}
}

// A host that cannot start its command reports it rather than panicking.
func TestUnstartableCommandIsReported(t *testing.T) {
	h := NewHost(HostOptions{
		Command: filepath.Join(t.TempDir(), "does-not-exist"),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer h.Close()

	if _, err := h.Start(context.Background()); err == nil {
		t.Fatal("starting a nonexistent command succeeded")
	}
}

func TestNewHostAppliesDefaults(t *testing.T) {
	h := NewHost(HostOptions{Command: "x"})
	if h.opts.CallTimeout != DefaultCallTimeout {
		t.Errorf("CallTimeout = %s, want %s", h.opts.CallTimeout, DefaultCallTimeout)
	}
	if h.opts.MemoryLimitMB != DefaultMemoryLimitMB {
		t.Errorf("MemoryLimitMB = %d, want %d", h.opts.MemoryLimitMB, DefaultMemoryLimitMB)
	}
	if h.opts.Logger == nil || h.opts.now == nil {
		t.Error("NewHost left a nil logger or clock")
	}
}

// The memory limit is enforced from /proc, so it works where Gravix runs and
// is a documented no-op elsewhere. Both halves are worth pinning: a limit that
// silently does nothing is worse than none, and a reader of this code should
// not have to guess which platforms it covers.
func TestMemoryLimitEnforcement(t *testing.T) {
	h := newTestHost(t, "ok", func(o *HostOptions) {
		// A limit no process can satisfy, so the check must fire if it works
		// at all on this platform.
		o.MemoryLimitMB = 0
	})
	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// NewHost turns a zero limit into the default, so set it directly.
	h.mu.Lock()
	h.opts.MemoryLimitMB = -1
	err := h.checkMemoryLocked()
	pid := h.cmd.Process.Pid
	h.mu.Unlock()

	if _, available := processRSSMB(pid); !available {
		t.Skip("/proc is unavailable on this platform; the memory limit is a documented no-op here")
	}

	if err == nil {
		t.Fatal("a process over its limit was not reported")
	}
	if !strings.Contains(err.Error(), "was restarted") || !strings.Contains(err.Error(), "fake-notifier") {
		t.Errorf("error %q does not name the plugin and what happened", err)
	}
}

func TestProcessRSSReportsUnavailableForAnAbsentProcess(t *testing.T) {
	// A pid that cannot exist: the reader must report "unknown" rather than
	// a fabricated zero, or a missing process would look like a compliant one.
	if _, ok := processRSSMB(-1); ok {
		t.Fatal("processRSSMB reported a size for an impossible pid")
	}
}

// A call against a host whose subprocess is gone reports a crash rather than
// panicking on a nil pipe.
func TestCallWithNoRunningProcess(t *testing.T) {
	h := newTestHost(t, "ok", func(o *HostOptions) {
		o.now = func() time.Time { return time.Now().Add(time.Hour) }
	})
	if _, err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	h.mu.Lock()
	h.killLocked()
	h.mu.Unlock()

	// The host restarts it, because the backoff window has passed.
	if _, err := h.Call(context.Background(), describeMethod, nil); err != nil {
		t.Fatalf("a host with a dead subprocess did not restart it: %v", err)
	}
}

// Impl refuses a kind it does not know, rather than returning something that
// fails later at the call site.
func TestImplRejectsAnUnknownKind(t *testing.T) {
	h := NewHost(HostOptions{Command: "x", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	h.manifest = Manifest{Name: "odd", Kind: "transform"}

	if _, err := h.Impl(nil); !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("err = %v, want ErrManifestInvalid", err)
	}
}
