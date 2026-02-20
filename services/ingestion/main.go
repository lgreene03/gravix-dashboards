package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/schemas"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/protobuf/encoding/protojson"
)

const maxBodyBytes = 1 << 20 // 1 MB max request body

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
		[]string{"path", "status"},
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
)

func init() {
	prometheus.MustRegister(ingestionRequestsTotal)
	prometheus.MustRegister(ingestionBatchSizeBytes)
	prometheus.MustRegister(prometheus.NewBuildInfoCollector())
	prometheus.MustRegister(ingestionFsyncDurationSeconds)
}

// RateLimiter implements a simple token-bucket rate limiter.
// It allows up to 'rate' requests per second with a burst capacity.
type RateLimiter struct {
	tokens    atomic.Int64
	rate      int64 // tokens added per second
	maxTokens int64 // burst capacity
}

func NewRateLimiter(ratePerSecond, burst int64) *RateLimiter {
	rl := &RateLimiter{
		rate:      ratePerSecond,
		maxTokens: burst,
	}
	rl.tokens.Store(burst)
	go rl.refill()
	return rl
}

func (rl *RateLimiter) refill() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		current := rl.tokens.Load()
		newTokens := current + rl.rate
		if newTokens > rl.maxTokens {
			newTokens = rl.maxTokens
		}
		rl.tokens.Store(newTokens)
	}
}

// Allow returns true if a request is permitted, consuming one token.
func (rl *RateLimiter) Allow() bool {
	for {
		current := rl.tokens.Load()
		if current <= 0 {
			return false
		}
		if rl.tokens.CompareAndSwap(current, current-1) {
			return true
		}
	}
}

func rateLimitMiddleware(rl *RateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow() {
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

func (ds *DurableSink) Close() error {
	ds.cancel()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for _, f := range ds.activeFiles {
		f.Close()
	}
	return nil
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
		log.Printf("Error rotating file %s: %v", currentPath, err)
		return
	}

	// 3. Trigger Upload (Async from the lock, but we call it here for MVP simplicity)
	go ds.uploadFile(topic, batchPath, time.Now().UTC())
}

// uploadFile uploads the local batch to the object store
func (ds *DurableSink) uploadFile(topic, sourcePath string, t time.Time) {
	// Destination Key: raw/<topic>/YYYY-MM-DD/HH/<uuid>.jsonl
	dayStr := t.Format("2006-01-02")
	hourStr := t.Format("15")
	destKey := fmt.Sprintf("raw/%s/%s/%s/%s", topic, dayStr, hourStr, filepath.Base(sourcePath))

	f, err := os.Open(sourcePath)
	if err != nil {
		log.Printf("Error opening source file %s: %v", sourcePath, err)
		return
	}
	defer f.Close()

	if err := ds.store.Put(ds.ctx, destKey, f); err != nil {
		log.Printf("Error uploading %s to storage (file preserved for retry): %v", sourcePath, err)
		return // Do NOT delete the local file — it will be retried on next startup scan
	}

	// Upload succeeded — safe to delete the local batch
	if err := os.Remove(sourcePath); err != nil {
		log.Printf("Warning: uploaded %s but failed to remove local file: %v", sourcePath, err)
	}
	log.Printf("Uploaded %s to storage key %s", sourcePath, destKey)
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

		log.Printf("Found orphaned batch file: %s", path)
		// Upload using file mod time as heuristic
		ds.uploadFile(topic, path, info.ModTime().UTC())
		return nil
	})
	if err != nil {
		log.Printf("Startup scan error: %v", err)
	}
}

func main() {
	port := flag.Int("port", 8080, "HTTP port")
	baseDir := flag.String("base-dir", "./data", "Base directory for buffer and raw storage")
	flag.Parse()

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		log.Println("WARNING: API_KEY environment variable not set. Authentication disabled.")
	} else {
		log.Println("API Key authentication enabled.")
	}

	bufferDir := filepath.Join(*baseDir, "buffer")
	rawDir := filepath.Join(*baseDir, "raw")

	var store storage.ObjectStore
	if os.Getenv("S3_ENDPOINT") != "" {
		log.Println("Initializing S3/MinIO Storage...")
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
			log.Fatalf("Failed to initialize S3 store: %v", err)
		}
	} else {
		log.Printf("Initializing Local Storage at %s...", rawDir)
		var err error
		store, err = storage.NewLocalStore(rawDir)
		if err != nil {
			log.Fatalf("Failed to initialize local store: %v", err)
		}
	}

	log.Printf("Initializing Durable Sink (Buffer: %s)...", bufferDir)
	sink, err := NewDurableSink(bufferDir, store)
	if err != nil {
		log.Fatalf("Failed to create sink: %v", err)
	}
	defer sink.Close()

	// Rate limiter: 100 requests/sec with burst of 200
	rl := NewRateLimiter(100, 200)

	// Wrap handlers with rate limiting + auth middleware
	http.Handle("/api/v1/facts", rateLimitMiddleware(rl, authMiddleware(apiKey, handleFacts(sink))))
	http.Handle("/api/v1/facts/batch", rateLimitMiddleware(rl, authMiddleware(apiKey, handleBatchFacts(sink))))
	http.Handle("/api/v1/events", rateLimitMiddleware(rl, authMiddleware(apiKey, handleEvents(sink))))

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
		Addr:         addr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown: listen for SIGINT/SIGTERM
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-shutdownCh
		log.Printf("Received %v, draining connections (10s)...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		}
	}()

	log.Printf("Starting ingestion service on %s...", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Println("Server stopped gracefully.")
}

// authMiddleware checks for X-API-Key header if apiKey is configured
func authMiddleware(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if apiKey != "" {
			reqKey := r.Header.Get("X-API-Key")
			if subtle.ConstantTimeCompare([]byte(reqKey), []byte(apiKey)) != 1 {
				writeErrorJSON(w, http.StatusUnauthorized, "invalid or missing X-API-Key header")
				return
			}
		}
		next(w, r)
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

func handleFacts(sink *DurableSink) http.HandlerFunc {
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

		fact, err := schemas.ParseRequestFact(body)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("invalid RequestFact: %v", err))
			return
		}

		marshalOpts := protojson.MarshalOptions{UseProtoNames: true}
		cleanData, err := marshalOpts.Marshal(fact)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "failed to marshal fact")
			return
		}

		if err := sink.Write("request_facts", cleanData); err != nil {
			log.Printf("Sink write error: %v", err)
			ingestionRequestsTotal.WithLabelValues("/api/v1/facts", "500").Inc()
			writeErrorJSON(w, http.StatusInternalServerError, "failed to persist fact")
			return
		}

		ingestionRequestsTotal.WithLabelValues("/api/v1/facts", "201").Inc()
		ingestionBatchSizeBytes.WithLabelValues("request_facts").Observe(float64(len(cleanData)))
		w.WriteHeader(http.StatusCreated)
	}
}

// handleBatchFacts handles JSONL (newline-delimited JSON) payloads with multiple facts per request.
func handleBatchFacts(sink *DurableSink) http.HandlerFunc {
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

		lines := splitJSONL(body)
		if len(lines) == 0 {
			writeErrorJSON(w, http.StatusBadRequest, "empty request body")
			return
		}

		accepted := 0
		var errors []string
		marshalOpts := protojson.MarshalOptions{UseProtoNames: true}

		for i, line := range lines {
			if len(line) == 0 {
				continue
			}

			fact, err := schemas.ParseRequestFact(line)
			if err != nil {
				errors = append(errors, fmt.Sprintf("line %d: %v", i+1, err))
				continue
			}

			cleanData, err := marshalOpts.Marshal(fact)
			if err != nil {
				errors = append(errors, fmt.Sprintf("line %d: marshal error", i+1))
				continue
			}

			if err := sink.Write("request_facts", cleanData); err != nil {
				log.Printf("Sink write error (batch line %d): %v", i+1, err)
				writeErrorJSON(w, http.StatusInternalServerError, "failed to persist facts")
				return
			}
			accepted++
		}

		ingestionRequestsTotal.WithLabelValues("/api/v1/facts/batch", "200").Inc()
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

func handleEvents(sink *DurableSink) http.HandlerFunc {
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

		if err := sink.Write("service_events", cleanData); err != nil {
			log.Printf("Sink write error: %v", err)
			ingestionRequestsTotal.WithLabelValues("/api/v1/events", "500").Inc()
			writeErrorJSON(w, http.StatusInternalServerError, "failed to persist event")
			return
		}

		ingestionRequestsTotal.WithLabelValues("/api/v1/events", "201").Inc()
		ingestionBatchSizeBytes.WithLabelValues("service_events").Observe(float64(len(cleanData)))
		w.WriteHeader(http.StatusCreated)
	}
}
