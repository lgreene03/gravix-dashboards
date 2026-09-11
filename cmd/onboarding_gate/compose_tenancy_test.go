// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// This file guards the defect class that cost this repository the most CI time:
// a stack where every container reports healthy and no number ever reaches a
// chart. F-015 (ingestion wrote raw/raw/), F-016 (no user, so no Cube JWT) and
// F-027 (the rollups disagreed with ingestion about tenancy) were all that shape.
//
// F-027 in particular: ingestion is given TENANT_DB_PATH, so it authenticates
// per tenant and files facts under raw/<tenant-id>/request_facts/. The rollup
// jobs were not, so they took their single-tenant branch and read
// raw/request_facts/ — a prefix nothing writes — then wrote
// warehouse/request_metrics_minute/, a prefix Cube's multi-tenant glob does not
// read. Two halves of one pipeline, each correct alone, wired to different
// layouts.
//
// The guard deliberately does NOT list the services that need the variable. A
// check that matches on a name protects only the names its author knew about,
// and the next rollup job added to this stack would be unprotected on the day it
// lands. Instead the requirement is derived: the compose file says which
// Dockerfile builds each service, the Dockerfile says which Go package each
// binary comes from, and the package's own source says whether it changes
// behaviour based on TENANT_DB_PATH. Any binary that does must be told.

var composeFiles = []string{
	"../../docker-compose.bootstrap.yml",
	"../../docker-compose.yml",
}

type tenancyService struct {
	Image       string   `yaml:"image"`
	Build       *build   `yaml:"build"`
	Environment any      `yaml:"environment"`
	Volumes     []string `yaml:"volumes"`
	Entrypoint  any      `yaml:"entrypoint"`
	Command     any      `yaml:"command"`
}

type build struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
}

type tenancyDoc struct {
	Services map[string]tenancyService `yaml:"services"`
}

// tenancyEnvVar is the variable that switches every Gravix data-plane binary
// between its single-tenant and per-tenant data layout.
const tenancyEnvVar = "TENANT_DB_PATH"

// goBuildRe matches `go build ... -o <binary> <package>` as the Dockerfiles
// write it. Both captures are needed: the name is what compose invokes, the
// package is where the source that reads the environment lives.
var goBuildRe = regexp.MustCompile(`-o\s+(\S+)\s+(\./\S+)`)

// dockerEntrypointRe matches an exec-form ENTRYPOINT or CMD, whose first element
// is the binary a service runs when it overrides neither.
var dockerEntrypointRe = regexp.MustCompile(`(?m)^\s*(?:ENTRYPOINT|CMD)\s*\[\s*"([^"]+)"`)

// flatten renders any of compose's command shapes — a string, a list of
// strings, a list containing one heredoc script — as a single string. The shape
// is not the property under test and has changed twice in this repository
// already; what matters is which binary is named.
func flatten(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, flatten(e))
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(t)
	}
}

// envPairs renders compose's two environment shapes — a KEY=VALUE list or a
// mapping — as a name-to-value map.
func envPairs(v any) map[string]string {
	out := map[string]string{}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			s := flatten(e)
			name, value, found := strings.Cut(s, "=")
			if !found {
				// `- NAME` passes the variable through from the host. It counts
				// as declared; whether the host sets it is not a file property.
				out[strings.TrimSpace(s)] = ""
				continue
			}
			out[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	case map[string]any:
		for k, val := range t {
			out[k] = flatten(val)
		}
	}
	return out
}

// builtBinaries maps binary name to Go package for every binary a Dockerfile
// builds, and returns the binary its ENTRYPOINT/CMD runs by default.
func builtBinaries(t *testing.T, dockerfile string) (map[string]string, string) {
	t.Helper()
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatalf("read %s: %v", dockerfile, err)
	}
	text := string(data)

	bins := map[string]string{}
	for _, m := range goBuildRe.FindAllStringSubmatch(text, -1) {
		bins[m[1]] = m[2]
	}
	if len(bins) == 0 {
		t.Fatalf("%s builds no Go binary that this guard can find.\n"+
			"Either the build line changed shape, or the image no longer ships a\n"+
			"Gravix binary. Both need a human: a guard that silently finds nothing\n"+
			"to check reports green for a stack it never looked at.", dockerfile)
	}

	var entry string
	if m := dockerEntrypointRe.FindStringSubmatch(text); m != nil {
		entry = strings.TrimPrefix(m[1], "./")
	}
	return bins, entry
}

// readsTenancyVar reports whether the Go package at pkg (a ./-relative import
// path) changes behaviour based on TENANT_DB_PATH.
//
// It parses rather than greps, and looks only at string literals. The first
// version of this function searched the file text, and reported cmd/bootstrap_seed
// as tenancy-aware because a comment there mentions the variable by name — which
// would have forced a meaningless environment variable onto a service that takes
// its database path as an explicit flag. A guard whose false positives are
// silenced by changing production code is worse than no guard.
func readsTenancyVar(t *testing.T, pkg string) bool {
	t.Helper()
	dir := filepath.Join("../..", filepath.FromSlash(strings.TrimPrefix(pkg, "./")))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir %s (named by a Dockerfile build line): %v", dir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		// Mode 0: comments are not attached, so a comment naming the variable
		// cannot be mistaken for code reading it.
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if strings.Contains(lit.Value, tenancyEnvVar) {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// containerMounts returns the container-side path of every volume mapping.
func containerMounts(volumes []string) []string {
	var out []string
	for _, v := range volumes {
		parts := strings.Split(v, ":")
		if len(parts) >= 2 {
			out = append(out, parts[1])
		}
	}
	return out
}

// TestEveryTenancyAwareServiceIsToldWhichMode is the F-027 regression guard.
//
// For each compose file, for each service built from a repository Dockerfile, it
// resolves the binaries that service actually runs, asks each one's source
// whether TENANT_DB_PATH changes where it looks for data, and requires the
// service to answer that question — by environment variable or by an explicit
// path flag. A service that runs such a binary and says nothing is a service
// silently picking a layout the rest of the stack does not use.
func TestEveryTenancyAwareServiceIsToldWhichMode(t *testing.T) {
	checked := 0
	for _, composeFile := range composeFiles {
		data, err := os.ReadFile(composeFile)
		if err != nil {
			t.Fatalf("read %s: %v", composeFile, err)
		}
		var doc tenancyDoc
		// Decoding into a map is also what rejects a duplicate service key. The
		// full-stack file carried two `gateway:` blocks (F-011), which made it
		// unloadable by Compose as well; parsing into a yaml.Node would not have
		// noticed.
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s is not loadable: %v", composeFile, err)
		}

		names := make([]string, 0, len(doc.Services))
		for name := range doc.Services {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			svc := doc.Services[name]
			if svc.Build == nil || svc.Build.Dockerfile == "" {
				// A third-party image (nginx, cube) ships no Gravix binary.
				continue
			}
			dockerfile := filepath.Join("../..", svc.Build.Dockerfile)
			bins, defaultBin := builtBinaries(t, dockerfile)

			invoked := strings.TrimSpace(flatten(svc.Entrypoint) + " " + flatten(svc.Command))
			env := envPairs(svc.Environment)

			for bin, pkg := range bins {
				runs := strings.Contains(invoked, "./"+bin)
				if invoked == "" && bin == defaultBin {
					// Neither entrypoint nor command overridden: the image's own
					// ENTRYPOINT is what runs.
					runs = true
				}
				if !runs || !readsTenancyVar(t, pkg) {
					continue
				}
				checked++

				dbPath, declared := env[tenancyEnvVar]
				flagged := strings.Contains(invoked, "-tenant-db") ||
					strings.Contains(invoked, "-db=") ||
					strings.Contains(invoked, "-db ")
				if !declared && !flagged {
					t.Errorf("F-027 REGRESSION: %s service %q runs ./%s, whose source in %s\n"+
						"reads %s to decide whether data lives under raw/<tenant-id>/ or raw/,\n"+
						"but the service sets neither %s nor an explicit path flag.\n"+
						"It will silently pick a layout, and if another service in this same file\n"+
						"picked the other one, the pipeline is severed: facts are written where\n"+
						"nothing reads and metrics where nothing looks. Every container still\n"+
						"reports healthy.",
						filepath.Base(composeFile), name, bin, pkg, tenancyEnvVar, tenancyEnvVar)
					continue
				}
				if !declared || dbPath == "" {
					continue
				}
				// A declared path outside every mount is a path that cannot exist.
				mounts := containerMounts(svc.Volumes)
				reachable := false
				for _, m := range mounts {
					if dbPath == m || strings.HasPrefix(dbPath, strings.TrimSuffix(m, "/")+"/") {
						reachable = true
						break
					}
				}
				if !reachable {
					t.Errorf("F-027 REGRESSION: %s service %q sets %s=%s, but that path is\n"+
						"inside none of its mounts %v. The binary will find no database, fall\n"+
						"back to its single-tenant branch, and disagree with the rest of the\n"+
						"stack exactly as if the variable were absent.",
						filepath.Base(composeFile), name, tenancyEnvVar, dbPath, mounts)
				}
			}
		}
	}

	// A guard that finds nothing to check is indistinguishable from a passing
	// one. Both compose files run several tenancy-aware binaries; if this count
	// is zero the derivation broke, not the stack.
	if checked == 0 {
		t.Fatalf("this guard resolved no tenancy-aware binary in either compose file.\n"+
			"That is a defect in the guard, not a clean bill of health: the mapping\n"+
			"from service to Dockerfile to Go package must have broken. Files: %v",
			composeFiles)
	}
	t.Logf("checked %d tenancy-aware binary invocations across %d compose files", checked, len(composeFiles))
}

// TestTenancyModeIsUnanimousWithinAStack is the other half of F-027, stated
// directly: a single stack must not contain one service in per-tenant mode and
// another in single-tenant mode. The test above proves each service answered the
// question; this one proves they all gave the same answer.
func TestTenancyModeIsUnanimousWithinAStack(t *testing.T) {
	for _, composeFile := range composeFiles {
		data, err := os.ReadFile(composeFile)
		if err != nil {
			t.Fatalf("read %s: %v", composeFile, err)
		}
		var doc tenancyDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s is not loadable: %v", composeFile, err)
		}

		var perTenant, singleTenant []string
		for name, svc := range doc.Services {
			if svc.Build == nil || svc.Build.Dockerfile == "" {
				continue
			}
			bins, defaultBin := builtBinaries(t, filepath.Join("../..", svc.Build.Dockerfile))
			invoked := strings.TrimSpace(flatten(svc.Entrypoint) + " " + flatten(svc.Command))
			aware := false
			for bin, pkg := range bins {
				runs := strings.Contains(invoked, "./"+bin)
				if invoked == "" && bin == defaultBin {
					runs = true
				}
				if runs && readsTenancyVar(t, pkg) {
					aware = true
					break
				}
			}
			if !aware {
				continue
			}
			if _, ok := envPairs(svc.Environment)[tenancyEnvVar]; ok {
				perTenant = append(perTenant, name)
			} else {
				singleTenant = append(singleTenant, name)
			}
		}
		sort.Strings(perTenant)
		sort.Strings(singleTenant)

		if len(perTenant) > 0 && len(singleTenant) > 0 {
			t.Errorf("F-027 REGRESSION: %s is a split-brain stack.\n"+
				"  per-tenant  (%s set): %v\n"+
				"  single-tenant (unset): %v\n"+
				"Both groups mount the same ./data. The first writes and reads\n"+
				"raw/<tenant-id>/... , the second raw/... — so whichever side holds the\n"+
				"rollup produces nothing from what ingestion wrote, and the dashboard is\n"+
				"empty while `docker compose ps` is entirely green.",
				filepath.Base(composeFile), tenancyEnvVar, perTenant, singleTenant)
		}
		if len(perTenant) == 0 && len(singleTenant) == 0 {
			t.Errorf("%s: no tenancy-aware service found. The derivation from service to\n"+
				"Dockerfile to Go package has broken; this is not a clean result.",
				filepath.Base(composeFile))
		}
	}
}

// ─── The compose-validation step's environment ─────────────────────────────

// `docker compose config` resolves interpolation, and a ${VAR:?message}
// whose variable is unset fails the parse. The docker-lint job therefore has to
// supply a value for every such variable in the file it validates.
//
// That list was assembled by hand once and was wrong the first time: it named
// JWT_SECRET and missed MINIO_ROOT_PASSWORD, which would have turned a required
// check red on a file nothing in CI had ever loaded. Hand-maintained lists of
// names decay the moment someone adds a name, so this derives both sides — the
// requirement from the compose files, the provision from the workflow — and
// fails when they diverge.

// requiredVarRe matches ${VAR:?message}, the form that makes a variable
// mandatory. ${VAR} and ${VAR:-default} do not fail the parse and are excluded.
var requiredVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):\?`)

// composeConfigRe matches the compose file named by a `docker compose -f X config`
// command, so the test learns which file a step validates rather than assuming.
var composeConfigRe = regexp.MustCompile(`docker\s+compose\s+-f\s+(\S+)\s+config`)

type workflow struct {
	Jobs map[string]struct {
		Steps []struct {
			Name string            `yaml:"name"`
			Run  string            `yaml:"run"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestComposeValidationStepSuppliesEveryRequiredVariable(t *testing.T) {
	const workflowPath = "../../.github/workflows/ci.yml"
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}
	var wf workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", workflowPath, err)
	}

	validated := 0
	for jobName, job := range wf.Jobs {
		for _, step := range job.Steps {
			m := composeConfigRe.FindStringSubmatch(step.Run)
			if m == nil {
				continue
			}
			composePath := filepath.Join("../..", m[1])
			composeData, err := os.ReadFile(composePath)
			if err != nil {
				t.Errorf("job %q step %q validates %s, which cannot be read: %v",
					jobName, step.Name, m[1], err)
				continue
			}
			validated++

			supplied := map[string]bool{}
			for k := range step.Env {
				supplied[k] = true
			}
			seen := map[string]bool{}
			for _, v := range requiredVarRe.FindAllStringSubmatch(string(composeData), -1) {
				name := v[1]
				if seen[name] {
					continue
				}
				seen[name] = true
				if !supplied[name] {
					t.Errorf("%s declares ${%s:?…}, which `docker compose config` treats as\n"+
						"mandatory — the parse fails and job %q goes red — but step %q supplies no\n"+
						"value for it. Add %s to that step's env, or give the variable a :- default\n"+
						"in the compose file.",
						m[1], name, jobName, step.Name, name)
				}
			}
		}
	}

	// Both compose files must actually be validated somewhere. Dropping a step
	// would otherwise make this test pass by having nothing to check — the same
	// failure mode as a guard that finds nothing.
	if validated < 2 {
		t.Errorf("only %d compose file(s) are validated by a `docker compose ... config` step in\n"+
			"ci.yml; both docker-compose.yml and docker-compose.bootstrap.yml must be. Nothing in\n"+
			"CI loaded the full-stack file for four months, which is how F-011 survived.", validated)
	}
}
