// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryBuiltBinaryIsCopiedIntoTheImage is F-069. The rollup image's
// builder stage built iceberg-sync, and the final stage never copied it, so the
// iceberg-sync service printed "./iceberg-sync: not found" every five minutes
// and synced nothing. A binary a builder stage writes with -o and no later
// stage copies is either dead weight or, as here, a service with nothing to
// run.
func TestEveryBuiltBinaryIsCopiedIntoTheImage(t *testing.T) {
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "services", "*", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join(root, "action", "Dockerfile"))

	output := regexp.MustCompile(`go build\b[^&]*?\s-o\s+(\S+)`)
	copied := regexp.MustCompile(`(?m)^COPY\s+--from=\S+\s+(.+)$`)

	checked := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := strings.ReplaceAll(string(raw), "\\\n", " ")
		inImage := map[string]bool{}
		for _, m := range copied.FindAllStringSubmatch(src, -1) {
			fields := strings.Fields(m[1])
			for _, p := range fields[:len(fields)-1] {
				inImage[filepath.Base(p)] = true
			}
		}
		rel, _ := filepath.Rel(root, f)
		for _, m := range output.FindAllStringSubmatch(src, -1) {
			checked++
			if bin := filepath.Base(m[1]); !inImage[bin] {
				t.Errorf("%s builds %s and no later stage copies it, so the image does not contain it", rel, bin)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("found only %d go build outputs across %d Dockerfiles; the files changed shape and this test is checking too little", checked, len(files))
	}
}
