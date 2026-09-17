// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

import { DataSourcePlugin } from '@grafana/data';

import { DataSource } from './datasource';
import { ConfigEditor } from './ConfigEditor';
import { QueryEditor } from './QueryEditor';
import { GravixQuery, GravixDataSourceOptions } from './types';

export const plugin = new DataSourcePlugin<DataSource, GravixQuery, GravixDataSourceOptions>(DataSource)
  .setConfigEditor(ConfigEditor)
  .setQueryEditor(QueryEditor);
