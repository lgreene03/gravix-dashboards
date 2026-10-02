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

// TestCubeMeasuresDivideAsDoubles is CD-007's text check. The warehouse's
// counts are BIGINT, and Trino divides integers as integers, so a measure that
// divides one aggregate by another without a cast reports 0 for every ratio
// under 1 on the full stack, while DuckDB, on the bootstrap stack, gets it
// right. errorRate did. tests/e2e's TestCubeAnswersOnTheFullStack asks the full
// stack's Cube for the number; this catches the pattern before anything runs.
func TestCubeMeasuresDivideAsDoubles(t *testing.T) {
	models, err := filepath.Glob(filepath.Join(repoRoot(t), "cube", "model", "schema", "*.js"))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 {
		t.Fatal("no Cube models found")
	}
	uncast := regexp.MustCompile(`(?i)\b(sum|count)\([^()]*\)\s*/`)
	for _, m := range models {
		src, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range uncast.FindAllString(string(src), -1) {
			t.Errorf("%s divides %q without casting to DOUBLE first; on Trino that is integer "+
				"division (CD-007)", filepath.Base(m), hit)
		}
	}
}

// TestEveryCubeDeploymentNamesACacheDriver is F-074. In production mode, which
// every Gravix deployment runs, Cube's cache and queue driver defaults to Cube
// Store, and nothing here runs one, so Cube refuses every query. The bootstrap
// stack set memory in F-035; the full stack and the Helm chart without Redis
// never did, and nothing asked their Cube a question until the Cube upgrade
// measurement did.
func TestEveryCubeDeploymentNamesACacheDriver(t *testing.T) {
	root := repoRoot(t)
	for _, file := range []string{"docker-compose.yml", "docker-compose.bootstrap.yml"} {
		raw, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Services map[string]struct {
				Environment []string `yaml:"environment"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		cube, ok := doc.Services["cube"]
		if !ok {
			t.Fatalf("%s has no cube service", file)
		}
		found := false
		for _, e := range cube.Environment {
			if e == "CUBEJS_CACHE_AND_QUEUE_DRIVER=memory" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: cube does not set CUBEJS_CACHE_AND_QUEUE_DRIVER=memory, so it defaults to "+
				"Cube Store, which this stack does not run (F-074)", file)
		}
	}

	tpl, err := os.ReadFile(filepath.Join(root, "deploy", "gravix", "templates", "cube.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	memory := regexp.MustCompile(`(?s)\{\{-? else -?\}\}.*?name: CUBEJS_CACHE_AND_QUEUE_DRIVER\s+value: "memory"`)
	if !strings.Contains(string(tpl), "CUBEJS_CACHE_AND_QUEUE_DRIVER") || !memory.Match(tpl) {
		t.Errorf("the Helm chart's Cube must set CUBEJS_CACHE_AND_QUEUE_DRIVER to memory when Redis " +
			"is not configured; without it Cube defaults to Cube Store, which the chart does not run (F-074)")
	}
}
