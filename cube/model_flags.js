// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Stack capabilities, derived from the environment, for the data models to read.
//
// This file exists because of WHERE it runs, not because of what it computes.
//
// Cube v0.35 evaluates every file under the model directory with
// `vm.runInNewContext(content, sandbox)` (DataSchemaCompiler.compileJsFile). The
// sandbox is an explicit, closed object — `cube`, `view`, `context`, `addExport`,
// `setExport`, `asyncModule`, `require`, `COMPILE_CONTEXT` — and a fresh V8
// context has no `process`, because `process` is a Node global rather than a V8
// intrinsic. A model file that reads `process.env` therefore does not read the
// environment: guarded by `typeof process !== 'undefined'` it silently takes the
// false branch, and unguarded it throws a ReferenceError the compiler swallows
// into the errors report.
//
// That is F-037, and it was not a missing optimisation. Every environment-derived
// flag in the models was dead, so BOTH stacks compiled to the Trino SQL: the
// bootstrap stack, which runs DuckDB over Parquet and no Trino at all, was asking
// for `gravix.raw.request_metrics_minute` from a catalog that does not exist.
//
// The sandbox's `require` is the way out. For a path that does not resolve to
// another model file, Cube falls through to Node's own `require` (allowNodeRequire
// defaults to true in server-core), which loads the module in the ordinary Node
// context where `process` is live. The path is resolved against
// `repository.localPath()` — the model root, `/cube/conf/model` — so `..` puts
// this file beside cube.js in /cube/conf, deliberately OUTSIDE the directory Cube
// scans. A file inside the model directory would be found by resolveModuleFile
// and compiled in the sandbox again, reintroducing the same defect.
//
// If the mount is missing the require throws and the cube fails to register. That
// is intended: a loud failure beats today's silent wrong-SQL.
//
// cmd/onboarding_gate/cube_model_env_test.go asserts that the models get their
// flags from here and not from `process`, by compiling each one in a sandbox that
// has no `process` — the same way Cube does.

// Percentiles are exact only within a bucket, and the SQL that reads them differs
// by engine: DuckDB reads Parquet directly, Trino reads a catalog.
const isDuckDB = process.env.CUBEJS_DB_TYPE === 'duckdb';

// Tenant-partitioned warehouse layout: data/warehouse/<tenant>/<table>/... rather
// than data/warehouse/<table>/.... Set whenever the stack seeds a tenant database.
const isMultiTenant = !!process.env.TENANT_DB_PATH;

// NOTE: there is deliberately no `hasExternalStore` flag here. Pre-aggregations
// are not gated in the models — they are absent, because no stack in this
// repository runs a Cube Store to hold them. See the comment beside
// `preAggregations` in each model, and F-035 / F-038.

// The warehouse glob for one table, under whichever layout this stack writes.
// Kept here so the tenant-prefix rule has one definition rather than three.
function warehouseGlob(table) {
  return isMultiTenant
    ? `/cube/data/warehouse/*/${table}/**/*.parquet`
    : `/cube/data/warehouse/${table}/**/*.parquet`;
}

// The source SQL for one warehouse table on this stack's engine.
//
// hive_partitioning is explicit rather than left to DuckDB's auto-detection,
// because dayRangeSql below depends on event_day being the DATE taken from the
// directory name: that is what lets DuckDB skip a partition without opening it.
function tableSql(table) {
  return isDuckDB
    ? `SELECT * FROM read_parquet('${warehouseGlob(table)}', union_by_name=true, hive_partitioning=true)`
    : `SELECT * FROM gravix.raw.${table}`;
}

// A predicate restricting a cube's source to the days a query's date range
// touches, for use inside FILTER_PARAMS. `from` and `to` are the UTC timestamps
// Cube binds for the range, so their first ten characters are the UTC days that
// event_day partitions by, in every query time zone.
//
// DuckDB prunes on it: a one-day query over a week of standard-scale data fell
// from ~700 ms to ~130 ms at the bootstrap stack's half CPU (SD-024). Trino gets
// a no-op, because its table declares event_day as an ordinary column rather
// than a partition, and a predicate that buys nothing there was not changed
// without a Trino stack to measure it on.
//
// Cube calls this with both bounds only for a date range. For a single-bound
// filter on the time column — the dashboard's endpoints table sends its range as
// a "gte" and an "lte" filter — it calls it once per filter with ONE value, and
// does not say which bound that value is. No predicate is safe then, so nothing
// is pruned; the query's own filter still applies to the rows. Writing the
// missing bound into the SQL put a column named "undefined" in front of DuckDB,
// and every such query failed (F-059).
function dayRangeSql(from, to) {
  if (!isDuckDB || to === undefined) {
    return '1 = 1';
  }
  return `event_day BETWEEN CAST(substr(${from}, 1, 10) AS DATE) AND CAST(substr(${to}, 1, 10) AS DATE)`;
}

// How Cube decides a cached result is stale. On DuckDB the key is read from the
// Parquet footers — file count and compressed size — so it changes exactly when
// a rollup writes, costs no scan, and serves every repeat in between from
// Cube's in-memory result cache. That cache is what the warm figure measures
// now that no stack holds pre-aggregations (SD-024). A time-based key would
// have been simpler and wrong: `every: 5 minute` holds an empty first answer
// for up to five minutes after data lands, which the onboarding gate's budget
// cannot absorb. Measured: new data visible 12 s after the file appeared.
//
// Trino keeps Cube's default, for the same reason as dayRangeSql.
function refreshKeyFor(table) {
  return isDuckDB
    // A function, not a string: Cube evaluates refreshKey.sql by calling it, and
    // only wraps a literal written inside a model file itself.
    ? { sql: () => `SELECT count(*), sum(total_compressed_size) FROM parquet_metadata('${warehouseGlob(table)}')` }
    : undefined;
}

// A time column, cast for both engines. bucket_start is stored as text
// ("YYYY-MM-DD HH:MM:SS", UTC) in Parquet as well as in Trino. This once returned
// the bare column for DuckDB on the belief that Parquet already held a
// TIMESTAMP; it does not, and DuckDB then refused every query with a date range
// ("Cannot compare values of type VARCHAR and type TIMESTAMP WITH TIME ZONE"),
// which the dashboard rendered as an empty chart (F-053).
function timestampSql(column) {
  return `CAST(${column} AS TIMESTAMP)`;
}

// Only what the models actually use. An unused export here is an invitation to
// reintroduce a gate that cannot work.
module.exports = {
  tableSql,
  timestampSql,
  dayRangeSql,
  refreshKeyFor,
};
