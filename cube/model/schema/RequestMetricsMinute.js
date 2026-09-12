// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

const isDuckDB = (typeof process !== 'undefined' && process.env && process.env.CUBEJS_DB_TYPE === 'duckdb');
const isMultiTenant = (typeof process !== 'undefined' && process.env && !!process.env.TENANT_DB_PATH);

// Pre-aggregations need somewhere to put their rollup tables, and that is Cube
// Store — an `externalDriverFactory`. The bootstrap stack deliberately runs no
// Cube Store (F-035: it would contradict the single-VPS premise), so a query that
// matches a rollup here fails with
//   "externalDriverFactory is not provided"
// rather than falling back to the source. Cube prefers the rollup and errors; it
// does not degrade gracefully.
//
// So the rollups are declared only where they can actually be built. The condition
// is DERIVED from the capability rather than read from a separate on/off flag,
// because a flag can be set to disagree with the infrastructure and this cannot:
// no external store means no pre-aggregations, and there is no third state.
const hasExternalStore = (typeof process !== 'undefined' && process.env &&
    !(process.env.CUBEJS_CACHE_AND_QUEUE_DRIVER === 'memory' && !process.env.CUBEJS_CUBESTORE_HOST));

const requestMetricsSql = isDuckDB
  ? isMultiTenant
    ? `SELECT * FROM read_parquet('/cube/data/warehouse/*/request_metrics_minute/**/*.parquet', union_by_name=true)`
    : `SELECT * FROM read_parquet('/cube/data/warehouse/request_metrics_minute/**/*.parquet', union_by_name=true)`
  : `SELECT * FROM gravix.raw.request_metrics_minute`;

cube(`RequestMetricsMinute`, {
  sql: requestMetricsSql,

  joins: {
    // No joins for MVP
  },

  measures: {
    // Cube's default row-count measure counts METRIC ROWS — one per
    // minute-bucket per service/method/path — not requests. Beside requestCount
    // that is easy to misread: a service handling one request a minute for an
    // hour and one handling a million both report 60. It is hidden rather than
    // removed, because deleting a measure that existing saved queries may name
    // is a breaking change, while hiding it only takes it off the menu.
    // See SD-006.
    count: {
      type: `count`,
      title: `Metric Rows (not requests)`,
      shown: false,
      drillMembers: [service, pathTemplate, bucketStart]
    },

    requestCount: {
      sql: `request_count`,
      type: `sum`,
      title: `Total Requests`
    },

    errorCount: {
      sql: `error_count`,
      type: `sum`,
      title: `Total Errors`
    },

    errorRate: {
      sql: `sum(error_count) / NULLIF(sum(request_count), 0)`,
      type: `number`,
      format: `percent`,
      title: `Error Rate`
    },

    // The percentile columns are EXACT within a single one-minute bucket and have
    // no correct aggregation across buckets: the maximum of sixty one-minute p95s
    // is not the hour's p95, and on a heavy-tailed latency distribution it was
    // measured 62% above the true value.
    //
    // Cube cannot merge t-digests, so these measures are valid ONLY at minute
    // granularity. For any wider window use GET /api/v1/percentile, which merges
    // the latency_sketch column in Go with the same code that wrote it.
    // See contracts/request_metrics_minute.v2.yaml.
    //
    // The aggregation type is `min` rather than `max`, deliberately. If someone
    // queries these across a wider window despite the warning, `min` produces an
    // obviously-too-low number that gets noticed and reported, while `max`
    // produces a plausible-looking number that gets believed. Both are wrong;
    // only one is visibly wrong. Do not "fix" this to max.
    bucketP50LatencyMs: {
      sql: `p50_latency_ms`,
      type: `min`,
      title: `P50 Latency (single bucket only)`,
      meta: { aggregatable: false, correctOnlyAtGranularity: `minute` }
    },

    bucketP95LatencyMs: {
      sql: `p95_latency_ms`,
      type: `min`,
      title: `P95 Latency (single bucket only)`,
      meta: { aggregatable: false, correctOnlyAtGranularity: `minute` }
    },

    bucketP99LatencyMs: {
      sql: `p99_latency_ms`,
      type: `min`,
      title: `P99 Latency (single bucket only)`,
      meta: { aggregatable: false, correctOnlyAtGranularity: `minute` }
    }
  },

  dimensions: {
    // The serialised t-digest, exposed so GET /api/v1/percentile can read it and
    // hidden from the dashboard because it is not a value anyone queries: it is
    // the input to a merge, and kilobytes of binary per row at that.
    latencySketch: {
      sql: `latency_sketch`,
      type: `string`,
      title: `Latency Sketch (internal)`,
      shown: false
    },

    tenantId: {
      sql: `tenant_id`,
      type: `string`,
      title: `Tenant ID`
    },

    bucketStart: {
      sql: isDuckDB ? `bucket_start` : `CAST(bucket_start AS TIMESTAMP)`,
      type: `time`,
      title: `Time`
    },

    eventDay: {
      sql: `event_day`,
      type: `string`,
      title: `Day`
    },

    service: {
      sql: `service`,
      type: `string`,
      title: `Service`
    },

    method: {
      sql: `method`,
      type: `string`,
      title: `Method`
    },

    pathTemplate: {
      sql: `path_template`,
      type: `string`,
      title: `Endpoint`
    }
  },

  // Pre-aggregations roll rows up to day and hour, so they may contain ONLY
  // measures that survive a roll-up. The bucket* percentiles do not: rolling
  // them to a day would materialise min-of-1440-p95s and store it as if it were
  // the day's p95 — the same defect this model exists to remove, but cached.
  // Percentiles at any granularity above a minute come from
  // GET /api/v1/percentile, which merges the sketches.
  preAggregations: hasExternalStore ? {
    endpointDaily: {
      measures: [requestCount, errorCount],
      dimensions: [service, method, pathTemplate],
      timeDimension: bucketStart,
      granularity: `day`,
      refreshKey: {
        every: `5 minute`
      }
    },

    metricsHourly: {
      measures: [requestCount, errorCount],
      dimensions: [service],
      timeDimension: bucketStart,
      granularity: `hour`,
      refreshKey: {
        every: `5 minute`
      }
    }
  } : {},

  dataSource: `default`
});
