// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

import React, { ChangeEvent } from 'react';
import { InlineField, Input } from '@grafana/ui';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';

import { GravixDataSourceOptions } from './types';

type Props = DataSourcePluginOptionsEditorProps<GravixDataSourceOptions>;

/**
 * Connection settings: where Trino is, and nothing else.
 *
 * No credentials field. The plugin connects to Trino as the `gravix` user over
 * plain HTTP, which is what the reference stack exposes; a deployment that puts
 * Trino behind authentication should put Grafana behind the same network
 * boundary rather than have this plugin hold a secret.
 */
export function ConfigEditor({ options, onOptionsChange }: Props) {
  const { jsonData } = options;

  const onHostChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: { ...jsonData, trinoHost: event.target.value },
    });
  };

  const onPortChange = (event: ChangeEvent<HTMLInputElement>) => {
    const port = parseInt(event.target.value, 10);
    onOptionsChange({
      ...options,
      jsonData: { ...jsonData, trinoPort: Number.isNaN(port) ? undefined : port },
    });
  };

  return (
    <>
      <InlineField label="Trino host" labelWidth={16} tooltip="Defaults to localhost">
        <Input
          id="gravix-trino-host"
          onChange={onHostChange}
          value={jsonData.trinoHost ?? ''}
          placeholder="localhost"
          width={32}
        />
      </InlineField>
      <InlineField label="Trino port" labelWidth={16} tooltip="Defaults to 8081">
        <Input
          id="gravix-trino-port"
          type="number"
          onChange={onPortChange}
          value={jsonData.trinoPort ?? ''}
          placeholder="8081"
          width={32}
        />
      </InlineField>
    </>
  );
}
