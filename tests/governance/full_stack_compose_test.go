// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"os"
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
