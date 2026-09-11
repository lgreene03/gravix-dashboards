// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package manifest describes a derived metric file: what window it covers, what
// inputs produced it, and a digest of its contents.
//
// A manifest answers two questions about a Parquet file without reading it: is
// this the same window as that one, and did the contents change. The first is
// the idempotency key, which depends only on what the partition IS. The second
// is the content digest, which covers row values and deliberately ignores the
// Parquet container — a compression change or a library upgrade must not look
// like a data change.
package manifest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// SchemaVersion is the manifest format version. Bump on any field change.
//
// v2 added PreviousDigest and RevisedAt (GRVX-805). A v1 manifest is still
// readable: the two fields decode as empty, which is exactly what Revision 0
// means, so an unrevised v1 partition and an unrevised v2 partition say the same
// thing.
const SchemaVersion = 2

// Extension is the manifest file suffix. It deliberately does not end in
// ".parquet", so a manifest is never matched by the warehouse's read_parquet
// glob and read as data.
const Extension = ".manifest.json"

// singleTenant is the tenant segment used when a deployment has no tenants, so
// an idempotency key never contains an empty segment.
const singleTenant = "_single"

var (
	// ErrNoManifest is returned when a data file has no manifest beside it.
	ErrNoManifest = errors.New("manifest: no manifest for data file")
	// ErrSchemaTooNew is returned when a manifest was written by a newer binary.
	ErrSchemaTooNew = errors.New("manifest: schema version is newer than this binary supports")
	// ErrDigestMismatch is returned when a manifest's digest disagrees with the
	// rows it claims to describe.
	ErrDigestMismatch = errors.New("manifest: content digest does not match data file")
	// ErrNotRows is returned when a digest is asked for something that is not a
	// sequence of rows.
	ErrNotRows = errors.New("manifest: rows must be a slice or array")
)

// Manifest sits beside a metric Parquet file and describes it.
//
// Field order here is the serialised field order, and the golden fixture in
// testdata pins it. Renaming or reordering a field is a format change and needs
// a SchemaVersion bump.
type Manifest struct {
	SchemaVersion  int      `json:"schema_version"`
	Metric         string   `json:"metric"`
	MetricVersion  string   `json:"metric_version"`
	IdempotencyKey string   `json:"idempotency_key"`
	ContentDigest  string   `json:"content_digest"`
	TenantID       string   `json:"tenant_id"`
	EventDay       string   `json:"event_day"`
	WindowFrom     string   `json:"window_from"`
	WindowTo       string   `json:"window_to"`
	RowCount       int64    `json:"row_count"`
	FactCount      int64    `json:"fact_count"`
	SourceFactKeys []string `json:"source_fact_keys"`
	Revision       int      `json:"revision"`
	DataFile       string   `json:"data_file"`

	// PreviousDigest is the ContentDigest this partition held before the most
	// recent revision. Empty at Revision 0. It is what lets a consumer prove a
	// number changed rather than merely suspect it.
	PreviousDigest string `json:"previous_digest"`

	// RevisedAt is the RFC3339 UTC time of the most recent revision. Empty at
	// Revision 0.
	//
	// This is the only wall-clock value in a manifest, and it is deliberately
	// excluded from ContentDigest — which covers row content only — so recording
	// when a revision happened cannot make the output non-reproducible.
	RevisedAt string `json:"revised_at"`
}

// schemaTooNewError carries the versions involved while still matching
// ErrSchemaTooNew under errors.Is.
type schemaTooNewError struct {
	got       int
	supported int
}

func (e schemaTooNewError) Error() string {
	return fmt.Sprintf("manifest: schema version %d is newer than this binary supports (%d)", e.got, e.supported)
}

func (e schemaTooNewError) Unwrap() error { return ErrSchemaTooNew }

// IdempotencyKey returns the stable identity of a derived partition. It depends
// only on what the partition IS, never on when or how it was computed, so two
// files describing the same window share a key however they were produced.
func IdempotencyKey(metric, metricVersion, tenantID string, day time.Time) string {
	if tenantID == "" {
		tenantID = singleTenant
	}
	return fmt.Sprintf("%s:%s:%s:%s", metric, metricVersion, tenantID, day.UTC().Format("20060102"))
}

// ContentDigest returns a digest over row contents. Two partitions with the same
// rows in the same order have the same digest, on any machine.
//
// The digest covers row values only — never the Parquet container, its
// compression, or its metadata.
func ContentDigest(rows any) (string, error) {
	v := reflect.ValueOf(rows)
	if rows == nil || (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) {
		return "", ErrNotRows
	}

	h := sha256.New()
	for i := 0; i < v.Len(); i++ {
		canonical, err := canonicalJSON(v.Index(i).Interface())
		if err != nil {
			return "", fmt.Errorf("manifest: canonicalising row %d: %w", i, err)
		}
		if i > 0 {
			h.Write([]byte("\n"))
		}
		h.Write(canonical)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// canonicalJSON renders a row with object keys sorted and no whitespace.
//
// The value is marshalled, decoded into generic containers, and re-marshalled.
// That round trip is what sorts the keys, and it makes the digest independent of
// the order fields happen to be declared in — two structs with the same field
// names and values digest the same, which is what "content identity" has to mean
// if it is to survive a struct being tidied up.
func canonicalJSON(row any) ([]byte, error) {
	raw, err := json.Marshal(row)
	if err != nil {
		return nil, err
	}
	var generic any
	dec := json.NewDecoder(bytes.NewReader(raw))
	// UseNumber keeps integers exact; without it every number round-trips
	// through float64 and a large int64 loses its last digits.
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return json.Marshal(generic)
}

// Path returns the manifest path for a data file path. It never returns a path
// ending in ".parquet".
func Path(dataFile string) string {
	return strings.TrimSuffix(dataFile, ".parquet") + Extension
}

// Write serialises m to store at Path(m.DataFile).
//
// Callers must write the data file first. A manifest without its data file is a
// detectable inconsistency; a data file without a manifest is not.
func Write(ctx context.Context, store storage.ObjectStore, m *Manifest) error {
	path := Path(m.DataFile)
	data, err := Encode(m)
	if err != nil {
		return fmt.Errorf("manifest: write %s: %w", path, err)
	}
	if err := store.Put(ctx, path, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("manifest: write %s: %w", path, err)
	}
	return nil
}

// Encode renders a manifest in its on-disk form: indented JSON with a trailing
// newline, so a stored manifest reads cleanly in a terminal and diffs by field.
func Encode(m *Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Read loads the manifest for a data file. Returns ErrNoManifest when absent and
// ErrSchemaTooNew when it was written by a newer binary.
func Read(ctx context.Context, store storage.ObjectStore, dataFile string) (*Manifest, error) {
	path := Path(dataFile)

	exists, err := store.Exists(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", path, err)
	}
	if !exists {
		return nil, fmt.Errorf("%w %s", ErrNoManifest, dataFile)
	}

	rc, err := store.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", path, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", path, err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", path, err)
	}
	if m.SchemaVersion > SchemaVersion {
		return nil, schemaTooNewError{got: m.SchemaVersion, supported: SchemaVersion}
	}
	return &m, nil
}

// Verify checks that a data file's manifest describes the rows given. It is what
// turns the digest from a recorded value into a claim that can fail.
func Verify(ctx context.Context, store storage.ObjectStore, dataFile string, rows any) error {
	m, err := Read(ctx, store, dataFile)
	if err != nil {
		return err
	}
	digest, err := ContentDigest(rows)
	if err != nil {
		return err
	}
	if digest != m.ContentDigest {
		return fmt.Errorf("%w %s", ErrDigestMismatch, dataFile)
	}
	return nil
}

// Merge builds the manifest for a file produced by merging sources, per the
// compaction rules: the merged file keeps its own identity and digest, and
// inherits the union of its sources' lineage.
//
// merged carries the fields that describe the output itself — Metric,
// MetricVersion, TenantID, EventDay, WindowFrom, WindowTo, RowCount, DataFile and
// ContentDigest. Merge fills in the rest from the sources.
func Merge(merged Manifest, sources []*Manifest) *Manifest {
	merged.SchemaVersion = SchemaVersion
	merged.IdempotencyKey = IdempotencyKey(merged.Metric, merged.MetricVersion, merged.TenantID, parseDay(merged.EventDay))

	keys := map[string]struct{}{}
	var factCount int64
	revision := 0
	for _, s := range sources {
		if s == nil {
			continue
		}
		for _, k := range s.SourceFactKeys {
			keys[k] = struct{}{}
		}
		factCount += s.FactCount
		if s.Revision > revision {
			revision = s.Revision
		}
	}

	merged.SourceFactKeys = sortedKeys(keys)
	merged.FactCount = factCount
	merged.Revision = revision
	return &merged
}

// Revise returns the manifest a partition should carry after its rows changed.
//
// The revision counter is a factual statement that a published window's value
// changed after first publication. It is not an error: late facts are the system
// working as designed (docs/00-system-truth.md §5), and a counter that never
// advanced would leave a consumer unable to tell a corrected number from an
// original one.
func Revise(next Manifest, previous *Manifest, revisedAt time.Time) *Manifest {
	if previous == nil {
		// First publication. Nothing was superseded, so there is nothing to record.
		next.Revision = 0
		next.PreviousDigest = ""
		next.RevisedAt = ""
		return &next
	}

	if next.ContentDigest == previous.ContentDigest {
		// The rows are the same, so this is the same revision as before. Carrying
		// the previous values forward keeps a rebuild that changes nothing from
		// looking like a change.
		next.Revision = previous.Revision
		next.PreviousDigest = previous.PreviousDigest
		next.RevisedAt = previous.RevisedAt
		return &next
	}

	next.Revision = previous.Revision + 1
	next.PreviousDigest = previous.ContentDigest
	next.RevisedAt = revisedAt.UTC().Format(time.RFC3339)
	return &next
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parseDay(eventDay string) time.Time {
	t, err := time.Parse("2006-01-02", eventDay)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
