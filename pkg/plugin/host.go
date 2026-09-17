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
	"strconv"
	"strings"
	"sync"
	"time"
)

// jsonrpcVersion is the only version this host speaks.
const jsonrpcVersion = "2.0"

// describeMethod is the handshake: every plugin answers it with its Manifest.
const describeMethod = "gravix.describe"

// Method names for each kind's single operation.
const (
	notifyMethod  = "gravix.notify"
	exportMethod  = "gravix.export"
	convertMethod = "gravix.convert"
)

// rpcRequest is a JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("plugin returned error %d: %s", e.Code, e.Message)
}

// HostOptions configures a subprocess plugin host.
type HostOptions struct {
	// Command and Args launch the plugin.
	Command string
	Args    []string
	// Config is passed to the plugin on each call. Secret-typed values are
	// redacted before anything about this host is logged.
	Config map[string]any
	// CallTimeout defaults to DefaultCallTimeout.
	CallTimeout time.Duration
	// MemoryLimitMB defaults to DefaultMemoryLimitMB.
	MemoryLimitMB int
	// Logger receives host events. Defaults to slog.Default().
	Logger *slog.Logger
	// now is injectable so tests can drive backoff without sleeping.
	now func() time.Time
}

// Host runs one plugin as a subprocess and speaks JSON-RPC to it over stdio.
//
// Every failure mode is contained here rather than at the call site: a plugin
// that hangs, crashes, or grows without bound must never be able to stall
// alerting, export or ingestion. The host's job is to make a third party's bug
// their problem, not the pipeline's.
type Host struct {
	opts     HostOptions
	manifest Manifest

	mu           sync.Mutex
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	nextID       int
	failures     int
	disabled     bool
	restartCount int
	nextRestart  time.Time
}

// NewHost prepares a host. It does not start the subprocess; Start does.
func NewHost(opts HostOptions) *Host {
	if opts.CallTimeout <= 0 {
		opts.CallTimeout = DefaultCallTimeout
	}
	if opts.MemoryLimitMB <= 0 {
		opts.MemoryLimitMB = DefaultMemoryLimitMB
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	return &Host{opts: opts}
}

// Start launches the subprocess and performs the describe handshake.
//
// A plugin whose manifest fails validation — including an ABI mismatch — is
// killed here and never called, so an incompatible plugin costs one process
// start rather than a stream of confusing runtime errors.
func (h *Host) Start(ctx context.Context) (Manifest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.spawnLocked(); err != nil {
		return Manifest{}, err
	}

	raw, err := h.callLocked(ctx, describeMethod, nil)
	if err != nil {
		h.killLocked()
		return Manifest{}, err
	}

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		h.killLocked()
		return Manifest{}, fmt.Errorf("%w: describe response is not a manifest: %v", ErrManifestInvalid, err)
	}
	if err := m.Validate(); err != nil {
		h.killLocked()
		return Manifest{}, err
	}

	h.manifest = m
	return m, nil
}

// Manifest returns what the plugin declared at startup.
func (h *Host) Manifest() Manifest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.manifest
}

// Disabled reports whether the failure budget has been spent.
func (h *Host) Disabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.disabled
}

// Failures returns the current consecutive-failure count.
func (h *Host) Failures() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failures
}

// Close stops the subprocess.
func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.killLocked()
	return nil
}

// spawnLocked starts the subprocess. The caller holds h.mu.
func (h *Host) spawnLocked() error {
	cmd := exec.Command(h.opts.Command, h.opts.Args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("plugin: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("plugin: stdout pipe: %w", err)
	}
	// A plugin's stderr is its own; forwarding it to ours keeps a crash
	// diagnosable without the plugin being able to corrupt the RPC stream.
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("plugin: start %s: %w", h.opts.Command, err)
	}

	h.cmd = cmd
	h.stdin = stdin
	h.stdout = bufio.NewReader(stdout)
	return nil
}

// killLocked terminates the subprocess if one is running.
func (h *Host) killLocked() {
	if h.cmd == nil || h.cmd.Process == nil {
		return
	}
	_ = h.cmd.Process.Kill()
	_, _ = h.cmd.Process.Wait()
	h.cmd = nil
	h.stdin = nil
	h.stdout = nil
}

// Call invokes a method, applying the timeout, the memory limit, the failure
// budget and the restart policy.
func (h *Host) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.disabled {
		return nil, fmt.Errorf("%w: plugin %q disabled after %d consecutive failures",
			ErrDisabled, h.name(), FailureBudget)
	}

	if h.cmd == nil {
		if wait := h.nextRestart.Sub(h.opts.now()); wait > 0 {
			return nil, fmt.Errorf("%w: plugin %q is backing off for %s", ErrCrashed, h.name(), wait.Round(time.Second))
		}
		if err := h.spawnLocked(); err != nil {
			h.recordFailureLocked()
			return nil, err
		}
	}

	if err := h.checkMemoryLocked(); err != nil {
		h.restartLocked()
		h.recordFailureLocked()
		return nil, err
	}

	raw, err := h.callLocked(ctx, method, params)
	if err != nil {
		// A timeout, a dead pipe or a cancellation all leave the subprocess
		// in an unknown state: a response may still be in flight on the pipe,
		// and a reused stream would hand the next call this call's answer. So
		// the process is replaced rather than reused.
		cancelled := ctx.Err() != nil
		if cancelled || errors.Is(err, ErrTimeout) || errors.Is(err, ErrCrashed) {
			h.restartLocked()
		}
		// Cancellation is the host's own decision, not a plugin fault.
		// Counting it would let a shutdown spend a healthy plugin's budget.
		if !cancelled {
			h.recordFailureLocked()
		}
		return nil, err
	}

	h.failures = 0
	h.restartCount = 0
	return raw, nil
}

// callLocked writes one request and reads one response. The caller holds h.mu.
func (h *Host) callLocked(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if h.stdin == nil || h.stdout == nil {
		return nil, fmt.Errorf("%w: plugin %q is not running", ErrCrashed, h.name())
	}

	h.nextID++
	req := rpcRequest{JSONRPC: jsonrpcVersion, ID: h.nextID, Method: method, Params: params}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("plugin: encode request: %w", err)
	}
	if _, err := h.stdin.Write(append(body, '\n')); err != nil {
		return nil, fmt.Errorf("%w: writing to plugin %q: %v", ErrCrashed, h.name(), err)
	}

	// The read runs in a goroutine so the timeout can fire even when the
	// plugin never answers — a blocked Read would otherwise hold the call
	// forever, which is exactly the failure the timeout exists to prevent.
	type readResult struct {
		line []byte
		err  error
	}
	done := make(chan readResult, 1)
	// The reader is captured before the goroutine starts. Once the call
	// returns early — on a timeout or a cancellation — killLocked clears
	// h.stdout while this goroutine is still blocked in Read, so reading the
	// field from inside the goroutine would be a data race on the host.
	reader := h.stdout
	go func() {
		line, err := reader.ReadBytes('\n')
		done <- readResult{line: line, err: err}
	}()

	timeout := h.opts.CallTimeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("%w: plugin %q timed out after %s", ErrTimeout, h.name(), timeout)
	case res := <-done:
		if res.err != nil {
			return nil, fmt.Errorf("%w: reading from plugin %q: %v", ErrCrashed, h.name(), res.err)
		}
		var resp rpcResponse
		if err := json.Unmarshal(res.line, &resp); err != nil {
			return nil, fmt.Errorf("plugin %q returned malformed JSON-RPC: %w", h.name(), err)
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// restartLocked kills the subprocess and schedules the next attempt with
// exponential backoff, capped at MaxRestartBackoff.
func (h *Host) restartLocked() {
	h.killLocked()
	h.restartCount++

	backoff := time.Second << uint(min(h.restartCount-1, 10))
	if backoff > MaxRestartBackoff {
		backoff = MaxRestartBackoff
	}
	h.nextRestart = h.opts.now().Add(backoff)
}

// recordFailureLocked counts a failure and disables the plugin once the budget
// is spent.
func (h *Host) recordFailureLocked() {
	h.failures++
	if h.failures >= FailureBudget && !h.disabled {
		h.disabled = true
		h.killLocked()
		// The configuration goes through RedactConfig so a secret cannot
		// reach a log line by way of a diagnostic.
		h.opts.Logger.Error("plugin disabled after repeated failures",
			"plugin", h.name(),
			"failures", h.failures,
			"config", h.manifest.RedactConfig(h.opts.Config),
		)
	}
}

// checkMemoryLocked kills a subprocess that has exceeded its RSS limit.
//
// It reads /proc, so it is effective on Linux — where Gravix runs in
// production — and a no-op elsewhere. A limit that silently does nothing on
// the developer's laptop is worth saying out loud rather than pretending the
// check is universal.
func (h *Host) checkMemoryLocked() error {
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}

	rssMB, ok := processRSSMB(h.cmd.Process.Pid)
	if !ok {
		return nil
	}
	if rssMB <= h.opts.MemoryLimitMB {
		return nil
	}
	return fmt.Errorf("plugin %q exceeded %d MB and was restarted", h.name(), h.opts.MemoryLimitMB)
}

// processRSSMB reports a process's resident set size in MB, and whether it
// could be determined at all.
func processRSSMB(pid int) (int, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	const pageSize = 4096
	return int(pages * pageSize / (1024 * 1024)), true
}

// name is the plugin's name once known, or its command before the handshake.
func (h *Host) name() string {
	if h.manifest.Name != "" {
		return h.manifest.Name
	}
	return h.opts.Command
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─── Calling a hosted plugin through its interface ───

// hostedNotifier adapts a Host to the Notifier interface.
type hostedNotifier struct{ host *Host }

// hostedExporter adapts a Host to the Exporter interface.
type hostedExporter struct{ host *Host }

// hostedAdapter adapts a Host to the Adapter interface.
type hostedAdapter struct {
	host *Host
	// admit reports whether a fact's dimensions are within budget. It is
	// injected rather than imported so pkg/plugin does not depend on the
	// alerting or ingestion packages.
	admit func(f RequestFact) bool
}

// Impl returns the interface a hosted plugin satisfies, chosen by its
// manifest's kind, ready to hand to Registry.Register.
//
// admitFact is consulted for every fact an adapter produces. It may be nil for
// non-adapter kinds; for an adapter it must not be, because an adapter without
// a budget check is a way to put unbounded dimensions into the fact table.
func (h *Host) Impl(admitFact func(RequestFact) bool) (any, error) {
	switch h.Manifest().Kind {
	case KindNotifier:
		return &hostedNotifier{host: h}, nil
	case KindExporter:
		return &hostedExporter{host: h}, nil
	case KindAdapter:
		if admitFact == nil {
			return nil, fmt.Errorf("%w: adapter %q needs a cardinality check", ErrManifestInvalid, h.name())
		}
		return &hostedAdapter{host: h, admit: admitFact}, nil
	default:
		return nil, fmt.Errorf("%w: unknown kind %q", ErrManifestInvalid, h.Manifest().Kind)
	}
}

func (n *hostedNotifier) Notify(ctx context.Context, alert Alert) error {
	_, err := n.host.Call(ctx, notifyMethod, alert)
	return err
}

func (e *hostedExporter) Export(ctx context.Context, batch Batch) (ExportResult, error) {
	raw, err := e.host.Call(ctx, exportMethod, batch)
	if err != nil {
		return ExportResult{}, err
	}
	var res ExportResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return ExportResult{}, fmt.Errorf("plugin %q returned a malformed export result: %w", e.host.name(), err)
	}
	return res, nil
}

// convertParams is what an adapter receives.
type convertParams struct {
	Payload     []byte `json:"payload"`
	ContentType string `json:"content_type"`
}

func (a *hostedAdapter) Convert(ctx context.Context, payload []byte, contentType string) ([]RequestFact, error) {
	raw, err := a.host.Call(ctx, convertMethod, convertParams{Payload: payload, ContentType: contentType})
	if err != nil {
		return nil, err
	}

	var facts []RequestFact
	if err := json.Unmarshal(raw, &facts); err != nil {
		return nil, fmt.Errorf("plugin %q returned malformed facts: %w", a.host.name(), err)
	}

	// The budget is enforced on this side of the ABI. A plugin is third-party
	// code, so its promise to respect the cardinality bound is not evidence
	// that it did; docs/04-non-goals.md §5 is not waived by installing
	// something.
	kept := facts[:0]
	var rejected int
	for _, f := range facts {
		if !a.admit(f) {
			rejected++
			continue
		}
		kept = append(kept, f)
	}

	if rejected > 0 {
		a.host.opts.Logger.Warn("adapter produced facts exceeding the cardinality budget",
			"plugin", a.host.name(), "rejected", rejected, "accepted", len(kept))
		if len(kept) == 0 {
			return nil, fmt.Errorf("plugin %q produced a fact exceeding the cardinality budget", a.host.name())
		}
	}

	return kept, nil
}
