// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestFullStackOwnsItsDataDirectory is F-025. ./data is gitignored, so on a
// fresh clone Docker creates the bind-mount source as root:root, and every
// Gravix image runs as the non-root `gravix` user. The bootstrap stack fixed
// that with a one-shot root container (F-021); the full stack had nothing. Every
// full-stack service that mounts ./data must now wait for data-init to finish,
// and data-init must chown the directory as root.
func TestFullStackOwnsItsDataDirectory(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			User      string   `yaml:"user"`
			Command   any      `yaml:"command"`
			Volumes   []string `yaml:"volumes"`
			DependsOn map[string]struct {
				Condition string `yaml:"condition"`
			} `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("docker-compose.yml is not valid YAML: %v", err)
	}

	init, ok := doc.Services["data-init"]
	if !ok {
		t.Fatal("docker-compose.yml has no data-init service, so nothing makes ./data writable (F-025)")
	}
	if init.User != "root" {
		t.Errorf("data-init runs as %q; it must be root to chown a root-owned directory", init.User)
	}
	cmd := ""
	switch c := init.Command.(type) {
	case string:
		cmd = c
	case []any:
		for _, p := range c {
			if s, ok := p.(string); ok {
				cmd += s + " "
			}
		}
	}
	if !strings.Contains(cmd, "chown -R gravix:gravix /app/data") {
		t.Errorf("data-init's command %q does not chown /app/data to gravix", cmd)
	}

	mounting := 0
	for name, svc := range doc.Services {
		if name == "data-init" {
			continue
		}
		for _, v := range svc.Volumes {
			if !strings.HasPrefix(v, "./data:") {
				continue
			}
			mounting++
			if got := svc.DependsOn["data-init"].Condition; got != "service_completed_successfully" {
				t.Errorf("%s mounts ./data but does not wait for data-init to complete (condition %q)", name, got)
			}
		}
	}
	if mounting < 6 {
		t.Fatalf("only %d services mount ./data; the file changed shape and this test is checking too little", mounting)
	}
}

// TestFullStackSeedsAnAPIKey is F-058. With TENANT_DB_PATH set, ingestion
// ignores API_KEY, and a new user cannot create a key before verifying an email
// the default mailer never sends. The full stack therefore has to seed a tenant
// and a key on first boot, as the bootstrap stack does, and the smoke test has
// to use that key rather than the .env one.
func TestFullStackSeedsAnAPIKey(t *testing.T) {
	root := repoRoot(t)
	compose, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Command any `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(compose, &doc); err != nil {
		t.Fatal(err)
	}
	cmd := fmt.Sprint(doc.Services["data-init"].Command)
	for _, want := range []string{"./bootstrap_seed", "-db=/app/data/gravix.db", "-api-key-file=/app/data/api_key.txt"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("data-init does not run %s; the full stack would boot with no usable API key", want)
		}
	}

	smoke, err := os.ReadFile(filepath.Join(root, "scripts", "smoke_test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(smoke), "cat /app/data/api_key.txt") {
		t.Error("smoke_test.sh does not read the seeded key")
	}
	if strings.Contains(string(smoke), "grep '^API_KEY=' .env") {
		t.Error("smoke_test.sh falls back to the .env API_KEY, which ingestion ignores in this stack")
	}
}

// TestTrinoRendersCatalogsInsideTheContainer is F-066. The full stack's Trino
// rendered its catalog templates into a bind mount of the checkout. Where the
// image's user could not write there, the render failed silently and Trino ran
// on a rendered copy someone had committed, with stale credentials and no
// Iceberg catalog; where it could, it wrote the S3 secret into the working
// tree. The catalogs now go to a tmpfs, the render stops the container if it
// fails, and no rendered catalog may sit in the repository.
func TestTrinoRendersCatalogsInsideTheContainer(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compose := string(raw)
	start := strings.Index(compose, "\n  trino:\n")
	if start < 0 {
		t.Fatal("no trino service in docker-compose.yml")
	}
	end := strings.Index(compose[start+1:], "\n  cube:\n")
	if end < 0 {
		t.Fatal("cannot find the end of the trino service")
	}
	trino := compose[start : start+1+end]

	for _, want := range []string{
		"- /etc/trino/catalog:mode=1777",
		"set -e",
		"test -s /etc/trino/catalog/gravix.properties",
		"test -s /etc/trino/catalog/gravix_iceberg.properties",
	} {
		if !strings.Contains(trino, want) {
			t.Errorf("the trino service lacks %q", want)
		}
	}
	if strings.Contains(trino, "./storage/trino/config:/etc/trino") {
		t.Error("the trino service bind-mounts the whole config directory over /etc/trino again")
	}

	// Tracked files only: a checkout that ran the old compose file has
	// untracked renders there, which .gitignore now keeps out of commits.
	cmd := exec.Command("git", "ls-files", "--", "storage/trino/config/catalog")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Logf("git ls-files unavailable (%v); not checking for committed renders", err)
		return
	}
	if tracked := strings.TrimSpace(string(out)); tracked != "" {
		t.Errorf("rendered catalogs are committed, where Trino would never read them:\n%s", tracked)
	}
}

// TestIcebergCatalogTypeExistsInPinnedTrino is SD-059. GRVX-1106 configured
// iceberg.catalog.type=hadoop, which Trino has never had, and the catalog
// failed to load and took Trino down. The allowed values are Trino 435's
// io.trino.plugin.iceberg.CatalogType enum; a Trino upgrade that changes them
// updates this list in the same commit.
//
// The Spark check also depends on two settings here: tables at a fixed path,
// and the metastore in the bucket rather than on Trino's disk.
func TestIcebergCatalogTypeExistsInPinnedTrino(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "storage", "trino", "catalog", "gravix_iceberg.properties"))
	if err != nil {
		t.Fatal(err)
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			props[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	trino435 := map[string]bool{
		"TESTING_FILE_METASTORE": true, "HIVE_METASTORE": true, "GLUE": true,
		"REST": true, "JDBC": true, "NESSIE": true,
	}
	if typ := strings.ToUpper(props["iceberg.catalog.type"]); !trino435[typ] {
		t.Errorf("iceberg.catalog.type=%q is not a catalog type Trino 435 has; Trino fails to load the catalog", props["iceberg.catalog.type"])
	}
	if props["iceberg.unique-table-location"] != "false" {
		t.Error("iceberg.unique-table-location must be false: the Spark check reads each table from a fixed path")
	}
	if !strings.HasPrefix(props["hive.metastore.catalog.dir"], "s3a://") {
		t.Errorf("hive.metastore.catalog.dir=%q: the Iceberg metastore belongs in the bucket, with the tables", props["hive.metastore.catalog.dir"])
	}
}

// TestTrinoCanWriteItsMetastore is F-068. data-init gives ./data to gravix,
// uid 100, and Trino runs as uid 1000, so Trino could not write its Hive
// metastore and the tables Cube reads were never created. The metastore must be
// handed to uid 1000 after that chown, and Trino must wait for it.
func TestTrinoCanWriteItsMetastore(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Command   any      `yaml:"command"`
			Volumes   []string `yaml:"volumes"`
			DependsOn map[string]struct {
				Condition string `yaml:"condition"`
			} `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("docker-compose.yml is not valid YAML: %v", err)
	}

	cmd := ""
	switch c := doc.Services["data-init"].Command.(type) {
	case string:
		cmd = c
	case []any:
		for _, p := range c {
			if s, ok := p.(string); ok {
				cmd += s + "\n"
			}
		}
	}
	toGravix := strings.Index(cmd, "chown -R gravix:gravix /app/data")
	toTrino := strings.Index(cmd, "chown -R 1000:1000 /app/data/trino-metastore")
	switch {
	case toTrino < 0:
		t.Error("data-init does not give data/trino-metastore to Trino's uid 1000")
	case toGravix > toTrino:
		t.Error("data-init gives data/trino-metastore to uid 1000 before giving all of ./data to gravix, which undoes it")
	}

	trino := doc.Services["trino"]
	mounted := false
	for _, v := range trino.Volumes {
		if strings.HasPrefix(v, "./data/trino-metastore:") {
			mounted = true
		}
	}
	if !mounted {
		t.Fatal("trino no longer mounts ./data/trino-metastore; update this test with where its metastore went")
	}
	if got := trino.DependsOn["data-init"].Condition; got != "service_completed_successfully" {
		t.Errorf("trino does not wait for data-init to complete (condition %q), so it can start on a metastore it cannot write", got)
	}
}
