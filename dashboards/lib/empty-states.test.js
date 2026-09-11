// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// GRVX-907 §7, AC-1 through AC-8. Run with `make test-js`.
//
// Harness: node --test, the one GRVX-903 established for dashboards/lib —
// §7 says to extend it rather than introduce a second.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
    buildCurlCommand, renderEmptyStateCommand, FALLBACK_CURL_NOTE, uuidv7
} from './empty-states.js';
import { loadSLOPage } from './slo-cards.js';

const CONFIG = { ingestionApiUrl: 'http://localhost:8090', apiKey: 'grvx_test-key' };

// A document stub holding only what renderEmptyStateCommand touches.
function stubDoc(ids) {
    const els = {};
    for (const id of ids) els[id] = { textContent: '' };
    return { getElementById: id => els[id] || null, _els: els };
}

// payloadOf extracts and parses the -d '...' body, so assertions are about the
// JSON that would actually be sent rather than about substrings of a string.
function payloadOf(command) {
    const m = command.match(/-d '(\{.*\})'/s);
    assert.ok(m, `no -d payload in:\n${command}`);
    return JSON.parse(m[1]);
}

// ─── AC-1 ───

test('TestBuildCurlCommandFact', () => {
    const got = buildCurlCommand('fact', CONFIG);

    assert.ok(got.includes('-X POST http://localhost:8090/api/v1/facts'),
        `AC-1 FAILED: wrong target:\n${got}`);
    assert.ok(got.includes('X-API-Key: grvx_test-key'),
        `AC-1 FAILED: the live key is not in the command:\n${got}`);
    assert.ok(got.includes('"path_template":"/api/v1/health"'),
        `AC-1 FAILED: wrong path_template:\n${got}`);

    // The payload must be valid against the real rules, not merely present.
    const p = payloadOf(got);
    assert.equal(p.service, 'my-service');
    assert.equal(p.method, 'GET');
    assert.equal(p.status_code, 200);
    assert.equal(p.latency_ms, 42);
    assert.equal(p.user_agent_family, 'curl');
    assert.ok(p.status_code >= 100 && p.status_code <= 599, 'status_code out of range');
    assert.ok(p.latency_ms >= 0, 'negative latency_ms');
    // path_template rules: no raw id, no query string.
    assert.doesNotMatch(p.path_template, /\d{4,}/, 'path_template contains a raw numeric id');
    assert.doesNotMatch(p.path_template, /\?/, 'path_template contains a query string');
    assert.match(p.event_id, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    // Version 7, not 4. crypto.randomUUID() returns a v4 and every Gravix
    // endpoint rejects it with "event_id must be UUIDv7" — the spec named
    // randomUUID, and following it produced a command that 400s for every user
    // who pastes it. Found by POSTing the rendered command, not by reading it.
    // See SD-017.
    assert.equal(p.event_id[14], '7',
        `AC-1 FAILED: event_id is version ${p.event_id[14]}, not 7 — the command would 400`);
    // event_time must be now, not a fixed string: a stale timestamp is rejected
    // as too old, so a command copied from a screenshot would fail.
    const age = Date.now() - Date.parse(p.event_time);
    assert.ok(age >= 0 && age < 60_000, `event_time is ${age}ms old; it must be generated at render`);
});

test('TestBuildCurlCommandIsFreshEachRender', () => {
    const a = payloadOf(buildCurlCommand('fact', CONFIG));
    const b = payloadOf(buildCurlCommand('fact', CONFIG));
    assert.notEqual(a.event_id, b.event_id,
        'two renders produced the same event_id; the second POST would be a duplicate');
});

// ─── AC-2 ───

test('TestBuildCurlCommandEvent', () => {
    const got = buildCurlCommand('event', CONFIG);

    assert.ok(got.includes('-X POST http://localhost:8090/api/v1/events'),
        `AC-2 FAILED: wrong target:\n${got}`);
    assert.ok(got.includes('"event_type":"deploy_completed"'),
        `AC-2 FAILED: wrong event_type:\n${got}`);

    const p = payloadOf(got);
    for (const field of ['event_id', 'event_time', 'service', 'event_type', 'message']) {
        assert.ok(p[field], `AC-2 FAILED: ServiceEvent is missing required field ${field}`);
    }
    // A fact's fields must not leak into an event payload.
    assert.equal(p.status_code, undefined);
    assert.equal(p.path_template, undefined);
});

// ─── AC-3 ───

test('TestBuildCurlCommandFallbackWhenNoAPIKey', () => {
    const got = buildCurlCommand('fact', { ingestionApiUrl: 'http://localhost:8090', apiKey: '' });

    assert.ok(got.startsWith(FALLBACK_CURL_NOTE),
        `AC-3 FAILED: the note is missing or reworded:\n${got}`);
    // Still a runnable command after the note, with an obvious placeholder —
    // an empty -H "X-API-Key: " would look correct and fail with a 401.
    assert.ok(got.includes('X-API-Key: $API_KEY'),
        `AC-3 FAILED: no placeholder for the key:\n${got}`);
    assert.ok(got.includes('-X POST http://localhost:8090/api/v1/facts'),
        `AC-3 FAILED: the command itself is gone:\n${got}`);

    // With no config at all it still names a host rather than rendering
    // "undefined/api/v1/facts".
    const bare = buildCurlCommand('fact', {});
    assert.ok(bare.includes('http://localhost:8090/api/v1/facts'), `no default host:\n${bare}`);
    assert.doesNotMatch(bare, /undefined|null/, `unset config leaked into the command:\n${bare}`);
});

test('TestBuildCurlCommandTrimsTrailingSlash', () => {
    const got = buildCurlCommand('fact', { ingestionApiUrl: 'http://host:8090/', apiKey: 'k' });
    assert.ok(got.includes('http://host:8090/api/v1/facts'),
        `a trailing slash produced a double slash:\n${got}`);
});

// ─── AC-4, AC-5, AC-6 ───

test('TestRenderEmptyStateCommandOnboard', () => {
    const doc = stubDoc(['onboard-curl']);
    renderEmptyStateCommand('onboard', 'fact', CONFIG, doc);

    const text = doc._els['onboard-curl'].textContent;
    assert.ok(text.length > 0, 'AC-4 FAILED: nothing was rendered');
    assert.ok(text.includes('/api/v1/facts'), `AC-4 FAILED:\n${text}`);
});

test('TestRenderEmptyStateCommandEvents', () => {
    const doc = stubDoc(['events-empty-curl']);
    renderEmptyStateCommand('events-empty', 'event', CONFIG, doc);

    const text = doc._els['events-empty-curl'].textContent;
    assert.ok(text.length > 0, 'AC-5 FAILED: nothing was rendered');
    assert.ok(text.includes('/api/v1/events'), `AC-5 FAILED:\n${text}`);
});

test('TestRenderEmptyStateCommandSLO', () => {
    const doc = stubDoc(['slo-empty-curl']);
    renderEmptyStateCommand('slo-empty', 'fact', CONFIG, doc);

    const text = doc._els['slo-empty-curl'].textContent;
    assert.ok(text.length > 0, 'AC-6 FAILED: nothing was rendered');
    assert.ok(text.includes('/api/v1/facts'), `AC-6 FAILED:\n${text}`);
});

// ─── AC-7 ───

// TestShowEmptyPopulatesOnboardCurl is the end-to-end half: the command has to
// be filled in by the code path that shows the empty state, not only by calling
// the renderer directly. An empty state wired to nothing renders a blank <pre>,
// which is exactly the bug this spec fixes and would pass AC-4 regardless.
//
// app.js is a classic script with no module boundary, so the SLO page's empty
// path is used as the end-to-end proxy: it is the one call site whose whole
// chain lives in dashboards/lib.
test('TestShowEmptyPopulatesOnboardCurl', async () => {
    const doc = stubDoc(['onboard-curl']);
    const grid = { innerHTML: '', style: { display: '' } };
    const classes = new Set();
    const empty = {
        innerHTML: '', style: { display: '' },
        classList: { toggle: (n, on) => (on ? classes.add(n) : classes.delete(n)) }
    };

    await loadSLOPage({
        fetchServices: async () => ({ ok: true, json: async () => ({ services: [] }) }),
        fetchMetrics: async () => ({}),
        grid, empty,
        // Exactly what app.js passes.
        onEmpty: () => renderEmptyStateCommand('onboard', 'fact', CONFIG, doc)
    });

    assert.ok(classes.has('visible'), 'the empty state was not shown');
    const text = doc._els['onboard-curl'].textContent;
    assert.ok(text.includes('/api/v1/facts'),
        `AC-7 FAILED: showing the empty state left the command blank:\n${text}`);
});

// ─── AC-8 ───

test('TestRenderEmptyStateCommandMissingElementIsNoop', () => {
    const doc = stubDoc([]);
    assert.doesNotThrow(() => renderEmptyStateCommand('onboard', 'fact', CONFIG, doc),
        'AC-8 FAILED: a page without this empty state threw');
    assert.equal(renderEmptyStateCommand('nope', 'fact', CONFIG, doc), null);

    // And with no document at all, as in a non-browser context.
    assert.doesNotThrow(() => renderEmptyStateCommand('onboard', 'fact', CONFIG, null));
});

// ─── the markup this renders into ───

test('TestEmptyStateTargetsExistInMarkup', async () => {
    const { readFileSync } = await import('node:fs');
    const { fileURLToPath } = await import('node:url');
    const { dirname, join } = await import('node:path');
    const here = dirname(fileURLToPath(import.meta.url));
    const html = readFileSync(join(here, '..', 'index.html'), 'utf8');

    for (const id of ['onboard-curl', 'events-empty-curl', 'slo-empty-curl']) {
        const count = (html.match(new RegExp(`id="${id}"`, 'g')) || []).length;
        assert.equal(count, 1, `id="${id}" appears ${count} times in index.html, want exactly 1`);
    }

    // The old, unrunnable command must be gone: it told the reader to use a
    // shell variable nothing sets and a uuidgen they may not have.
    assert.doesNotMatch(html, /\$API_KEY/,
        'index.html still contains the hard-coded $API_KEY command');
    assert.doesNotMatch(html, /uuidgen/,
        'index.html still tells the reader to run uuidgen');
});

// ─── SD-017 regression ───

// TestEventIDsAreUUIDv7 pins the version across every path that can produce an
// id, because a v4 anywhere renders a command that fails with 400 and tells the
// reader Gravix is broken.
test('TestEventIDsAreUUIDv7', () => {
    const uuidRe = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

    for (let i = 0; i < 200; i++) {
        const u = uuidv7();
        assert.match(u, uuidRe, `generated ${u}, which is not a well-formed UUIDv7`);
    }

    // Both payload kinds, through the real command builder.
    for (const kind of ['fact', 'event']) {
        const id = payloadOf(buildCurlCommand(kind, CONFIG)).event_id;
        assert.match(id, uuidRe, `${kind} command carries ${id}, not a UUIDv7`);
    }

    // The timestamp prefix is the current time, which is what makes a v7 sort
    // by creation. A fixed prefix would still match the shape above.
    const a = uuidv7({ nowMs: () => 0x017f00000000 });
    const b = uuidv7({ nowMs: () => 0x017f00000001 });
    assert.ok(a < b, `v7 ids are not time-ordered: ${a} !< ${b}`);
    assert.ok(a.startsWith('017f0000'), `timestamp is not in the leading bytes: ${a}`);

    // No crypto available: still a v7, never a v4 placeholder.
    const fallback = uuidv7({ crypto: { getRandomValues: () => { throw new Error('blocked'); } } });
    assert.match(fallback, uuidRe, `the fallback placeholder is not a v7: ${fallback}`);
});
