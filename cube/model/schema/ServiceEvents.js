const isDuckDB = (typeof process !== 'undefined' && process.env && process.env.CUBEJS_DB_TYPE === 'duckdb');
const isMultiTenant = (typeof process !== 'undefined' && process.env && !!process.env.TENANT_DB_PATH);

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

  preAggregations: {
    recentEvents: {
      measures: [count],
      dimensions: [service, eventType],
      timeDimension: eventTime,
      granularity: `hour`,
      refreshKey: {
        every: `5 minute`
      }
    }
  },

  dataSource: `default`
});
