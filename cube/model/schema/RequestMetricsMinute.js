cube(`RequestMetricsMinute`, {
  sql: `SELECT * FROM gravix.raw.request_metrics_minute`,

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
    }
  },

  dimensions: {
    bucketStart: {
      sql: `bucket_start`,
      type: `string`,
      title: `Time`
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

  dataSource: `default`
});
