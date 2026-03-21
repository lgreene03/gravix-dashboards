package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/logging"
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

// topicForTenant returns the storage topic prefixed with tenant ID.
// In legacy mode (empty tenantID), returns the base topic unchanged.
func topicForTenant(tenantID, baseTopic string) string {
	if tenantID == "" {
		return baseTopic
	}
	return tenantID + "/" + baseTopic
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
)

func init() {
	prometheus.MustRegister(ingestionRequestsTotal)
	prometheus.MustRegister(ingestionBatchSizeBytes)
	prometheus.MustRegister(collectors.NewBuildInfoCollector())
	prometheus.MustRegister(ingestionFsyncDurationSeconds)
	prometheus.MustRegister(ingestionDLQWriteErrorsTotal)
	prometheus.MustRegister(ingestionUploadErrorsTotal)
	prometheus.MustRegister(ingestionOverageEventsTotal)
	prometheus.MustRegister(ingestionQuotaRejectedTotal)
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
}

func NewDurableSink(bufferDir string, store storage.ObjectStore) (*DurableSink, error) {
	if err := os.MkdirAll(bufferDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create buffer dir: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ds := &DurableSink{
		bufferDir:   bufferDir,
		store:       store,
		activeFiles: make(map[string]*os.File),
		ctx:         ctx,
		cancel:      cancel,
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

// uploadFile uploads the local batch to the object store
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

	if err := ds.store.Put(ds.ctx, destKey, f); err != nil {
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

func main() {
	logging.Init("ingestion")

	port := flag.Int("port", 8080, "HTTP port")
	baseDir := flag.String("base-dir", "./data", "Base directory for buffer and raw storage")
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

	if tenantDBPath != "" {
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
	rawDir := filepath.Join(*baseDir, "raw")

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
		slog.Info("initializing local storage", "dir", rawDir)
		var err error
		store, err = storage.NewLocalStore(rawDir)
		if err != nil {
			slog.Error("failed to initialize local store", "error", err)
			os.Exit(1)
		}
	}

	slog.Info("initializing durable sink", "buffer_dir", bufferDir)
	sink, err := NewDurableSink(bufferDir, store)
	if err != nil {
		slog.Error("failed to create sink", "error", err)
		os.Exit(1)
	}
	defer sink.Close()

	// Per-tenant rate limiting (fallback 100/s for legacy mode)
	trl := ratelimit.NewTenantLimiter(100, 200)
	defer trl.Close()

	// Wrap handlers: auth first (sets tenant context), then rate limit, then handler
	http.Handle("/api/v1/facts", authMW(tenantRateLimitMiddleware(trl, handleFacts(sink, tdb))))
	http.Handle("/api/v1/facts/batch", authMW(tenantRateLimitMiddleware(trl, handleBatchFacts(sink, tdb))))
	http.Handle("/api/v1/events", authMW(tenantRateLimitMiddleware(trl, handleEvents(sink, tdb))))

	http.Handle("/metrics", promhttp.Handler())

	http.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("up"))
	})

	http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		// In a real app, check DB connectivity or sink status.
		// For now, if the sink is initialized, we are ready.
		if sink != nil {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ready"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
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

func handleFacts(sink *DurableSink, tdb tenantdb.DB) http.HandlerFunc {
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

		fact, err := schemas.ParseRequestFact(body)
		if err != nil {
			// Write rejected fact to DLQ (async, non-blocking)
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

		// Increment event counter for billing (best-effort, non-blocking)
		incrementEventCounter(tdb, tenantID, 1)

		ingestionRequestsTotal.WithLabelValues("/api/v1/facts", "201", tenantID).Inc()
		ingestionBatchSizeBytes.WithLabelValues("request_facts").Observe(float64(len(cleanData)))
		w.WriteHeader(http.StatusCreated)
	}
}

// handleBatchFacts handles JSONL (newline-delimited JSON) payloads with multiple facts per request.
func handleBatchFacts(sink *DurableSink, tdb tenantdb.DB) http.HandlerFunc {
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
		marshalOpts := protojson.MarshalOptions{UseProtoNames: true}

		for i, line := range lines {
			if len(line) == 0 {
				continue
			}

			fact, err := schemas.ParseRequestFact(line)
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

		accepted := len(validRecords)

		// Increment event counter for billing (best-effort, non-blocking)
		if accepted > 0 {
			incrementEventCounter(tdb, tenantID, int64(accepted))
		}

		ingestionRequestsTotal.WithLabelValues("/api/v1/facts/batch", "200", tenantID).Inc()
		ingestionBatchSizeBytes.WithLabelValues("request_facts").Observe(float64(len(body)))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"accepted": accepted,
			"rejected": len(errors),
		}
		if len(errors) > 0 {
			resp["errors"] = errors
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
