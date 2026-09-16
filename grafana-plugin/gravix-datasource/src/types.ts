// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

import { DataQuery, DataSourceJsonData } from '@grafana/data';

/**
 * GravixQuery mirrors pkg/datasource.go's QueryModel field for field.
 *
 * The two are serialised across the Grafana frontend/backend boundary as the
 * same JSON, so a rename on one side without the other produces a query that
 * silently loses a filter rather than failing.
 */
export interface GravixQuery extends DataQuery {
  service: string;
  method?: string;
  pathTemplate?: string;
  field: GravixField;
}

/**
 * GravixField is the closed set pkg/datasource.go's fieldColumns accepts.
 *
 * Kept as a union type rather than a string so that adding an option to the
 * dropdown without adding it to the backend allowlist is a compile error here,
 * not a runtime rejection in front of a user.
 */
export type GravixField =
  | 'request_count'
  | 'error_count'
  | 'error_rate'
  | 'p50_latency_ms'
  | 'p95_latency_ms'
  | 'p99_latency_ms';

export const GRAVIX_FIELDS: GravixField[] = [
  'request_count',
  'error_count',
  'error_rate',
  'p50_latency_ms',
  'p95_latency_ms',
  'p99_latency_ms',
];

export const DEFAULT_QUERY: Partial<GravixQuery> = {
  field: 'request_count',
};

/** GravixDataSourceOptions is what ConfigEditor stores and NewDatasource reads. */
export interface GravixDataSourceOptions extends DataSourceJsonData {
  trinoHost?: string;
  trinoPort?: number;
}
