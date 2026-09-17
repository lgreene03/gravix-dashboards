// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// F-030 regression guard. Run with `make test-js`.
//
// Cube mounts a host directory as its schema directory and compiles EVERY .js
// file in it with `new vm.Script(src)` — see the stack trace in the failure this
// guard came from, which bottoms out at node:vm:99:7. That is not an ES module
// context, so a file using `import`, `export` or `import.meta` is a hard
// SyntaxError, and Cube's DataSchemaCompiler fails the WHOLE compile on one bad
// file: no cube is defined and every query returns an error.
//
// That is what happened. `RequestMetricsMinute.test.js` sat beside the model it
// tests, inside the mounted directory, from Phase 8 onward. Cube therefore never
// served a single row, through every subsequent piece of work — including the
// fourteen model tests in that very file, which passed the entire time because
// Node runs them as modules and nothing ran the model through Cube's compiler.
//
// So this guard uses Cube's own parser rather than looking for ESM keywords. A
// keyword check protects the constructs its author thought of; `new vm.Script`
// rejects exactly what Cube rejects, including constructs nobody has used yet.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, relative, resolve } from 'node:path';
import vm from 'node:vm';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, '..', '..');

const composeFiles = ['docker-compose.yml', 'docker-compose.bootstrap.yml'];

// modelDirsFromCompose finds the host directories mounted at Cube's schema path.
// Derived from the compose files rather than hardcoded, so moving or renaming the
// mount cannot leave this guard watching a directory nothing uses.
function modelDirsFromCompose() {
    const dirs = new Map();
    for (const file of composeFiles) {
        const text = readFileSync(join(repoRoot, file), 'utf8');
        for (const m of text.matchAll(/^\s*-\s*\.\/(\S+?):\/cube\/conf\/model\b/gm)) {
            dirs.set(m[1], file);
        }
    }
    return dirs;
}

function jsFilesUnder(dir) {
    const out = [];
    for (const entry of readdirSync(dir)) {
        const full = join(dir, entry);
        if (statSync(full).isDirectory()) {
            out.push(...jsFilesUnder(full));
        } else if (entry.endsWith('.js')) {
            out.push(full);
        }
    }
    return out;
}

test('F-030: every file in Cube\'s model directory parses the way Cube parses it', () => {
    const dirs = modelDirsFromCompose();

    // Finding no mount is not a pass. It means the regex no longer matches how
    // the compose files declare the mount, and the guard is watching nothing.
    assert.ok(dirs.size > 0,
        'no host directory is mounted at /cube/conf/model in either compose file. ' +
        'Either the mount moved or this guard\'s pattern went stale; both need a human.');

    let checked = 0;
    for (const [dir, composeFile] of dirs) {
        const files = jsFilesUnder(join(repoRoot, dir));
        assert.ok(files.length > 0, `${dir} (mounted by ${composeFile}) holds no .js file`);

        for (const file of files) {
            const rel = relative(repoRoot, file);
            checked++;
            try {
                // Exactly what Cube's DataSchemaCompiler does before running it.
                new vm.Script(readFileSync(file, 'utf8'), { filename: rel });
            } catch (err) {
                assert.fail(
                    `${rel} is in ${dir}, which ${composeFile} mounts as Cube's schema ` +
                    `directory, and Cube cannot parse it:\n\n    ${err.message}\n\n` +
                    'Cube compiles every .js file there with `new vm.Script`, which is not an ' +
                    'ES module context — so `import`, `export` and `import.meta` are syntax ' +
                    'errors. One unparseable file fails the whole schema compile: no cube is ' +
                    'defined and every query errors, while the containers all report healthy. ' +
                    'If this is a test file, move it to tests/cube/ (F-030).');
            }
        }
    }
    assert.ok(checked > 0, 'no .js file was checked; the derivation broke');
});

// ─── F-037: the models must actually read their environment ────────────────

// The sandbox below is Cube's, transcribed rather than approximated, and the one
// thing it deliberately withholds is the subject of the test.
//
// Cube compiles model files with vm.runInNewContext. A fresh V8 context has no
// `process` — that is a Node global, not a V8 intrinsic — so a model file reading
// process.env reads nothing: guarded by `typeof process !== 'undefined'` it takes
// the false branch in silence. Every environment conditional in cube/model/schema/
// was dead for exactly that reason, and both stacks compiled to the Trino SQL,
// including the bootstrap stack, which runs DuckDB over Parquet and no Trino.
//
// The guard that missed this injected a `process` into its own sandbox and then
// checked that a flag named hasExternalStore flipped. It passed for the same reason
// the defect existed: it exercised a code path Cube never runs. A test sandbox more
// generous than the real one proves nothing about the real one.
//
// So this checks the requirement instead of the mechanism: point the models at two
// different engines and the SQL that comes out must differ. Any route to a model
// that ignores its environment fails it, whatever the flag is called.

function loadCube(file, env, modelRoot) {
    let captured = null;
    const names = new Proxy({}, {
        has: () => true,
        get: (_t, p) => {
            if (p === 'cube') return (name, def) => { captured = def; };
            if (p === 'Symbol') return Symbol;
            if (p === 'require') return (spec) => {
                // Cube falls through to Node's own require for a path that is not
                // another model file, resolving it against the model root — which
                // is what puts model_flags.js in an ordinary Node context.
                const target = resolve(modelRoot, spec);
                const module = { exports: {} };
                vm.runInNewContext(readFileSync(target, 'utf8'),
                    { module, exports: module.exports, process: { env } },
                    { filename: target });
                return module.exports;
            };
            // Deliberately absent: `process`. Cube's sandbox has none.
            return String(p);
        },
    });
    vm.runInNewContext('with(names){' + readFileSync(file, 'utf8') + '}', { names });
    return captured;
}

test('F-037: every model produces different SQL for DuckDB and for Trino', () => {
    const dirs = modelDirsFromCompose();
    assert.ok(dirs.size > 0, 'no model directory is mounted; this guard is watching nothing');

    let checked = 0;
    for (const dir of dirs.keys()) {
        const modelRoot = join(repoRoot, dir);
        for (const file of jsFilesUnder(modelRoot)) {
            const rel = relative(repoRoot, file);

            const duck = loadCube(file, { CUBEJS_DB_TYPE: 'duckdb', TENANT_DB_PATH: '/d' }, modelRoot);
            const trino = loadCube(file, { CUBEJS_DB_TYPE: 'trino', TENANT_DB_PATH: '/d' }, modelRoot);
            assert.ok(duck && trino, `${rel} did not register a cube`);
            checked++;

            assert.notEqual(duck.sql, trino.sql,
                `${rel} produces the same SQL for DuckDB and Trino:\n    ${duck.sql}\n` +
                'The model is not reading its environment. Cube evaluates model files in a vm ' +
                'sandbox with no `process`, so process.env there reads nothing — take the value ' +
                'from ../model_flags.js, which Cube loads through Node\'s require (F-037).');

            assert.match(duck.sql, /read_parquet/,
                `${rel} does not read Parquet under DuckDB:\n    ${duck.sql}`);
            assert.doesNotMatch(trino.sql, /read_parquet/,
                `${rel} reads Parquet under Trino:\n    ${trino.sql}`);
        }
    }
    assert.ok(checked > 0, 'no model was checked; the derivation broke');
});

test('F-037: the tenant prefix reaches the warehouse glob', () => {
    const dirs = modelDirsFromCompose();
    let checked = 0;
    for (const dir of dirs.keys()) {
        const modelRoot = join(repoRoot, dir);
        for (const file of jsFilesUnder(modelRoot)) {
            const rel = relative(repoRoot, file);
            const multi = loadCube(file, { CUBEJS_DB_TYPE: 'duckdb', TENANT_DB_PATH: '/d' }, modelRoot);
            const single = loadCube(file, { CUBEJS_DB_TYPE: 'duckdb' }, modelRoot);
            assert.ok(multi && single, `${rel} did not register a cube`);
            checked++;
            assert.notEqual(multi.sql, single.sql,
                `${rel} globs the same path with and without TENANT_DB_PATH:\n    ${multi.sql}\n` +
                'The rollups write warehouse/<tenant>/<table>/, so a single-tenant glob matches ' +
                'nothing and the dashboard is silently empty (F-027, F-037).');
        }
    }
    assert.ok(checked > 0, 'no model was checked; the derivation broke');
});
