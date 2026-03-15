package gravix

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"
)

// batcher accumulates RequestFact entries and flushes them as JSONL
// to the batch endpoint on a size or time threshold.
type batcher struct {
	mu       sync.Mutex
	buffer   []RequestFact
	sendFn   func(ctx context.Context, body []byte) (*BatchResult, error)
	size     int
	interval time.Duration
	done     chan struct{}
	wg       sync.WaitGroup
	errFn    func(error) // optional error callback
}

// newBatcher creates a batcher with the given flush settings.
func newBatcher(size int, interval time.Duration, sendFn func(ctx context.Context, body []byte) (*BatchResult, error)) *batcher {
	b := &batcher{
		buffer:   make([]RequestFact, 0, size),
		sendFn:   sendFn,
		size:     size,
		interval: interval,
		done:     make(chan struct{}),
	}
	b.wg.Add(1)
	go b.flushLoop()
	return b
}

// add appends a fact to the buffer and triggers a flush if the batch is full.
func (b *batcher) add(f RequestFact) {
	b.mu.Lock()
	b.buffer = append(b.buffer, f)
	needFlush := len(b.buffer) >= b.size
	b.mu.Unlock()

	if needFlush {
		b.flush(context.Background())
	}
}

// flush sends all buffered facts to the batch endpoint.
func (b *batcher) flush(ctx context.Context) {
	b.mu.Lock()
	if len(b.buffer) == 0 {
		b.mu.Unlock()
		return
	}
	facts := b.buffer
	b.buffer = make([]RequestFact, 0, b.size)
	b.mu.Unlock()

	body, err := marshalJSONL(facts)
	if err != nil {
		if b.errFn != nil {
			b.errFn(err)
		}
		return
	}

	_, err = b.sendFn(ctx, body)
	if err != nil && b.errFn != nil {
		b.errFn(err)
	}
}

// flushLoop runs a periodic flush on the configured interval.
func (b *batcher) flushLoop() {
	defer b.wg.Done()
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.flush(context.Background())
		case <-b.done:
			// Final flush on shutdown.
			b.flush(context.Background())
			return
		}
	}
}

// close signals the flush loop to stop and waits for the final flush.
func (b *batcher) close() {
	close(b.done)
	b.wg.Wait()
}

// marshalJSONL encodes a slice of facts as newline-delimited JSON.
func marshalJSONL(facts []RequestFact) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, f := range facts {
		if err := enc.Encode(f); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
