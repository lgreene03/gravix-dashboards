// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package export

import (
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// rowWriter encodes a stream of rows of one type into one output file.
//
// Every writer is constructed around an io.Writer it does not own, so the
// caller decides where the bytes go — a file, an object store upload, or
// stdout — and the same encoder serves all three.
type rowWriter[T any] interface {
	Write(rows []T) error
	// Close flushes. It does not close the underlying writer.
	Close() error
}

// jsonlWriter emits one JSON object per line, field names taken from each
// field's json tag.
type jsonlWriter[T any] struct {
	enc *json.Encoder
}

func newJSONLWriter[T any](w io.Writer) *jsonlWriter[T] {
	return &jsonlWriter[T]{enc: json.NewEncoder(w)}
}

func (jw *jsonlWriter[T]) Write(rows []T) error {
	for i := range rows {
		if err := jw.enc.Encode(rows[i]); err != nil {
			return fmt.Errorf("export: encode jsonl row: %w", err)
		}
	}
	return nil
}

func (jw *jsonlWriter[T]) Close() error { return nil }

// csvWriter emits a header row followed by one row per record, with columns
// in struct declaration order so the header is stable across exports.
type csvWriter[T any] struct {
	w            *csv.Writer
	wroteHeader  bool
	columns      []string
	fieldIndexes []int
}

func newCSVWriter[T any](w io.Writer) *csvWriter[T] {
	var zero T
	cols, idx := csvColumns(reflect.TypeOf(zero))
	return &csvWriter[T]{w: csv.NewWriter(w), columns: cols, fieldIndexes: idx}
}

func (cw *csvWriter[T]) Write(rows []T) error {
	if !cw.wroteHeader {
		if err := cw.w.Write(cw.columns); err != nil {
			return fmt.Errorf("export: write csv header: %w", err)
		}
		cw.wroteHeader = true
	}

	record := make([]string, len(cw.fieldIndexes))
	for i := range rows {
		v := reflect.ValueOf(rows[i])
		for j, fi := range cw.fieldIndexes {
			record[j] = csvCell(v.Field(fi))
		}
		if err := cw.w.Write(record); err != nil {
			return fmt.Errorf("export: write csv row: %w", err)
		}
	}
	return nil
}

func (cw *csvWriter[T]) Close() error {
	// A CSV export of an empty partition is still a CSV: the header alone
	// tells a reader what the columns are, which is more useful than a
	// zero-byte file they have to guess about.
	if !cw.wroteHeader {
		if err := cw.w.Write(cw.columns); err != nil {
			return fmt.Errorf("export: write csv header: %w", err)
		}
		cw.wroteHeader = true
	}
	cw.w.Flush()
	return cw.w.Error()
}

// csvColumns returns the column names and the struct field indexes behind
// them, in declaration order, skipping unexported and json:"-" fields.
func csvColumns(t reflect.Type) ([]string, []int) {
	var cols []string
	var idx []int
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		name := jsonName(f)
		if name == "" {
			continue
		}
		cols = append(cols, name)
		idx = append(idx, i)
	}
	return cols, idx
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			tag = tag[:i]
			break
		}
	}
	if tag == "" {
		return f.Name
	}
	return tag
}

// csvCell renders one field. A map or slice becomes its JSON encoding in a
// single cell rather than being dropped: CSV has no nested types, and losing
// a column silently would make the export a lie.
func csvCell(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			// []byte — render as base64 via JSON so it survives a round trip.
			data, err := json.Marshal(v.Interface())
			if err != nil {
				return ""
			}
			return trimJSONQuotes(string(data))
		}
		fallthrough
	case reflect.Map, reflect.Struct:
		data, err := json.Marshal(v.Interface())
		if err != nil {
			return ""
		}
		return string(data)
	default:
		return fmt.Sprint(v.Interface())
	}
}

func trimJSONQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// parquetWriter buffers a partition and encodes it with the same ZSTD codec
// the warehouse itself uses, so an exported file and a warehouse file are the
// same kind of object.
type parquetWriter[T any] struct {
	w *parquet.GenericWriter[T]
}

func newParquetWriter[T any](w io.Writer) *parquetWriter[T] {
	return &parquetWriter[T]{
		w: parquet.NewGenericWriter[T](w, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault})),
	}
}

func (pw *parquetWriter[T]) Write(rows []T) error {
	if len(rows) == 0 {
		return nil
	}
	if _, err := pw.w.Write(rows); err != nil {
		return fmt.Errorf("export: write parquet rows: %w", err)
	}
	return nil
}

func (pw *parquetWriter[T]) Close() error {
	if err := pw.w.Close(); err != nil {
		return fmt.Errorf("export: close parquet writer: %w", err)
	}
	return nil
}

// newRowWriter builds the encoder for a format. gzip wraps csv and jsonl when
// requested; parquet is already compressed, so Compress is ignored for it —
// double-compressing would cost CPU and save nothing.
func newRowWriter[T any](w io.Writer, format Format, compress bool) (rowWriter[T], func() error, error) {
	closeNothing := func() error { return nil }

	switch format {
	case FormatParquet:
		return newParquetWriter[T](w), closeNothing, nil
	case FormatCSV, FormatJSONL:
		out := w
		closeOuter := closeNothing
		if compress {
			gz := gzip.NewWriter(w)
			out = gz
			closeOuter = gz.Close
		}
		if format == FormatCSV {
			return newCSVWriter[T](out), closeOuter, nil
		}
		return newJSONLWriter[T](out), closeOuter, nil
	default:
		return nil, closeNothing, fmt.Errorf("%w: %q", ErrUnknownFormat, format)
	}
}

// fileExtension is the suffix an exported file carries, so the name alone
// tells a reader (and their tooling) how to open it.
func fileExtension(format Format, compress bool) string {
	switch format {
	case FormatParquet:
		return ".parquet"
	case FormatCSV:
		if compress {
			return ".csv.gz"
		}
		return ".csv"
	case FormatJSONL:
		if compress {
			return ".jsonl.gz"
		}
		return ".jsonl"
	default:
		return ".out"
	}
}
