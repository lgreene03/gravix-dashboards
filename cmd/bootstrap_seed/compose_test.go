// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const composeFile = "../../docker-compose.bootstrap.yml"

type composeService struct {
	Environment []any          `yaml:"environment"`
	Volumes     []string       `yaml:"volumes"`
	DependsOn   map[string]dep `yaml:"depends_on"`
	Entrypoint  any            `yaml:"entrypoint"`
}

type dep struct {
	Condition string `yaml:"condition"`
}

type composeFileDoc struct {
	Services map[string]composeService `yaml:"services"`
}

func loadCompose(t *testing.T) composeFileDoc {
	t.Helper()
	data, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("read compose file: %v", err)
	}
	var doc composeFileDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("AC-7 FAILED: the bootstrap compose file is not valid YAML: %v", err)
	}
	return doc
}

// ─── AC-7 ───

// TestBootstrapComposeConfigValid checks the file a first-time user runs.
//
// It parses rather than shelling out to `docker compose config`, so it holds
// without a Docker daemon; CI runs the real command as well.
func TestBootstrapComposeConfigValid(t *testing.T) {
	doc := loadCompose(t)

	for _, name := range []string{"bootstrap-init", "synthetic-traffic"} {
		if _, ok := doc.Services[name]; !ok {
			t.Errorf("AC-7 FAILED: no %q service; the stack needs no .env edits only if "+
				"something creates the tenant and something produces traffic", name)
		}
	}

	// Everything that reads the tenant database, and the dashboard that reads
	// the generated config, must wait for the seed to finish — not merely start.
	for _, name := range []string{"gateway", "ingestion", "dashboard"} {
		svc, ok := doc.Services[name]
		if !ok {
			t.Errorf("AC-7 FAILED: no %q service", name)
			continue
		}
		d, ok := svc.DependsOn["bootstrap-init"]
		if !ok {
			t.Errorf("AC-7 FAILED: %s does not depend on bootstrap-init, so it can start "+
				"against an unseeded database and reject every request", name)
			continue
		}
		if d.Condition != "service_completed_successfully" {
			t.Errorf("AC-7 FAILED: %s waits on bootstrap-init with condition %q; only "+
				"service_completed_successfully waits for the seed to finish", name, d.Condition)
		}
	}

	dashboard := doc.Services["dashboard"]
	var mountsConfig bool
	for _, v := range dashboard.Volumes {
		if strings.Contains(v, "dashboard_config.js") {
			mountsConfig = true
		}
		// The generated config lives beside the API key. Mounting the directory
		// instead of the one file would publish api_key.txt over HTTP.
		source, target, ok := strings.Cut(v, ":")
		if !ok {
			continue
		}
		if strings.TrimSuffix(source, "/") == "./data" && strings.Contains(target, "nginx/html") {
			t.Errorf("AC-7 FAILED: the dashboard serves the whole data directory (%q), which "+
				"publishes api_key.txt at a URL", v)
		}
	}
	if !mountsConfig {
		t.Error("AC-7 FAILED: the dashboard does not mount dashboard_config.js, so the " +
			"generated settings never reach the browser")
	}
}

// TestComposeEnvironmentEntriesAreStrings is the F-010 regression.
//
// `CUBEJS_DB_DUCKDB_DATABASE_PATH=:memory:` ends in a colon, which makes an
// unquoted YAML entry a mapping key rather than a string. Compose then rejects
// the whole file — no service starts, and the error names a type, not a line.
// The bootstrap stack had never once parsed.
func TestComposeEnvironmentEntriesAreStrings(t *testing.T) {
	doc := loadCompose(t)

	for name, svc := range doc.Services {
		for i, entry := range svc.Environment {
			if _, ok := entry.(string); ok {
				continue
			}
			t.Errorf("F-010 REGRESSION: %s.environment[%d] parsed as %T, not a string. A value "+
				"ending in a colon must be quoted, or Compose refuses to start anything: %v",
				name, i, entry, entry)
		}
	}
}

// TestBootstrapSeedIsBuiltIntoItsImage checks the wiring that no test can
// otherwise reach: the compose service runs ./bootstrap_seed, and that binary
// exists only if the Dockerfile builds and copies it.
func TestBootstrapSeedIsBuiltIntoItsImage(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "services", "rollup", "Dockerfile"))
	if err != nil {
		t.Fatalf("read rollup Dockerfile: %v", err)
	}
	body := string(data)

	if !strings.Contains(body, "-o bootstrap_seed ./cmd/bootstrap_seed") {
		t.Error("AC-7 FAILED: the rollup image does not build cmd/bootstrap_seed")
	}
	if !strings.Contains(body, "COPY --from=builder /app/bootstrap_seed .") {
		t.Error("AC-7 FAILED: the rollup image builds bootstrap_seed but never copies it into " +
			"the runtime stage, so the container would exit 127")
	}

	doc := loadCompose(t)
	init, ok := doc.Services["bootstrap-init"]
	if !ok {
		t.Fatal("AC-7 FAILED: no bootstrap-init service")
	}
	entry, ok := init.Entrypoint.([]any)
	if !ok || len(entry) == 0 || entry[0] != "./bootstrap_seed" {
		t.Errorf("AC-7 FAILED: bootstrap-init's entrypoint is %v, want [./bootstrap_seed]",
			init.Entrypoint)
	}
}
