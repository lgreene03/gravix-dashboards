// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEveryCubeTableIsWrittenOnTheBootstrapStack is F-071. On the bootstrap
// stack Cube reads the warehouse's Parquet directly, so every table a model
// names has to be written by some job in that stack. ServiceEvents reads
// service_events_detail, which only the full stack's jobs wrote: Cube answered
// "No files found", the dashboard turned the error into an empty list, and the
// events tab stayed blank while synthetic traffic sent an event every 30
// seconds.
func TestEveryCubeTableIsWrittenOnTheBootstrapStack(t *testing.T) {
	root := repoRoot(t)

	models, err := filepath.Glob(filepath.Join(root, "cube", "model", "schema", "*.js"))
	if err != nil {
		t.Fatal(err)
	}
	tableRef := regexp.MustCompile("tableSql\\(\\s*['\"`](\\w+)['\"`]\\s*\\)")
	read := map[string]string{}
	for _, m := range models {
		src, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range tableRef.FindAllStringSubmatch(string(src), -1) {
			read[ref[1]] = filepath.Base(m)
		}
	}
	if len(read) < 3 {
		t.Fatalf("found %d tables named by the Cube models; the models changed shape and this test is checking too little", len(read))
	}

	raw, err := os.ReadFile(filepath.Join(root, "docker-compose.bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Command any `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("docker-compose.bootstrap.yml is not valid YAML: %v", err)
	}
	written := map[string]string{}
	output := regexp.MustCompile(`-output-dir\s+\./data/warehouse/(\w+)`)
	for name, svc := range doc.Services {
		var cmd string
		switch c := svc.Command.(type) {
		case string:
			cmd = c
		case []any:
			for _, p := range c {
				if s, ok := p.(string); ok {
					cmd += s + "\n"
				}
			}
		}
		for _, m := range output.FindAllStringSubmatch(cmd, -1) {
			written[m[1]] = name
		}
	}

	for table, model := range read {
		if _, ok := written[table]; !ok {
			var have []string
			for w := range written {
				have = append(have, w)
			}
			t.Errorf("%s reads warehouse table %q, and no job in the bootstrap stack writes it "+
				"(written: %s)", model, table, strings.Join(have, ", "))
		}
	}
}
