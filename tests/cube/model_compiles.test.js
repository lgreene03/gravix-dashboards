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
import { dirname, join, relative } from 'node:path';
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

// ─── F-036: rollups are declared only where they can be built ──────────────

// A pre-aggregation needs an external store — Cube Store — to hold its rollup
// table. The bootstrap stack deliberately runs none, and Cube does NOT degrade
// gracefully when a query matches a rollup it cannot build: it fails the query
// with "externalDriverFactory is not provided" rather than reading the source.
//
// So the model declares its rollups only when a store exists. This checks both
// states, because a condition that is always true and a condition that is always
// false both look like "it works" from one side.

function loadCube(file, env) {
    let captured = null;
    const names = new Proxy({}, {
        has: () => true,
        get: (_t, p) => {
            if (p === 'cube') return (name, def) => { captured = def; };
            if (p === 'process') return { env };
            if (p === 'Symbol') return Symbol;
            return String(p);
        },
    });
    vm.runInNewContext('with(names){' + readFileSync(file, 'utf8') + '}', { names });
    return captured;
}

test('F-036: rollups are declared with an external store and withheld without one', () => {
    const dirs = modelDirsFromCompose();
    const files = [];
    for (const dir of dirs.keys()) {
        files.push(...jsFilesUnder(join(repoRoot, dir)));
    }
    assert.ok(files.length > 0, 'no model files found');

    let declaring = 0;
    for (const file of files) {
        const rel = relative(repoRoot, file);

        // With a store available — the full stack's shape.
        const withStore = loadCube(file, { CUBEJS_DB_TYPE: 'duckdb' });
        // Memory driver and no Cube Store — the bootstrap stack's shape.
        const noStore = loadCube(file, {
            CUBEJS_DB_TYPE: 'duckdb',
            CUBEJS_CACHE_AND_QUEUE_DRIVER: 'memory',
        });
        // Memory driver but a Cube Store host is named: a store exists, so the
        // rollups must come back. This is what catches a condition keyed on the
        // driver alone.
        const memoryPlusStore = loadCube(file, {
            CUBEJS_DB_TYPE: 'duckdb',
            CUBEJS_CACHE_AND_QUEUE_DRIVER: 'memory',
            CUBEJS_CUBESTORE_HOST: 'cubestore',
        });

        assert.ok(withStore && noStore && memoryPlusStore, `${rel} did not register a cube`);

        const n = Object.keys(withStore.preAggregations || {}).length;
        if (n === 0) continue;   // a cube with no rollups has nothing to withhold
        declaring++;

        assert.equal(Object.keys(noStore.preAggregations || {}).length, 0,
            `${rel} still declares ${n} pre-aggregation(s) with the memory driver and no Cube ` +
            'Store. Cube will route a matching query to a rollup it cannot build and fail it ' +
            'with "externalDriverFactory is not provided" — it does not fall back to the ' +
            'source (F-036).');

        assert.equal(Object.keys(memoryPlusStore.preAggregations || {}).length, n,
            `${rel} withholds its pre-aggregations even though CUBEJS_CUBESTORE_HOST names a ` +
            'store. The condition must track whether a store EXISTS, not merely which cache ' +
            'driver is set.');
    }

    assert.ok(declaring > 0,
        'no cube declares a pre-aggregation in any state, so this guard proved nothing. ' +
        'Either the models changed or the loader broke.');
});
