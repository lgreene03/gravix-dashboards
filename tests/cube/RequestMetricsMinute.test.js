// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// GRVX-808 §4.1. Run with:
//   node --test tests/cube/RequestMetricsMinute.test.js
//
// This file lives OUTSIDE cube/model/ deliberately. docker-compose mounts that
// whole directory as Cube's schema directory, and Cube's DataSchemaCompiler
// compiles every .js in it through vm.runInNewContext — which is not an ES
// module context, so the `import.meta` on line 19 below is a hard SyntaxError
// there. One unparseable file fails the entire compile, no cube gets defined,
// and every query returns an error. That is F-030: this file, sitting beside the
// model it tests, stopped Cube serving any data at all from Phase 8 until it was
// moved. A test file does not belong in a directory that is mounted into a
// running service as that service's configuration.
//
// The Go tests in pkg/gatewaycore/percentile_handler_test.go assert the model's
// text. These load it, so they catch what text cannot: a measure referenced from
// a pre-aggregation after it was deleted, a `meta` block that is a comment rather
// than a property, a model that throws on one of the four configurations.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, resolve } from 'node:path';
import vm from 'node:vm';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, '..', '..');
const modelPath = join(repoRoot, 'cube', 'model', 'schema', 'RequestMetricsMinute.js');
const clientPath = join(repoRoot, 'dashboards', 'lib', 'cube-client.js');
const appPath = join(repoRoot, 'dashboards', 'app.js');

// loadModel evaluates the model with a stubbed `cube()` and the given env, and
// returns the definition it registered.
//
// Cube's DSL refers to members by bare identifier — `measures: [requestCount]`,
// `drillMembers: [service, ...]` — which are resolved by Cube at load time, not
// by JavaScript. Evaluating the file therefore needs those names to exist. The
// Proxy supplies each one as its own name, so a reference to a member that has
// been deleted still evaluates, and assertMembersExist below is what catches it.
// The sandbox mirrors Cube's, and the two differences are deliberate.
//
// It provides `require`, resolved against the model directory the way Cube's does,
// because that is how the models reach ../model_flags.js.
//
// And it does NOT provide `process`. Cube's sandbox does not: it compiles model
// files with vm.runInNewContext and a fresh V8 context has no `process`, which is a
// Node global rather than a V8 intrinsic. An earlier version of this helper injected
// one, and that is why F-037 survived so long — every environment conditional in the
// models was dead in production while this loader reported it working. A test
// sandbox that is more generous than the real one proves nothing about the real one.
function loadModel(env = {}) {
  const src = readFileSync(modelPath, 'utf8');
  const modelRoot = dirname(dirname(modelPath));
  let captured = null;

  const names = new Proxy({}, {
    has: () => true,
    get: (_t, prop) => {
      if (prop === 'cube') return (name, def) => { captured = { name, def }; };
      if (prop === 'require') return (p) => loadFlags(modelRoot, p, env);
      if (prop === Symbol.unscopables) return undefined;
      return String(prop);
    }
  });

  vm.runInNewContext(`with (names) { ${src} }`, { names });
  assert.ok(captured, 'the model did not call cube()');
  return captured;
}

// loadFlags evaluates cube/model_flags.js in an ordinary Node context — with the
// case's environment — which is exactly where Cube's require puts it.
function loadFlags(modelRoot, spec, env) {
  const file = resolve(modelRoot, spec);
  const module = { exports: {} };
  vm.runInNewContext(readFileSync(file, 'utf8'), {
    module, exports: module.exports, process: { env },
  }, { filename: file });
  return module.exports;
}

// loadCubeClient evaluates the browser IIFE and returns the CubeClient global.
function loadCubeClient() {
  const src = readFileSync(clientPath, 'utf8');
  return vm.runInNewContext(`${src}; CubeClient;`, { localStorage: undefined });
}

const CONFIGS = [
  ['duckdb single-tenant', { CUBEJS_DB_TYPE: 'duckdb' }],
  ['duckdb multi-tenant', { CUBEJS_DB_TYPE: 'duckdb', TENANT_DB_PATH: '/data/tenants.db' }],
  ['trino single-tenant', { CUBEJS_DB_TYPE: 'trino' }],
  ['trino multi-tenant', { CUBEJS_DB_TYPE: 'trino', TENANT_DB_PATH: '/data/tenants.db' }]
];

// ─── AC-9: all four configurations load ───

test('the model loads in all four configurations', () => {
  for (const [label, env] of CONFIGS) {
    const { name, def } = loadModel(env);
    assert.equal(name, 'RequestMetricsMinute', label);
    assert.ok(def.sql.includes('SELECT * FROM'), `${label}: no SQL source`);
  }
});

test('each configuration selects every column, so latency_sketch is readable', () => {
  for (const [label, env] of CONFIGS) {
    const { def } = loadModel(env);
    assert.ok(def.sql.startsWith('SELECT * FROM'), `${label}: ${def.sql}`);
  }
});

// ─── AC-1, AC-2: no max over a percentile ───

test('no measure aggregates a percentile column with max', () => {
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });
  for (const [name, measure] of Object.entries(def.measures)) {
    if (!measure.sql || !/p\d\d_latency_ms/.test(measure.sql)) continue;
    assert.notEqual(measure.type, 'max', `${name} aggregates a percentile with max`);
    assert.equal(measure.meta?.aggregatable, false, `${name} is not marked non-aggregatable`);
    assert.equal(measure.meta?.correctOnlyAtGranularity, 'minute',
      `${name} does not record the granularity it is correct at`);
  }
});

test('the removed measures are gone, not merely renamed', () => {
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });
  for (const gone of ['p50Latency', 'p95Latency', 'p99Latency']) {
    assert.equal(def.measures[gone], undefined, `${gone} is still defined`);
  }
  for (const kept of ['bucketP50LatencyMs', 'bucketP95LatencyMs', 'bucketP99LatencyMs']) {
    assert.ok(def.measures[kept], `${kept} is missing`);
  }
});

// ─── AC-5: the correct measures are untouched ───

test('the measures that were already correct are unchanged', () => {
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });
  assert.equal(def.measures.requestCount.type, 'sum');
  assert.equal(def.measures.errorCount.type, 'sum');
  assert.equal(def.measures.errorRate.sql, 'sum(error_count) / NULLIF(sum(request_count), 0)');
});

// ─── AC-10: the sketch is present but hidden ───

test('latencySketch is exposed and hidden', () => {
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });
  const d = def.dimensions.latencySketch;
  assert.ok(d, 'latencySketch is not exposed');
  assert.equal(d.sql, 'latency_sketch');
  assert.equal(d.shown, false);
});

// ─── the defect the text assertions could not see ───

test('every pre-aggregation names only measures that exist', () => {
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });
  for (const [aggName, agg] of Object.entries(def.preAggregations)) {
    for (const measure of agg.measures || []) {
      assert.ok(def.measures[measure],
        `pre-aggregation ${aggName} references measure ${measure}, which does not exist`);
    }
    for (const dim of agg.dimensions || []) {
      assert.ok(def.dimensions[dim],
        `pre-aggregation ${aggName} references dimension ${dim}, which does not exist`);
    }
  }
});

test('no pre-aggregation materialises a non-aggregatable measure', () => {
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });
  for (const [aggName, agg] of Object.entries(def.preAggregations)) {
    if (agg.granularity === 'minute') continue;
    for (const measure of agg.measures || []) {
      assert.notEqual(def.measures[measure].meta?.aggregatable, false,
        `pre-aggregation ${aggName} rolls ${measure} up to ${agg.granularity}, ` +
        `caching a number the model itself says cannot be aggregated`);
    }
  }
});

test('drillMembers name members that exist', () => {
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });
  for (const [name, measure] of Object.entries(def.measures)) {
    for (const member of measure.drillMembers || []) {
      assert.ok(def.dimensions[member] || def.measures[member],
        `measure ${name} drills into ${member}, which does not exist`);
    }
  }
});

// ─── AC-11: the dashboard never asks Cube for a coarse percentile ───

test('TestDashboardRoutesCoarsePercentiles', () => {
  const client = loadCubeClient();
  const p95 = 'RequestMetricsMinute.bucketP95LatencyMs';

  // A percentile at minute granularity is exact in the row, so Cube may answer.
  assert.equal(client.routeFor([p95], 'minute'), 'cube');

  // Anything wider has no correct answer in Cube, at any aggregation type.
  for (const granularity of ['hour', 'day', 'week', 'month', undefined, null, '']) {
    assert.equal(client.routeFor([p95], granularity), 'gateway',
      `granularity ${String(granularity)} was routed to Cube`);
  }

  // Non-percentile measures are unaffected, whatever the granularity.
  for (const granularity of ['minute', 'hour', 'day']) {
    assert.equal(client.routeFor(['RequestMetricsMinute.requestCount'], granularity), 'cube');
  }

  // A mixed query contains a percentile, so it routes to the gateway: splitting
  // it and letting Cube answer the percentile half is the defect returning.
  assert.equal(client.routeFor([p95, 'RequestMetricsMinute.requestCount'], 'day'), 'gateway');

  // And an empty or absent measure list must not throw.
  assert.equal(client.routeFor([], 'day'), 'cube');
  assert.equal(client.routeFor(undefined, 'day'), 'cube');
});

test('every percentile measure the client knows about exists in the model', () => {
  const client = loadCubeClient();
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });

  for (const measure of client.PERCENTILE_MEASURES) {
    const short = measure.split('.')[1];
    assert.ok(def.measures[short], `the client routes ${measure}, which the model does not define`);
  }

  // And the reverse: a non-aggregatable measure the client does not know about
  // would be queried from Cube at any granularity, silently.
  for (const [name, measure] of Object.entries(def.measures)) {
    if (measure.meta?.aggregatable !== false) continue;
    assert.ok(client.PERCENTILE_MEASURES.includes(`RequestMetricsMinute.${name}`),
      `${name} is non-aggregatable but the dashboard client does not route it`);
  }
});

test('the gateway request carries the quantile the measure names', () => {
  const client = loadCubeClient();
  const req = client.percentileRequest('RequestMetricsMinute.bucketP95LatencyMs', {
    from: '2026-09-09T00:00:00Z',
    to: '2026-09-09T01:00:00Z',
    granularity: 'hour',
    filters: { service: 'api', user_id: 'nope' }
  });

  assert.equal(req.path, '/api/v1/percentile');
  assert.equal(req.params.quantile, 0.95);
  assert.equal(req.params.metric, 'request_metrics_minute');
  assert.equal(req.params.granularity, 'hour');
  assert.equal(req.params.service, 'api');

  // High-cardinality dimensions are a non-goal; an unknown filter is dropped
  // rather than forwarded.
  assert.equal(req.params.user_id, undefined);
});

// app.js is a browser script with top-level DOM access, so it cannot be loaded
// here. What can be checked is that no Cube query it builds names a percentile:
// routing helpers are worthless if a query bypasses them.
test('no Cube query in the dashboard asks for a percentile measure', () => {
  const app = readFileSync(appPath, 'utf8');
  const client = loadCubeClient();
  const short = client.PERCENTILE_MEASURES.map(m => m.split('.')[1]);

  // Every `measures: [ ... ]` literal in the file, whether it goes to
  // fetchCubeData or straight to fetch(CUBE_API_URL).
  const literals = app.match(/measures:\s*\[[^\]]*\]/g) || [];
  assert.ok(literals.length > 0, 'no measures literal found; the regex needs updating');

  for (const literal of literals) {
    for (const measure of short) {
      if (!literal.includes(measure)) continue;
      // A percentile may appear only in a single-measure literal, which
      // fetchCubeData can route wholesale to the gateway.
      const count = (literal.match(/RequestMetricsMinute\./g) || []).length;
      assert.equal(count, 1,
        `a Cube query mixes ${measure} with other measures, so it cannot be routed:\n${literal}`);
    }
  }
});

test('the dashboard renders percentiles through the routing helpers', () => {
  const app = readFileSync(appPath, 'utf8');
  assert.match(app, /CubeClient\.routeFor\(/, 'fetchCubeData does not consult routeFor');
  assert.match(app, /CubeClient\.percentileRequest\(/, 'nothing builds a gateway percentile request');
  assert.match(app, /fetchPercentileSeries\(/, 'no gateway percentile fetch exists');
});

// ─── GRVX-1006 AC-3 ───

// TestNoPercentileInPreAggregations closes a gap the existing guards leave.
//
// 'no measure aggregates a percentile column with max' finds percentile columns
// with /p\d\d_latency_ms/ — exactly two digits, and only that spelling. GRVX-806's
// evolve feature writes `extra_quantile_ms` for a retroactively added percentile,
// which that pattern does not match. Measured: a `max` over `extra_quantile_ms`,
// rolled up to a day inside a pre-aggregation and carrying no meta annotation,
// passed all fourteen tests. That is the defect Phase 8 removed — the maximum of
// 1,440 one-minute p95s stored as if it were the day's p95 — reintroduced through
// a column the regex did not know about, and cached on top. See F-024.
//
// So this checks the invariant rather than the spelling. A pre-aggregation rolls
// rows up to a coarser grain, so every measure in one must survive that roll-up.
// `sum` and the count family do. `min`, `max` and `avg` over a per-bucket scalar
// do not, whatever the column is called: there is no function of per-bucket
// percentiles that yields the window's percentile, which is why GRVX-804 stores a
// mergeable sketch and GRVX-808 serves percentiles from it instead.
test('TestNoPercentileInPreAggregations', () => {
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });

  // Aggregation types that genuinely survive a roll-up.
  const rollupSafe = new Set(['sum', 'count', 'countDistinct', 'countDistinctApprox']);

  let checked = 0;
  for (const [aggName, agg] of Object.entries(def.preAggregations)) {
    if (agg.granularity === 'minute') continue;
    for (const measureName of agg.measures || []) {
      const measure = def.measures[measureName];
      assert.ok(measure, `pre-aggregation ${aggName} names ${measureName}, which does not exist`);
      checked++;

      assert.ok(rollupSafe.has(measure.type),
        `preaggregation "${aggName}" includes a percentile measure; percentiles are served by ` +
        `sketch merge — ${measureName} aggregates with "${measure.type}", which does not survive ` +
        `a roll-up to ${agg.granularity}. Only ${[...rollupSafe].join('/')} do.`);
    }
  }

  // The model currently declares no pre-aggregations, so there is nothing to check
  // and `checked` is 0. That is not this guard going stale — it is the stronger
  // form of the same property, and it is asserted rather than assumed: a rollup
  // needs a Cube Store to hold it, no stack in this repository runs one, and Cube
  // fails a query that matches a rollup it cannot build rather than reading the
  // source. See F-035 and F-038.
  //
  // If pre-aggregations ever come back, `checked` becomes non-zero and the loop
  // above resumes doing the work. Either way the invariant holds; what must never
  // happen is a pre-aggregation existing AND this guard reporting nothing.
  const declared = Object.keys(def.preAggregations || {}).length;
  assert.ok(declared === 0 || checked > 0,
    `the model declares ${declared} pre-aggregation(s) but this guard checked no measures; ` +
    'it is no longer guarding anything');
});

// A second angle on the same rule: catch a percentile by what it reads, with a
// pattern wide enough to include the columns the evolve feature adds. Two
// independent checks, because the type check would miss a percentile someone
// declared `sum` and the column check would miss one in a column nobody named
// suggestively.
test('TestNoPercentileColumnIsRolledUp', () => {
  const { def } = loadModel({ CUBEJS_DB_TYPE: 'duckdb' });

  // Every column the warehouse holds that is a per-bucket scalar percentile:
  // the three fixed ones, and the retroactive pair pkg/evolve writes.
  const percentileColumn = /(p\d+_latency_ms|quantile|percentile)/i;

  for (const [aggName, agg] of Object.entries(def.preAggregations)) {
    if (agg.granularity === 'minute') continue;
    for (const measureName of agg.measures || []) {
      const sql = def.measures[measureName]?.sql || '';
      assert.ok(!percentileColumn.test(sql),
        `preaggregation "${aggName}" includes a percentile measure; percentiles are served by ` +
        `sketch merge — ${measureName} reads ${sql}`);
    }
  }
});
