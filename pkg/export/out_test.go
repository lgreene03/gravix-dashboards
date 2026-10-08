// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package export

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// A stream with nothing to export writes nothing. Closing a Parquet or gzip
// writer emits a footer, and the gateway answers an empty range with 422 only
// while it has not sent a byte (DD-041).
func TestEmptyRangeWritesNothingToTheStream(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []Format{FormatParquet, FormatCSV, FormatJSONL} {
		for _, compress := range []bool{false, true} {
			var out bytes.Buffer
			_, err := Run(context.Background(), store, Request{
				Dataset: DatasetFacts, Format: f, Compress: compress,
				From: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
				Destination: "-", Out: &out,
			})
			if !errors.Is(err, ErrNoData) {
				t.Errorf("%s compress=%v: err = %v, want ErrNoData", f, compress, err)
			}
			if out.Len() != 0 {
				t.Errorf("%s compress=%v: wrote %d bytes for an empty range", f, compress, out.Len())
			}
		}
	}
}
