package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/schemas"
)

// DurableSink provides fsync-backed appends and async background uploads.
type DurableSink struct {
	bufferDir string // e.g. /tmp/buffer/
	rawDir    string // e.g. /data/raw/ (simulated S3)

	activeFiles map[string]*os.File
	mu          sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
}

func NewDurableSink(bufferDir, rawDir string) (*DurableSink, error) {
	if err := os.MkdirAll(bufferDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create buffer dir: %w", err)
	}
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create raw dir: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ds := &DurableSink{
		bufferDir:   bufferDir,
		rawDir:      rawDir,
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
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync error: %w", err)
	}

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

// uploadFile simulates S3 upload by moving to raw directory structure
func (ds *DurableSink) uploadFile(topic, sourcePath string, t time.Time) {
	// Destination: raw/<topic>/YYYY-MM-DD/HH/<uuid>.jsonl
	dayStr := t.Format("2006-01-02")
	hourStr := t.Format("15")
	destDir := filepath.Join(ds.rawDir, topic, dayStr, hourStr)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Printf("Error creating dest dir %s: %v", destDir, err)
		return
	}

	destName := filepath.Base(sourcePath)
	destPath := filepath.Join(destDir, destName)

	// In real S3, this is PutObject. Here, os.Rename (Move).
	// Cross-device rename might fail (buffer in /tmp, raw in /data), so we effectively copy+delete if rename fails
	if err := os.Rename(sourcePath, destPath); err != nil {
		// Fallback: Copy + Delete
		// (MVP simplification: assuming same filesystem for Docker volume)
		log.Printf("Rename failed (cross-device?), trying copy: %v", err)
		if err := copyFile(sourcePath, destPath); err != nil {
			log.Printf("Copy failed: %v", err)
			return
		}
		os.Remove(sourcePath)
	}

	log.Printf("Uploaded %s to %s", sourcePath, destPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
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

	log.Printf("Initializing Durable Sink (Buffer: %s, Raw: %s)...", bufferDir, rawDir)
	sink, err := NewDurableSink(bufferDir, rawDir)
	if err != nil {
		log.Fatalf("Failed to create sink: %v", err)
	}
	defer sink.Close()

	// Wrap handlers with auth middleware
	http.Handle("/api/v1/facts", authMiddleware(apiKey, handleFacts(sink)))
	http.Handle("/api/v1/events", authMiddleware(apiKey, handleEvents(sink)))

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Starting ingestion service on %s...", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}

// authMiddleware checks for X-API-Key header if apiKey is configured
func authMiddleware(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if apiKey != "" {
			reqKey := r.Header.Get("X-API-Key")
			if reqKey != apiKey {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func handleFacts(sink *DurableSink) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		fact, err := schemas.ParseRequestFact(body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid RequestFact: %v", err), http.StatusBadRequest)
			return
		}

		cleanData, err := json.Marshal(fact)
		if err != nil {
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}

		if err := sink.Write("request_facts", cleanData); err != nil {
			log.Printf("Sink write error: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated) // 201 Created (Durable)
	}
}

func handleEvents(sink *DurableSink) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		event, err := schemas.ParseServiceEvent(body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid ServiceEvent: %v", err), http.StatusBadRequest)
			return
		}

		cleanData, err := json.Marshal(event)
		if err != nil {
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}

		if err := sink.Write("service_events", cleanData); err != nil {
			log.Printf("Sink write error: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated) // 201 Created (Durable)
	}
}
