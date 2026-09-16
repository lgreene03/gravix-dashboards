//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// GRVX-1104 AC-7. The one criterion that needs Grafana itself.
//
// Everything else about the plugin is unit-tested inside its own module. This
// proves the thing those tests cannot: that Grafana, given the built dist/,
// recognises it as an installed datasource plugin. A plugin that compiles and
// does not load is indistinguishable from one that was never written.

const grafanaImage = "grafana/grafana:11.1.0"
const pluginID = "gravix-datasource"

// TestGrafanaLoadsPlugin builds the plugin, mounts it into a Grafana container,
// and asks Grafana's own API whether it is installed.
func TestGrafanaLoadsPlugin(t *testing.T) {
	requireDocker(t)

	root := repoRootFromE2E(t)
	dist := filepath.Join(root, "grafana-plugin", "gravix-datasource", "dist")

	buildPluginDist(t, root)

	port := freePort(t)
	name := fmt.Sprintf("gravix-plugin-test-%d", port)

	// --rm and an explicit remove on cleanup: a container left running holds a
	// port and the next run fails for a reason that has nothing to do with the
	// code.
	run := exec.Command("docker", "run", "--rm", "-d",
		"--name", name,
		"-p", fmt.Sprintf("127.0.0.1:%d:3000", port),
		"-e", "GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS="+pluginID,
		"-e", "GF_AUTH_ANONYMOUS_ENABLED=true",
		"-e", "GF_AUTH_ANONYMOUS_ORG_ROLE=Admin",
		"-v", dist+":/var/lib/grafana/plugins/"+pluginID,
		grafanaImage)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("starting Grafana: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForGrafana(t, base)

	plugins := listPlugins(t, base)
	if !plugins[pluginID] {
		logs, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
		t.Errorf("Grafana does not list %q among its plugins.\nGrafana logs:\n%s", pluginID, logs)
	}
}

// buildPluginDist produces the backend binary, the frontend bundle and
// plugin.json, which is exactly what a user is told to build in
// docs-site/docs/grafana-plugin.md.
func buildPluginDist(t *testing.T, root string) {
	t.Helper()

	mod := filepath.Join(root, "grafana-plugin", "gravix-datasource")

	backend := exec.Command("go", "build", "-o", "dist/gpx_gravix_datasource", "./cmd")
	backend.Dir = mod
	backend.Env = append(backend.Environ(), "CGO_ENABLED=0")
	if out, err := backend.CombinedOutput(); err != nil {
		t.Fatalf("building the plugin backend: %v\n%s", err, out)
	}

	// The frontend bundle is committed to dist/ by nobody — it is built. If
	// node_modules is absent this installs it, which is slow and is the
	// documented path, so the test does what the docs say rather than
	// something easier.
	for _, args := range [][]string{{"install", "--no-audit", "--no-fund"}, {"run", "build"}} {
		npm := exec.Command("npm", args...)
		npm.Dir = mod
		if out, err := npm.CombinedOutput(); err != nil {
			t.Fatalf("npm %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForGrafana(t *testing.T, base string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 3 * time.Second}
	for {
		resp, err := client.Get(base + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Grafana did not become healthy at %s within 90s", base)
		case <-time.After(2 * time.Second):
		}
	}
}

// listPlugins returns the set of plugin ids Grafana reports.
func listPlugins(t *testing.T, base string) map[string]bool {
	t.Helper()

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(base + "/api/plugins?embedded=0")
	if err != nil {
		t.Fatalf("listing plugins: %v", err)
	}
	defer resp.Body.Close()

	var list []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decoding the plugin list: %v", err)
	}

	out := map[string]bool{}
	for _, p := range list {
		out[p.ID] = true
	}
	return out
}
