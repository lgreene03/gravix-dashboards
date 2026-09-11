// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/baseline"
	"github.com/lgreene/gravix-dashboards/pkg/circuitbreaker"
	"github.com/lgreene/gravix-dashboards/pkg/discovery"
	"github.com/lgreene/gravix-dashboards/pkg/lateness"
	"github.com/lgreene/gravix-dashboards/pkg/logging"
	"github.com/lgreene/gravix-dashboards/pkg/pathlearn"
	"github.com/lgreene/gravix-dashboards/pkg/ratelimit"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/lgreene/gravix-dashboards/schemas"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/protobuf/encoding/protojson"
)

const maxBodyBytes = 1 << 20 // 1 MB max request body

// DLQEntry represents a rejected fact stored in the dead letter queue.
type DLQEntry struct {
	Timestamp time.Time       `json:"timestamp"`
	TenantID  string          `json:"tenant_id,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	FactType  string          `json:"fact_type"`
	Error     string          `json:"error"`
	RawJSON   json.RawMessage `json:"raw_json"`
}

// writeDLQEntries marshals DLQ entries as JSONL and writes them to storage
// under the dlq/ topic prefix. Runs async — errors are logged but not propagated.
func writeDLQEntries(sink *DurableSink, tenantID, factType string, entries []DLQEntry) {
	if len(entries) == 0 {
		return
	}
	var records [][]byte
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			slog.Error("dlq marshal error", "error", err)
			continue
		}
		records = append(records, data)
	}
	if len(records) == 0 {
		return
	}
	topic := topicForTenant(tenantID, "dlq/request_facts")
	if err := sink.WriteBatch(topic, records); err != nil {
		slog.Error("dlq write error", "error", err)
		ingestionDLQWriteErrorsTotal.Inc()
	} else {
		slog.Info("dlq entries written", "count", len(records), "tenant_id", tenantID)
	}
}

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const tenantInfoKey contextKey = "tenantInfo"

// getTenantID extracts the tenant ID from the request context.
// Returns empty string in legacy (single-key) mode.
func getTenantID(r *http.Request) string {
	if info, ok := r.Context().Value(tenantInfoKey).(*tenantdb.APIKeyInfo); ok {
		return info.TenantID
	}
	return ""
}

// getTenantPlan extracts the tenant's plan from the request context.
// Returns empty string in legacy (single-key) mode.
func getTenantPlan(r *http.Request) string {
	if info, ok := r.Context().Value(tenantInfoKey).(*tenantdb.APIKeyInfo); ok {
		return info.Plan
	}
	return ""
}

// getTenantOverageAllowed extracts the tenant's overage_allowed flag from context.
func getTenantOverageAllowed(r *http.Request) bool {
	if info, ok := r.Context().Value(tenantInfoKey).(*tenantdb.APIKeyInfo); ok {
		return info.OverageAllowed
	}
	return false
}

// requireScope returns middleware that checks the API key has the given scope.
// In single-key mode (no tenant info), all scopes are allowed.
func requireScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if info, ok := r.Context().Value(tenantInfoKey).(*tenantdb.APIKeyInfo); ok {
			if !info.HasScope(scope) {
				writeErrorJSON(w, http.StatusForbidden, "API key missing required scope: "+scope)
				return
			}
		}
		next(w, r)
	}
}

// topicForTenant returns the storage topic prefixed with tenant ID.
// In legacy mode (empty tenantID), returns the base topic unchanged.
func topicForTenant(tenantID, baseTopic string) string {
	if tenantID == "" {
		return baseTopic
	}
	return tenantID + "/" + baseTopic
}

// unprocessableNote is returned to a caller whose fact can be stored but can
// never appear in a metric, because its event_time predates the retention window.
// Accepting such a fact silently would be the worst option available: the sender
// would have no way to know its data vanished.
const unprocessableNote = "event_time precedes the retention window; stored but not aggregated"

// latenessConfig bounds the lateness classification. It is a variable so a test
// can narrow the windows without waiting thirty days.
var latenessConfig = lateness.DefaultConfig()

// futureClockWarn rate-limits the wrong-clock warning to once per minute, so a
// sender with a badly set clock cannot flood the log.
var futureClockWarn struct {
	mu   sync.Mutex
	last time.Time
}

// classifyFact records how late an accepted fact was and returns its class.
// It never changes whether the fact is accepted — lateness is a property of the
// delivery, not of the fact's validity.
func classifyFact(fact *gravixv1.RequestFact, at time.Time) lateness.Class {
	eventTime := fact.EventTime.AsTime()
	class := lateness.Classify(latenessConfig, eventTime, at)
	factsByLatenessTotal.WithLabelValues(string(class), fact.Service).Inc()

	if ahead := lateness.FutureBy(latenessConfig, eventTime, at); ahead > 0 {
		warnFutureClock(fact.Service, ahead, at)
	}
	return class
}

// warnFutureClock logs at most once per minute that a sender's clock looks wrong.
func warnFutureClock(service string, ahead time.Duration, at time.Time) {
	futureClockWarn.mu.Lock()
	defer futureClockWarn.mu.Unlock()
	if at.Sub(futureClockWarn.last) < time.Minute {
		return
	}
	futureClockWarn.last = at
	slog.Warn(fmt.Sprintf("fact event_time is %s in the future; check the sender's clock", ahead),
		"service", service, "ahead", ahead.String())
}

// writeUnprocessableEntries copies facts that can never be aggregated into their
// own DLQ prefix, so an operator can find them. They are also written to the raw
// path as normal: they are valid, immutable facts.
func writeUnprocessableEntries(sink *DurableSink, tenantID string, entries []DLQEntry) {
	if len(entries) == 0 {
		return
	}
	records := make([][]byte, 0, len(entries))
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			slog.Error("unprocessable dlq marshal error", "error", err)
			continue
		}
		records = append(records, data)
	}
	if len(records) == 0 {
		return
	}
	topic := topicForTenant(tenantID, "dlq/unprocessable")
	if err := sink.WriteBatch(topic, records); err != nil {
		slog.Error("unprocessable dlq write error", "error", err)
		ingestionDLQWriteErrorsTotal.Inc()
	}
}

// unprocessableEntry builds the DLQ record for a fact that cannot be aggregated.
func unprocessableEntry(tenantID, reqID string, at time.Time, raw []byte) DLQEntry {
	return DLQEntry{
		Timestamp: at,
		TenantID:  tenantID,
		RequestID: reqID,
		FactType:  "request_fact",
		Error:     unprocessableNote,
		RawJSON:   json.RawMessage(raw),
	}
}

// writeErrorJSON writes a structured JSON error response.
func writeErrorJSON(w http.ResponseWriter, code int, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": errMsg,
		"code":  code,
	})
}

var (
	ingestionRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingestion_requests_total",
			Help: "Total number of ingestion requests.",
		},
		[]string{"path", "status", "tenant"},
	)
	ingestionBatchSizeBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingestion_batch_size_bytes",
			Help:    "Size of ingestion batches written to disk.",
			Buckets: prometheus.ExponentialBuckets(100, 10, 6),
		},
		[]string{"topic"},
	)
	ingestionFsyncDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingestion_fsync_duration_seconds",
			Help:    "Duration of fsync operations.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"topic"},
	)
	ingestionDLQWriteErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ingestion_dlq_write_errors_total",
			Help: "Total dead letter queue write failures.",
		},
	)
	ingestionUploadErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ingestion_upload_errors_total",
			Help: "Total storage upload failures.",
		},
	)
	ingestionOverageEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingestion_overage_events_total",
			Help: "Total events accepted over plan limit (paid plans only).",
		},
		[]string{"tenant_id"},
	)
	ingestionQuotaRejectedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingestion_quota_rejected_total",
			Help: "Total requests rejected due to quota exceeded.",
		},
		[]string{"tenant_id"},
	)
	// factsByLatenessTotal counts accepted facts by how late they were relative to
	// their own event_time. Both labels are bounded: class has exactly the four
	// values lateness.Classes() defines, and service is already a bounded
	// dimension. Nothing per-fact — no path_template, no request id — may be added
	// here; docs/04-non-goals.md forbids high cardinality in our own telemetry as
	// much as in the product.
	factsByLatenessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gravix_facts_received_by_lateness_total",
			Help: "Accepted facts by lateness class and service.",
		},
		[]string{"class", "service"},
	)

	ingestionTraceSamplesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingestion_trace_samples_total",
			Help: "Total trace samples received.",
		},
		[]string{"tenant", "sampled"},
	)
	circuitBreakerState = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Current circuit breaker state (0=closed, 1=open, 2=half_open).",
		},
		func() float64 { return circuitBreakerStateValue.Load().(float64) },
	)
	circuitBreakerTripsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "circuit_breaker_trips_total",
			Help: "Total number of times the circuit breaker has tripped open.",
		},
	)
)

// circuitBreakerStateValue stores the current CB state as a float64 for the gauge.
var circuitBreakerStateValue atomic.Value

func init() {
	circuitBreakerStateValue.Store(float64(0))
	prometheus.MustRegister(ingestionRequestsTotal)
	prometheus.MustRegister(ingestionBatchSizeBytes)
	prometheus.MustRegister(collectors.NewBuildInfoCollector())
	prometheus.MustRegister(ingestionFsyncDurationSeconds)
	prometheus.MustRegister(ingestionDLQWriteErrorsTotal)
	prometheus.MustRegister(ingestionUploadErrorsTotal)
	prometheus.MustRegister(ingestionOverageEventsTotal)
	prometheus.MustRegister(ingestionQuotaRejectedTotal)
	prometheus.MustRegister(factsByLatenessTotal)
	prometheus.MustRegister(ingestionTraceSamplesTotal)
	prometheus.MustRegister(circuitBreakerState)
	prometheus.MustRegister(circuitBreakerTripsTotal)
}

func tenantRateLimitMiddleware(trl *ratelimit.TenantLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := getTenantID(r)
		plan := getTenantPlan(r)
		if !trl.Allow(tenantID, plan) {
			writeErrorJSON(w, http.StatusTooManyRequests, "rate limit exceeded, try again later")
			return
		}
		next(w, r)
	}
}

// DurableSink provides fsync-backed appends and async background uploads.
type DurableSink struct {
	bufferDir string              // e.g. /tmp/buffer/
	store     storage.ObjectStore // The abstracted storage (Local or S3)

	activeFiles map[string]*os.File
	mu          sync.Mutex
	uploadWg    sync.WaitGroup // tracks in-flight upload goroutines

	ctx    context.Context
	cancel context.CancelFunc

	cb             *circuitbreaker.CircuitBreaker
	maxBufferBytes int64 // max buffer size before returning 503
}

func NewDurableSink(bufferDir string, store storage.ObjectStore, cb *circuitbreaker.CircuitBreaker, maxBufferBytes int64) (*DurableSink, error) {
	if err := os.MkdirAll(bufferDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create buffer dir: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ds := &DurableSink{
		bufferDir:      bufferDir,
		store:          store,
		activeFiles:    make(map[string]*os.File),
		ctx:            ctx,
		cancel:         cancel,
		cb:             cb,
		maxBufferBytes: maxBufferBytes,
	}

	// Startup: Check for any previously rotated but not uploaded files
	go ds.startupScan()

	// Background: File Rotation & Upload Loop
	go ds.backgroundRotationLoop()

	// Background: Periodic retry for failed uploads
	go ds.retryLoop()

	return ds, nil
}

// Write appends data to the active buffer file and fsyncs.
// Topic is used as directory/prefix.
func (ds *DurableSink) Write(topic string, data []byte) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	f, ok := ds.activeFiles[topic]
	if !ok {
		// Ensure topic dir exists in buffer
		topicDir := filepath.Join(ds.bufferDir, topic)
		if err := os.MkdirAll(topicDir, 0755); err != nil {
			return fmt.Errorf("failed to create topic buffer dir: %w", err)
		}

		// Open current.jsonl in append mode
		path := filepath.Join(topicDir, "current.jsonl")
		var err error
		f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open buffer file %s: %w", path, err)
		}
		ds.activeFiles[topic] = f
	}

	// Append Data + Newline
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write error: %w", err)
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		return fmt.Errorf("newline write error: %w", err)
	}

	// CRITICAL: Fsync for Durability
	syncStart := time.Now()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync error: %w", err)
	}
	ingestionFsyncDurationSeconds.WithLabelValues(topic).Observe(time.Since(syncStart).Seconds())

	return nil
}

// WriteBatch writes multiple records to the buffer in a single fsync call.
func (ds *DurableSink) WriteBatch(topic string, records [][]byte) error {
	if len(records) == 0 {
		return nil
	}
	ds.mu.Lock()
	defer ds.mu.Unlock()

	f, ok := ds.activeFiles[topic]
	if !ok {
		topicDir := filepath.Join(ds.bufferDir, topic)
		if err := os.MkdirAll(topicDir, 0755); err != nil {
			return fmt.Errorf("failed to create topic buffer dir: %w", err)
		}
		path := filepath.Join(topicDir, "current.jsonl")
		var err error
		f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open buffer file %s: %w", path, err)
		}
		ds.activeFiles[topic] = f
	}

	// Write all records, then fsync once
	for _, data := range records {
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("write error: %w", err)
		}
		if _, err := f.Write([]byte("\n")); err != nil {
			return fmt.Errorf("newline write error: %w", err)
		}
	}

	syncStart := time.Now()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync error: %w", err)
	}
	ingestionFsyncDurationSeconds.WithLabelValues(topic).Observe(time.Since(syncStart).Seconds())

	return nil
}

func (ds *DurableSink) Close() error {
	ds.cancel()
	// Final rotation to flush any buffered data before shutdown
	ds.rotateAll()
	// Wait for all in-flight uploads to finish (with timeout)
	done := make(chan struct{})
	go func() {
		ds.uploadWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("all uploads completed before shutdown")
	case <-time.After(15 * time.Second):
		slog.Warn("timed out waiting for uploads to finish during shutdown")
	}
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for _, f := range ds.activeFiles {
		f.Close()
	}
	return nil
}

// retryLoop periodically scans for failed uploads and retries them.
func (ds *DurableSink) retryLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ds.ctx.Done():
			return
		case <-ticker.C:
			ds.startupScan()
		}
	}
}

// backgroundRotationLoop runs every minute to rotate active files
func (ds *DurableSink) backgroundRotationLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ds.ctx.Done():
			return
		case <-ticker.C:
			ds.rotateAll()
		}
	}
}

// rotateAll closes current files, renames them, and triggers upload
func (ds *DurableSink) rotateAll() {
	ds.mu.Lock()
	// Copy topic list to avoid holding lock during upload if possible,
	// but we need to rotate safely.
	topics := make([]string, 0, len(ds.activeFiles))
	for t := range ds.activeFiles {
		topics = append(topics, t)
	}
	ds.mu.Unlock()

	for _, topic := range topics {
		ds.rotateTopic(topic)
	}
}

// rotateTopic performs safe rotation
func (ds *DurableSink) rotateTopic(topic string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	f, ok := ds.activeFiles[topic]
	if !ok {
		return
	}

	// 1. Close current
	f.Close()
	delete(ds.activeFiles, topic)

	// 2. Rename to batch_<ts>_<uuid>.jsonl
	topicDir := filepath.Join(ds.bufferDir, topic)
	currentPath := filepath.Join(topicDir, "current.jsonl")

	// Check if file has data (size > 0)
	info, err := os.Stat(currentPath)
	if err == nil && info.Size() == 0 {
		return // Empty file, skip rotation
	}

	timestamp := time.Now().UTC().Format("20060102150405")
	fileID := uuid.New().String()
	batchName := fmt.Sprintf("batch_%s_%s.jsonl", timestamp, fileID)
	batchPath := filepath.Join(topicDir, batchName)

	if err := os.Rename(currentPath, batchPath); err != nil {
		slog.Error("error rotating file", "path", currentPath, "error", err)
		return
	}

	// 3. Trigger Upload (Async from the lock, but we call it here for MVP simplicity)
	ds.uploadWg.Add(1)
	go func() {
		defer ds.uploadWg.Done()
		ds.uploadFile(topic, batchPath, time.Now().UTC())
	}()
}

// localStoreRoot is the directory the local object store is rooted at for a
// given --base-dir.
//
// It is the data root itself, NOT <base-dir>/raw. uploadFile's destination
// keys already begin with "raw/", and every reader in the repository resolves
// that same "raw/<topic>/" layout beneath the data root: the rollup jobs
// default to ./data/raw/request_facts, cmd/purge and cmd/cli recompute root
// their stores at ./data, and the gateway's DLQ endpoints list
// "dlq/request_facts/" under ./data/raw.
//
// Rooting the store at <base-dir>/raw instead put the "raw/" from the key on
// top of it, so facts landed in <base-dir>/raw/raw/<topic>/ — a directory no
// reader ever looks in. The rollup found no input, produced no metrics, and
// the dashboard stayed empty with every service reporting healthy. See F-015.
func localStoreRoot(baseDir string) string {
	return baseDir
}

// uploadFile uploads the local batch to the object store, wrapped in a circuit breaker.
func (ds *DurableSink) uploadFile(topic, sourcePath string, t time.Time) {
	// Destination Key: raw/<topic>/YYYY-MM-DD/HH/<uuid>.jsonl
	dayStr := t.Format("2006-01-02")
	hourStr := t.Format("15")
	destKey := fmt.Sprintf("raw/%s/%s/%s/%s", topic, dayStr, hourStr, filepath.Base(sourcePath))

	f, err := os.Open(sourcePath)
	if err != nil {
		slog.Error("error opening source file", "path", sourcePath, "error", err)
		return
	}
	defer f.Close()

	uploadFn := func() error {
		return ds.store.Put(ds.ctx, destKey, f)
	}

	if ds.cb != nil {
		err = ds.cb.Execute(uploadFn)
	} else {
		err = uploadFn()
	}

	if err != nil {
		slog.Error("error uploading to storage, file preserved for retry", "path", sourcePath, "error", err)
		ingestionUploadErrorsTotal.Inc()
		return // Do NOT delete the local file — it will be retried on next startup scan
	}

	// Upload succeeded — safe to delete the local batch
	if err := os.Remove(sourcePath); err != nil {
		slog.Warn("uploaded but failed to remove local file", "path", sourcePath, "error", err)
	}
	slog.Info("uploaded to storage", "path", sourcePath, "key", destKey)
}

// bufferSizeBytes returns the total size of all files in the buffer directory.
func (ds *DurableSink) bufferSizeBytes() int64 {
	var total int64
	filepath.Walk(ds.bufferDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// IsBufferFull returns true if the buffer has exceeded the max allowed size.
func (ds *DurableSink) IsBufferFull() bool {
	if ds.maxBufferBytes <= 0 {
		return false
	}
	return ds.bufferSizeBytes() > ds.maxBufferBytes
}

// startupScan checks for any leftover batch files in buffer and uploads them
func (ds *DurableSink) startupScan() {
	// Walk buffer dir
	err := filepath.Walk(ds.bufferDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(path) == "current.jsonl" {
			return nil
		} // Ignore active file

		// Found a batch file!
		// Infer topic from parent dir name
		dir := filepath.Dir(path)
		topic := filepath.Base(dir)

		slog.Info("found orphaned batch file", "path", path)
		// Upload using file mod time as heuristic
		ds.uploadFile(topic, path, info.ModTime().UTC())
		return nil
	})
	if err != nil {
		slog.Error("startup scan error", "error", err)
	}
}

// shouldSampleTrace performs deterministic head-based sampling using trace_id.
// All spans from the same trace will get the same decision.
func shouldSampleTrace(traceID string, rate float64) bool {
	if rate >= 1.0 {
		return true
	}
	if rate <= 0 {
		return false
	}
	h := sha256.Sum256([]byte(traceID))
	// Use first 4 bytes as a uint32 to get a uniform distribution
	n := binary.BigEndian.Uint32(h[:4])
	threshold := uint32(rate * float64(1<<32-1))
	return n <= threshold
}

// getTraceSampleRate reads TRACE_SAMPLE_RATE from env (default 0.01 = 1%).
func getTraceSampleRate() float64 {
	s := os.Getenv("TRACE_SAMPLE_RATE")
	if s == "" {
		return 0.01
	}
	rate, err := strconv.ParseFloat(s, 64)
	if err != nil || rate < 0 || rate > 1 {
		slog.Warn("invalid TRACE_SAMPLE_RATE, using default 0.01", "value", s)
		return 0.01
	}
	return rate
}

func handleTraces(sink *DurableSink, tdb tenantdb.DB, sampleRate float64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")
			return
		}
		if !requireJSON(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
			return
		}
		defer r.Body.Close()

		ts, err := schemas.ParseTraceSample(body)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("invalid TraceSample: %v", err))
			return
		}

		tenantID := getTenantID(r)

		// Deterministic sampling: all spans from the same trace get the same decision
		if !shouldSampleTrace(ts.TraceId, sampleRate) {
			ingestionTraceSamplesTotal.WithLabelValues(tenantID, "false").Inc()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"sampled": false})
			return
		}

		marshalOpts := protojson.MarshalOptions{UseProtoNames: true}
		cleanData, err := marshalOpts.Marshal(ts)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "failed to marshal trace sample")
			return
		}

		topic := topicForTenant(tenantID, "trace_samples")
		if err := sink.Write(topic, cleanData); err != nil {
			slog.Error("sink write error for trace", "error", err)
			writeErrorJSON(w, http.StatusInternalServerError, "failed to persist trace sample")
			return
		}

		ingestionTraceSamplesTotal.WithLabelValues(tenantID, "true").Inc()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{"sampled": true})
	}
}

func main() {
	logging.Init("ingestion")

	port := flag.Int("port", 8080, "HTTP port")
	baseDir := flag.String("base-dir", "./data", "Base directory for buffer and raw storage")
	discoveryDB := flag.String("discovery-db", "", "Path to the service discovery database (default <base-dir>/discovery.db)")
	warehouseDir := flag.String("warehouse-dir", "", "Path to the rollup warehouse, read for alert baselines (default <base-dir>/warehouse)")
	flag.Parse()

	// Auth mode: multi-tenant (TENANT_DB_PATH) or legacy (API_KEY)
	var tdb tenantdb.DB
	var authMW func(http.HandlerFunc) http.HandlerFunc

	tenantDBPath := os.Getenv("TENANT_DB_PATH")
	apiKey := os.Getenv("API_KEY")

	// Validate API key minimum length in legacy mode
	if tenantDBPath == "" && apiKey != "" && len(apiKey) < 16 {
		slog.Error("API_KEY must be at least 16 characters")
		os.Exit(1)
	}

	// Validate S3 endpoint URL if set
	if s3ep := os.Getenv("S3_ENDPOINT"); s3ep != "" && !strings.HasPrefix(s3ep, "http") {
		slog.Error("S3_ENDPOINT must be a valid HTTP URL", "value", s3ep)
		os.Exit(1)
	}

	if dbDriver := os.Getenv("DB_DRIVER"); dbDriver != "" {
		var err error
		tdb, err = tenantdb.OpenFromEnv()
		if err != nil {
			slog.Error("failed to open tenant database", "error", err)
			os.Exit(1)
		}
		defer tdb.Close()
		authMW = func(next http.HandlerFunc) http.HandlerFunc {
			return multiTenantAuthMiddleware(tdb.APIKeys(), next)
		}
		slog.Info("multi-tenant auth enabled", "driver", dbDriver)
	} else if tenantDBPath != "" {
		var err error
		tdb, err = tenantdb.Open(tenantDBPath)
		if err != nil {
			slog.Error("failed to open tenant database", "error", err)
			os.Exit(1)
		}
		defer tdb.Close()
		authMW = func(next http.HandlerFunc) http.HandlerFunc {
			return multiTenantAuthMiddleware(tdb.APIKeys(), next)
		}
		slog.Info("multi-tenant auth enabled", "db", tenantDBPath)
	} else if apiKey != "" {
		authMW = func(next http.HandlerFunc) http.HandlerFunc {
			return authMiddleware(apiKey, next)
		}
		slog.Info("legacy single-key auth enabled")
	} else {
		slog.Error("set TENANT_DB_PATH (multi-tenant) or API_KEY (legacy) to enable authentication")
		os.Exit(1)
	}

	bufferDir := filepath.Join(*baseDir, "buffer")

	var store storage.ObjectStore
	if os.Getenv("S3_ENDPOINT") != "" {
		slog.Info("initializing S3/MinIO storage")
		var err error
		store, err = storage.NewS3Store(
			context.Background(),
			os.Getenv("S3_ENDPOINT"),
			os.Getenv("S3_REGION"),
			os.Getenv("S3_BUCKET"),
			os.Getenv("S3_ACCESS_KEY"),
			os.Getenv("S3_SECRET_KEY"),
		)
		if err != nil {
			slog.Error("failed to initialize S3 store", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Info("initializing local storage", "dir", localStoreRoot(*baseDir))
		var err error
		store, err = storage.NewLocalStore(localStoreRoot(*baseDir))
		if err != nil {
			slog.Error("failed to initialize local store", "error", err)
			os.Exit(1)
		}
	}

	// Circuit breaker for S3 uploads
	cb := circuitbreaker.New(circuitbreaker.Options{
		FailureThreshold: getEnvInt("CB_FAILURE_THRESHOLD", 5),
		ResetTimeout:     time.Duration(getEnvInt("CB_RESET_TIMEOUT_SEC", 30)) * time.Second,
		OnStateChange: func(from, to circuitbreaker.State) {
			slog.Warn("circuit breaker state change", "from", from.String(), "to", to.String())
			circuitBreakerStateValue.Store(float64(to))
			if to == circuitbreaker.StateOpen {
				circuitBreakerTripsTotal.Inc()
			}
		},
	})

	maxBufferMB := int64(getEnvInt("MAX_BUFFER_SIZE_MB", 500))

	slog.Info("initializing durable sink", "buffer_dir", bufferDir, "max_buffer_mb", maxBufferMB)
	sink, err := NewDurableSink(bufferDir, store, cb, maxBufferMB*1024*1024)
	if err != nil {
		slog.Error("failed to create sink", "error", err)
		os.Exit(1)
	}
	defer sink.Close()

	// Services register themselves by sending a fact. This is why there is no
	// registration step to forget: the list cannot drift from what is actually
	// reporting. It is deliberately independent of the tenant database, so it
	// works the same in legacy single-key mode.
	discoveryPath := *discoveryDB
	if discoveryPath == "" {
		discoveryPath = filepath.Join(*baseDir, "discovery.db")
	}
	reg, err := discovery.Open(discoveryPath)
	if err != nil {
		slog.Error("failed to open discovery registry", "error", err, "path", discoveryPath)
		os.Exit(1)
	}
	defer reg.Close()

	warehousePath := *warehouseDir
	if warehousePath == "" {
		warehousePath = filepath.Join(*baseDir, "warehouse")
	}

	// Normalizes obviously-dynamic path segments so a naive framework
	// integration does not lose every fact to the DLQ, and bounds the
	// cardinality of everything it cannot classify. In-memory and per-process:
	// see SD-016 for what that means under a scaled deployment.
	learner := pathlearn.NewLearner(
		pathlearn.DefaultSegmentDistinctBudget,
		pathlearn.DefaultTemplateBudgetPerService)

	// Per-tenant rate limiting (fallback 100/s for legacy mode)
	trl := ratelimit.NewTenantLimiter(100, 200)
	defer trl.Close()

	// Trace sampling rate
	traceSampleRate := getTraceSampleRate()
	slog.Info("trace sampling configured", "rate", traceSampleRate)

	// Buffer-full middleware: reject new data when buffer exceeds limit
	bufferCheck := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if sink.IsBufferFull() {
				w.Header().Set("Retry-After", "30")
				writeErrorJSON(w, http.StatusServiceUnavailable, "buffer capacity exceeded, try again later")
				return
			}
			next(w, r)
		}
	}

	// Wrap handlers: auth first (sets tenant context), then scope check, rate limit, buffer check, handler
	http.Handle("/api/v1/facts", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("ingest:write", handleFacts(sink, tdb, reg, learner))))))
	http.Handle("/api/v1/facts/batch", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("ingest:write", handleBatchFacts(sink, tdb, reg, learner))))))
	http.Handle("/api/v1/events", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("ingest:write", handleEvents(sink, tdb))))))
	http.Handle("/api/v1/traces", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("traces:write", handleTraces(sink, tdb, traceSampleRate))))))
	http.Handle("/v1/traces", authMW(tenantRateLimitMiddleware(trl, bufferCheck(requireScope("traces:write", handleOTLPTraces(sink))))))
	http.Handle("/api/v1/deploy", authMW(tenantRateLimitMiddleware(trl, bufferCheck(handleDeployWebhook(sink, tdb)))))
	// Read-only and off the ingest path, so it carries neither the rate limiter
	// nor the buffer check: a full buffer is precisely when someone needs to see
	// what is reporting.
	http.Handle("/api/v1/services", authMW(requireScope("admin:read", handleServices(reg))))
	// Thresholds a user never has to choose, derived from their own traffic.
	// Read and write are separate scopes: seeing what would be proposed is not
	// the same permission as arming it.
	http.Handle("/api/v1/alert-proposals", authMW(requireScope("admin:read", handleAlertProposals(warehousePath))))
	http.Handle("/api/v1/alert-proposals/arm", authMW(requireScope("admin:write", handleArmAlertProposal(warehousePath, tdb))))

	http.Handle("/metrics", promhttp.Handler())

	http.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("up"))
	})

	http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := map[string]string{
			"db":      "ok",
			"storage": "ok",
			"circuit": cb.State().String(),
		}
		if sink == nil {
			status["storage"] = "unavailable"
		}
		if cb.State() == circuitbreaker.StateOpen {
			status["storage"] = "degraded"
		}
		if sink == nil || cb.State() == circuitbreaker.StateOpen {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		json.NewEncoder(w).Encode(status)
	})

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:           addr,
		Handler:        logging.RequestIDMiddleware(securityHeadersMiddleware(http.DefaultServeMux)),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB max header size
	}

	// Graceful shutdown: listen for SIGINT/SIGTERM
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-shutdownCh
		slog.Info("received signal, draining connections", "signal", sig, "timeout", "10s")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("http shutdown error", "error", err)
		}
	}()

	slog.Info("starting ingestion service", "addr", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("listen and serve error", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped gracefully")
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")

		if r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// authMiddleware checks for X-API-Key header. API key is always required.
func authMiddleware(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqKey := r.Header.Get("X-API-Key")
		if subtle.ConstantTimeCompare([]byte(reqKey), []byte(apiKey)) != 1 {
			writeErrorJSON(w, http.StatusUnauthorized, "invalid or missing X-API-Key header")
			return
		}
		next(w, r)
	}
}

// multiTenantAuthMiddleware validates API keys against the tenant database
// and stores tenant info in the request context.
func multiTenantAuthMiddleware(keyRepo tenantdb.APIKeyRepo, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqKey := r.Header.Get("X-API-Key")
		if reqKey == "" {
			writeErrorJSON(w, http.StatusUnauthorized, "missing X-API-Key header")
			return
		}

		info, err := keyRepo.ValidateKey(r.Context(), reqKey)
		if err != nil {
			writeErrorJSON(w, http.StatusUnauthorized, "invalid or missing X-API-Key header")
			return
		}

		ctx := context.WithValue(r.Context(), tenantInfoKey, info)
		next(w, r.WithContext(ctx))
	}
}

// requireJSON checks Content-Type header contains application/json.
// Returns true if valid, false (and writes 415 response) if invalid.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" || !strings.Contains(ct, "application/json") {
		writeErrorJSON(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}

func handleFacts(sink *DurableSink, tdb tenantdb.DB, reg *discovery.Registry, learner *pathlearn.Learner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")
			return
		}
		if !requireJSON(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
			return
		}
		defer r.Body.Close()

		tenantID := getTenantID(r)

		// Check quota before processing
		if checkQuota(tdb, tenantID, getTenantPlan(r), getTenantOverageAllowed(r)) {
			writeErrorJSON(w, http.StatusTooManyRequests, "monthly event quota exceeded — upgrade your plan")
			ingestionRequestsTotal.WithLabelValues("/api/v1/facts", "429", tenantID).Inc()
			return
		}

		reqID := logging.GetRequestID(r.Context())

		// Decode first, normalize, then validate. The rules are unchanged and
		// still run — they just run on a path_template whose dynamic segments
		// have been collapsed, so an integration reporting /users/42 is fixed
		// rather than silently sent to the DLQ.
		fact, err := schemas.UnmarshalRequestFactUnvalidated(body)
		if err != nil {
			go writeDLQEntries(sink, tenantID, "request_fact", []DLQEntry{{
				Timestamp: time.Now().UTC(),
				TenantID:  tenantID,
				RequestID: reqID,
				FactType:  "request_fact",
				Error:     err.Error(),
				RawJSON:   json.RawMessage(body),
			}})
			writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("invalid RequestFact: %v", err))
			return
		}

		decision := learner.Learn(fact.Service, fact.Method, fact.PathTemplate)
		if !decision.Accepted {
			go writeDLQEntries(sink, tenantID, "request_fact", []DLQEntry{{
				Timestamp: time.Now().UTC(),
				TenantID:  tenantID,
				RequestID: reqID,
				FactType:  "request_fact",
				Error:     decision.Reason,
				RawJSON:   json.RawMessage(body),
			}})
			ingestionRequestsTotal.WithLabelValues("/api/v1/facts", "400", tenantID).Inc()
			writeErrorJSON(w, http.StatusBadRequest, decision.Reason)
			return
		}
		fact.PathTemplate = decision.Template

		if err := schemas.ValidateRequestFact(fact); err != nil {
			go writeDLQEntries(sink, tenantID, "request_fact", []DLQEntry{{
				Timestamp: time.Now().UTC(),
				TenantID:  tenantID,
				RequestID: reqID,
				FactType:  "request_fact",
				Error:     err.Error(),
				RawJSON:   json.RawMessage(body),
			}})
			writeErrorJSON(w, http.StatusBadRequest,
				fmt.Sprintf("invalid RequestFact: validation error: %v", err))
			return
		}

		marshalOpts := protojson.MarshalOptions{UseProtoNames: true}
		cleanData, err := marshalOpts.Marshal(fact)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "failed to marshal fact")
			return
		}
		topic := topicForTenant(tenantID, "request_facts")
		if err := sink.Write(topic, cleanData); err != nil {
			slog.Error("sink write error", "error", err)
			ingestionRequestsTotal.WithLabelValues("/api/v1/facts", "500", tenantID).Inc()
			writeErrorJSON(w, http.StatusInternalServerError, "failed to persist fact")
			return
		}

		// Recorded only after the write succeeded, so the registry never claims a
		// service whose fact was not persisted. In-memory and lock-only: no disk
		// I/O between here and the 201.
		reg.RecordFact(fact.Service, fact.EventTime.AsTime())
		reg.RecordTemplate(fact.Service, fact.Method, fact.PathTemplate, fact.EventTime.AsTime())

		// Increment event counter for billing (best-effort, non-blocking)
		incrementEventCounter(tdb, tenantID, 1)

		// The fact is stored either way. Classification decides only what we report
		// back and what we count, never whether it was accepted.
		class := classifyFact(fact, time.Now().UTC())

		ingestionBatchSizeBytes.WithLabelValues("request_facts").Observe(float64(len(cleanData)))

		if class == lateness.ClassUnprocessable {
			go writeUnprocessableEntries(sink, tenantID,
				[]DLQEntry{unprocessableEntry(tenantID, reqID, time.Now().UTC(), body)})

			ingestionRequestsTotal.WithLabelValues("/api/v1/facts", "202", tenantID).Inc()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"accepted": 1,
				"note":     unprocessableNote,
			})
			return
		}

		ingestionRequestsTotal.WithLabelValues("/api/v1/facts", "201", tenantID).Inc()
		w.WriteHeader(http.StatusCreated)
	}
}

// handleServices lists every service that has ever sent a fact.
//
// It answers the question a new user asks first — "is my data arriving?" —
// without waiting for the five-minute rollup that the dashboard's Cube query
// depends on. An empty registry is a 200 with an empty list, not a 404: no data
// yet is a state to render, not an error to report.
//
// Aggregate only. One row per service with three counters, never anything about
// an individual request.
func handleServices(reg *discovery.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only GET is accepted")
			return
		}

		services, err := reg.ListServices(r.Context())
		if err != nil {
			slog.Error("failed to list services", "error", err)
			writeErrorJSON(w, http.StatusInternalServerError, "failed to list services")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"services": services}); err != nil {
			slog.Error("failed to encode services", "error", err)
		}
	}
}

// baselineLookbackDays is how much history a proposal is derived from. Seven
// days covers a weekly cycle, so a service that is quiet at weekends does not
// get a traffic-drop threshold set from weekdays alone.
const baselineLookbackDays = 7

// handleAlertProposals returns ready-to-arm alert rules with thresholds already
// computed from what the user's own services actually do.
//
// A threshold nobody can choose is a threshold nobody sets: a new user does not
// know their service's normal error rate, so asking for a number yields either
// one that never fires or one that always does.
func handleAlertProposals(warehouseDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only GET is accepted")
			return
		}

		proposals, err := computeProposals(r.Context(), warehouseDir, getTenantID(r))
		if err != nil {
			slog.Error("failed to compute alert proposals", "error", err)
			writeErrorJSON(w, http.StatusInternalServerError, "failed to compute alert proposals")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"proposals": proposals}); err != nil {
			slog.Error("failed to encode alert proposals", "error", err)
		}
	}
}

// computeProposals flattens every service's proposals into one list.
//
// No rolled-up data yet is an empty list, not an error: it is the state of every
// stack whose first rollup has not run, and the remedy is to wait.
func computeProposals(ctx context.Context, warehouseDir, tenantID string) ([]baseline.Proposal, error) {
	baselines, err := baseline.Compute(ctx, warehouseDir, tenantID, baselineLookbackDays, time.Now())
	if errors.Is(err, baseline.ErrNoData) {
		return []baseline.Proposal{}, nil
	}
	if err != nil {
		return nil, err
	}

	proposals := make([]baseline.Proposal, 0, len(baselines)*3)
	for _, b := range baselines {
		proposals = append(proposals, baseline.Propose(b)...)
	}
	return proposals, nil
}

// handleArmAlertProposal turns one proposal into a live alert rule.
//
// The threshold is recomputed here rather than read from the request, so a
// stale or forged number cannot be armed. The client names which proposal it
// wants; the server decides what it means.
func handleArmAlertProposal(warehouseDir string, tdb tenantdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")
			return
		}
		if tdb == nil {
			writeErrorJSON(w, http.StatusNotImplemented,
				"alert proposals require a tenant database (TENANT_DB_PATH)")
			return
		}
		// An armed rule needs a notification channel, and a channel needs a real
		// tenant: notification_channels.tenant_id is NOT NULL REFERENCES
		// tenants(id). Reaching the insert without one turns a misconfiguration
		// into a 500 reading "failed to create notification channel", which says
		// nothing about the cause.
		if getTenantID(r) == "" {
			writeErrorJSON(w, http.StatusNotImplemented,
				"alert proposals require an authenticated tenant; this request carried none")
			return
		}

		var req struct {
			Service    string `json:"service"`
			ProposalID string `json:"proposal_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "service and proposal_id are required")
			return
		}
		if req.Service == "" || req.ProposalID == "" {
			writeErrorJSON(w, http.StatusBadRequest, "service and proposal_id are required")
			return
		}

		ctx := r.Context()
		tenantID := getTenantID(r)

		baselines, err := baseline.Compute(ctx, warehouseDir, tenantID, baselineLookbackDays, time.Now())
		if err != nil && !errors.Is(err, baseline.ErrNoData) {
			slog.Error("failed to compute baselines while arming", "error", err)
			writeErrorJSON(w, http.StatusInternalServerError, "failed to compute alert proposals")
			return
		}

		proposal, err := baseline.Find(baselines, req.Service, req.ProposalID)
		if err != nil {
			writeErrorJSON(w, http.StatusNotFound,
				fmt.Sprintf("no proposal %s for service %s", req.ProposalID, req.Service))
			return
		}

		channel, err := localAlertChannel(ctx, tdb, tenantID)
		if err != nil {
			slog.Error("failed to resolve the local alert channel", "error", err)
			writeErrorJSON(w, http.StatusInternalServerError, "failed to create notification channel")
			return
		}

		rule := &tenantdb.AlertRule{
			TenantID:        tenantID,
			Name:            fmt.Sprintf("%s: %s (auto-proposed)", proposal.Service, proposal.Name),
			Metric:          proposal.Metric,
			Operator:        proposal.Operator,
			Threshold:       proposal.Threshold,
			WindowMinutes:   proposal.WindowMinutes,
			Service:         proposal.Service,
			ChannelID:       channel.ID,
			CooldownMinutes: 30,
			Status:          "active",
		}
		if err := tdb.AlertRules().Create(ctx, rule); err != nil {
			slog.Error("failed to create alert rule", "error", err)
			writeErrorJSON(w, http.StatusInternalServerError, "failed to create alert rule")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"alert_rule_id": rule.ID,
			"channel_id":    channel.ID,
		}); err != nil {
			slog.Error("failed to encode arm response", "error", err)
		}
	}
}

// localAlertChannelName is the one channel auto-armed rules are attached to.
const localAlertChannelName = "Local (dashboard only)"

// localAlertChannel finds or creates the channel auto-armed rules notify.
//
// alert_rules.channel_id is NOT NULL REFERENCES notification_channels(id), so
// there is no "no channel" state — a rule cannot exist without one. Rather than
// make a self-hoster configure Slack before they can arm anything, rules go to a
// "log" channel that delivers nowhere: the alert still fires and still lands in
// alert history, which is what the dashboard reads.
//
// Reused rather than recreated, so arming five rules leaves one channel.
func localAlertChannel(ctx context.Context, tdb tenantdb.DB, tenantID string) (*tenantdb.NotificationChannel, error) {
	existing, err := tdb.NotificationChannels().ListByTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	for _, ch := range existing {
		if ch.Name == localAlertChannelName && ch.Type == "log" {
			return ch, nil
		}
	}

	ch := &tenantdb.NotificationChannel{
		TenantID: tenantID,
		Name:     localAlertChannelName,
		Type:     "log",
		Config:   "{}",
		Status:   "active",
	}
	if err := tdb.NotificationChannels().Create(ctx, ch); err != nil {
		return nil, fmt.Errorf("create notification channel: %w", err)
	}
	return ch, nil
}

// handleBatchFacts handles JSONL (newline-delimited JSON) payloads with multiple facts per request.
func handleBatchFacts(sink *DurableSink, tdb tenantdb.DB, reg *discovery.Registry, learner *pathlearn.Learner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")
			return
		}
		if !requireJSON(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
			return
		}
		defer r.Body.Close()

		tenantID := getTenantID(r)

		// Check quota before processing batch
		if checkQuota(tdb, tenantID, getTenantPlan(r), getTenantOverageAllowed(r)) {
			writeErrorJSON(w, http.StatusTooManyRequests, "monthly event quota exceeded — upgrade your plan")
			ingestionRequestsTotal.WithLabelValues("/api/v1/facts/batch", "429", tenantID).Inc()
			return
		}

		lines := splitJSONL(body)
		if len(lines) == 0 {
			writeErrorJSON(w, http.StatusBadRequest, "empty request body")
			return
		}

		reqID := logging.GetRequestID(r.Context())
		now := time.Now().UTC()

		var validRecords [][]byte
		var errors []string
		var dlqEntries []DLQEntry
		var unprocessable []DLQEntry
		marshalOpts := protojson.MarshalOptions{UseProtoNames: true}

		for i, line := range lines {
			if len(line) == 0 {
				continue
			}

			// Same three steps as the single-fact path: decode, normalize,
			// validate. Each failure keeps the existing per-line shape, so a
			// budget rejection reads like any other rejected line.
			fact, err := schemas.UnmarshalRequestFactUnvalidated(line)
			if err != nil {
				errMsg := fmt.Sprintf("line %d: %v", i+1, err)
				errors = append(errors, errMsg)
				dlqEntries = append(dlqEntries, DLQEntry{
					Timestamp: now,
					TenantID:  tenantID,
					RequestID: reqID,
					FactType:  "request_fact",
					Error:     errMsg,
					RawJSON:   json.RawMessage(line),
				})
				continue
			}

			decision := learner.Learn(fact.Service, fact.Method, fact.PathTemplate)
			if !decision.Accepted {
				errMsg := fmt.Sprintf("line %d: %s", i+1, decision.Reason)
				errors = append(errors, errMsg)
				dlqEntries = append(dlqEntries, DLQEntry{
					Timestamp: now,
					TenantID:  tenantID,
					RequestID: reqID,
					FactType:  "request_fact",
					Error:     errMsg,
					RawJSON:   json.RawMessage(line),
				})
				continue
			}
			fact.PathTemplate = decision.Template

			if err := schemas.ValidateRequestFact(fact); err != nil {
				errMsg := fmt.Sprintf("line %d: validation error: %v", i+1, err)
				errors = append(errors, errMsg)
				dlqEntries = append(dlqEntries, DLQEntry{
					Timestamp: now,
					TenantID:  tenantID,
					RequestID: reqID,
					FactType:  "request_fact",
					Error:     errMsg,
					RawJSON:   json.RawMessage(line),
				})
				continue
			}

			cleanData, err := marshalOpts.Marshal(fact)
			if err != nil {
				errMsg := fmt.Sprintf("line %d: marshal error", i+1)
				errors = append(errors, errMsg)
				dlqEntries = append(dlqEntries, DLQEntry{
					Timestamp: now,
					TenantID:  tenantID,
					RequestID: reqID,
					FactType:  "request_fact",
					Error:     errMsg,
					RawJSON:   json.RawMessage(line),
				})
				continue
			}

			validRecords = append(validRecords, cleanData)
			// Spec §6.5 puts this in the loop, before the batch write. That
			// differs from the single-fact path above, which records only after
			// a successful write: if WriteBatch fails, these services stay in
			// the registry and a client retry counts them twice. Discovery
			// counters are a "what exists" aid, not billing, so the spec's
			// placement is followed rather than silently improved. See SD-014.
			reg.RecordFact(fact.Service, fact.EventTime.AsTime())
			reg.RecordTemplate(fact.Service, fact.Method, fact.PathTemplate, fact.EventTime.AsTime())

			if classifyFact(fact, now) == lateness.ClassUnprocessable {
				unprocessable = append(unprocessable,
					unprocessableEntry(tenantID, reqID, now, line))
			}
		}

		// Write all valid records in a single batch (one fsync)
		topic := topicForTenant(tenantID, "request_facts")
		if len(validRecords) > 0 {
			if err := sink.WriteBatch(topic, validRecords); err != nil {
				slog.Error("sink batch write error", "error", err)
				writeErrorJSON(w, http.StatusInternalServerError, "failed to persist facts")
				return
			}
		}

		// Write rejected facts to DLQ (async, non-blocking)
		if len(dlqEntries) > 0 {
			go writeDLQEntries(sink, tenantID, "request_fact", dlqEntries)
		}

		// Unprocessable facts were accepted and stored above. They are copied into
		// their own DLQ prefix as well, so an operator can find data that will
		// never reach a metric.
		if len(unprocessable) > 0 {
			go writeUnprocessableEntries(sink, tenantID, unprocessable)
		}

		accepted := len(validRecords)

		// Increment event counter for billing (best-effort, non-blocking)
		if accepted > 0 {
			incrementEventCounter(tdb, tenantID, int64(accepted))
		}

		ingestionBatchSizeBytes.WithLabelValues("request_facts").Observe(float64(len(body)))

		// 202 rather than 200 when some facts can never be aggregated: every fact
		// was accepted, but not all of them will appear in a metric, and the caller
		// is entitled to know that from the status line.
		status := http.StatusOK
		if len(unprocessable) > 0 {
			status = http.StatusAccepted
		}
		ingestionRequestsTotal.WithLabelValues("/api/v1/facts/batch", strconv.Itoa(status), tenantID).Inc()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		resp := map[string]interface{}{
			"accepted": accepted,
			"rejected": len(errors),
		}
		if len(errors) > 0 {
			resp["errors"] = errors
		}
		if len(unprocessable) > 0 {
			resp["unprocessable"] = len(unprocessable)
			resp["note"] = unprocessableNote
		}
		json.NewEncoder(w).Encode(resp)
	}
}

// splitJSONL splits a byte slice on newlines, returning non-empty lines.
func splitJSONL(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	// Last line (may not end with newline)
	if start < len(data) {
		line := data[start:]
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

func handleEvents(sink *DurableSink, tdb tenantdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")
			return
		}
		if !requireJSON(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
			return
		}
		defer r.Body.Close()

		event, err := schemas.ParseServiceEvent(body)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("invalid ServiceEvent: %v", err))
			return
		}

		marshalOpts := protojson.MarshalOptions{UseProtoNames: true}
		cleanData, err := marshalOpts.Marshal(event)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "failed to marshal event")
			return
		}

		tenantID := getTenantID(r)
		topic := topicForTenant(tenantID, "service_events")
		if err := sink.Write(topic, cleanData); err != nil {
			slog.Error("sink write error", "error", err)
			ingestionRequestsTotal.WithLabelValues("/api/v1/events", "500", tenantID).Inc()
			writeErrorJSON(w, http.StatusInternalServerError, "failed to persist event")
			return
		}

		// Increment event counter for billing (best-effort, non-blocking)
		incrementEventCounter(tdb, tenantID, 1)

		ingestionRequestsTotal.WithLabelValues("/api/v1/events", "201", tenantID).Inc()
		ingestionBatchSizeBytes.WithLabelValues("service_events").Observe(float64(len(cleanData)))
		w.WriteHeader(http.StatusCreated)
	}
}

// incrementEventCounter increments the daily event counter for a tenant.
// This is best-effort and non-blocking — billing metering should not
// slow down or fail the ingestion hot path.
func incrementEventCounter(tdb tenantdb.DB, tenantID string, count int64) {
	if tdb == nil || tenantID == "" {
		return
	}
	today := time.Now().UTC().Format("2006-01-02")
	go func() {
		if err := tdb.EventCounters().Increment(context.Background(), tenantID, today, count); err != nil {
			slog.Warn("failed to increment event counter", "tenant_id", tenantID, "error", err)
		}
	}()
}

// checkQuota verifies a tenant hasn't exceeded their monthly event limit.
// Returns true if the request should be rejected, false if allowed.
// For paid plans with overage_allowed=true, allows but flags the overage.
func checkQuota(tdb tenantdb.DB, tenantID, plan string, overageAllowed bool) bool {
	if tdb == nil || tenantID == "" {
		return false // no tenant DB → single tenant mode, no quota
	}

	var eventLimit int64
	switch plan {
	case "pro":
		eventLimit = 50_000_000
	case "starter":
		eventLimit = 10_000_000
	default:
		eventLimit = 1_000_000
	}

	// Sum current month's usage
	ctx := context.Background()
	year, month, _ := time.Now().UTC().Date()
	var monthTotal int64
	for day := 1; day <= 31; day++ {
		d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		if d.Month() != month {
			break
		}
		dayStr := d.Format("2006-01-02")
		count, _ := tdb.EventCounters().GetCount(ctx, tenantID, dayStr)
		monthTotal += count
	}

	if monthTotal < eventLimit {
		return false // under limit
	}

	if overageAllowed {
		// Paid plan: allow but flag
		ingestionOverageEventsTotal.WithLabelValues(tenantID).Inc()
		return false
	}

	// Free plan: reject
	ingestionQuotaRejectedTotal.WithLabelValues(tenantID).Inc()
	return true
}

// getEnvInt reads an integer from env, returning def if missing/invalid.
func getEnvInt(key string, def int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
