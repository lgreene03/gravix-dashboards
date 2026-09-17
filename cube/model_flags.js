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
// by engine: DuckDB reads Parquet directly and already has TIMESTAMP columns,
// Trino reads a catalog and needs the cast.
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
function tableSql(table) {
  return isDuckDB
    ? `SELECT * FROM read_parquet('${warehouseGlob(table)}', union_by_name=true)`
    : `SELECT * FROM gravix.raw.${table}`;
}

// A column that is already a TIMESTAMP in Parquet but needs a cast out of Trino.
function timestampSql(column) {
  return isDuckDB ? column : `CAST(${column} AS TIMESTAMP)`;
}

// Only what the models actually use. An unused export here is an invitation to
// reintroduce a gate that cannot work.
module.exports = {
  tableSql,
  timestampSql,
};
