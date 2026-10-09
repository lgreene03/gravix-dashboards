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

// TestHelmAnalyticsJobsReadTheTenantDatabase is F-079. The chart's analytics
// jobs set no tenant database, or one they could not open, so on Helm every
// rollup ran single-tenant and read a prefix nothing writes, and nothing made
// gravix.raw's tables or the serving views Cube reads. Every job that lists
// tenants must take the tenant database from the chart's one helper, and the
// catalog sync must exist.
func TestHelmAnalyticsJobsReadTheTenantDatabase(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "deploy", "gravix", "templates")
	for _, name := range []string{"rollup-job.yaml", "events-rollup-job.yaml", "events-detail-job.yaml",
		"retention-job.yaml", "trino-catalog-sync-job.yaml"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		for _, helper := range []string{`"gravix.tenantDB.env"`, `"gravix.tenantDB.volumeMounts"`, `"gravix.tenantDB.podSpec"`} {
			if !strings.Contains(src, helper) {
				t.Errorf("%s does not include %s; the job cannot see the tenants", name, helper)
			}
		}
		if strings.Contains(src, "TENANT_DB_PATH") {
			t.Errorf("%s sets TENANT_DB_PATH itself; only the helper knows whether the file is reachable", name)
		}
	}
}

// TestHelmTrinoIsTheVersionComposeRuns is F-079 too. The chart pinned Trino
// 351 with a catalog the version Compose runs cannot start with, and
// production values pinned that version against it. Every Trino test in this
// repository runs against Compose's version; the chart must run the same one,
// with a config that version accepts.
func TestHelmTrinoIsTheVersionComposeRuns(t *testing.T) {
	root := repoRoot(t)
	compose, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`image: trinodb/trino:(\S+)`).FindSubmatch(compose)
	if m == nil {
		t.Fatal("no trinodb/trino image in docker-compose.yml")
	}
	want := string(m[1])

	files, _ := filepath.Glob(filepath.Join(root, "deploy", "gravix", "values*.yaml"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Storage struct {
				Trino struct {
					Image struct {
						Tag *string `yaml:"tag"`
					} `yaml:"image"`
				} `yaml:"trino"`
			} `yaml:"storage"`
		}
		if err := yaml.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if tag := v.Storage.Trino.Image.Tag; tag != nil && *tag != want {
			t.Errorf("%s pins Trino %s; Compose runs %s", filepath.Base(f), *tag, want)
		}
	}

	tpl, err := os.ReadFile(filepath.Join(root, "deploy", "gravix", "templates", "trino.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"connector.name=hive-hadoop2", "query.max-total-memory-per-node", "discovery-server.enabled"} {
		if strings.Contains(string(tpl), gone) {
			t.Errorf("trino.yaml sets %s, which Trino %s does not accept", gone, want)
		}
	}
}
