// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

import { DataSourceInstanceSettings, CoreApp } from '@grafana/data';
import { DataSourceWithBackend } from '@grafana/runtime';

import { GravixQuery, GravixDataSourceOptions, DEFAULT_QUERY } from './types';

/**
 * DataSourceWithBackend does the work: every query is forwarded to the Go
 * backend over Grafana's own transport, so there is no second implementation of
 * the query logic in TypeScript to disagree with the one in Go.
 */
export class DataSource extends DataSourceWithBackend<GravixQuery, GravixDataSourceOptions> {
  constructor(instanceSettings: DataSourceInstanceSettings<GravixDataSourceOptions>) {
    super(instanceSettings);
  }

  getDefaultQuery(_: CoreApp): Partial<GravixQuery> {
    return DEFAULT_QUERY;
  }

  /** A query with no service is refused by the backend; do not send it. */
  filterQuery(query: GravixQuery): boolean {
    return !!query.service;
  }
}
