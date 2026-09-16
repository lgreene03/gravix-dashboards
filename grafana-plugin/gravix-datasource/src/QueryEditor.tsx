// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

import React, { ChangeEvent } from 'react';
import { InlineField, Input, Select } from '@grafana/ui';
import { QueryEditorProps, SelectableValue } from '@grafana/data';

import { DataSource } from './datasource';
import { GravixQuery, GravixDataSourceOptions, GravixField, GRAVIX_FIELDS } from './types';

type Props = QueryEditorProps<DataSource, GravixQuery, GravixDataSourceOptions>;

/**
 * Four inputs, one of them a closed dropdown.
 *
 * Deliberately not a SQL box. docs/04-non-goals.md §6 refuses a query
 * language, and the field list is closed on both sides: the options here are
 * GRAVIX_FIELDS, and the backend accepts only the same six names.
 */
export function QueryEditor({ query, onChange, onRunQuery }: Props) {
  const fieldOptions: Array<SelectableValue<GravixField>> = GRAVIX_FIELDS.map((f) => ({
    label: f,
    value: f,
  }));

  const onServiceChange = (event: ChangeEvent<HTMLInputElement>) => {
    onChange({ ...query, service: event.target.value });
  };

  const onMethodChange = (event: ChangeEvent<HTMLInputElement>) => {
    onChange({ ...query, method: event.target.value });
    onRunQuery();
  };

  const onPathChange = (event: ChangeEvent<HTMLInputElement>) => {
    onChange({ ...query, pathTemplate: event.target.value });
    onRunQuery();
  };

  const onFieldChange = (selected: SelectableValue<GravixField>) => {
    onChange({ ...query, field: selected.value ?? 'request_count' });
    onRunQuery();
  };

  return (
    <>
      <InlineField label="Service" labelWidth={16} required tooltip="Required — the backend refuses a query without it">
        <Input
          id="gravix-service"
          onChange={onServiceChange}
          onBlur={onRunQuery}
          value={query.service ?? ''}
          placeholder="checkout-api"
          width={32}
        />
      </InlineField>
      <InlineField label="Method" labelWidth={16} tooltip="Optional — blank matches every method">
        <Input
          id="gravix-method"
          onChange={onMethodChange}
          value={query.method ?? ''}
          placeholder="GET"
          width={32}
        />
      </InlineField>
      <InlineField label="Path template" labelWidth={16} tooltip="Optional — blank matches every path">
        <Input
          id="gravix-path"
          onChange={onPathChange}
          value={query.pathTemplate ?? ''}
          placeholder="/orders/{id}"
          width={32}
        />
      </InlineField>
      <InlineField label="Field" labelWidth={16}>
        <Select
          inputId="gravix-field"
          options={fieldOptions}
          value={query.field ?? 'request_count'}
          onChange={onFieldChange}
          width={32}
        />
      </InlineField>
    </>
  );
}
