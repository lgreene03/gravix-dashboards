const isDuckDB = (typeof process !== 'undefined' && process.env && process.env.CUBEJS_DB_TYPE === 'duckdb');
const isMultiTenant = (typeof process !== 'undefined' && process.env && !!process.env.TENANT_DB_PATH);

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
    count: {
      type: `count`,
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

    // Latency aggregation over pre-aggregated percentiles is tricky.
    // Ideally we re-aggregate T-Digests.
    // For MVP, we take the MAX of the p95s in the time bucket, or AVG.
    // MAX is safer for "did ANYONE see bad latency?"
    p95Latency: {
      sql: `p95_latency_ms`,
      type: `max`,
      title: `P95 Latency (Max)`
    },

    p50Latency: {
      sql: `p50_latency_ms`,
      type: `max`,
      title: `P50 Latency (Max)`
    },

    p99Latency: {
      sql: `p99_latency_ms`,
      type: `max`,
      title: `P99 Latency (Max)`
    }
  },

  dimensions: {
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

  preAggregations: {
    endpointDaily: {
      measures: [requestCount, errorCount, p50Latency, p95Latency, p99Latency],
      dimensions: [service, method, pathTemplate],
      timeDimension: bucketStart,
      granularity: `day`,
      refreshKey: {
        every: `5 minute`
      }
    },

    metricsHourly: {
      measures: [requestCount, errorCount, p50Latency, p95Latency, p99Latency],
      dimensions: [service],
      timeDimension: bucketStart,
      granularity: `hour`,
      refreshKey: {
        every: `5 minute`
      }
    }
  },

  dataSource: `default`
});
