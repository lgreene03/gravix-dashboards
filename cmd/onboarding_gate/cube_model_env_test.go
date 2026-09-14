// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This file guards F-037, whose lesson is the one this repository keeps relearning:
// a guard written against the mechanism that produces a property passes for every
// route to breaking the property that does not go through that mechanism.
//
// F-037 itself: Cube v0.35 evaluates every file under the model directory with
// vm.runInNewContext and an explicit, closed sandbox — cube, view, context,
// addExport, setExport, asyncModule, require, COMPILE_CONTEXT. A fresh V8 context
// has no `process`, because `process` is a Node global rather than a V8 intrinsic.
// So `process.env.CUBEJS_DB_TYPE` in a model file does not read the environment:
// guarded by `typeof process !== 'undefined'` it silently takes the false branch.
//
// Every environment-derived flag in the models was therefore dead, and BOTH stacks
// compiled to the Trino SQL — including the bootstrap stack, which runs DuckDB over
// Parquet and no Trino at all, so it asked a catalog that does not exist for
// gravix.raw.request_metrics_minute.
//
// The guard that missed this matched on the NAME `hasExternalStore` and injected a
// fake `process` into its own sandbox to test it. It passed for the same reason the
// defect existed: it tested the flag, not the sandbox the flag runs in.
//
// So this guard does not look at names, or at the text of the models at all. It
// evaluates each model the way Cube does — in a context with no `process` — and
// asserts the SQL that comes out actually changes with the environment. Any route
// to a model that silently ignores its environment fails it, whatever the flag is
// called and wherever it is written.
//
// The models are JavaScript, so this needs a node binary. Without one the test
// skips rather than passing vacuously: a guard that cannot run has not held.

// repoRootDir is the repository root relative to this package, matching the
// "../.." the other guards in this directory already use.
func repoRootDir(t *testing.T) string {
	t.Helper()
	return filepath.FromSlash("../..")
}

// modelRoot is the directory Cube mounts at /cube/conf/model.
func modelRoot(t *testing.T) string {
	t.Helper()
	// Absolute, because the harness hands it to Node's createRequire, which
	// rejects a relative path.
	dir, err := filepath.Abs(filepath.Join(repoRootDir(t), "cube", "model"))
	if err != nil {
		t.Fatalf("resolving the cube model directory: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cube model directory not found at %s: %v", dir, err)
	}
	return dir
}

// nodeBin finds an interpreter for the model files.
func nodeBin(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("node"); err == nil {
		return p
	}
	t.Skip("node not available; cannot evaluate the Cube models the way Cube does")
	return ""
}

// harness evaluates every model file the way DataSchemaCompiler.compileJsFile does
// — vm.runInNewContext with Cube's sandbox and nothing else — and prints the `sql`
// of each cube it defines as JSON.
//
// The sandbox below is deliberately a transcription of Cube's, not an approximation
// of it. It omits `process` because Cube's omits `process`; that omission is the
// whole subject of the test. `require` mirrors Cube's fallthrough for a path that
// does not resolve to another model file: Node's own require, resolved against the
// model root, which is what puts model_flags.js in the ordinary Node context.
const modelEvalHarness = `
const fs = require('fs'), path = require('path'), vm = require('vm'), Module = require('module');
const MODEL_ROOT = process.argv[2];
function walk(d, b) { let o = [];
  for (const f of fs.readdirSync(path.join(d, b || ''))) {
    const r = b ? path.join(b, f) : f;
    if (fs.statSync(path.join(d, r)).isDirectory()) o = o.concat(walk(d, r));
    else if (r.endsWith('.js')) o.push(r);
  } return o; }

const out = {};
for (const rel of walk(MODEL_ROOT)) {
  const content = fs.readFileSync(path.join(MODEL_ROOT, rel), 'utf8');
  const base = {
    cube: (name, def) => { out[String(name)] = def && def.sql; },
    view: () => {}, context: () => {}, addExport: () => {}, setExport: () => {},
    asyncModule: () => {},
    require: (p) => Module.createRequire(path.join(MODEL_ROOT, 'model.js'))(
      p.startsWith('.') ? path.resolve(MODEL_ROOT, p) : p),
    COMPILE_CONTEXT: { securityContext: {} },
  };
  // Cube transpiles a cube's member references (drillMembers: [service, ...])
  // into functions with the symbols injected, so a bare member identifier is
  // never evaluated as a global. This harness does not transpile, so unknown
  // identifiers resolve to undefined instead of throwing.
  //
  // WITHHELD is the exception, and it is the subject of the test: Cube's sandbox
  // has no 'process', so this one does not either. An unguarded process.env still
  // throws here exactly as it would under Cube, and a guarded one still silently
  // takes its false branch — which the SQL comparisons above then catch.
  const WITHHELD = new Set(['process']);
  const sandbox = new Proxy(base, {
    has: (t, k) => !WITHHELD.has(k),
    get: (t, k) => (WITHHELD.has(k) ? undefined : t[k]),
  });
  vm.runInNewContext(content, sandbox, { filename: rel, timeout: 15000 });
}
console.log(JSON.stringify(out));
`

// compileModelSQL evaluates the models under env and returns cube name -> sql.
func compileModelSQL(t *testing.T, env map[string]string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harness, []byte(modelEvalHarness), 0o600); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(nodeBin(t), harness, modelRoot(t))
	// A closed environment, so the test cannot pass by inheriting a variable the
	// case did not set.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("evaluating the Cube models failed under env %v: %v\n%s", env, err, stderr)
	}

	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("harness output was not JSON: %v\n%s", err, out)
	}
	if len(got) == 0 {
		t.Fatalf("no cubes were defined; the harness is not exercising the models")
	}
	return got
}

// TestModelSQLRespondsToTheEngine is the guard proper. Cube is pointed at DuckDB in
// the bootstrap stack and Trino in the full stack, and the two read completely
// different sources — read_parquet over the warehouse files versus a catalog table.
// A model that produces the same SQL for both is not configured; it is broken for
// one of them, which is precisely what F-037 was.
func TestModelSQLRespondsToTheEngine(t *testing.T) {
	duck := compileModelSQL(t, map[string]string{
		"CUBEJS_DB_TYPE":                "duckdb",
		"TENANT_DB_PATH":                "/cube/data/gravix.db",
		"CUBEJS_CACHE_AND_QUEUE_DRIVER": "memory",
	})
	trino := compileModelSQL(t, map[string]string{
		"CUBEJS_DB_TYPE": "trino",
		"TENANT_DB_PATH": "/cube/data/gravix.db",
	})

	for name, duckSQL := range duck {
		trinoSQL, ok := trino[name]
		if !ok {
			t.Fatalf("cube %q is defined under DuckDB but not under Trino", name)
		}
		if duckSQL == trinoSQL {
			t.Errorf("cube %q produces identical SQL for DuckDB and Trino:\n  %s\n"+
				"The model is not reading its environment. Cube evaluates model files in a vm\n"+
				"sandbox with no `process`, so process.env here reads nothing — take the value\n"+
				"from ../model_flags.js, which Cube loads through Node's require. See F-037.",
				name, duckSQL)
			continue
		}
		if !strings.Contains(duckSQL, "read_parquet") {
			t.Errorf("cube %q under DuckDB does not read Parquet: %s", name, duckSQL)
		}
		if strings.Contains(trinoSQL, "read_parquet") {
			t.Errorf("cube %q under Trino reads Parquet: %s", name, trinoSQL)
		}
	}
}

// TestModelSQLRespondsToTenancy is the same argument for the other axis. The
// warehouse is laid out as warehouse/<tenant>/<table>/ when a tenant database is
// seeded and warehouse/<table>/ when it is not, and a glob written for the wrong
// one matches nothing at all — the silent-empty-dashboard shape of F-015 and F-027.
func TestModelSQLRespondsToTenancy(t *testing.T) {
	multi := compileModelSQL(t, map[string]string{
		"CUBEJS_DB_TYPE": "duckdb",
		"TENANT_DB_PATH": "/cube/data/gravix.db",
	})
	single := compileModelSQL(t, map[string]string{
		"CUBEJS_DB_TYPE": "duckdb",
	})

	for name, multiSQL := range multi {
		singleSQL, ok := single[name]
		if !ok {
			t.Fatalf("cube %q is defined for a tenant stack but not a single-tenant one", name)
		}
		if multiSQL == singleSQL {
			t.Errorf("cube %q produces identical SQL with and without TENANT_DB_PATH:\n  %s\n"+
				"The tenant prefix is not reaching the warehouse glob. See F-037 and F-027.",
				name, multiSQL)
		}
	}
}

// TestModelsDeclareNoPreAggregations records an infrastructure fact as a test,
// because the alternative is a rollup that fails every query it was meant to serve.
//
// A Cube pre-aggregation is materialised in Cube Store, reached through an
// externalDriverFactory. No stack here runs one — no compose file and nothing under
// deploy/ defines a cubestore service — and Cube does not fall back to the source
// when a matching rollup cannot be built; it fails the query. So a declared rollup
// is strictly worse than none.
//
// This also keeps GRVX-1006 honest: with no pre-aggregations, every latency figure
// this stack produces is a cold read from Parquet. Quoting one as pre-aggregated
// would repeat F-020 and F-022.
func TestModelsDeclareNoPreAggregations(t *testing.T) {
	root := repoRootDir(t)

	// First the infrastructure half of the claim, so the test fails if someone gives
	// Cube an external store without revisiting the models.
	//
	// An earlier version of this guard looked for CUBEJS_CUBESTORE_HOST and nothing
	// else — the one name its author happened to know. That is the same mistake this
	// file exists to prevent, and Cube's own OptsHandler.initializeCoreOptions names
	// eleven triggers, not one:
	//
	//   externalDbType = opts.externalDbType
	//     || process.env.CUBEJS_EXT_DB_TYPE
	//     || ((getEnv('devMode') || definedExtDBVariables.length > 0) && 'cubestore')
	//
	// where definedExtDBVariables is any of CUBEJS_EXT_DB_{URL,HOST,NAME,PORT,USER,PASS}
	// or CUBEJS_CUBESTORE_{HOST,PORT,USER,PASS}. Dev mode is the one that matters most
	// and was missed entirely: with CUBEJS_DEV_MODE truthy the official cubejs/cube
	// image starts an EMBEDDED Cube Store on port 3030, so an external store appears
	// with no service, no host variable, and nothing in the compose file to grep for.
	//
	// Both compose files set CUBEJS_DEV_MODE=${CUBEJS_DEV_MODE:-false}, so the default
	// has no store. A reader who exports CUBEJS_DEV_MODE=true gets one.
	extStoreEnv := []string{
		"CUBEJS_EXT_DB_TYPE",
		"CUBEJS_EXT_DB_URL", "CUBEJS_EXT_DB_HOST", "CUBEJS_EXT_DB_NAME",
		"CUBEJS_EXT_DB_PORT", "CUBEJS_EXT_DB_USER", "CUBEJS_EXT_DB_PASS",
		"CUBEJS_CUBESTORE_HOST", "CUBEJS_CUBESTORE_PORT",
		"CUBEJS_CUBESTORE_USER", "CUBEJS_CUBESTORE_PASS",
	}
	for _, rel := range []string{"docker-compose.yml", "docker-compose.bootstrap.yml"} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(b)
		for _, v := range extStoreEnv {
			if strings.Contains(text, v+"=") {
				t.Errorf("%s sets %s, which gives Cube an external pre-aggregation store. "+
					"Pre-aggregations may be worth restoring — as a plain object literal, never "+
					"behind a ternary (F-038) — but this test and the comments in "+
					"cube/model/schema/ must be updated deliberately, not left to drift.", rel, v)
			}
		}
		// Dev mode defaults the external store to an embedded Cube Store. A literal
		// true here would enable it for everyone; the ${VAR:-false} form leaves it to
		// the reader, which is what both files do today.
		if strings.Contains(text, "CUBEJS_DEV_MODE=true") {
			t.Errorf("%s hardcodes CUBEJS_DEV_MODE=true. The official cubejs/cube image then "+
				"starts an embedded Cube Store, so pre-aggregations become buildable and the "+
				"models' claim that none can be built stops holding. See F-038.", rel)
		}
	}

	// Then the models. Read as text rather than evaluated, because the harness above
	// captures `sql` only; this is a cheap, direct statement of the same fact.
	dir := modelRoot(t)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".js") {
			return err
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(trimmed, "preAggregations") && !strings.Contains(trimmed, "preAggregations: {}") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s declares a pre-aggregation:\n  %s\n"+
					"No stack in this repository runs a Cube Store, so Cube cannot build it and "+
					"will fail every query that matches it rather than reading the source. See F-035 and F-038.",
					rel, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
}

// TestFlagsModuleIsOutsideTheModelDirectory guards the one structural property the
// fix depends on. Cube's require resolves a path against the model root and falls
// through to Node only when resolveModuleFile does not find it among the model
// files. Move model_flags.js under cube/model/ and Cube would compile it in the
// sandbox instead — no `process`, and F-037 returns silently and in full.
func TestFlagsModuleIsOutsideTheModelDirectory(t *testing.T) {
	root := repoRootDir(t)

	flags := filepath.Join(root, "cube", "model_flags.js")
	if _, err := os.Stat(flags); err != nil {
		t.Fatalf("cube/model_flags.js is missing: %v", err)
	}
	if strings.HasPrefix(flags, modelRoot(t)+string(filepath.Separator)) {
		t.Fatalf("cube/model_flags.js is inside the model directory; Cube would evaluate " +
			"it in its sandbox, where there is no `process`. See F-037.")
	}

	// And it has to be mounted, or the require throws at compile time.
	for _, rel := range []string{"docker-compose.yml", "docker-compose.bootstrap.yml"} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(b), "./cube/model_flags.js:/cube/conf/model_flags.js") {
			t.Errorf("%s does not mount cube/model_flags.js at /cube/conf/model_flags.js. "+
				"Without it the models' require throws and no cube registers. See F-037.", rel)
		}
	}
}
