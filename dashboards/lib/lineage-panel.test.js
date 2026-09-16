// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// GRVX-809 §8.3. Run with:
//   node --test dashboards/lib/lineage-panel.test.js
//
// No jsdom, no test framework, no dependency of any kind — the dashboard has no
// build step and its tests must not smuggle one in. The rendering functions are
// pure and return strings, so most of this file needs no DOM at all; the small
// stub below exists only for the parts that are genuinely about focus and
// keyboard behaviour, which cannot be tested any other way.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import {
    esc, formatBucket, renderLineage, renderFactKeys, renderExactness, renderIntegrity,
    renderRecompute, renderFailure, renderLoading, failureKindForStatus, lineageUrl,
    retryAfterSeconds, FAILURE_COPY, LineagePanel
} from './lineage-panel.js';

const here = dirname(fileURLToPath(import.meta.url));
const dashboardsDir = join(here, '..');

// ─── fixtures ───

function lineageFixture(overrides) {
    return Object.assign({
        metric: 'request_metrics_minute',
        metric_version: 'v2',
        contract_ref: 'latency_p95@v2',
        bucket: '2026-09-09T14:23:00Z',
        filters: { service: 'api', method: 'GET' },
        values: { request_count: 4, error_count: 1, p95_latency_ms: 84 },
        formula: 'percentile(latency_ms, 0.95) within bucket',
        grain: 'minute',
        exactness: 'sketch',
        error_bound: 'relative error <= 1% for q in [0.5, 0.99]',
        mergeability: 'sketch_merge',
        merge_note: 'Merge the latency_sketch column, then query the quantile.',
        data_file: 'warehouse/request_metrics_minute/event_day=2026-09-09/f.parquet',
        idempotency_key: 'request_metrics_minute:v2::20260909',
        content_digest: 'sha256:abc123',
        source_fact_keys: ['raw/request_facts/2026-09-09/14/facts_1430.jsonl'],
        fact_count: 5,
        current_revision: 0,
        revision_history: [],
        recompute_cmd: 'gravix recompute --metric request_metrics_minute --from 2026-09-09 --to 2026-09-10'
    }, overrides || {});
}

// ─── a minimal DOM, only as large as the controller needs ───

class StubClassList {
    constructor() { this.items = new Set(); }
    add(c) { this.items.add(c); }
    contains(c) { return this.items.has(c); }
}

class StubElement {
    constructor(tag) {
        this.tagName = (tag || 'div').toUpperCase();
        this.attributes = {};
        this.classList = new StubClassList();
        this.listeners = {};
        this.hidden = false;
        this._html = '';
        this.focusCount = 0;
        this.parent = null;
    }
    setAttribute(k, v) { this.attributes[k] = String(v); }
    getAttribute(k) { return this.attributes[k]; }
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
    dispatch(type, event) { (this.listeners[type] || []).forEach(fn => fn(event)); }
    focus() { this.focusCount += 1; }
    set innerHTML(v) { this._html = v; }
    get innerHTML() { return this._html; }
    get textContent() { return this._html.replace(/<[^>]*>/g, ''); }
    // The controller only queries for the two data attributes it owns.
    querySelector(selector) {
        const attr = selector.replace(/[[\]]/g, '');
        if (!this._html.includes(attr)) return null;
        const match = this._html.match(new RegExp('<code[^>]*' + attr + '[^>]*>([^<]*)</code>'));
        return { textContent: match ? match[1] : '' };
    }
}

function stubDocument() {
    const doc = new StubElement('document');
    return doc;
}

function clickTarget(matcher) {
    return { closest: sel => (sel === matcher ? {} : null) };
}

function newPanel(extra) {
    const container = new StubElement('div');
    const doc = stubDocument();
    const panel = new LineagePanel(container, Object.assign({ document: doc }, extra || {}));
    return { panel, container, doc };
}

// ─── AC-1: every §5.2 section is present, in order ───

test('TestLineagePanelSectionsRendered', () => {
    const html = renderLineage(lineageFixture(), { locale: 'en-GB', timeZone: 'UTC' });

    const sections = [
        'request_metrics_minute@v2',   // 1. heading
        'Values',                       // 2.
        'How this is computed',         // 3.
        'How exact this is',            // 4.
        'How it may be combined',       // 5.
        'Where it came from',           // 6.
        'Integrity',                    // 7.
        'Reproduce this number'         // 8.
    ];
    let cursor = -1;
    for (const s of sections) {
        const at = html.indexOf(s);
        assert.ok(at >= 0, `section missing: ${s}`);
        assert.ok(at > cursor, `section out of order: ${s}`);
        cursor = at;
    }

    // The content of each, not just its heading.
    assert.match(html, /request_count/);
    assert.match(html, /percentile\(latency_ms, 0\.95\)/);
    assert.match(html, /sketch_merge/);
    assert.match(html, /facts_1430\.jsonl/);
    assert.match(html, /sha256:abc123/);
    assert.match(html, /gravix recompute/);

    // The bucket is shown for a human, with its zone, so "14:23" means 14:23
    // somewhere. An ISO string is the failure mode, not the goal — the earlier
    // version of this assertion matched "-09" inside the ISO date and passed on
    // exactly that fallback.
    const formatted = formatBucket('2026-09-09T14:23:00Z', 'en-GB', 'UTC');
    assert.ok(html.includes(formatted), 'the heading does not carry the formatted bucket');
    assert.match(formatted, /GMT|UTC|[+-]\d{1,2}(:\d{2})?$/, `no zone shown: ${formatted}`);
    assert.ok(!/^\d{4}-\d{2}-\d{2}T/.test(formatted),
        `the bucket fell back to an ISO string: ${formatted}`);
    assert.match(formatted, /Sep|Sept/, `the date is not human-readable: ${formatted}`);
});

test('the panel never renders a fact record', () => {
    // Even if the server were to leak one, the panel must not display it. The
    // renderer reads named fields only; an unexpected key is ignored.
    const html = renderLineage(lineageFixture({
        event_id: '0199a1f0-0000-7000-8000-000000000000',
        facts: [{ event_id: 'x', latency_ms: 10 }]
    }));
    assert.ok(!html.includes('0199a1f0'), 'an event id reached the panel');
    assert.ok(!html.includes('"latency_ms": 10'), 'a fact record reached the panel');
});

// ─── AC-4: approximate values look different ───

test('TestApproximateWarningRendered', () => {
    const html = renderExactness(lineageFixture({
        exactness: 'approximate',
        known_defect: 'Counts requests that were never completed.'
    }));

    assert.match(html, /role="status"/, 'the warning is not announced to assistive tech');
    assert.match(html, /Counts requests that were never completed\./);
    // Not colour alone: the word and an icon both carry it.
    assert.match(html, /Approximate/);
    assert.match(html, /lineage-panel__warning-icon/);

    // And an exact value must NOT get the warning, or the difference means nothing.
    const exact = renderExactness(lineageFixture({ exactness: 'exact' }));
    assert.ok(!exact.includes('lineage-panel__warning'), 'an exact value rendered the warning region');
    assert.ok(!exact.includes('role="status"'));
});

test('an approximate metric with no stated defect still warns', () => {
    const html = renderExactness({ exactness: 'approximate', error_bound: 'unknown' });
    assert.match(html, /role="status"/);
    assert.match(html, /Approximate/);
});

// ─── AC-5: revisions ───

test('TestRevisionLineRendered', () => {
    const html = renderIntegrity(lineageFixture({
        current_revision: 2,
        content_digest: 'sha256:new',
        revision_history: [
            { number: 1, digest: 'sha256:first', revised_at: '2026-09-09T15:00:00Z' },
            { number: 2, digest: 'sha256:second', revised_at: '2026-09-10T09:30:00Z' }
        ]
    }));

    assert.match(html, /Revised 2 times/);
    assert.match(html, /last revised 2026-09-10T09:30:00Z/);
    assert.match(html, /previously sha256:second/);

    // An unrevised partition says nothing about revisions.
    const clean = renderIntegrity(lineageFixture());
    assert.ok(!clean.includes('Revised'), 'an unrevised partition claimed a revision');
});

test('a single revision is not pluralised', () => {
    const html = renderIntegrity(lineageFixture({
        current_revision: 1,
        revision_history: [{ number: 1, digest: 'sha256:first', revised_at: '2026-09-09T15:00:00Z' }]
    }));
    assert.match(html, /Revised 1 time;/);
});

// ─── AC-6: the 409 state is actionable ───

test('TestNoManifestStateRendersRecomputeCmd', () => {
    const cmd = 'gravix recompute --metric request_metrics_minute --from 2026-09-09 --to 2026-09-10';
    const html = renderFailure('no_manifest', cmd);

    // The copy is escaped into the HTML, so check a distinctive fragment of it
    // here and pin the constant itself against the spec text below.
    assert.ok(html.includes('it was computed before Gravix recorded provenance. Run this to make it available:'),
        'the §5.4 copy is not present');
    assert.match(html, /data-lineage-cmd/);
    assert.match(html, /data-lineage-copy/);
    assert.ok(html.includes(cmd), 'the command itself is missing');
});

// ─── AC-7: every failure state, verbatim ───

test('TestAllFailureStatesCopy', () => {
    // The constants are pinned against GRVX-809 §5.4 verbatim, so a reworded
    // error has to be a deliberate edit here rather than a drift.
    assert.equal(FAILURE_COPY.no_manifest,
        "Lineage isn't available for this bucket — it was computed before Gravix recorded provenance. Run this to make it available:");
    assert.equal(FAILURE_COPY.no_row, 'No data in this bucket for the current filters.');
    assert.equal(FAILURE_COPY.network,
        "Couldn't reach the Gravix gateway. Check that it's running: docker compose ps gateway");
    assert.equal(FAILURE_COPY.unauthorized, 'Your session expired. Reload the page to sign in again.');

    assert.ok(renderFailure('no_row').includes(FAILURE_COPY.no_row));
    assert.ok(renderFailure('network').includes('docker compose ps gateway'));
    assert.ok(renderFailure('unauthorized').includes(FAILURE_COPY.unauthorized));
    assert.ok(renderFailure('malformed').includes('This is a Gravix bug'));

    const limited = renderFailure('rate_limited', 12);
    assert.ok(limited.includes('Too many requests. This panel will retry in'));
    assert.match(limited, /data-lineage-countdown>12</, 'the countdown is not live');

    // Every state names the fix, not just the failure.
    for (const [kind, copy] of Object.entries(FAILURE_COPY)) {
        if (kind === 'rate_limited') continue;
        assert.ok(copy.length > 20, `${kind} copy is too terse to be actionable`);
    }

    // Status codes map onto the right state.
    assert.equal(failureKindForStatus(409), 'no_manifest');
    assert.equal(failureKindForStatus(404), 'no_row');
    assert.equal(failureKindForStatus(401), 'unauthorized');
    assert.equal(failureKindForStatus(429), 'rate_limited');
    assert.equal(failureKindForStatus(500), 'malformed');
});

test('a malformed payload renders the bug copy rather than throwing', () => {
    for (const bad of [null, undefined, {}, { metric: 'x' }, { bucket: 'y' }, 'not an object']) {
        const html = renderLineage(bad);
        assert.ok(html.includes('This is a Gravix bug'), `did not handle ${JSON.stringify(bad)}`);
    }
});

// ─── AC-8, AC-9: keyboard and non-modal ───

test('TestPanelKeyboardAccessible', () => {
    const { panel, container, doc } = newPanel();
    const opener = new StubElement('td');

    panel.open(opener, renderLineage(lineageFixture()));
    assert.equal(container.hidden, false);

    // Escape closes and returns focus to whatever opened it.
    doc.dispatch('keydown', { key: 'Escape' });
    assert.equal(container.hidden, true, 'Escape did not close the panel');
    assert.equal(opener.focusCount, 1, 'focus was not returned to the opener');

    // Escape on a closed panel is a no-op, not a second focus call.
    doc.dispatch('keydown', { key: 'Escape' });
    assert.equal(opener.focusCount, 1);

    // Other keys do not close it.
    panel.open(opener, renderLineage(lineageFixture()));
    doc.dispatch('keydown', { key: 'a' });
    assert.equal(container.hidden, false);
});

test('TestPanelIsNonModal', () => {
    const { panel, container } = newPanel();
    panel.open(new StubElement('td'), renderLineage(lineageFixture()));

    assert.equal(container.getAttribute('role'), 'region');
    assert.equal(container.getAttribute('aria-label'), 'Lineage');

    // A modal would be role="dialog" with aria-modal, and would trap focus. This
    // has none of those, deliberately: provenance is read alongside the number.
    assert.notEqual(container.getAttribute('role'), 'dialog');
    assert.equal(container.getAttribute('aria-modal'), undefined);
    assert.ok(!container.innerHTML.includes('inert'));
    assert.equal(typeof panel.trapFocus, 'undefined', 'the panel has a focus trap');
});

test('the close button closes the panel', () => {
    const { panel, container } = newPanel();
    const opener = new StubElement('td');
    panel.open(opener, renderLineage(lineageFixture()));

    container.dispatch('click', { target: clickTarget('[data-lineage-close]') });
    assert.equal(container.hidden, true);
    assert.equal(opener.focusCount, 1);
});

test('the copy button copies the command', () => {
    const copied = [];
    const { panel, container } = newPanel({ clipboard: { writeText: t => copied.push(t) } });
    panel.open(new StubElement('td'), renderFailure('no_manifest', 'gravix recompute --from 2026-09-09'));

    container.dispatch('click', { target: clickTarget('[data-lineage-copy]') });
    assert.deepEqual(copied, ['gravix recompute --from 2026-09-09']);
});

// ─── loading, fetching ───

test('the panel shows a loading state, never an empty one', () => {
    const html = renderLoading();
    assert.match(html, /aria-busy="true"/);
    assert.match(html, /Loading lineage/);
});

test('load() lands on a §5.4 state for every failure', async () => {
    const cases = [
        [{ ok: false, status: 409, json: async () => ({ recompute_cmd: 'gravix recompute --x' }) }, 'gravix recompute --x'],
        [{ ok: false, status: 404, json: async () => ({}) }, FAILURE_COPY.no_row],
        [{ ok: false, status: 401, json: async () => ({}) }, FAILURE_COPY.unauthorized],
        [{ ok: false, status: 500, json: async () => ({}) }, 'This is a Gravix bug'],
        [{ ok: false, status: 429, json: async () => ({}), headers: { get: () => '30' } }, '30</span>s']
    ];
    for (const [resp, expected] of cases) {
        const { panel, container } = newPanel({ fetch: async () => resp });
        await panel.load({ bucket: '2026-09-09T14:23:00Z' });
        assert.ok(container.innerHTML.includes(expected),
            `status ${resp.status} rendered:\n${container.innerHTML}`);
    }
});

test('a network failure renders the network copy rather than throwing', async () => {
    const { panel, container } = newPanel({ fetch: async () => { throw new Error('ECONNREFUSED'); } });
    await panel.load({ bucket: '2026-09-09T14:23:00Z' });
    assert.ok(container.innerHTML.includes('docker compose ps gateway'));
});

test('a success renders the lineage', async () => {
    const { panel, container } = newPanel({
        fetch: async () => ({ ok: true, status: 200, json: async () => lineageFixture() })
    });
    await panel.load({ bucket: '2026-09-09T14:23:00Z' });
    assert.match(container.innerHTML, /Reproduce this number/);
});

// ─── the request ───

test('only declared dimensions are ever requested', () => {
    const url = lineageUrl('/api/v1/lineage', {
        bucket: '2026-09-09T14:23:00Z',
        service: 'api',
        method: 'GET',
        path_template: '/users/{id}',
        user_id: '42',
        request_id: 'abc'
    });
    assert.match(url, /service=api/);
    assert.match(url, /path_template=%2Fusers%2F%7Bid%7D/);
    assert.ok(!url.includes('user_id'), 'a high-cardinality filter was sent');
    assert.ok(!url.includes('request_id'), 'a high-cardinality filter was sent');
});

test('retryAfterSeconds defaults rather than producing NaN', () => {
    assert.equal(retryAfterSeconds({ headers: { get: () => '30' } }), 30);
    assert.equal(retryAfterSeconds({ headers: { get: () => 'later' } }), 5);
    assert.equal(retryAfterSeconds({}), 5);
    assert.equal(retryAfterSeconds(null), 5);
});

// ─── escaping ───

test('every dynamic value is escaped', () => {
    const html = renderLineage(lineageFixture({
        formula: '<img src=x onerror=alert(1)>',
        merge_note: '"><script>alert(2)</script>',
        source_fact_keys: ['<script>alert(3)</script>']
    }));
    assert.ok(!html.includes('<script>'), 'a script tag survived escaping');
    assert.ok(!html.includes('<img'), 'an img tag survived escaping');
    assert.match(html, /&lt;img/);
    assert.equal(esc(`<&">'`), '&lt;&amp;&quot;&gt;&#39;');
});

// ─── AC-6's tail: a long fact list collapses ───

test('TestManyFactKeysCollapse', () => {
    const keys = Array.from({ length: 27 }, (_, i) => `raw/request_facts/2026-09-09/14/f${i}.jsonl`);
    const html = renderFactKeys(keys);

    assert.match(html, /and 17 more/);
    assert.match(html, /<details>/, 'the tail is not expandable');
    assert.ok(html.includes('f0.jsonl') && html.includes('f9.jsonl'), 'the first ten are not shown');
    assert.ok(html.includes('f26.jsonl'), 'the tail is not in the document at all');

    // Exactly ten needs no details element.
    const ten = renderFactKeys(keys.slice(0, 10));
    assert.ok(!ten.includes('<details>'));
    // And none is stated rather than shown as an empty list.
    assert.match(renderFactKeys([]), /No source files recorded/);
});

// ─── AC-12, AC-13, AC-14: the dashboard's own constraints ───

test('TestNoUpsellInDashboard', () => {
    const files = ['app.js', 'index.html', 'styles.css', 'lib/lineage-panel.js', 'lib/cube-client.js'];

    // Charter §7.4 forbids showing a free user a locked feature and inviting them
    // to pay. What it forbids is the invitation: a call to action, a padlock, a
    // "Pro" badge on something they cannot use. A feature that is simply absent
    // for their plan is not an upsell — nobody is being sold anything — which is
    // why this matches calls to action rather than every mention of a plan name.
    const banned = [
        /upgrade\s+(to|now|your)/i,
        /go\s+pro\b/i,
        /unlock\s+(this|these|with|by)/i,
        /premium\s+feature/i,
        /start\s+(your\s+)?free\s+trial/i,
        /available\s+on\s+(the\s+)?(pro|scale|enterprise)/i,
        /🔒|&#128274;/
    ];
    for (const f of files) {
        const text = readFileSync(join(dashboardsDir, f), 'utf8');
        text.split('\n').forEach((line, i) => {
            for (const pattern of banned) {
                assert.ok(!pattern.test(line), `${f}:${i + 1} contains upsell copy: ${line.trim()}`);
            }
        });
    }
});

test('TestNoBuildStepAdded', () => {
    for (const f of ['package.json', 'package-lock.json', 'vite.config.js', 'webpack.config.js', 'tsconfig.json']) {
        assert.throws(() => readFileSync(join(dashboardsDir, f)), `dashboards/${f} exists; that is a build step`);
    }
    // And no new external source in the panel.
    const panel = readFileSync(join(dashboardsDir, 'lib', 'lineage-panel.js'), 'utf8');
    for (const host of ['cdn.', 'unpkg', 'jsdelivr', 'http://', 'https://']) {
        assert.ok(!panel.includes(host), `lineage-panel.js references ${host}`);
    }
    // No import from anywhere but a relative path.
    const imports = panel.match(/^import .* from ['"](.*)['"]/gm) || [];
    for (const line of imports) {
        assert.match(line, /['"]\.\.?\//, `non-relative import: ${line}`);
    }
});

// ─── AC-10: contrast, computed rather than asserted ───

function relativeLuminance(hex) {
    const h = hex.replace('#', '');
    const channels = [0, 2, 4].map(i => parseInt(h.slice(i, i + 2), 16) / 255)
        .map(c => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)));
    return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

function contrast(a, b) {
    const [hi, lo] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x);
    return (hi + 0.05) / (lo + 0.05);
}

// tokensFor reads the custom properties actually defined for one theme state, so
// the test fails if someone edits a colour rather than if someone edits the test.
function tokensFor(css, selector) {
    const at = css.indexOf(selector);
    assert.ok(at >= 0, `theme block not found: ${selector}`);
    const block = css.slice(at, css.indexOf('}', at));
    const out = {};
    for (const m of block.matchAll(/(--[a-z-]+):\s*(#[0-9a-fA-F]{6})/g)) {
        out[m[1]] = m[2];
    }
    return out;
}

test('TestPanelContrastAllThemes', () => {
    const css = readFileSync(join(dashboardsDir, 'styles.css'), 'utf8');
    const themes = {
        light: tokensFor(css, ':root {'),
        dark: tokensFor(css, '[data-theme="dark"] {'),
        system: tokensFor(css, ':root:not([data-theme="light"]) {')
    };

    // Exactly the foreground/background pairs the panel puts together.
    const pairs = [
        ['--text-primary', '--card-bg'],
        ['--text-secondary', '--card-bg'],
        ['--text-primary', '--bg-secondary'],
        ['--badge-warning-text', '--badge-warning-bg']
    ];

    for (const [name, tokens] of Object.entries(themes)) {
        for (const [fg, bg] of pairs) {
            assert.ok(tokens[fg], `${name} does not define ${fg}`);
            assert.ok(tokens[bg], `${name} does not define ${bg}`);
            const ratio = contrast(tokens[fg], tokens[bg]);
            assert.ok(ratio >= 4.5,
                `${name}: ${fg} on ${bg} is ${ratio.toFixed(2)}:1, below WCAG AA 4.5:1`);
        }
    }

    // The system-default block must define every token the dark block does, or a
    // viewer on system dark gets a half-themed panel.
    for (const token of Object.keys(themes.dark)) {
        assert.ok(themes.system[token], `the system-default block does not define ${token}`);
    }
});

test('the panel uses no colour that is undefined in any theme', () => {
    const css = readFileSync(join(dashboardsDir, 'styles.css'), 'utf8');
    const panelCss = css.slice(css.indexOf('/* ─── Lineage panel'));
    const used = new Set([...panelCss.matchAll(/var\((--[a-z-]+)\)/g)].map(m => m[1]));
    assert.ok(used.size > 0, 'the panel styles were not found');

    for (const selector of [':root {', '[data-theme="dark"] {', ':root:not([data-theme="light"]) {']) {
        const block = css.slice(css.indexOf(selector), css.indexOf('}', css.indexOf(selector)));
        for (const token of used) {
            assert.ok(block.includes(token + ':'), `${token} is not defined in ${selector}`);
        }
    }

    // And no literal colour in the panel styles, which would bypass theming.
    assert.ok(!/#[0-9a-fA-F]{3,6}\b/.test(panelCss.replace(/\/\*[\s\S]*?\*\//g, '')),
        'the panel styles contain a hard-coded colour');
});

// ─── AC-11: no horizontal scroll at 400px ───

test('TestPanelNoHorizontalScrollAt400px', () => {
    const css = readFileSync(join(dashboardsDir, 'styles.css'), 'utf8');
    const panelCss = css.slice(css.indexOf('/* ─── Lineage panel'));

    // The two things that can be arbitrarily long — object keys and the recompute
    // command — scroll inside their own box.
    for (const cls of ['.lineage-panel__keys', '.lineage-panel__code']) {
        const at = panelCss.indexOf(cls);
        assert.ok(at >= 0, `${cls} has no rule`);
    }
    assert.match(panelCss, /overflow-x:\s*auto/);

    // Nothing may set a min-width wider than a phone, and the panel itself is
    // bounded by its container.
    const minWidths = [...panelCss.matchAll(/min-width:\s*(\d+)px/g)].map(m => Number(m[1]));
    for (const w of minWidths) {
        assert.ok(w <= 400, `a min-width of ${w}px would force the page to scroll at 400px`);
    }
    assert.match(panelCss, /max-width:\s*100%/);

    // At phone width the grid becomes one column rather than two.
    assert.match(panelCss, /@media \(max-width: 480px\)/);
});

test('the panel is rendered as one column at phone width', () => {
    const css = readFileSync(join(dashboardsDir, 'styles.css'), 'utf8');
    const at = css.indexOf('@media (max-width: 480px)');
    const block = css.slice(at, at + 400);
    assert.match(block, /grid-template-columns:\s*1fr/);
});

// ─── the grain mismatch the spec did not reconcile (SD-010) ───

test('a coarser click is stated, not hidden', () => {
    const html = renderLineage(lineageFixture(), { clickedGranularity: 'hour' });
    assert.match(html, /role="status"/);
    assert.match(html, /You clicked a value covering one hour/);
    assert.match(html, /explains the minute at the start of it/);
    assert.match(html, /smaller than the one you clicked/);
});

test('no note when the click matches the grain', () => {
    for (const g of ['minute', undefined, null, '']) {
        const html = renderLineage(lineageFixture(), { clickedGranularity: g });
        assert.ok(!html.includes('You clicked a value covering'),
            `a spurious grain note for granularity ${String(g)}`);
    }
});

// ─── bucket conversion ───

test('toBucketISO reads every shape the layers return', async () => {
    const { toBucketISO } = await import('./lineage-panel.js');
    // Cube: no zone, milliseconds. A missing zone is UTC, because that is the
    // only thing Gravix stores.
    assert.equal(toBucketISO('2026-09-09T14:00:00.000'), '2026-09-09T14:00:00Z');
    assert.equal(toBucketISO('2026-09-09 14:00:00'), '2026-09-09T14:00:00Z');
    // The percentile endpoint: already RFC3339.
    assert.equal(toBucketISO('2026-09-09T14:00:00Z'), '2026-09-09T14:00:00Z');
    // An offset is respected rather than overwritten.
    assert.equal(toBucketISO('2026-09-09T14:00:00+01:00'), '2026-09-09T14:00:00+01:00');
    assert.equal(toBucketISO('2026-09-09T14:00:00.500Z'), '2026-09-09T14:00:00Z');
    // Nothing in, nothing out — never "undefinedZ".
    for (const empty of ['', null, undefined]) {
        assert.equal(toBucketISO(empty), '');
    }
});

test('the dashboard keeps no second copy of the conversion', () => {
    const app = readFileSync(join(dashboardsDir, 'app.js'), 'utf8');
    // app.js may call it, but must not reimplement it: two copies of a timestamp
    // rule is two rules.
    assert.match(app, /GravixLineageRender/, 'app.js does not delegate to the module');
    assert.ok(!/v\s*\+=\s*'Z'/.test(app), 'app.js reimplements the zone default');
});

// ─── AC-13: the bundle budget ───

test('TestDashboardBundleBudget', async () => {
    const { gzipSync } = await import('node:zlib');
    const { readdirSync } = await import('node:fs');

    // What this measures, and why it changed.
    //
    // It used to subtract a constant captured before GRVX-809, which made it a
    // permanent ceiling on the whole dashboard rather than a budget for one
    // change — the opposite of what the comment here claimed. Every feature
    // since spent the lineage panel's allowance, and GRVX-902 tripped it by 31
    // bytes for a change that had nothing to do with the panel (F-012).
    //
    // The baseline now lives in a committed file. A change that legitimately
    // grows the bundle updates that file in the same commit, so the growth is a
    // reviewable number in the diff instead of a silent accumulation — and the
    // budget keeps meaning "what the change in front of you adds".
    const baselinePath = join(dashboardsDir, 'bundle-baseline.json');
    const baseline = JSON.parse(readFileSync(baselinePath, 'utf8'));
    const BUDGET = 8 * 1024;

    const shipped = ['app.js', 'styles.css']
        .concat(readdirSync(join(dashboardsDir, 'lib'))
            .filter(f => f.endsWith('.js') && !f.endsWith('.test.js'))
            .sort()
            .map(f => join('lib', f)));

    const bytes = Buffer.concat(shipped.map(f => readFileSync(join(dashboardsDir, f))));
    const size = gzipSync(bytes).length;
    const delta = size - baseline.gzip_bytes;
    const sinceOrigin = size - baseline.origin_gzip_bytes;

    // Cumulative growth is reported every run but never fails the test. Whether
    // the dashboard should have a hard total ceiling, and what it should be, is
    // a product call about load time rather than something to infer from
    // wherever the bundle happened to sit when this guard was written.
    console.log(`  dashboards/ gzipped: ${size} bytes` +
        ` (${delta >= 0 ? '+' : ''}${delta} vs baseline ${baseline.gzip_bytes},` +
        ` recorded for ${baseline.recorded_for};` +
        ` ${sinceOrigin >= 0 ? '+' : ''}${sinceOrigin} since ${baseline.origin_label})`);

    assert.ok(delta <= BUDGET,
        `the dashboard grew ${delta} bytes gzipped since the recorded baseline, over the ` +
        `${BUDGET}-byte budget. If the growth is intended, update ${baselinePath} to ${size} ` +
        `in this same commit so the increase is visible in the diff.`);

    // And no test file is shipped.
    assert.ok(!shipped.some(f => f.includes('.test.')), 'a test file is in the shipped set');
});

// ─── the two layout rules that stop content being clipped ───
//
// Both were found by rendering the page in a real browser at 400px, not by
// reading the CSS — the page did not scroll in either case, so a document-level
// scroll check saw nothing while the right-hand side of the panel was simply
// absent. These pin the fixes.

test('the dashboard grid track cannot exceed its container', () => {
    const css = readFileSync(join(dashboardsDir, 'styles.css'), 'utf8');
    const responsive = css.slice(css.indexOf('/* Responsive: Tablet */'));

    // `1fr` carries an implicit min-width:auto, so a card with wide content
    // pushes the single column past the grid. Measured at 400px: a 524px track
    // in a 368px grid, with #main-content's overflow-x:auto hiding the difference.
    const tracks = [...responsive.matchAll(/\.grid\s*\{[^}]*grid-template-columns:\s*([^;]+);/g)]
        .map(m => m[1].trim());
    assert.ok(tracks.length >= 2, 'the responsive grid rules were not found');
    for (const track of tracks) {
        assert.ok(!/^1fr$/.test(track),
            `a bare 1fr track will overflow its container: ${track}`);
        assert.match(track, /minmax\(\s*0/, `track does not allow shrinking: ${track}`);
    }
});

test('flex and grid children in the panel are allowed to shrink', () => {
    const css = readFileSync(join(dashboardsDir, 'styles.css'), 'utf8');
    const panelCss = css.slice(css.indexOf('/* ─── Lineage panel'));

    // A flex or grid item will not go below its content width unless told to.
    // The warning's text and the definition list's values are the two places
    // long content lands.
    assert.match(panelCss, /\.lineage-panel__warning\s*>\s*:not\([^)]*\)\s*\{[^}]*min-width:\s*0/,
        'the warning text cannot shrink, so it is clipped at narrow widths');
    assert.match(panelCss, /\.lineage-panel dd\s*\{[^}]*min-width:\s*0/,
        'a long digest will widen the definition list past its container');
    assert.match(panelCss, /\.lineage-panel dd\s*\{[^}]*overflow-wrap:\s*anywhere/,
        'a 71-character digest has no break opportunity without this');
    assert.match(panelCss, /grid-template-columns:\s*minmax\([^)]*\)\s+minmax\(\s*0/,
        'the value column cannot shrink below its content');
});

test('the warning region is separated from what follows it', () => {
    const css = readFileSync(join(dashboardsDir, 'styles.css'), 'utf8');
    const at = css.indexOf('.lineage-panel__warning {');
    const block = css.slice(at, css.indexOf('}', at));
    assert.match(block, /margin:[^;]*\d/, 'the warning butts against the next section');
});
