// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// F-075. Run with `make test-js`.
//
// The dashboard keeps a date range as a "gte" and an "lte" filter on a time
// member, and sent them to Cube as they were. Trino refuses to compare the
// timestamp column with the text Cube binds, so every range-filtered query
// failed there. toCubeFilters sends each pair as one inDateRange instead.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import vm from 'node:vm';

// cube-client.js is a classic script that defines a global, as index.html
// loads it, so it is evaluated as one here.
const here = dirname(fileURLToPath(import.meta.url));
const context = { localStorage: { getItem: () => null, setItem: () => {} } };
vm.runInNewContext(readFileSync(join(here, 'cube-client.js'), 'utf8'), context);
const { toCubeFilters } = context.CubeClient;
const plain = (v) => JSON.parse(JSON.stringify(v));

const service = { member: 'RequestMetricsMinute.service', operator: 'equals', values: ['checkout'] };
const range = (member, from, to) => [
    { member, operator: 'gte', values: [from] },
    { member, operator: 'lte', values: [to] },
];

test('a date pair becomes one inDateRange, where the pair was', () => {
    const got = plain(toCubeFilters([service,
        ...range('RequestMetricsMinute.bucketStart', '2026-09-24T00:00:00', '2026-10-01T23:59:59')]));
    assert.deepEqual(got, [service, {
        member: 'RequestMetricsMinute.bucketStart', operator: 'inDateRange',
        values: ['2026-09-24T00:00:00', '2026-10-01T23:59:59'],
    }]);
});

test('the events cube\'s range is rewritten the same way', () => {
    const got = plain(toCubeFilters(range('ServiceEvents.eventTime', '2026-10-01T00:00:00', '2026-10-01T23:59:59')));
    assert.deepEqual(got, [{
        member: 'ServiceEvents.eventTime', operator: 'inDateRange',
        values: ['2026-10-01T00:00:00', '2026-10-01T23:59:59'],
    }]);
});

test('the bounds pair up by operator, whichever comes first', () => {
    const [gte, lte] = range('RequestMetricsMinute.bucketStart', '2026-09-24T00:00:00', '2026-10-01T23:59:59');
    const got = plain(toCubeFilters([lte, gte]));
    assert.deepEqual(got[0].values, ['2026-09-24T00:00:00', '2026-10-01T23:59:59']);
    assert.equal(got.length, 1);
});

test('a lone bound, other members and other filters pass through unchanged', () => {
    const lone = { member: 'RequestMetricsMinute.bucketStart', operator: 'gte', values: ['2026-09-24T00:00:00'] };
    const count = { member: 'RequestMetricsMinute.requestCount', operator: 'gte', values: ['10'] };
    const deploy = { member: 'ServiceEvents.eventType', operator: 'contains', values: ['deploy'] };
    assert.deepEqual(plain(toCubeFilters([lone, count, deploy])), [lone, count, deploy]);
    assert.deepEqual(plain(toCubeFilters([])), []);
    assert.deepEqual(plain(toCubeFilters(undefined)), []);
});

test('the dashboard\'s own filters are not changed', () => {
    const filters = [service, ...range('RequestMetricsMinute.bucketStart', '2026-09-24T00:00:00', '2026-10-01T23:59:59')];
    const before = JSON.stringify(filters);
    toCubeFilters(filters);
    assert.equal(JSON.stringify(filters), before);
});
