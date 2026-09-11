// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// GRVX-1004 §7, the JS half. Run with `make test-js`.
//
// The parity test is the point of this file. pkg/costmodel regenerates
// tco.fixtures.expected.json from the same fixture inputs on every Go test run,
// so if either implementation changes without the other, one of the two suites
// goes red. A calculator that disagrees with the model behind it is worse than
// having neither.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import {
    DEPLOYMENT_BOOTSTRAP_VPS, DEPLOYMENT_AWS_SINGLE, DEPLOYMENT_AWS_MULTI,
    BASIS_MEASURED, BASIS_LIST_PRICE, BASIS_ESTIMATE, MAX_PRICE_AGE_DAYS,
    CAVEAT_BOOTSTRAP, CAVEAT_AWS_SINGLE, CAVEAT_AWS_MULTI, CAVEAT_PROVENANCE,
    CAVEAT_ESTIMATED_PRICES,
    estimateAll, validatePrices, storageGB,
} from './tco.js';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, '..', '..');

function readJSON(...parts) {
    return JSON.parse(readFileSync(join(repoRoot, ...parts), 'utf8'));
}

// prices.yaml is YAML, but a deliberately flat subset of it: the page fetches a
// JSON rendering at runtime. Parsed here with a small reader rather than a
// dependency, because dashboards/ has no build step.
function loadPrices() {
    const text = readFileSync(join(repoRoot, 'pkg', 'costmodel', 'prices.yaml'), 'utf8');
    const out = { version: 0, retrieved: '', currency: '' };
    let section = null;
    let key = null;
    for (const rawLine of text.split('\n')) {
        if (!rawLine.trim() || rawLine.trimStart().startsWith('#')) continue;
        const indent = rawLine.length - rawLine.trimStart().length;
        const line = rawLine.trim();
        const [name, ...rest] = line.split(':');
        const value = rest.join(':').trim();

        if (indent === 0) {
            if (value !== '') {
                if (name === 'version') out.version = Number(value);
                else out[name] = value.replace(/^"|"$/g, '');
                section = null;
            } else {
                section = name;
                out[section] = {};
            }
            continue;
        }
        if (indent === 2 && section) {
            key = name;
            out[section][key] = {};
            continue;
        }
        if (indent === 4 && section && key) {
            const v = value.replace(/^"|"$/g, '');
            out[section][key][name] = name === 'value' ? Number(v) : v;
        }
    }
    return out;
}

const prices = loadPrices();
const fixtures = readJSON('pkg', 'costmodel', 'fixtures.json');
const expected = readJSON('dashboards', 'lib', 'tco.fixtures.expected.json');

function inputsFor(c) {
    return {
        eventsPerMonth: c.events_per_month,
        retentionDays: c.retention_days,
        services: c.services,
        dashboardUsers: c.dashboard_users,
        bytesPerEvent: c.bytes_per_event,
        measurementSource: fixtures.measurement_source,
    };
}

// The prices file is dated, and validatePrices refuses anything over 90 days.
// Pinning "now" to the file's own date keeps these tests about the model rather
// than about how long ago it was written.
const asOf = new Date(prices.retrieved + 'T00:00:00Z');

test('the price file parsed into the shape the model expects', () => {
    assert.equal(prices.version, 1, 'version');
    assert.match(prices.retrieved, /^\d{4}-\d{2}-\d{2}$/, 'retrieved date');
    for (const section of ['bootstrap_vps', 'aws_single', 'aws_multi']) {
        assert.ok(prices[section], `missing section ${section}`);
    }
    assert.equal(typeof prices.aws_single.node_usd_month.value, 'number');
    assert.ok(prices.aws_single.node_usd_month.value > 0);
    assert.equal(prices.bootstrap_vps.instance_usd_month.basis, BASIS_ESTIMATE);
});

// ─── AC-7 ───

test('AC-7: the JS model agrees with the Go model on every fixture', () => {
    assert.ok(fixtures.cases.length > 0, 'no fixture cases');

    for (const c of fixtures.cases) {
        const want = expected[c.name];
        assert.ok(want, `no expected totals for "${c.name}"; run \`go test ./pkg/costmodel/\``);

        const estimates = estimateAll(inputsFor(c), prices, asOf);
        assert.equal(estimates.length, 3, `${c.name}: wrong number of estimates`);

        for (const e of estimates) {
            const goTotal = want[e.deployment];
            assert.notEqual(goTotal, undefined, `${c.name}: Go produced no ${e.deployment}`);
            const delta = Math.abs(e.totalUSDMonth - goTotal);
            assert.ok(delta <= 0.01,
                `parity: deployment ${e.deployment} Go $${goTotal} vs JS $${e.totalUSDMonth} ` +
                `(case "${c.name}", delta $${delta.toFixed(4)})`);
        }
    }
});

// ─── AC-1, mirrored ───

test('AC-1: estimateAll returns all three deployments', () => {
    const estimates = estimateAll(inputsFor(fixtures.cases[1]), prices, asOf);
    const seen = estimates.map(e => e.deployment);
    for (const d of [DEPLOYMENT_BOOTSTRAP_VPS, DEPLOYMENT_AWS_SINGLE, DEPLOYMENT_AWS_MULTI]) {
        assert.ok(seen.includes(d), `no estimate for ${d}; the bootstrap figure must never render alone`);
    }
});

// ─── AC-2, mirrored ───

test('AC-2: there is no exported way to price one deployment', async () => {
    const module = await import('./tco.js');
    for (const [name, value] of Object.entries(module)) {
        if (typeof value !== 'function') continue;
        assert.ok(!/^estimate(?!All)/.test(name),
            `${name} looks like a single-deployment estimator; only estimateAll may be exported`);
    }
});

// ─── AC-3, mirrored ───

test('AC-3: every mandatory caveat is present verbatim', () => {
    const estimates = estimateAll(inputsFor(fixtures.cases[1]), prices, asOf);
    const required = {
        [DEPLOYMENT_BOOTSTRAP_VPS]: CAVEAT_BOOTSTRAP,
        [DEPLOYMENT_AWS_SINGLE]: CAVEAT_AWS_SINGLE,
        [DEPLOYMENT_AWS_MULTI]: CAVEAT_AWS_MULTI,
    };
    for (const e of estimates) {
        assert.ok(e.caveats.includes(required[e.deployment]),
            `estimate for ${e.deployment} is missing its mandatory caveat`);
        assert.ok(e.caveats.includes(CAVEAT_PROVENANCE),
            `estimate for ${e.deployment} is missing the provenance caveat`);
        assert.ok(e.caveats.includes(CAVEAT_ESTIMATED_PRICES),
            `estimate for ${e.deployment} carries estimated prices without saying so`);
    }
});

// The caveat text must be identical between the two implementations, or one
// renders a sentence the other does not.
test('the caveat strings match pkg/costmodel byte for byte', () => {
    const goSource = readFileSync(join(repoRoot, 'pkg', 'costmodel', 'costmodel.go'), 'utf8');
    const checks = [
        ['CaveatBootstrap', CAVEAT_BOOTSTRAP],
        ['CaveatAWSSingle', CAVEAT_AWS_SINGLE],
        ['CaveatAWSMulti', CAVEAT_AWS_MULTI],
    ];
    for (const [name, text] of checks) {
        // The Go constants are written across concatenated lines; compare on
        // collapsed whitespace so formatting is not the thing under test.
        const collapsed = goSource.replace(/"\s*\+\s*\n\s*"/g, '').replace(/\s+/g, ' ');
        assert.ok(collapsed.includes(text.replace(/\s+/g, ' ')),
            `${name} in tco.js does not appear in costmodel.go; the two have drifted`);
    }
});

// ─── AC-4, mirrored ───

test('AC-4: every line item carries a basis, and measured items cite a bench result', () => {
    const valid = new Set([BASIS_MEASURED, BASIS_LIST_PRICE, BASIS_ESTIMATE]);
    for (const c of fixtures.cases) {
        for (const e of estimateAll(inputsFor(c), prices, asOf)) {
            for (const li of e.lineItems) {
                assert.ok(valid.has(li.basis), `${e.deployment} "${li.name}" has basis ${li.basis}`);
                assert.ok(li.source.trim().length > 0, `${e.deployment} "${li.name}" has no source`);
                if (li.basis === BASIS_MEASURED) {
                    assert.ok(li.source.includes('bench/results/'),
                        `${e.deployment} "${li.name}" is marked measured but cites ${li.source}`);
                }
            }
        }
    }
});

// ─── AC-5, mirrored ───

test('AC-5: prices older than 90 days are refused', () => {
    const retrieved = new Date(prices.retrieved + 'T00:00:00Z');

    const tooOld = new Date(retrieved.getTime() + (MAX_PRICE_AGE_DAYS + 1) * 86400000);
    assert.throws(() => validatePrices(prices, tooOld), /re-verify before publishing/);

    const atLimit = new Date(retrieved.getTime() + MAX_PRICE_AGE_DAYS * 86400000);
    assert.doesNotThrow(() => validatePrices(prices, atLimit));

    assert.throws(() => validatePrices({ version: 1, retrieved: 'recently' }, retrieved),
        /unparseable retrieved date/);
    assert.throws(() => validatePrices(null, retrieved), /no price data/);
});

// ─── AC-6, mirrored ───

test('AC-6: BytesPerEvent must come from a bench result', () => {
    const base = inputsFor(fixtures.cases[1]);

    assert.throws(() => estimateAll({ ...base, measurementSource: '' }, prices, asOf),
        /not a constant/, 'accepted an input with no measurement source');
    assert.throws(() => estimateAll({ ...base, bytesPerEvent: 0 }, prices, asOf),
        /not a constant/, 'accepted zero bytes per event');
    assert.throws(() => estimateAll({ ...base, eventsPerMonth: 0 }, prices, asOf),
        /must be positive/, 'accepted zero events');
    assert.throws(() => estimateAll({ ...base, retentionDays: 0 }, prices, asOf),
        /must be positive/, 'accepted zero retention');
});

// ─── SD-021, mirrored ───

test('the computed multiple accompanies the "roughly 10x" caveat', () => {
    const estimates = estimateAll(inputsFor(fixtures.cases[1]), prices, asOf);
    const bootstrap = estimates.find(e => e.deployment === DEPLOYMENT_BOOTSTRAP_VPS);

    for (const e of estimates) {
        if (e.deployment === DEPLOYMENT_BOOTSTRAP_VPS) continue;
        const note = e.caveats.find(c => c.includes('the multiple is'));
        assert.ok(note, `${e.deployment} does not state its computed multiple`);
        assert.ok(note.includes('Budget from this figure, not from the multiple.'),
            `${e.deployment} does not tell the reader which number to use`);

        const stated = note.match(/the multiple is ([\d.]+)x/)[1];
        const actual = (e.totalUSDMonth / bootstrap.totalUSDMonth).toFixed(1);
        assert.equal(stated, actual,
            `${e.deployment} caveat says ${stated}x but the estimates give ${actual}x`);
    }
});

test('storage scales with retention and with bytes per event', () => {
    const base = inputsFor(fixtures.cases[1]);
    assert.ok(storageGB({ ...base, retentionDays: 90 }) > storageGB({ ...base, retentionDays: 7 }));
    assert.ok(storageGB({ ...base, bytesPerEvent: 400 }) > storageGB(base));
});
