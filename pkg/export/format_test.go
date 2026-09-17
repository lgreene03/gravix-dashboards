// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package export

import (
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type sampleRow struct {
	Name     string            `json:"name"`
	Count    int64             `json:"count"`
	Ratio    float64           `json:"ratio"`
	OK       bool              `json:"ok"`
	Blob     []byte            `json:"blob"`
	Tags     map[string]string `json:"tags"`
	Ignored  string            `json:"-"`
	unexport string
}

func sampleRows() []sampleRow {
	return []sampleRow{
		{Name: "first", Count: 1, Ratio: 0.5, OK: true, Blob: []byte("hi"), Tags: map[string]string{"k": "v"}, Ignored: "no", unexport: "no"},
		{Name: "second, with comma", Count: 2, Ratio: 0.25, OK: false, Tags: map[string]string{}},
	}
}

func TestCSVWriterHeaderAndValues(t *testing.T) {
	var buf bytes.Buffer
	w := newCSVWriter[sampleRow](&buf)
	if err := w.Write(sampleRows()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}

	wantHeader := []string{"name", "count", "ratio", "ok", "blob", "tags"}
	if !reflect.DeepEqual(records[0], wantHeader) {
		t.Fatalf("header = %v, want %v (json:\"-\" and unexported fields excluded)", records[0], wantHeader)
	}
	if len(records) != 3 {
		t.Fatalf("got %d records, want header + 2 rows", len(records))
	}
	if records[1][0] != "first" || records[1][1] != "1" || records[1][3] != "true" {
		t.Errorf("row 1 = %v", records[1])
	}
	// A value containing a comma must survive the round trip as one field.
	if records[2][0] != "second, with comma" {
		t.Errorf("comma-bearing value = %q", records[2][0])
	}
	// The map has no CSV type; it must be JSON in one cell, not dropped.
	if records[1][5] != `{"k":"v"}` {
		t.Errorf("map cell = %q", records[1][5])
	}
}

// An empty partition still gets a header: it tells the reader the columns,
// which is more use than a zero-byte file they have to guess about.
func TestCSVWriterWritesHeaderForEmptyInput(t *testing.T) {
	var buf bytes.Buffer
	w := newCSVWriter[sampleRow](&buf)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "name,count,ratio,ok,blob,tags") {
		t.Fatalf("empty csv = %q, want a header row", buf.String())
	}
}

func TestJSONLWriterOneObjectPerLine(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONLWriter[sampleRow](&buf)
	if err := w.Write(sampleRows()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
		if _, present := got["Ignored"]; present {
			t.Errorf("line %d carries a json:\"-\" field", i)
		}
	}
}

func TestParquetWriterProducesReadableFile(t *testing.T) {
	var buf bytes.Buffer
	w := newParquetWriter[sampleRow](&buf)
	if err := w.Write(sampleRows()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := buf.Bytes(); len(got) < 4 || string(got[:4]) != "PAR1" {
		t.Fatalf("output is not a parquet file")
	}

	rows, err := decodeParquet[sampleRow](buf.Bytes())
	if err != nil {
		t.Fatalf("decodeParquet: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("read %d rows, want 2", len(rows))
	}
	if rows[0].Name != "first" || rows[0].Count != 1 {
		t.Errorf("row 0 = %+v", rows[0])
	}
}

func TestNewRowWriterGzipsWhenAsked(t *testing.T) {
	for _, format := range []Format{FormatCSV, FormatJSONL} {
		t.Run(string(format), func(t *testing.T) {
			var buf bytes.Buffer
			w, closeOuter, err := newRowWriter[sampleRow](&buf, format, true)
			if err != nil {
				t.Fatalf("newRowWriter: %v", err)
			}
			if err := w.Write(sampleRows()); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if err := closeOuter(); err != nil {
				t.Fatalf("closeOuter: %v", err)
			}

			gz, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("output is not gzip: %v", err)
			}
			plain, err := io.ReadAll(gz)
			if err != nil {
				t.Fatalf("read gzip: %v", err)
			}
			if !bytes.Contains(plain, []byte("first")) {
				t.Errorf("decompressed output lost the data: %q", plain)
			}
		})
	}
}

func TestNewRowWriterRejectsUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if _, _, err := newRowWriter[sampleRow](&buf, "avro", false); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("err = %v, want ErrUnknownFormat", err)
	}
}

func TestFileExtensionMatchesFormatAndCompression(t *testing.T) {
	cases := []struct {
		format   Format
		compress bool
		want     string
	}{
		{FormatParquet, false, ".parquet"},
		{FormatParquet, true, ".parquet"}, // already compressed; never .gz
		{FormatCSV, false, ".csv"},
		{FormatCSV, true, ".csv.gz"},
		{FormatJSONL, false, ".jsonl"},
		{FormatJSONL, true, ".jsonl.gz"},
	}
	for _, tc := range cases {
		if got := fileExtension(tc.format, tc.compress); got != tc.want {
			t.Errorf("fileExtension(%s, %v) = %q, want %q", tc.format, tc.compress, got, tc.want)
		}
	}
}

func TestHowToReadNamesTheRightTool(t *testing.T) {
	cases := []struct {
		dataset  Dataset
		format   Format
		compress bool
		contains []string
	}{
		{DatasetMetrics, FormatParquet, false, []string{"duckdb", "read_parquet", "metrics_*.parquet"}},
		{DatasetFacts, FormatCSV, false, []string{"duckdb", "read_csv_auto", "facts_*.csv"}},
		{DatasetFacts, FormatJSONL, false, []string{"duckdb", "read_json_auto", "facts_*.jsonl"}},
		{DatasetEvents, FormatCSV, true, []string{"gunzip", "events_*.csv.gz"}},
	}

	for _, tc := range cases {
		got := HowToRead(tc.dataset, tc.format, tc.compress)
		for _, want := range tc.contains {
			if !strings.Contains(got, want) {
				t.Errorf("HowToRead(%s, %s, %v) = %q, want it to contain %q", tc.dataset, tc.format, tc.compress, got, want)
			}
		}
	}
}

func TestSchemaOfDescribesColumnsNotGoTypes(t *testing.T) {
	schema := schemaOf[sampleRow]()

	want := map[string]string{
		"name":  "string",
		"count": "integer",
		"ratio": "double",
		"ok":    "boolean",
		"blob":  "bytes",
		"tags":  "map<string,string>",
	}
	if !reflect.DeepEqual(schema, want) {
		t.Fatalf("schemaOf = %v, want %v", schema, want)
	}

	// A manifest reader should never meet a Go spelling.
	for col, typ := range schema {
		for _, leak := range []string{"int32", "int64", "float64", "uint8", "[]"} {
			if strings.Contains(typ, leak) {
				t.Errorf("column %q has Go-flavoured type %q", col, typ)
			}
		}
	}
}

func TestDecodeJSONLSkipsBlankLines(t *testing.T) {
	data := []byte("{\"name\":\"a\"}\n\n   \n{\"name\":\"b\"}\n")
	rows, err := decodeJSONL[sampleRow](data)
	if err != nil {
		t.Fatalf("decodeJSONL: %v", err)
	}
	if len(rows) != 2 || rows[0].Name != "a" || rows[1].Name != "b" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestDecodeJSONLReportsMalformedLine(t *testing.T) {
	if _, err := decodeJSONL[sampleRow]([]byte("{\"name\":\"a\"}\n{not json\n")); err == nil {
		t.Fatal("malformed jsonl accepted")
	}
}
