// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// zstdLevel matches the level expression in a parquet-go zstd codec literal.
var zstdLevel = regexp.MustCompile(`zstd\.Codec\{\s*Level:\s*([A-Za-z0-9_.]+)\s*\}`)

// TestEveryParquetWriterUsesTheRecomputeLevel closes CD-005. pkg/recompute pins
// its zstd level so a recomputed partition is byte-identical run to run, and
// Gravix's recomputability claim rests on comparing those bytes. Five other
// production writers used zstd.SpeedDefault, so the same rows written by two
// writers produced different digests, and a partition touched by both could
// never again report Unchanged.
//
// Every non-test Parquet writer must take its level from
// recompute.CompressionLevel. Test fixtures are exempt: they produce inputs,
// and a fixture written at a different level is a useful check that readers do
// not depend on one.
func TestEveryParquetWriterUsesTheRecomputeLevel(t *testing.T) {
	root := repoRoot(t)
	var writers int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range zstdLevel.FindAllStringSubmatch(string(src), -1) {
			writers++
			level := m[1]
			ok := level == "recompute.CompressionLevel" ||
				(level == "CompressionLevel" && filepath.ToSlash(filepath.Dir(rel)) == "pkg/recompute")
			if !ok {
				t.Errorf("%s writes Parquet at zstd level %s; use recompute.CompressionLevel so the "+
					"same rows produce the same bytes whichever job writes them (CD-005)", rel, level)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if writers < 5 {
		t.Fatalf("found only %d Parquet writers; the pattern no longer matches how the "+
			"repository constructs a zstd codec, so this test is checking nothing", writers)
	}
}
