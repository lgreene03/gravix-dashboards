const isDuckDB = process.env.CUBEJS_DB_TYPE === 'duckdb';
const isMultiTenant = !!process.env.TENANT_DB_PATH;

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
    count: {
      type: `count`,
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

  preAggregations: {
    dailySummary: {
      type: `rollup`,
      measures: [eventCount],
      dimensions: [service, eventType, eventDay],
      refreshKey: {
        every: `10 minute`
      }
    }
  },

  dataSource: `default`
});
