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
	Command     any            `yaml:"command"`
	User        string         `yaml:"user"`
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

// commandText flattens a service's entrypoint and command into one string,
// whatever shape they take. The compose file legitimately moved from
// entrypoint:[./bootstrap_seed] + command:[flags] to a shell invocation when
// F-021 required a chown first, and a test that asserts the literal shape
// breaks on a change that keeps every property it was meant to protect.
func commandText(svc composeService) string {
	var parts []string
	for _, field := range []any{svc.Entrypoint, svc.Command} {
		switch v := field.(type) {
		case string:
			parts = append(parts, v)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					parts = append(parts, s)
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

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
	if cmd := commandText(init); !strings.Contains(cmd, "./bootstrap_seed") {
		t.Errorf("AC-7 FAILED: bootstrap-init never invokes ./bootstrap_seed; it runs: %s", cmd)
	}
}

// The login file has no useful default inside the container: the sibling flags
// are absolute /app/data paths, so an unset -login-file would write the
// dashboard password relative to the image's working directory instead of the
// mounted volume, where nobody would find it. See F-016.
func TestBootstrapInitIsToldWhereToWriteTheLogin(t *testing.T) {
	doc := loadCompose(t)
	init, ok := doc.Services["bootstrap-init"]
	if !ok {
		t.Fatal("no bootstrap-init service")
	}
	cmd := commandText(init)
	idx := strings.Index(cmd, "-login-file=")
	if idx < 0 {
		t.Fatalf("bootstrap-init has no -login-file flag; it runs: %s", cmd)
	}
	found := strings.Fields(cmd[idx+len("-login-file="):])[0]

	// Must land on the mounted volume, beside the API key, or it is lost when
	// the container exits.
	if !strings.HasPrefix(found, "/app/data/") {
		t.Errorf("-login-file=%q is not under the /app/data mount, so it would not survive the container", found)
	}

	// And it must not be reachable over HTTP. The dashboard deliberately mounts
	// the single config file rather than the data directory; a login file
	// served on port 8000 would be worse than the empty dashboard it fixes.
	dashboard, ok := doc.Services["dashboard"]
	if !ok {
		t.Fatal("no dashboard service")
	}
	for _, v := range dashboard.Volumes {
		source := strings.SplitN(v, ":", 2)[0]
		if source == "./data" || source == "./data/" {
			t.Errorf("dashboard mounts %q, which would serve the login file over HTTP", v)
		}
		if strings.Contains(v, "login.txt") {
			t.Errorf("dashboard mounts the login file: %q", v)
		}
	}
}

// TestBootstrapInitOwnsTheDataVolume is the regression guard for F-021.
//
// ./data is gitignored, so on a fresh clone it does not exist and Docker creates
// the bind-mount target as root:root. Every image in this stack runs as the
// non-root `gravix` user, so nothing could write it: `docker compose up` died on
// the first container with "service bootstrap-init didn't complete successfully:
// exit 1", and SQLite reported the permission failure as "unable to open
// database file: out of memory (14)", which sends a reader looking for a memory
// problem that does not exist.
//
// The fix is one privileged one-shot container that chowns the mount and exits.
// This test holds both halves: that it does the chown, and that it is the ONLY
// service granted root.
func TestBootstrapInitOwnsTheDataVolume(t *testing.T) {
	doc := loadCompose(t)

	init, ok := doc.Services["bootstrap-init"]
	if !ok {
		t.Fatal("no bootstrap-init service")
	}
	if init.User != "root" && init.User != "0" && init.User != "0:0" {
		t.Errorf("bootstrap-init runs as %q; it needs root to chown a bind mount Docker created "+
			"as root:root on a fresh clone", init.User)
	}

	cmd := commandText(init)
	if !strings.Contains(cmd, "chown") || !strings.Contains(cmd, "gravix") {
		t.Errorf("bootstrap-init does not chown /app/data to gravix; every other service "+
			"runs non-root and cannot write it. It runs: %s", cmd)
	}

	// Twice: once before seeding so the seeder can write, once after so the
	// files it created as root can be read by the services that are not.
	// synthetic-traffic reads api_key.txt at mode 0600.
	if n := strings.Count(cmd, "chown"); n < 2 {
		t.Errorf("bootstrap-init chowns %d time(s); it must also chown AFTER seeding, or the "+
			"0600 api_key.txt stays root-owned and synthetic-traffic cannot read it", n)
	}

	// The seeder's exit status must still reach compose, or a failed seed looks
	// like a successful one and every dependent service starts against an
	// unprovisioned database.
	if !strings.Contains(cmd, "exit $$status") && !strings.Contains(cmd, "exit $status") {
		t.Errorf("bootstrap-init does not propagate bootstrap_seed's exit status, so a failed "+
			"seed would report success: %s", cmd)
	}

	// And root stays confined to this one container.
	for name, svc := range doc.Services {
		if name == "bootstrap-init" {
			continue
		}
		if svc.User == "root" || svc.User == "0" || svc.User == "0:0" {
			t.Errorf("service %q runs as root; only the one-shot bootstrap-init may, and only "+
				"to fix the bind-mount ownership", name)
		}
	}
}
