// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package storage measures Gravix's on-disk footprint per ingested event,
// decomposed into the components GRVX-1003 §5.1 budgets separately.
//
// A single total hides which term regressed, which is the whole reason §5.1 is a
// table rather than a number. It is also what turns "we are over budget" into a
// statement someone can act on: the components differ by two orders of
// magnitude, and only one of them is worth attention.
package storage

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// BudgetBytesPerEvent is GRVX-1003 §5.1's total. It is not adjusted to match a
// measurement — §6 step 7 requires reporting the shortfall per component
// instead, because a target that moves to meet the result was never a target.
const BudgetBytesPerEvent = 120.0

// Component budgets from §5.1.
const (
	BudgetRaw       = 70.0
	BudgetRolledUp  = 30.0
	BudgetSketch    = 15.0
	BudgetManifests = 1.0
	BudgetSlack     = 4.0
)

// SketchColumn is the MetricRow column measured separately, because GRVX-804
// owns its budget and a regression there is that spec's, not this one's.
const SketchColumn = "latency_sketch"

// Footprint is one measurement of a warehouse tree.
type Footprint struct {
	Events int64 `json:"events"`

	// Raw JSONL facts, as stored. Compressed if the deployment compresses them,
	// which is the single largest term and the one §5.1 assumes is compressed.
	RawBytes int64 `json:"raw_bytes"`
	// Rolled-up and compacted Parquet, EXCLUDING the sketch column so the
	// components sum to the total without double-counting.
	RolledUpBytes int64 `json:"rolled_up_bytes"`
	// The latency_sketch column alone, across every Parquet file.
	SketchBytes int64 `json:"sketch_bytes"`
	// Partition manifests.
	ManifestBytes int64 `json:"manifest_bytes"`

	// RawCompressed records whether any raw fact file was stored compressed.
	// §5.1's ≤70 budget assumes they are; nothing in the core does it, and a
	// measurement that does not say so invites the reader to assume it happened.
	RawCompressed bool `json:"raw_compressed"`
}

// PerEvent figures. Zero events yields zero rather than a panic, because an
// empty warehouse is a legitimate thing to measure on day 0.
func (f *Footprint) perEvent(b int64) float64 {
	if f.Events == 0 {
		return 0
	}
	return float64(b) / float64(f.Events)
}

func (f *Footprint) RawPerEvent() float64       { return f.perEvent(f.RawBytes) }
func (f *Footprint) RolledUpPerEvent() float64  { return f.perEvent(f.RolledUpBytes) }
func (f *Footprint) SketchPerEvent() float64    { return f.perEvent(f.SketchBytes) }
func (f *Footprint) ManifestsPerEvent() float64 { return f.perEvent(f.ManifestBytes) }

// TotalPerEvent is the figure G4.4 budgets.
func (f *Footprint) TotalPerEvent() float64 {
	return f.perEvent(f.RawBytes + f.RolledUpBytes + f.SketchBytes + f.ManifestBytes)
}

// MeetsBudget reports whether the total is within §5.1.
func (f *Footprint) MeetsBudget() bool { return f.TotalPerEvent() <= BudgetBytesPerEvent }

// Shortfall is GRVX-1003 §6.1's message for a budget that is not met, naming the
// component that accounts for the overage rather than only the total.
func (f *Footprint) Shortfall() string {
	if f.MeetsBudget() {
		return ""
	}
	total := f.TotalPerEvent()
	over := total - BudgetBytesPerEvent

	worst, worstOver := "", 0.0
	for _, c := range []struct {
		name   string
		got    float64
		budget float64
	}{
		{"raw", f.RawPerEvent(), BudgetRaw},
		{"rolled_up", f.RolledUpPerEvent(), BudgetRolledUp},
		{"sketch", f.SketchPerEvent(), BudgetSketch},
		{"manifests", f.ManifestsPerEvent(), BudgetManifests},
	} {
		if d := c.got - c.budget; d > worstOver {
			worst, worstOver = c.name, d
		}
	}

	return fmt.Sprintf("storage: %.2f bytes/event, budget %.0f; over by %.2f in %s",
		total, BudgetBytesPerEvent, over, worst)
}

// Report renders every component against its budget. §5.1: each is measured and
// reported separately.
func (f *Footprint) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-12s %12s %10s %8s\n", "component", "bytes/event", "budget", "status")
	for _, c := range []struct {
		name   string
		got    float64
		budget float64
	}{
		{"raw", f.RawPerEvent(), BudgetRaw},
		{"rolled_up", f.RolledUpPerEvent(), BudgetRolledUp},
		{"sketch", f.SketchPerEvent(), BudgetSketch},
		{"manifests", f.ManifestsPerEvent(), BudgetManifests},
	} {
		status := "ok"
		if c.got > c.budget {
			status = "OVER"
		}
		fmt.Fprintf(&b, "%-12s %12.2f %10.0f %8s\n", c.name, c.got, c.budget, status)
	}
	fmt.Fprintf(&b, "%-12s %12.2f %10.0f %8s\n", "TOTAL", f.TotalPerEvent(), BudgetBytesPerEvent,
		map[bool]string{true: "ok", false: "OVER"}[f.MeetsBudget()])
	if !f.RawCompressed {
		fmt.Fprintf(&b, "\nnote: no raw fact file is stored compressed. §5.1 budgets raw at ≤70 "+
			"bytes/event \"compressed at rest\", so this total is not comparable to that budget "+
			"until something compresses them.\n")
	}
	return b.String()
}

// Measure walks a warehouse tree and decomposes its footprint.
//
// root is the directory holding raw/ and warehouse/, as docker-compose lays
// them out. events is the number of ingested facts the tree represents, which
// the caller knows and the tree does not record.
func Measure(root string, events int64) (*Footprint, error) {
	f := &Footprint{Events: events}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size := info.Size()

		switch {
		case strings.HasSuffix(path, ".manifest.json"):
			f.ManifestBytes += size

		case strings.HasSuffix(path, ".jsonl"):
			f.RawBytes += size

		case strings.HasSuffix(path, ".jsonl.zst"), strings.HasSuffix(path, ".jsonl.gz"):
			f.RawBytes += size
			f.RawCompressed = true

		case strings.HasSuffix(path, ".parquet"):
			sketch, err := sketchColumnBytes(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			f.SketchBytes += sketch
			f.RolledUpBytes += size - sketch
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

// sketchColumnBytes returns the compressed bytes the latency_sketch column
// occupies in one Parquet file, read from the file's own metadata rather than
// estimated. A file without the column contributes zero, which is how a
// pre-GRVX-804 partition measures correctly instead of failing.
func sketchColumnBytes(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, err
	}

	// TotalCompressedSize, not TotalUncompressedSize: a storage budget is about
	// what is on disk.
	var total int64
	for _, rg := range file.Metadata().RowGroups {
		for _, col := range rg.Columns {
			if len(col.MetaData.PathInSchema) == 1 &&
				col.MetaData.PathInSchema[0] == SketchColumn {
				total += col.MetaData.TotalCompressedSize
			}
		}
	}
	return total, nil
}
