// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// GRVX-903 §7, AC-1 through AC-6. Run with:
//   make test-js
//   node --test dashboards/lib/slo-cards.test.js
//
// §7 offers two harnesses and says the implementer picks one. This is the first:
// node --test, no dependency of any kind, following the pattern GRVX-809
// established for dashboards/lib. The chromedp alternative would add a Go
// dependency and a browser to prove properties that are pure arithmetic and
// string building.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
    SLO_TARGET_AVAILABILITY, availabilityPercent, errorBudgetRemainingPercent,
    formatPercent, formatCount, formatLatency, renderSLOCard, loadSLOPage, esc
} from './slo-cards.js';

// ─── the smallest element stub that these assertions need ───
//
// innerHTML is a plain string and style.display a plain property, which is all
// loadSLOPage touches. cards() counts rendered cards by their marker class
// rather than by parsing HTML, because a parser here would be a second
// implementation of the thing under test.

function stubElement() {
    const classes = new Set();
    return {
        innerHTML: '',
        style: { display: '' },
        classList: {
            toggle: (name, on) => (on ? classes.add(name) : classes.delete(name)),
            contains: name => classes.has(name)
        },
        // visible() asks the question the user cares about, not the question the
        // implementation happens to answer. An earlier version of this file
        // asserted on style.display, which passed while the empty state was in
        // fact permanently hidden by .empty-state { display: none } in the CSS.
        visible: () => classes.has('visible')
    };
}

function cards(grid) {
    return (grid.innerHTML.match(/class="card slo-card/g) || []).length;
}

function servicesResponse(names) {
    return {
        ok: true,
        json: async () => ({ services: names.map(n => ({ name: n, request_count: 1 })) })
    };
}

function metricsFor(overrides = {}) {
    return async () => Object.assign({ errorRate: 0, p95LatencyMs: 0, requestCount: 0 }, overrides);
}

// ─── AC-1 ───

test('TestSLOPageEmptyWhenNoServices', async () => {
    const grid = stubElement();
    const empty = stubElement();

    await loadSLOPage({
        fetchServices: async () => servicesResponse([]),
        fetchMetrics: metricsFor(),
        grid, empty
    });

    assert.ok(empty.visible(), 'AC-1 FAILED: the empty state is hidden');
    assert.equal(grid.style.display, 'none', 'AC-1 FAILED: the grid is still shown');
    assert.equal(cards(grid), 0, 'AC-1 FAILED: the grid rendered cards for no services');
    assert.equal(grid.innerHTML, '', 'AC-1 FAILED: the grid has leftover content');
});

test('TestSLOPageEmptyWhenDiscoveryFails', async () => {
    for (const [label, fetchServices] of [
        ['a non-2xx response', async () => ({ ok: false, json: async () => ({}) })],
        ['a rejected request', async () => { throw new Error('network'); }],
        ['a body with no services key', async () => ({ ok: true, json: async () => ({}) })]
    ]) {
        const grid = stubElement();
        const empty = stubElement();
        await loadSLOPage({ fetchServices, fetchMetrics: metricsFor(), grid, empty });

        assert.ok(empty.visible(), `${label}: the empty state should be shown`);
        assert.equal(cards(grid), 0, `${label}: no cards should be rendered`);
    }
});

// ─── AC-2 ───

test('TestSLOPageRendersOneCardPerService', async () => {
    const grid = stubElement();
    const empty = stubElement();

    await loadSLOPage({
        fetchServices: async () => servicesResponse(['auth', 'checkout']),
        fetchMetrics: metricsFor({ errorRate: 0.0005, p95LatencyMs: 84, requestCount: 4820 }),
        grid, empty
    });

    assert.equal(cards(grid), 2, 'AC-2 FAILED: wrong number of cards');
    assert.ok(!empty.visible(), 'AC-2 FAILED: the empty state is still shown');
    assert.equal(grid.style.display, 'grid', 'AC-2 FAILED: the grid is hidden');
    assert.match(grid.innerHTML, /data-service="auth"/);
    assert.match(grid.innerHTML, /data-service="checkout"/);
});

test('TestSLOPageClearsPreviousCards', async () => {
    const grid = stubElement();
    const empty = stubElement();
    const opts = name => ({
        fetchServices: async () => servicesResponse([name]),
        fetchMetrics: metricsFor(),
        grid, empty
    });

    await loadSLOPage(opts('first'));
    await loadSLOPage(opts('second'));

    assert.equal(cards(grid), 1, 'a second load appended instead of replacing');
    assert.doesNotMatch(grid.innerHTML, /data-service="first"/,
        'a service that is no longer discovered is still on the page');
});

// ─── AC-3 ───

test('TestSLOCardsPreserveServiceOrder', async () => {
    const grid = stubElement();
    const empty = stubElement();

    // Metrics resolve in reverse order of the request, so rendering in
    // resolution order would reverse the page. Without this delay the test
    // passes whether or not the implementation preserves order.
    const delays = { alpha: 30, mike: 15, zeta: 0 };

    await loadSLOPage({
        fetchServices: async () => servicesResponse(['alpha', 'mike', 'zeta']),
        fetchMetrics: name => new Promise(resolve =>
            setTimeout(() => resolve({ errorRate: 0, p95LatencyMs: 1, requestCount: 1 }),
                delays[name])),
        grid, empty
    });

    const order = [...grid.innerHTML.matchAll(/data-service="([^"]+)"/g)].map(m => m[1]);
    assert.deepEqual(order, ['alpha', 'mike', 'zeta'],
        'AC-3 FAILED: cards rendered in resolution order, so the page reshuffles on every refresh');
});

// ─── AC-4 ───

test('TestAvailabilityFormula', () => {
    assert.equal(formatPercent(availabilityPercent(0.001)), '99.900%',
        'AC-4 FAILED: availability for a 0.1% error rate');

    assert.equal(formatPercent(availabilityPercent(0)), '100.000%');
    assert.equal(formatPercent(availabilityPercent(1)), '0.000%');
    // Clamped: a rate outside [0,1] is bad data, not a negative availability.
    assert.equal(formatPercent(availabilityPercent(1.5)), '0.000%');
    assert.equal(formatPercent(availabilityPercent(-0.5)), '100.000%');
    // Cube returns measures as strings often enough that this is not hypothetical.
    assert.equal(formatPercent(availabilityPercent('0.001')), '99.900%');
    assert.equal(formatPercent(availabilityPercent(null)), '100.000%');
    assert.equal(formatPercent(availabilityPercent(undefined)), '100.000%');
    assert.equal(formatPercent(availabilityPercent(NaN)), '100.000%');
});

// ─── AC-5 ───

test('TestErrorBudgetRemainingFormula', () => {
    assert.equal(formatPercent(errorBudgetRemainingPercent(0)), '100.000%',
        'AC-5 FAILED: a perfect error rate should leave the whole budget');

    // The budget is a share of the allowance, not of all requests. At a 99.9%
    // target, half the allowance is an error rate of 0.05%.
    assert.equal(formatPercent(errorBudgetRemainingPercent(0.0005)), '50.000%');
    // Exactly at target: the budget is gone, not "99.9% left".
    assert.equal(formatPercent(errorBudgetRemainingPercent(0.001)), '0.000%');
    // Past target: clamped at zero rather than going negative.
    assert.equal(formatPercent(errorBudgetRemainingPercent(0.01)), '0.000%');

    assert.equal(SLO_TARGET_AVAILABILITY, 0.999,
        'AC-5 FAILED: the target is not the fixed 99.9% this spec mandates');
});

// ─── AC-6 ───

test('TestSLOCardRendersWithNoRollupDataYet', async () => {
    const grid = stubElement();
    const empty = stubElement();

    // Cube returns nothing for a service discovered minutes ago: it was seen on
    // a fact, but no rollup has run. This is every new user's first five
    // minutes, so it must read as "not yet", never as an error.
    await loadSLOPage({
        fetchServices: async () => servicesResponse(['brand-new']),
        fetchMetrics: async () => null,
        grid, empty
    });

    assert.equal(cards(grid), 1, 'AC-6 FAILED: a service with no rolled-up rows got no card');
    assert.match(grid.innerHTML, /data-service="brand-new"/);
    assert.match(grid.innerHTML, /100\.000%/, 'AC-6 FAILED: availability should read 100% at zero traffic');
    // "Error budget left" is a label, so a bare /error/ match would fire on a
    // perfectly good card — an earlier version of this assertion did exactly
    // that. What must not appear is a rendering failure or a failure marker.
    assert.doesNotMatch(grid.innerHTML, /NaN|undefined|null/,
        'AC-6 FAILED: the card rendered a missing value literally');
    assert.doesNotMatch(grid.innerHTML, /query failed/,
        'AC-6 FAILED: "no rollup yet" was reported as a query failure');
    assert.match(grid.innerHTML, /<dd class="slo-requests">0<\/dd>/,
        'AC-6 FAILED: request volume should read zero, not blank');
});

test('TestSLOCardMarksAFailedQuery', async () => {
    const grid = stubElement();
    const empty = stubElement();

    await loadSLOPage({
        fetchServices: async () => servicesResponse(['flaky']),
        fetchMetrics: async () => { throw new Error('cube unreachable'); },
        grid, empty
    });

    assert.equal(cards(grid), 1, 'a failed per-service query should still render its card');
    assert.match(grid.innerHTML, /title="query failed"/,
        '§6.1 FAILED: a failed query is indistinguishable from real zeros');
});

// ─── rendering details the criteria do not reach ───

test('TestSLOCardEscapesServiceName', () => {
    const html = renderSLOCard('<img src=x onerror=alert(1)>', { errorRate: 0 });
    assert.doesNotMatch(html, /<img/, 'a service name is attacker-controlled: it arrives on a fact');
    assert.match(html, /&lt;img/);
    assert.equal(esc('a"b'), 'a&quot;b');
});

test('TestSLOCardMarksABreach', () => {
    const healthy = renderSLOCard('ok', { errorRate: 0.0001 });
    const breached = renderSLOCard('bad', { errorRate: 0.02 });

    assert.doesNotMatch(healthy, /slo-card-breached/, 'a service inside its target is marked breached');
    assert.match(breached, /slo-card-breached/, 'a service past its target is not marked');
});

test('TestVolumeAndLatencyFormatting', () => {
    assert.equal(formatCount(0), '0');
    assert.equal(formatCount(482), '482');
    assert.equal(formatCount(4820), '4.8k');
    assert.equal(formatCount(2_400_000), '2.4M');
    assert.equal(formatCount('4820'), '4.8k');
    assert.equal(formatCount(null), '0');

    assert.equal(formatLatency(0), '0 ms');
    assert.equal(formatLatency(84.46), '84.5 ms');
    assert.equal(formatLatency(1234.6), '1235 ms');
    assert.equal(formatLatency('84.46'), '84.5 ms');
});
