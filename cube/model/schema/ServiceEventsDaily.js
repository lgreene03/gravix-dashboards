cube(`ServiceEventsDaily`, {
  sql: `SELECT * FROM gravix.raw.service_events_daily`,

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
    eventDay: {
      sql: `event_day`,
      type: `string`,
      title: `Day`,
      format: `date`
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

  dataSource: `default`
});
