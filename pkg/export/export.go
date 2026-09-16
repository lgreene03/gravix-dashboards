// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package export writes Gravix data to open formats. It is a bulk file
// operation, not a query interface: it takes a dataset and a time range and
// nothing else, which is what keeps it clear of non-goal §5.
//
// There is no plan gate, volume cap or row limit anywhere in this package, and
// there must never be one. Export is the anti-lock-in guarantee (charter §2.3);
// a user who can leave easily is a user who can stay by choice.
package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/parquet-go/parquet-go"
)

// Dataset is what to export.
type Dataset string

const (
	// DatasetFacts exports raw RequestFacts — the recompute source of truth.
	DatasetFacts Dataset = "facts"
	// DatasetMetrics exports rolled-up minute metrics.
	DatasetMetrics Dataset = "metrics"
	// DatasetEvents exports ServiceEvents.
	DatasetEvents Dataset = "events"
)

// Format is the output encoding.
type Format string

const (
	FormatParquet Format = "parquet"
	FormatCSV     Format = "csv"
	FormatJSONL   Format = "jsonl"
)

// Request is one export.
//
// Note what is absent: there is no filter, predicate, selector or expression
// field, and adding one would turn this into the per-request query interface
// docs/04-non-goals.md §5 forbids. A caller who wants to ask questions of the
// data runs SQL over the exported Parquet.
type Request struct {
	Dataset     Dataset
	Format      Format
	From        time.Time // inclusive
	To          time.Time // exclusive
	TenantID    string    // empty for single-tenant
	Destination string    // "file:///path", "s3://bucket/prefix", or "-" for stdout
	Compress    bool      // gzip for csv and jsonl; parquet is already compressed
}

// Result reports what was written.
type Result struct {
	Files        []string      `json:"files"`
	Rows         int64         `json:"rows"`
	BytesWritten int64         `json:"bytes_written"`
	Manifest     string        `json:"manifest"`
	Duration     time.Duration `json:"duration"`
}

var (
	ErrUnknownDataset = errors.New("export: unknown dataset")
	ErrUnknownFormat  = errors.New("export: unknown format")
	ErrEmptyRange     = errors.New("export: To must be after From")
	ErrBadDestination = errors.New("export: destination must be file://, s3://, or -")
	ErrNoData         = errors.New("export: no data in range")
)

// ManifestSchemaVersion is the version of the manifest document itself, so a
// reader written against today's manifest can tell whether it understands
// tomorrow's.
const ManifestSchemaVersion = 1

// Version is the Gravix version recorded in a manifest. It is a variable so a
// build can stamp it; it is not read from anywhere else.
var Version = "dev"

// Manifest is written beside every export's files.
//
// HowToRead is the point of the whole document. An export that arrives without
// instructions for opening it outside Gravix is only technically an export.
type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	ExportedAt    time.Time         `json:"exported_at"`
	GravixVersion string            `json:"gravix_version"`
	Dataset       Dataset           `json:"dataset"`
	Format        Format            `json:"format"`
	From          time.Time         `json:"from"`
	To            time.Time         `json:"to"`
	Files         []string          `json:"files"`
	Rows          int64             `json:"rows"`
	Schema        map[string]string `json:"schema"`
	HowToRead     string            `json:"how_to_read"`
}

// FactRow mirrors the RequestFact wire schema. It is declared here rather than
// aliased from the generated protobuf type because an export file's columns
// are a published interface: a reader's script breaks if they change, so they
// change deliberately, in this struct, and not as a side effect of a proto
// edit.
type FactRow struct {
	EventID         string `json:"event_id" parquet:"event_id"`
	EventTime       string `json:"event_time" parquet:"event_time"`
	Service         string `json:"service" parquet:"service"`
	Method          string `json:"method" parquet:"method"`
	PathTemplate    string `json:"path_template" parquet:"path_template"`
	StatusCode      int32  `json:"status_code" parquet:"status_code"`
	LatencyMs       int32  `json:"latency_ms" parquet:"latency_ms"`
	UserAgentFamily string `json:"user_agent_family" parquet:"user_agent_family"`
	TenantID        string `json:"tenant_id" parquet:"tenant_id"`
}

// EventRow mirrors the ServiceEvent wire schema, for the same reason FactRow
// is declared rather than aliased.
type EventRow struct {
	EventID    string            `json:"event_id" parquet:"event_id"`
	EventTime  string            `json:"event_time" parquet:"event_time"`
	Service    string            `json:"service" parquet:"service"`
	EventType  string            `json:"event_type" parquet:"event_type"`
	EntityID   string            `json:"entity_id" parquet:"entity_id"`
	Message    string            `json:"message" parquet:"message"`
	Properties map[string]string `json:"properties" parquet:"properties"`
	TenantID   string            `json:"tenant_id" parquet:"tenant_id"`
}

// MetricRow is the warehouse row shape, aliased from the package that owns the
// reproducibility contract so an exported metrics file and a warehouse file
// always carry the same columns.
type MetricRow = recompute.MetricRow

// Run executes an export. It streams: memory use is bounded by one partition,
// not by the size of the range, so exporting 90 days does not need 90 days of
// RAM.
func Run(ctx context.Context, store storage.ObjectStore, req Request) (*Result, error) {
	start := time.Now()

	if err := validate(req); err != nil {
		return nil, err
	}

	dest, err := parseDestination(req.Destination)
	if err != nil {
		return nil, err
	}

	var res *Result
	switch req.Dataset {
	case DatasetFacts:
		res, err = runDataset[FactRow](ctx, store, req, dest, rawPrefix(req.TenantID, "request_facts"), decodeJSONL[FactRow])
	case DatasetEvents:
		res, err = runDataset[EventRow](ctx, store, req, dest, rawPrefix(req.TenantID, "service_events"), decodeJSONL[EventRow])
	case DatasetMetrics:
		res, err = runDataset[MetricRow](ctx, store, req, dest, warehousePrefix(req.TenantID, "request_metrics_minute"), decodeParquet[MetricRow])
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownDataset, req.Dataset)
	}
	if err != nil {
		return nil, err
	}

	res.Duration = time.Since(start)
	return res, nil
}

func validate(req Request) error {
	switch req.Dataset {
	case DatasetFacts, DatasetMetrics, DatasetEvents:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownDataset, req.Dataset)
	}

	switch req.Format {
	case FormatParquet, FormatCSV, FormatJSONL:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownFormat, req.Format)
	}

	if !req.To.After(req.From) {
		return ErrEmptyRange
	}
	return nil
}

// destination is a parsed Destination: exactly one of its fields is set.
type destination struct {
	stdout bool
	dir    string // local filesystem directory, from file://
	prefix string // object-store key prefix, from s3://
}

func parseDestination(raw string) (destination, error) {
	switch {
	case raw == "-":
		return destination{stdout: true}, nil
	case strings.HasPrefix(raw, "file://"):
		dir := strings.TrimPrefix(raw, "file://")
		if dir == "" {
			return destination{}, ErrBadDestination
		}
		return destination{dir: dir}, nil
	case strings.HasPrefix(raw, "s3://"):
		prefix := strings.TrimPrefix(raw, "s3://")
		if prefix == "" {
			return destination{}, ErrBadDestination
		}
		return destination{prefix: prefix}, nil
	default:
		return destination{}, ErrBadDestination
	}
}

// rawPrefix is the object-store prefix for a JSONL topic, matching the
// "raw/<topic>/<day>/<hour>/" layout ingestion writes.
func rawPrefix(tenantID, topic string) string {
	if tenantID == "" {
		return "raw/" + topic
	}
	return "raw/" + tenantID + "/" + topic
}

// warehousePrefix is the object-store prefix for a Parquet metric, matching
// the "warehouse/<metric>/event_day=<day>/" layout the rollup writes.
func warehousePrefix(tenantID, metric string) string {
	if tenantID == "" {
		return "warehouse/" + metric
	}
	return "warehouse/" + tenantID + "/" + metric
}

// partitionKeysFor returns the keys belonging to one day, for either layout.
func partitionKeysFor(ctx context.Context, store storage.ObjectStore, prefix, day string, hive bool) ([]string, error) {
	p := prefix + "/" + day
	if hive {
		p = prefix + "/event_day=" + day
	}
	keys, err := store.List(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("export: list %s: %w", p, err)
	}
	return keys, nil
}

// decoder turns one source object's bytes into rows.
type decoder[T any] func(data []byte) ([]T, error)

func decodeJSONL[T any](data []byte) ([]T, error) {
	var rows []T
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row T
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("export: decode jsonl: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func decodeParquet[T any](data []byte) ([]T, error) {
	f, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("export: open parquet: %w", err)
	}
	reader := parquet.NewGenericReader[T](f)
	defer reader.Close()

	rows := make([]T, reader.NumRows())
	if len(rows) == 0 {
		return nil, nil
	}
	n, err := reader.Read(rows)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("export: read parquet rows: %w", err)
	}
	return rows[:n], nil
}

// runDataset walks the range one day at a time. Only one partition's rows are
// held at once, which is what makes the memory bound the partition rather than
// the range.
func runDataset[T any](ctx context.Context, store storage.ObjectStore, req Request, dest destination, prefix string, dec decoder[T]) (*Result, error) {
	hive := req.Dataset == DatasetMetrics

	res := &Result{}
	var stdoutWriter *countingWriter
	var stdoutRows rowWriter[T]
	var closeStdout func() error

	// stdout is one stream, not one file per day, so its encoder is opened
	// once and fed every partition.
	if dest.stdout {
		stdoutWriter = &countingWriter{w: os.Stdout}
		var err error
		stdoutRows, closeStdout, err = newRowWriter[T](stdoutWriter, req.Format, req.Compress)
		if err != nil {
			return nil, err
		}
	}

	for day := req.From.UTC().Truncate(24 * time.Hour); day.Before(req.To.UTC()); day = day.AddDate(0, 0, 1) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		dayStr := day.Format("2006-01-02")

		keys, err := partitionKeysFor(ctx, store, prefix, dayStr, hive)
		if err != nil {
			return nil, err
		}

		var rows []T
		for _, key := range keys {
			data, err := readObject(ctx, store, key)
			if err != nil {
				return nil, err
			}
			decoded, err := dec(data)
			if err != nil {
				return nil, fmt.Errorf("export: %s: %w", key, err)
			}
			rows = append(rows, decoded...)
		}

		if len(rows) == 0 {
			continue
		}
		res.Rows += int64(len(rows))

		if dest.stdout {
			if err := stdoutRows.Write(rows); err != nil {
				return nil, err
			}
			continue
		}

		name := fmt.Sprintf("%s_%s%s", req.Dataset, strings.ReplaceAll(dayStr, "-", ""), fileExtension(req.Format, req.Compress))
		written, err := writePartition(ctx, store, dest, name, req, rows)
		if err != nil {
			return nil, err
		}
		res.Files = append(res.Files, name)
		res.BytesWritten += written
	}

	if dest.stdout {
		if err := stdoutRows.Close(); err != nil {
			return nil, err
		}
		if err := closeStdout(); err != nil {
			return nil, err
		}
		res.BytesWritten = stdoutWriter.n
	}

	if res.Rows == 0 {
		return nil, fmt.Errorf("%w: %s .. %s", ErrNoData, req.From.UTC().Format(time.RFC3339), req.To.UTC().Format(time.RFC3339))
	}

	// stdout gets no manifest file: there is nowhere to put it, and mixing it
	// into the data stream would corrupt the very file the manifest describes.
	if !dest.stdout {
		manifestName, err := writeManifest(ctx, store, dest, req, res, schemaOf[T]())
		if err != nil {
			return nil, err
		}
		res.Manifest = manifestName
	}

	return res, nil
}

func readObject(ctx context.Context, store storage.ObjectStore, key string) ([]byte, error) {
	rc, err := store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("export: get %s: %w", key, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("export: read %s: %w", key, err)
	}
	return data, nil
}

func writePartition[T any](ctx context.Context, store storage.ObjectStore, dest destination, name string, req Request, rows []T) (int64, error) {
	var buf bytes.Buffer
	rw, closeOuter, err := newRowWriter[T](&buf, req.Format, req.Compress)
	if err != nil {
		return 0, err
	}
	if err := rw.Write(rows); err != nil {
		return 0, err
	}
	if err := rw.Close(); err != nil {
		return 0, err
	}
	if err := closeOuter(); err != nil {
		return 0, err
	}

	if err := put(ctx, store, dest, name, buf.Bytes()); err != nil {
		return 0, err
	}
	return int64(buf.Len()), nil
}

// put writes one output file to wherever the destination points.
func put(ctx context.Context, store storage.ObjectStore, dest destination, name string, data []byte) error {
	if dest.dir != "" {
		if err := os.MkdirAll(dest.dir, 0o755); err != nil {
			return fmt.Errorf("cannot write to file://%s: %w", dest.dir, err)
		}
		full := filepath.Join(dest.dir, name)
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return fmt.Errorf("cannot write to %s: %w", full, err)
		}
		return nil
	}

	key := path.Join(dest.prefix, name)
	if err := store.Put(ctx, key, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("cannot write to s3://%s: %w", key, err)
	}
	return nil
}

func writeManifest(ctx context.Context, store storage.ObjectStore, dest destination, req Request, res *Result, schema map[string]string) (string, error) {
	m := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		GravixVersion: Version,
		Dataset:       req.Dataset,
		Format:        req.Format,
		From:          req.From.UTC(),
		To:            req.To.UTC(),
		Files:         res.Files,
		Rows:          res.Rows,
		Schema:        schema,
		HowToRead:     HowToRead(req.Dataset, req.Format, req.Compress),
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("export: encode manifest: %w", err)
	}
	data = append(data, '\n')

	const name = "manifest.json"
	if err := put(ctx, store, dest, name, data); err != nil {
		return "", err
	}
	return name, nil
}

// HowToRead returns a command that opens this export with a tool that is not
// Gravix. It is exported so the CLI can print it too — the instruction is no
// use if it only ever lands in a file the user has not opened yet.
func HowToRead(dataset Dataset, format Format, compress bool) string {
	glob := fmt.Sprintf("%s_*%s", dataset, fileExtension(format, compress))

	switch format {
	case FormatParquet:
		return fmt.Sprintf("duckdb -c \"SELECT * FROM read_parquet('%s') LIMIT 10;\"", glob)
	case FormatCSV:
		if compress {
			return fmt.Sprintf("gunzip -c %s | head", glob)
		}
		return fmt.Sprintf("duckdb -c \"SELECT * FROM read_csv_auto('%s') LIMIT 10;\"", glob)
	case FormatJSONL:
		if compress {
			return fmt.Sprintf("gunzip -c %s | head", glob)
		}
		return fmt.Sprintf("duckdb -c \"SELECT * FROM read_json_auto('%s') LIMIT 10;\"", glob)
	default:
		return ""
	}
}

// countingWriter counts bytes so a stdout export can still report a size.
type countingWriter struct {
	w io.Writer
	n int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}

// schemaOf describes a row type as column name → type, for the manifest.
//
// It is derived from the struct rather than hand-written, so a manifest can
// never describe a column set the files do not have.
func schemaOf[T any]() map[string]string {
	var zero T
	t := reflect.TypeOf(zero)
	schema := make(map[string]string, t.NumField())

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name := jsonName(f)
		if name == "" {
			continue
		}
		schema[name] = manifestType(f.Type)
	}
	return schema
}

// manifestType maps a Go type to the vocabulary a manifest reader expects,
// rather than leaking Go spellings like "int32" or "[]uint8".
func manifestType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "double"
	case reflect.Bool:
		return "boolean"
	case reflect.Map:
		return "map<string,string>"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return "bytes"
		}
		return "list"
	default:
		return t.String()
	}
}
