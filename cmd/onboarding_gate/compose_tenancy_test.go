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

// ENTRYPOINT and CMD are matched separately as well, because Compose treats them
// oppositely: a service's `command` is APPENDED to ENTRYPOINT as arguments, but
// REPLACES CMD wholesale. So naming the binary in `command` is a bug against an
// ENTRYPOINT image and mandatory against a CMD-only one. The first version of the
// guard below used the combined pattern and reported load-generator — whose image
// has only CMD — as a defect. Conflating the two produces a false positive that
// would have been "fixed" by breaking a working service.
var (
	dockerExecEntrypointRe = regexp.MustCompile(`(?m)^\s*ENTRYPOINT\s*\[\s*"([^"]+)"`)
	dockerExecCmdRe        = regexp.MustCompile(`(?m)^\s*CMD\s*\[\s*"([^"]+)"`)
)

// imageInvocation reports the binary an image's ENTRYPOINT declares, and the one
// its CMD declares, as separate answers.
func imageInvocation(t *testing.T, dockerfile string) (entrypoint, cmd string) {
	t.Helper()
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatalf("read %s: %v", dockerfile, err)
	}
	if m := dockerExecEntrypointRe.FindStringSubmatch(string(data)); m != nil {
		entrypoint = strings.TrimPrefix(m[1], "./")
	}
	if m := dockerExecCmdRe.FindStringSubmatch(string(data)); m != nil {
		cmd = strings.TrimPrefix(m[1], "./")
	}
	return entrypoint, cmd
}

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

// ─── One stack, one signing secret ─────────────────────────────────────────

// The gateway signs the dashboard's token with JWT_SECRET; cube/cube.js
// checkAuth verifies it with JWT_SECRET and reads securityContext.tenant_id out
// of the result, which queryRewrite then turns into a mandatory filter. The two
// services must therefore resolve that variable to the same value.
//
// docker-compose.yml had them disagreeing — the gateway taking ${JWT_SECRET:?…}
// from the environment, cube carrying a literal — so every token the gateway
// issued would be rejected, leaving an empty dashboard with every container
// healthy. That is F-016's failure mode, and it was invisible only because the
// file's duplicate service key (F-011) stopped Compose loading it at all.
// Fixing the duplicate is what made the mismatch reachable. Recorded as F-029.
//
// The check is on the expression, not the resolved value: two services reading
// the same variable agree whatever the operator sets it to, while two literals
// that happen to match today drift the moment one is edited.
func TestOneStackResolvesOneSigningSecret(t *testing.T) {
	const secretVar = "JWT_SECRET"

	for _, composeFile := range composeFiles {
		data, err := os.ReadFile(composeFile)
		if err != nil {
			t.Fatalf("read %s: %v", composeFile, err)
		}
		var doc tenancyDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s is not loadable: %v", composeFile, err)
		}

		// Service name -> the expression it assigns to the variable.
		declared := map[string]string{}
		for name, svc := range doc.Services {
			if v, ok := envPairs(svc.Environment)[secretVar]; ok {
				declared[name] = v
			}
		}
		if len(declared) < 2 {
			// One declaring service cannot disagree with itself, and zero means
			// the stack does not use JWT auth. Neither is a defect.
			continue
		}

		names := make([]string, 0, len(declared))
		for n := range declared {
			names = append(names, n)
		}
		sort.Strings(names)

		want := declared[names[0]]
		for _, n := range names[1:] {
			if declared[n] != want {
				t.Errorf("F-029 REGRESSION: in %s, %q and %q assign different expressions to %s:\n"+
					"  %-12s %s\n"+
					"  %-12s %s\n"+
					"The gateway signs the dashboard's token with this secret and Cube verifies it\n"+
					"with the same name. Two different values mean Cube rejects every token the\n"+
					"gateway issues: an empty dashboard with every container reporting healthy.",
					filepath.Base(composeFile), names[0], n, secretVar,
					names[0], want, n, declared[n])
			}
		}
	}
}

// ─── command must not repeat the image's entrypoint binary ─────────────────

// Compose APPENDS a service's `command` to the image's ENTRYPOINT. So a service
// whose command begins with the same binary the Dockerfile already declares runs
// it as `./x ./x --flag`, where Go's flag package stops at the first positional
// argument and every flag after it is silently ignored.
//
// The bootstrap stack did this for both the gateway and ingestion. Nothing broke
// visibly: the gateway's dropped --tenant-db was covered by TENANT_DB_PATH, and
// ingestion's dropped --base-dir happened to equal the built-in default. A flag
// that is ignored while its value is right by luck is the kind of defect that
// surfaces the day someone changes the value. See F-032.
func TestServiceCommandDoesNotRepeatTheEntrypointBinary(t *testing.T) {
	checked := 0
	for _, composeFile := range composeFiles {
		data, err := os.ReadFile(composeFile)
		if err != nil {
			t.Fatalf("read %s: %v", composeFile, err)
		}
		var doc tenancyDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s is not loadable: %v", composeFile, err)
		}

		names := make([]string, 0, len(doc.Services))
		for n := range doc.Services {
			names = append(names, n)
		}
		sort.Strings(names)

		for _, name := range names {
			svc := doc.Services[name]
			if svc.Build == nil || svc.Build.Dockerfile == "" {
				continue
			}
			// A service that overrides entrypoint (e.g. to /bin/sh -c) is running
			// a script, not appending to the image's entrypoint, so the rule does
			// not apply.
			if strings.TrimSpace(flatten(svc.Entrypoint)) != "" {
				continue
			}
			cmd := strings.Fields(flatten(svc.Command))
			if len(cmd) == 0 {
				continue
			}
			entrypointBin, cmdBin := imageInvocation(t, filepath.Join("../..", svc.Build.Dockerfile))
			first := strings.TrimPrefix(cmd[0], "./")

			if entrypointBin != "" {
				checked++
				if first == entrypointBin {
					t.Errorf("F-032 REGRESSION: %s service %q sets command starting with %q, but its\n"+
						"image's ENTRYPOINT is already [\"./%s\"]. Compose APPENDS command to entrypoint,\n"+
						"so this runs `./%s ./%s ...` — Go's flag package stops at the first positional\n"+
						"argument and every flag after it is silently ignored. Drop the binary and pass\n"+
						"only flags.",
						filepath.Base(composeFile), name, cmd[0], entrypointBin, entrypointBin, entrypointBin)
				}
				continue
			}

			if cmdBin != "" {
				// The opposite rule: command REPLACES CMD, so it has to name
				// something executable. A command of bare flags would have Docker
				// try to exec the first flag.
				checked++
				if strings.HasPrefix(first, "-") {
					t.Errorf("F-032 REGRESSION: %s service %q sets command starting with the flag %q,\n"+
						"but its image declares no ENTRYPOINT — only CMD [\"./%s\"]. Compose REPLACES\n"+
						"CMD rather than appending to it, so this asks Docker to execute %q. Name the\n"+
						"binary first here.",
						filepath.Base(composeFile), name, cmd[0], cmdBin, cmd[0])
				}
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no service with an image entrypoint and an explicit command was found in %v;\n"+
			"the derivation broke rather than the stacks being clean", composeFiles)
	}
}

// ─── no signing secret is committed to a compose file ──────────────────────

// The bootstrap stack shipped JWT_SECRET=supersecretjwtkey12345! on two services.
// Two separate faults in one literal: it is published in this repository, so
// anyone could mint a token for any tenant; and at 23 characters it is shorter
// than the 32 services/gateway/main.go requires, so the gateway exited 1 on every
// boot and the dashboard's login could never succeed (F-029, F-032).
//
// The rule is on the shape, not on the string: a signing secret in a compose file
// must come from the environment or from a file, never from a literal. That holds
// for a secret nobody has thought of yet, where a check for this one value would
// not.
func TestNoSigningSecretIsHardcodedInCompose(t *testing.T) {
	secretish := regexp.MustCompile(`(?i)(SECRET|PASSWORD|TOKEN|API_KEY)`)
	// A literal is anything that is not an interpolation and not obviously a path.
	interpolated := regexp.MustCompile(`\$\{[^}]+\}`)

	for _, composeFile := range composeFiles {
		data, err := os.ReadFile(composeFile)
		if err != nil {
			t.Fatalf("read %s: %v", composeFile, err)
		}
		var doc tenancyDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s is not loadable: %v", composeFile, err)
		}
		for name, svc := range doc.Services {
			for k, v := range envPairs(svc.Environment) {
				if !secretish.MatchString(k) || v == "" {
					continue
				}
				// _FILE variables name a path, which is the shape we want.
				if strings.HasSuffix(k, "_FILE") {
					continue
				}
				if interpolated.MatchString(v) {
					continue
				}
				t.Errorf("F-029/F-032 REGRESSION: %s service %q sets %s to a literal value.\n"+
					"A secret written into a compose file is published with this repository, and a\n"+
					"literal cannot be rotated per deployment. Use ${%s:?...} to require it from the\n"+
					"environment, or %s_FILE pointing at a file bootstrap_seed generates.",
					filepath.Base(composeFile), name, k, k, k)
			}
		}
	}
}

// ─── F-030: the dashboard must not serve its own test files ────────────────

// ./dashboards is the nginx document root, so every file in that tree is
// reachable over HTTP — including dashboards/lib/*.test.js, served at
// /lib/*.test.js on every deployed bootstrap stack.
//
// storage/dashboard/nginx.conf denies them. Two things about that rule can break
// silently, and both are checked here:
//
//  1. the pattern must actually match the test files and must NOT match the real
//     application scripts, and
//  2. it must appear BEFORE the \.(css|js)$ block. nginx evaluates regex
//     locations in file order and takes the first match, so the same rule placed
//     after that block never runs — and the config still loads, still looks
//     right, and still serves the tests.
//
// The second is the reason this test exists. A reviewer reading the diff sees a
// deny rule; nothing about the file says the order is load-bearing.
func TestDashboardDoesNotServeTestFiles(t *testing.T) {
	const confPath = "../../storage/dashboard/nginx.conf"
	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read %s: %v", confPath, err)
	}
	conf := string(data)

	locRe := regexp.MustCompile(`(?m)^\s*location\s+~\*\s+(\S+)\s*\{`)
	matches := locRe.FindAllStringSubmatchIndex(conf, -1)
	if len(matches) == 0 {
		t.Fatal("no regex location blocks found; the config shape changed and this guard is " +
			"watching nothing")
	}

	var denyIdx, jsIdx = -1, -1
	var denyPattern string
	for i, m := range matches {
		pat := conf[m[2]:m[3]]
		switch {
		case strings.Contains(pat, `test`):
			if denyIdx == -1 {
				denyIdx, denyPattern = i, pat
			}
		case strings.Contains(pat, `css|js`):
			if jsIdx == -1 {
				jsIdx = i
			}
		}
	}

	if denyIdx == -1 {
		t.Fatalf("storage/dashboard/nginx.conf has no location block denying test files.\n"+
			"./dashboards is the document root, so %s is served at /lib/*.test.js on every\n"+
			"deployed stack (F-030).", "dashboards/lib/*.test.js")
	}
	if jsIdx == -1 {
		t.Fatal("no \\.(css|js)$ location found; the config shape changed")
	}
	if denyIdx > jsIdx {
		t.Errorf("F-030 REGRESSION: the test-file deny rule (%s) appears AFTER the \\.(css|js)$\n"+
			"block. nginx takes the first matching regex location in file order, so the deny\n"+
			"never runs and the tests are served again — with a config that loads cleanly and\n"+
			"reads as if it were correct.", denyPattern)
	}

	// Go's RE2 and nginx's PCRE agree on a pattern this simple; the risk being
	// checked is a wrong pattern, not a dialect difference.
	deny, err := regexp.Compile(`(?i)` + strings.TrimPrefix(denyPattern, `~*`))
	if err != nil {
		t.Fatalf("the deny pattern %q does not compile: %v", denyPattern, err)
	}

	served, err := filepath.Glob("../../dashboards/lib/*.js")
	if err != nil || len(served) == 0 {
		t.Fatalf("found no dashboard scripts to check (err=%v); the tree moved", err)
	}
	var tests, app int
	for _, f := range served {
		name := filepath.Base(f)
		isTest := strings.HasSuffix(name, ".test.js")
		blocked := deny.MatchString(name)
		switch {
		case isTest && !blocked:
			t.Errorf("F-030 REGRESSION: %s is a test file and the deny pattern %s does not match "+
				"it, so it is served at /lib/%s", name, denyPattern, name)
		case !isTest && blocked:
			t.Errorf("the deny pattern %s matches %s, which is application code the dashboard "+
				"needs. The rule is too broad.", denyPattern, name)
		}
		if isTest {
			tests++
		} else {
			app++
		}
	}
	if tests == 0 || app == 0 {
		t.Fatalf("checked %d test file(s) and %d application file(s); both must be non-zero or "+
			"this proves nothing", tests, app)
	}
	t.Logf("%d test file(s) denied, %d application file(s) still served", tests, app)
}
