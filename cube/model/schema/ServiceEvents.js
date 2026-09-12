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
    ? `SELECT * FROM read_parquet('/cube/data/warehouse/*/service_events_detail/**/*.parquet', union_by_name=true)`
    : `SELECT * FROM read_parquet('/cube/data/warehouse/service_events_detail/**/*.parquet', union_by_name=true)`
  : `SELECT * FROM gravix.raw.service_events_detail`;

cube(`ServiceEvents`, {
  sql: serviceEventsSql,

  joins: {
    // No joins for MVP
  },

  measures: {
    count: {
      type: `count`,
      drillMembers: [service, eventType, eventTime],
      title: `Event Count`
    }
  },

  dimensions: {
    tenantId: {
      sql: `tenant_id`,
      type: `string`,
      title: `Tenant ID`
    },

    eventTime: {
      sql: isDuckDB ? `event_time` : `CAST(event_time AS TIMESTAMP)`,
      type: `time`,
      title: `Event Time`
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
    },

    entityId: {
      sql: `entity_id`,
      type: `string`,
      title: `Entity ID`
    },

    message: {
      sql: `message`,
      type: `string`,
      title: `Message`
    },

    properties: {
      sql: `properties`,
      type: `string`,
      title: `Properties`
    }
  },

  preAggregations: hasExternalStore ? {
    recentEvents: {
      measures: [count],
      dimensions: [service, eventType],
      timeDimension: eventTime,
      granularity: `hour`,
      refreshKey: {
        every: `5 minute`
      }
    }
  } : {},

  dataSource: `default`
});
