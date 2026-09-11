// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// GRVX-908 §7, AC-1 through AC-10. Run with `make test-js`.
// Harness: node --test, extending dashboards/lib as GRVX-903 established.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
    FIRST_ROLLUP_ESTIMATE_SECONDS, FIRST_SERVICE_SEEN_KEY, ALMOST_THERE_TEXT,
    formatCountdown, computeCountdownText, checkFirstRunState, startCountdown,
    activeCountdownId, stopCountdown
} from './first-run.js';

const START = new Date('2026-09-11T12:00:00.000Z');
const at = seconds => new Date(START.getTime() + seconds * 1000);

function stubDoc() {
    const els = {
        'onboard-waiting': { style: { display: 'block' } },
        'onboard-countdown': { style: { display: 'none' } },
        'onboard-countdown-text': { textContent: '' }
    };
    return { getElementById: id => els[id] || null, _els: els };
}

function stubStorage(initial = {}) {
    const data = { ...initial };
    return {
        getItem: k => (k in data ? data[k] : null),
        setItem: (k, v) => { data[k] = String(v); },
        _data: data
    };
}

// A controllable clock, so a four-minute countdown is testable in no time.
function fakeTimers() {
    let nextId = 1;
    const active = new Map();
    return {
        setInterval: (fn) => { const id = nextId++; active.set(id, fn); return id; },
        clearInterval: (id) => { active.delete(id); },
        tick: () => { for (const fn of [...active.values()]) fn(); },
        count: () => active.size
    };
}

function servicesResponse(n) {
    return async () => ({
        ok: true,
        json: async () => ({ services: Array.from({ length: n }, (_, i) => ({ name: `svc-${i}` })) })
    });
}

// ─── AC-1, AC-2, AC-3 ───

test('TestFormatCountdownTypical', () => {
    assert.equal(formatCountdown(245), '4:05', 'AC-1 FAILED');
    assert.equal(formatCountdown(240), '4:00');
    assert.equal(formatCountdown(61), '1:01');
    assert.equal(formatCountdown(60), '1:00');
    assert.equal(formatCountdown(59), '0:59');
    // Two digits on seconds always, or "4:5" reads as four minutes five.
    assert.equal(formatCountdown(65), '1:05');
    // No leading zero on minutes, per §5.
    assert.doesNotMatch(formatCountdown(65), /^0\d/);
});

test('TestFormatCountdownZero', () => {
    assert.equal(formatCountdown(0), '0:00', 'AC-2 FAILED');
});

test('TestFormatCountdownClampsNegative', () => {
    assert.equal(formatCountdown(-5), '0:00', 'AC-3 FAILED');
    assert.equal(formatCountdown(-10000), '0:00');
    // Non-numeric input must not render "NaN:NaN" into the page.
    assert.equal(formatCountdown(NaN), '0:00');
    assert.equal(formatCountdown(undefined), '0:00');
});

// ─── AC-4, AC-5, AC-6 ───

test('TestComputeCountdownTextAtStart', () => {
    assert.equal(computeCountdownText(START, at(0)),
        'First rollup completes in ~4:00', 'AC-4 FAILED');
});

test('TestComputeCountdownTextNearEnd', () => {
    assert.equal(computeCountdownText(START, at(239)),
        'First rollup completes in ~0:01', 'AC-5 FAILED');
    assert.equal(computeCountdownText(START, at(180)), 'First rollup completes in ~1:00');
});

test('TestComputeCountdownTextAfterEstimateElapsed', () => {
    assert.equal(computeCountdownText(START, at(240)), ALMOST_THERE_TEXT, 'AC-6 FAILED at 240');
    assert.equal(computeCountdownText(START, at(300)), ALMOST_THERE_TEXT, 'AC-6 FAILED at 300');
    assert.equal(computeCountdownText(START, at(100000)), ALMOST_THERE_TEXT);

    // It must not start counting again, or the estimate would look like a lie
    // that keeps renewing itself.
    assert.doesNotMatch(computeCountdownText(START, at(600)), /completes in/);
});

test('TestComputeCountdownTextHandlesClockSkew', () => {
    // firstSeenAt in the future: show the full estimate rather than a negative
    // duration or a jump straight to "almost there".
    assert.equal(computeCountdownText(START, at(-30)), 'First rollup completes in ~4:00');
    // An unparseable stored timestamp must not render NaN into the page.
    const text = computeCountdownText(new Date('not a date'), at(0));
    assert.doesNotMatch(text, /NaN/, `rendered NaN: ${text}`);
});

test('TestEstimateMatchesItsDerivation', () => {
    // 30s (half the 60s rotation) + 150s (half the 300s rollup) + 60s startup.
    assert.equal(FIRST_ROLLUP_ESTIMATE_SECONDS, 240);
    assert.equal(30 + 150 + 60, FIRST_ROLLUP_ESTIMATE_SECONDS,
        'the constant no longer matches the derivation documented beside it');
});

// ─── AC-7 ───

test('TestCheckFirstRunStateNoServices', async () => {
    const doc = stubDoc();
    const storage = stubStorage();

    const got = await checkFirstRunState({
        fetchServices: servicesResponse(0), document: doc, storage, now: () => START
    });

    assert.equal(got, 'waiting', 'AC-7 FAILED');
    assert.equal(doc._els['onboard-waiting'].style.display, 'block', 'AC-7 FAILED: waiting hidden');
    assert.equal(doc._els['onboard-countdown'].style.display, 'none', 'AC-7 FAILED: countdown shown');
    // Nothing recorded: the clock starts when traffic is seen, not when the
    // page is opened.
    assert.equal(storage.getItem(FIRST_SERVICE_SEEN_KEY), null,
        'AC-7 FAILED: the countdown clock started with no traffic');
});

test('TestCheckFirstRunStateFallsBackToWaitingOnFailure', async () => {
    for (const [label, fetchServices] of [
        ['a non-2xx response', async () => ({ ok: false, json: async () => ({}) })],
        ['a rejected request', async () => { throw new Error('network'); }],
        ['a body with no services key', async () => ({ ok: true, json: async () => ({}) })]
    ]) {
        const doc = stubDoc();
        const got = await checkFirstRunState({
            fetchServices, document: doc, storage: stubStorage(), now: () => START
        });
        // "We do not know" must resolve to the curl command, not to a countdown
        // promising data that may not exist.
        assert.equal(got, 'waiting', `${label}: should fall back to waiting`);
        assert.equal(doc._els['onboard-countdown'].style.display, 'none', label);
    }
});

// ─── AC-8 ───

test('TestCheckFirstRunStateFirstService', async () => {
    const doc = stubDoc();
    const storage = stubStorage();
    const timers = fakeTimers();

    const got = await checkFirstRunState({
        fetchServices: servicesResponse(1), document: doc, storage, now: () => START,
        setInterval: timers.setInterval, clearInterval: timers.clearInterval
    });

    assert.equal(got, 'countdown', 'AC-8 FAILED');
    assert.equal(storage.getItem(FIRST_SERVICE_SEEN_KEY), START.toISOString(),
        'AC-8 FAILED: the first-seen moment was not recorded');
    assert.equal(doc._els['onboard-waiting'].style.display, 'none', 'AC-8 FAILED: waiting still shown');
    assert.equal(doc._els['onboard-countdown'].style.display, 'block', 'AC-8 FAILED: countdown hidden');
    // Painted immediately, not after the first tick.
    assert.equal(doc._els['onboard-countdown-text'].textContent, 'First rollup completes in ~4:00',
        'AC-8 FAILED: the countdown showed the markup placeholder for a second first');

    stopCountdown({ clearInterval: timers.clearInterval });
});

// ─── AC-9 ───

test('TestCheckFirstRunStateMonotonicFirstSeen', async () => {
    const doc = stubDoc();
    const storage = stubStorage();
    const timers = fakeTimers();
    const opts = now => ({
        fetchServices: servicesResponse(2), document: doc, storage, now: () => now,
        setInterval: timers.setInterval, clearInterval: timers.clearInterval
    });

    await checkFirstRunState(opts(START));
    const first = storage.getItem(FIRST_SERVICE_SEEN_KEY);

    // A later poll must not restart the clock, or the countdown would sit at
    // 4:00 forever and never reach zero.
    await checkFirstRunState(opts(at(90)));
    assert.equal(storage.getItem(FIRST_SERVICE_SEEN_KEY), first,
        'AC-9 FAILED: a later poll overwrote the first-seen time');
    assert.equal(doc._els['onboard-countdown-text'].textContent,
        'First rollup completes in ~2:30',
        'AC-9 FAILED: the countdown restarted instead of continuing');

    stopCountdown({ clearInterval: timers.clearInterval });
});

test('TestCheckFirstRunStateSurvivesUnusableStorage', async () => {
    // A sandboxed context where sessionStorage throws rather than returning
    // null. Degraded (the countdown restarts each poll) but never broken.
    const throwing = {
        getItem: () => { throw new Error('storage disabled'); },
        setItem: () => { throw new Error('storage disabled'); }
    };
    const doc = stubDoc();
    const timers = fakeTimers();

    const got = await checkFirstRunState({
        fetchServices: servicesResponse(1), document: doc, storage: throwing, now: () => START,
        setInterval: timers.setInterval, clearInterval: timers.clearInterval
    });

    assert.equal(got, 'countdown', 'unusable storage must not prevent the countdown');
    assert.match(doc._els['onboard-countdown-text'].textContent, /First rollup completes in/);

    stopCountdown({ clearInterval: timers.clearInterval });
});

// ─── AC-10 ───

test('TestStartCountdownReplacesPriorInterval', () => {
    const doc = stubDoc();
    const timers = fakeTimers();
    const deps = {
        document: doc, setInterval: timers.setInterval, clearInterval: timers.clearInterval,
        now: () => at(0)
    };

    const first = startCountdown(START, deps);
    assert.equal(timers.count(), 1, 'AC-10 FAILED: the first call started no interval');

    const second = startCountdown(START, deps);
    assert.equal(timers.count(), 1,
        'AC-10 FAILED: two intervals are running, so the text is painted twice a second and ' +
        'one of them is never cleared');
    assert.notEqual(first, second, 'AC-10 FAILED: the interval was not actually replaced');
    assert.equal(activeCountdownId(), second);

    stopCountdown({ clearInterval: timers.clearInterval });
    assert.equal(timers.count(), 0);
});

// TestCountdownStopsItself: a timer that keeps firing forever on an unattended
// tab is a battery cost with nothing to show for it.
test('TestCountdownStopsItself', () => {
    const doc = stubDoc();
    const timers = fakeTimers();
    let clock = 0;

    startCountdown(START, {
        document: doc,
        setInterval: timers.setInterval,
        clearInterval: timers.clearInterval,
        now: () => at(clock)
    });
    assert.equal(timers.count(), 1);

    clock = 239;
    timers.tick();
    assert.equal(doc._els['onboard-countdown-text'].textContent, 'First rollup completes in ~0:01');
    assert.equal(timers.count(), 1, 'stopped one second early');

    clock = 240;
    timers.tick();
    assert.equal(doc._els['onboard-countdown-text'].textContent, ALMOST_THERE_TEXT);
    assert.equal(timers.count(), 0, 'the interval kept running after the estimate elapsed');
    assert.equal(activeCountdownId(), null);
});

test('TestStartCountdownAfterEstimateStartsNoTimer', () => {
    const doc = stubDoc();
    const timers = fakeTimers();

    // Reopening a tab long after traffic started: paint the final text, do not
    // start a timer that would immediately clear itself.
    startCountdown(START, {
        document: doc, setInterval: timers.setInterval, clearInterval: timers.clearInterval,
        now: () => at(9999)
    });

    assert.equal(doc._els['onboard-countdown-text'].textContent, ALMOST_THERE_TEXT);
    assert.equal(timers.count(), 0, 'a timer was started that had nothing left to count');
});

// ─── the markup this drives ───

test('TestCountdownTargetsExistInMarkup', async () => {
    const { readFileSync } = await import('node:fs');
    const { fileURLToPath } = await import('node:url');
    const { dirname, join } = await import('node:path');
    const here = dirname(fileURLToPath(import.meta.url));
    const html = readFileSync(join(here, '..', 'index.html'), 'utf8');

    for (const id of ['onboard-waiting', 'onboard-countdown', 'onboard-countdown-text']) {
        const count = (html.match(new RegExp(`id="${id}"`, 'g')) || []).length;
        assert.equal(count, 1, `id="${id}" appears ${count} times, want exactly 1`);
    }
    // GRVX-907's target moved but must still be there, inside the waiting state.
    assert.match(html, /id="onboard-curl"/);
    // The countdown starts hidden: it must not flash before the check runs.
    const block = html.slice(html.indexOf('id="onboard-countdown"'));
    assert.match(block.slice(0, 120), /display:\s*none/,
        'the countdown is visible before any service has been discovered');
});
