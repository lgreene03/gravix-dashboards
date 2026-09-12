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

const serviceEventsSql = isDuckDB
  ? isMultiTenant
    ? `SELECT * FROM read_parquet('/cube/data/warehouse/*/service_events_daily/**/*.parquet', union_by_name=true)`
    : `SELECT * FROM read_parquet('/cube/data/warehouse/service_events_daily/**/*.parquet', union_by_name=true)`
  : `SELECT * FROM gravix.raw.service_events_daily`;

cube(`ServiceEventsDaily`, {
  sql: serviceEventsSql,

  joins: {
    // No joins for MVP
  },

  measures: {
    // Cube's built-in row count counts ROWS of the daily table — one per
    // service/event_type/day — not events. Beside eventCount that is easy to
    // misread: a service with one event a day for a week and one with a million
    // both report 7. Hidden rather than removed, because deleting a measure that
    // existing saved queries may name is a breaking change. Same trap, same fix
    // as RequestMetricsMinute.count. See SD-006.
    count: {
      type: `count`,
      title: `Daily Rows (not events)`,
      shown: false,
      drillMembers: [service, eventType, eventDay]
    },

    eventCount: {
      sql: `event_count`,
      type: `sum`,
      title: `Total Events`
    }
  },

  dimensions: {
    tenantId: {
      sql: `tenant_id`,
      type: `string`,
      title: `Tenant ID`
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

    eventType: {
      sql: `event_type`,
      type: `string`,
      title: `Event Type`
    }
  },

  preAggregations: hasExternalStore ? {
    dailySummary: {
      type: `rollup`,
      measures: [eventCount],
      dimensions: [service, eventType, eventDay],
      refreshKey: {
        every: `10 minute`
      }
    }
  } : {},

  dataSource: `default`
});
