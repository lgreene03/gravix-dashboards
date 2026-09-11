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

// ─── AC-8 through AC-11: the page ───
//
// Static analysis of tco.html, not a browser. dashboards/ has no build step and
// `make test-js` is node --test with no dependencies; adding a headless browser
// to CI for four assertions would cost more than it catches. What follows checks
// the properties a browser would have verified, by reading the markup and CSS —
// and says plainly, in AC-10, which half of that criterion it cannot cover.

const pageHTML = readFileSync(join(repoRoot, 'dashboards', 'tco.html'), 'utf8');

test('AC-8: the page renders all three deployments together', () => {
    // The render path maps over whatever estimateAll returns, and estimateAll
    // returns three — so the page cannot render one in isolation without a
    // deliberate code change. Both halves are checked.
    assert.match(pageHTML, /estimates\.map\(/,
        'the page does not map over the full estimate list');
    assert.ok(!/estimates\[0\]|estimates\.find\(|\.filter\(/.test(pageHTML),
        'the page indexes or filters the estimate list; it must render all three');

    for (const d of [DEPLOYMENT_BOOTSTRAP_VPS, DEPLOYMENT_AWS_SINGLE, DEPLOYMENT_AWS_MULTI]) {
        assert.ok(pageHTML.includes(d), `the page has no title mapping for ${d}`);
    }

    // Every caveat is rendered, not collapsed behind a disclosure. A caveat you
    // have to click is a caveat the reader does not see.
    assert.match(pageHTML, /caveats\.map\(/, 'caveats are not all rendered');
    assert.ok(!/<details|<summary/i.test(pageHTML),
        'the page hides content behind a disclosure element');

    // And the basis badge travels with every line item.
    assert.match(pageHTML, /badge-\$\{escapeHTML\(li\.basis\)\}/,
        'line items do not carry a basis badge');
});

test('AC-8: no rule can hide a deployment card once rendered', () => {
    // Mutation testing found the hole this closes. Every AC-8 assertion above is
    // about the JS render path, and all of them passed against a page carrying
    //
    //     .deployment:nth-child(n+2) { display: none; }
    //
    // which shows the bootstrap figure alone — the precise thing §3's first rule
    // forbids — without touching a line of JavaScript. A guard on the render path
    // protects the render path, not the property.
    const css = pageHTML.slice(pageHTML.indexOf('<style>'), pageHTML.indexOf('</style>'));

    // Any rule whose selector reaches a deployment card or their container.
    const rules = [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)]
        .filter(m => /deployment/.test(m[1]));
    assert.ok(rules.length > 0, 'no .deployment rules found; the selector was renamed');

    const hiding = [
        /display:\s*none/i,
        /visibility:\s*hidden/i,
        /content-visibility:\s*hidden/i,
        /opacity:\s*0(?![.\d])/i,
        /(?:max-)?height:\s*0(?![.\d])/i,
    ];
    for (const [, selector, body] of rules) {
        for (const pattern of hiding) {
            assert.ok(!pattern.test(body),
                `the rule "${selector.trim()}" hides a deployment card (${pattern}). ` +
                'All three deployments render together or not at all — GRVX-1004 §3.');
        }
    }

    // The same thing done in markup rather than CSS.
    assert.ok(!/<section class="deployment"[^>]*\bhidden\b/.test(pageHTML),
        'a deployment card carries the hidden attribute');
    assert.ok(!/<section class="deployment"[^>]*style="[^"]*display:\s*none/i.test(pageHTML),
        'a deployment card is hidden by an inline style');
});

test('AC-9: no upsell, sales CTA, or lead capture', () => {
    const forbidden = [
        /contact\s+sales/i, /talk\s+to\s+sales/i, /book\s+a\s+demo/i, /request\s+a\s+demo/i,
        /start\s+(your\s+)?free\s+trial/i, /upgrade\s+to\s+pro/i, /get\s+a\s+quote/i,
        /enterprise\s+plan/i, /pricing\s+plans/i,
    ];
    for (const pattern of forbidden) {
        assert.ok(!pattern.test(pageHTML), `the page contains an upsell matching ${pattern}`);
    }

    // No lead capture of any kind: no email or tel input, no form that posts
    // anywhere, no mailto.
    const inputTypes = [...pageHTML.matchAll(/<input[^>]*\btype="([^"]+)"/g)].map(m => m[1]);
    for (const t of inputTypes) {
        assert.ok(t === 'number', `the page has an <input type="${t}">; only numeric inputs belong here`);
    }
    assert.ok(!/<form[^>]*\baction=/i.test(pageHTML), 'a form posts somewhere');
    assert.ok(!/mailto:/i.test(pageHTML), 'the page contains a mailto: link');
    assert.ok(!/<input[^>]*\bname="email"/i.test(pageHTML), 'the page has an email field');
});

test('AC-10 (responsive): nothing forces a horizontal scroll at 400px', () => {
    const css = pageHTML.slice(pageHTML.indexOf('<style>'), pageHTML.indexOf('</style>'));

    // A min-width wider than the narrowest target viewport is the usual cause.
    for (const m of css.matchAll(/min-width:\s*(\d+)px/g)) {
        const px = Number(m[1]);
        const inMediaQuery = css.slice(0, m.index).lastIndexOf('@media') >
            css.slice(0, m.index).lastIndexOf('}');
        assert.ok(inMediaQuery || px <= 400,
            `a min-width of ${px}px outside a media query would overflow a 400px viewport`);
    }
    // Fixed widths are the other cause.
    for (const m of css.matchAll(/[^-]width:\s*(\d+)px/g)) {
        assert.ok(Number(m[1]) <= 400, `a fixed width of ${m[1]}px would overflow`);
    }

    // The side gutter is set once, and vertical padding must not zero it.
    assert.match(css, /padding-inline:/, 'body has no explicit side padding');
    assert.match(css, /padding-block:/,
        'body uses a padding shorthand for vertical space, which would reset the side gutter');

    // The one element allowed to be wider than the page is the line-item table,
    // and only inside its own scroll container.
    assert.match(css, /\.table-scroll\s*\{[^}]*overflow-x:\s*auto/,
        'the line-item table has no horizontal scroll container');
    assert.match(pageHTML, /<div class="table-scroll">/, 'the table is not wrapped in one');

    // The three-column layout must be a media query, not the default.
    const defaultGrid = css.match(/\.deployments\s*\{[^}]*grid-template-columns:\s*([^;]+);/);
    assert.ok(defaultGrid, 'no .deployments grid');
    assert.equal(defaultGrid[1].trim(), '1fr',
        'the default layout is multi-column; it must stack on a narrow screen');
});

test('AC-10 (themes): every token is defined in all three theme states', () => {
    const css = pageHTML.slice(pageHTML.indexOf('<style>'), pageHTML.indexOf('</style>'));

    const block = (re) => {
        const m = css.match(re);
        assert.ok(m, `no theme block matching ${re}`);
        return new Set([...m[1].matchAll(/(--[a-z-]+):/g)].map(t => t[1]));
    };

    // Light is the base, on bare :root — a token defined only inside a media
    // query or a [data-theme] block has no value in the other states.
    const light = block(/:root\s*\{([^}]*)\}/);
    const systemDark = block(/@media\s*\(prefers-color-scheme:\s*dark\)\s*\{\s*:root:not\(\[data-theme="light"\]\)\s*\{([^}]*)\}/);
    const explicitDark = block(/:root\[data-theme="dark"\]\s*\{([^}]*)\}/);

    assert.ok(light.size > 0, 'no tokens on bare :root');
    for (const token of light) {
        assert.ok(systemDark.has(token),
            `${token} has no dark value under prefers-color-scheme; it would keep its light value`);
        assert.ok(explicitDark.has(token),
            `${token} has no value under [data-theme="dark"]; the explicit toggle would not reach it`);
    }
    for (const token of systemDark) {
        assert.ok(light.has(token), `${token} is defined only in dark; it has no light value`);
    }

    // body must paint its own background, or it borrows the host's.
    assert.match(css, /body\s*\{[^}]*background:\s*var\(--bg\)/,
        'body does not set an explicit background');

    // NOTE: this asserts the tokens exist in all three states. It does not
    // render the page, so it cannot catch a contrast failure or a token whose
    // dark value is simply wrong. A browser-based check would; adding one to
    // `make test-js` is deliberately out of scope for this spec.
});

test('AC-11: capacity planning is reconciled against the measurement', () => {
    const doc = readFileSync(join(repoRoot, 'docs', 'capacity-planning.md'), 'utf8');

    // It must link the calculator.
    assert.ok(doc.includes('dashboards/tco.html'),
        'capacity-planning.md does not link the cost calculator');

    // Every figure the measurement supersedes must be marked, not silently left
    // standing beside the new one.
    assert.ok(/203\.78/.test(doc), 'the measured raw bytes/event does not appear');
    assert.ok(/2\.89/.test(doc), 'the measured Parquet bytes/event does not appear');
    assert.ok(/206\.68/.test(doc), 'the measured total does not appear');

    // The old estimates are retained deliberately, so a reader who sized from
    // them can see what changed — but they must be labelled as estimates.
    assert.ok(/planning estimate/i.test(doc),
        'the superseded figures are not labelled as estimates');

    // And the mechanism correction must be present: the old table called
    // aggregation a compression ratio, which is the part that misleads.
    assert.ok(/not\s+compression/i.test(doc) || /aggregates/i.test(doc),
        'capacity-planning.md still presents the Parquet reduction as compression');
    assert.ok(/cannot be recovered from the warehouse/i.test(doc),
        'it does not say per-event data cannot be recovered from Parquet');
});
