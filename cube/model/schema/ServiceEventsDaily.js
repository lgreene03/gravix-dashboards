// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Stack capabilities come from ../model_flags.js, which Cube loads through Node's
// own require rather than evaluating in its model sandbox. Reading process.env
// here instead would silently do nothing: the sandbox has no `process`. See F-037
// and the comment at the top of that file.
const { tableSql, timestampSql } = require('../model_flags.js');

const serviceEventsSql = tableSql('service_events_daily');

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

  // No pre-aggregations. This is a property of the infrastructure, not a
  // preference.
  //
  // A rollup has to be materialised somewhere, and for Cube that somewhere is
  // Cube Store, reached through an `externalDriverFactory`. No stack in this
  // repository runs one: neither docker-compose.yml nor
  // docker-compose.bootstrap.yml nor deploy/ defines a cubestore service, and
  // CUBEJS_CUBESTORE_HOST is never set. Cube does not degrade gracefully when a
  // declared rollup cannot be built — it prefers the rollup and fails the query
  // with "externalDriverFactory is not provided" rather than reading the source.
  // So declaring one here would break exactly the queries it was meant to speed
  // up. See F-035 and F-038.
  //
  // These definitions were previously written as `preAggregations: cond ? {…} : {}`.
  // That form never compiled: Cube's CubePropContextTranspiler resolves member
  // references by walking the ObjectProperty chain up to the cube's top-level
  // object, and a ConditionalExpression breaks that walk, so `measures: [requestCount]`
  // reached the sandbox as an undefined identifier. The condition was always false
  // for an unrelated reason (F-037), which is the only thing that kept the error
  // hidden. If a Cube Store is ever added, restore them as a plain object literal
  // — never behind a ternary — and prove it compiles.
  //
  //   dailySummary: {
  //     type: `rollup`,
  //     measures: [eventCount],
  //     dimensions: [service, eventType, eventDay],
  //     refreshKey: { every: `10 minute` }
  //   }
  preAggregations: {},

  dataSource: `default`
});
