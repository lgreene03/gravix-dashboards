// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Batcher accumulates facts from concurrent requests and fsyncs them together,
// so N requests cost one fsync instead of N. Each caller still blocks until its
// own facts are durable, which is what docs/00-system-truth.md §6 requires: the
// guarantee is preserved, the syscall is amortised.
//
// The measurement this exists to move, taken on the machine that wrote it:
// one fsync per fact gives ~4,500 facts/sec/core, one fsync per eight gives
// ~43,000, and one per 512 gives ~500,000. A bare fsync costs 0.157 ms against
// 0.221 ms for the whole single-fact write, so roughly 70% of the cost of
// ingesting one fact is the syscall, not Gravix. Batching is therefore the only
// lever that matters here, and it is why this is not a micro-optimisation of
// the validation path.
type Batcher struct {
	sink   io.Writer
	syncer Syncer
	cfg    BatcherConfig

	queue chan *batchItem

	// mu guards queued and closed. queued counts FACTS, not items, because the
	// queue depth that matters is how much unsynced work is outstanding.
	mu     sync.Mutex
	queued int
	closed bool

	wg   sync.WaitGroup
	stop chan struct{}
}

// Syncer is the fsync half of the durable sink. Separated from io.Writer so a
// test can substitute one that counts syncs, or fails them.
type Syncer interface {
	Sync() error
}

// BatcherConfig bounds the batching.
type BatcherConfig struct {
	MaxBatchSize  int           // facts per fsync; default 512
	MaxBatchDelay time.Duration // longest a caller waits for peers; default 2ms
	QueueDepth    int           // facts queued before backpressure; default 8192
}

// Defaults, named so the tests and the report can refer to them.
const (
	DefaultMaxBatchSize  = 512
	DefaultMaxBatchDelay = 2 * time.Millisecond
	DefaultQueueDepth    = 8192
)

func (c BatcherConfig) withDefaults() BatcherConfig {
	if c.MaxBatchSize <= 0 {
		c.MaxBatchSize = DefaultMaxBatchSize
	}
	if c.MaxBatchDelay <= 0 {
		c.MaxBatchDelay = DefaultMaxBatchDelay
	}
	if c.QueueDepth <= 0 {
		c.QueueDepth = DefaultQueueDepth
	}
	return c
}

var (
	ErrQueueFull = errors.New("ingestion: queue full")
	ErrClosed    = errors.New("ingestion: batcher closed")
)

// batchItem is one caller's facts and the channel its result comes back on.
type batchItem struct {
	facts [][]byte
	done  chan error
}

// NewBatcher returns a Batcher writing to sink.
func NewBatcher(sink io.Writer, syncer Syncer, cfg BatcherConfig) *Batcher {
	cfg = cfg.withDefaults()
	b := &Batcher{
		sink:   sink,
		syncer: syncer,
		cfg:    cfg,
		// Item capacity, not fact capacity: the fact count is what
		// admission control uses, and it is tracked separately.
		queue: make(chan *batchItem, cfg.QueueDepth),
		stop:  make(chan struct{}),
	}
	b.wg.Add(1)
	go b.run()
	return b
}

// Append enqueues facts and blocks until they are durable, or until ctx ends.
// It returns only after the fsync covering these facts has completed.
//
// A non-nil error means the caller MUST NOT acknowledge: either the facts are
// not durable, or it is not known whether they are. §5.2 — a dropped fact is a
// correctness incident, not a capacity event.
func (b *Batcher) Append(ctx context.Context, facts [][]byte) error {
	if len(facts) == 0 {
		return nil
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	// Admission is by fact count, and it is checked before enqueueing rather
	// than after: accepting facts and then discovering the queue is full would
	// mean either dropping them or blocking unboundedly, and both are worse
	// than refusing.
	if b.queued+len(facts) > b.cfg.QueueDepth {
		b.mu.Unlock()
		return ErrQueueFull
	}
	b.queued += len(facts)
	b.mu.Unlock()

	item := &batchItem{facts: facts, done: make(chan error, 1)}

	select {
	case b.queue <- item:
	case <-ctx.Done():
		b.release(len(facts))
		return ctx.Err()
	}

	select {
	case err := <-item.done:
		return err
	case <-ctx.Done():
		// The facts may still be written and synced by the batch loop. That is
		// fine and deliberate: the contract is that a caller does not
		// acknowledge on error, not that nothing reaches disk. Writing a fact
		// the client never learns about is recoverable; acknowledging one that
		// is not durable is not.
		return ctx.Err()
	}
}

func (b *Batcher) release(n int) {
	b.mu.Lock()
	b.queued -= n
	if b.queued < 0 {
		b.queued = 0
	}
	b.mu.Unlock()
}

// run is the single writer. One goroutine owns the sink, so the write ordering
// is the fsync ordering and no lock is needed around the file itself.
func (b *Batcher) run() {
	defer b.wg.Done()

	for {
		var first *batchItem
		select {
		case item := <-b.queue:
			first = item
		case <-b.stop:
			b.drain()
			return
		}

		batch := []*batchItem{first}
		count := len(first.facts)

		// Collect peers for at most MaxBatchDelay. This is the whole trade: a
		// caller waits up to that long for company, and in exchange the fsync
		// rate falls by up to MaxBatchSize.
		if count < b.cfg.MaxBatchSize {
			timer := time.NewTimer(b.cfg.MaxBatchDelay)
		collect:
			for count < b.cfg.MaxBatchSize {
				select {
				case item := <-b.queue:
					batch = append(batch, item)
					count += len(item.facts)
				case <-timer.C:
					break collect
				case <-b.stop:
					break collect
				}
			}
			timer.Stop()
		}

		b.commit(batch, count)
	}
}

// drain handles whatever is queued at shutdown, so Close does not strand a
// caller waiting on a channel nobody will write to.
func (b *Batcher) drain() {
	for {
		select {
		case item := <-b.queue:
			b.commit([]*batchItem{item}, len(item.facts))
		default:
			return
		}
	}
}

// commit writes every item's facts, fsyncs once, and reports the same result to
// every contributing caller.
func (b *Batcher) commit(batch []*batchItem, count int) {
	err := b.writeAll(batch)
	if err == nil {
		err = b.syncer.Sync()
		if err != nil {
			err = fmt.Errorf("fsync error: %w", err)
		}
	}

	for _, item := range batch {
		item.done <- err
	}
	b.release(count)
}

func (b *Batcher) writeAll(batch []*batchItem) error {
	for _, item := range batch {
		for _, fact := range item.facts {
			if _, err := b.sink.Write(fact); err != nil {
				return err
			}
			if _, err := b.sink.Write(newline); err != nil {
				return err
			}
		}
	}
	return nil
}

var newline = []byte("\n")

// Close flushes and stops the batcher. Appends after Close return ErrClosed.
func (b *Batcher) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	close(b.stop)
	b.wg.Wait()
	return nil
}

// QueuedFacts reports how many facts are awaiting an fsync. For tests and for
// the overload metric; not part of the durability contract.
func (b *Batcher) QueuedFacts() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.queued
}
