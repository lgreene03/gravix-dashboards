-- Copyright 2026 The Gravix Authors
-- SPDX-License-Identifier: BUSL-1.1
--
-- This file is part of Gravix Enterprise Edition and is NOT open source.
-- Licensed under the Business Source License 1.1. See ee/LICENSE.
-- Change Date: two years from this version's publication. Change License: Apache-2.0.

CREATE TABLE IF NOT EXISTS bucket_configs (
    tenant_id           TEXT PRIMARY KEY,
    endpoint            TEXT NOT NULL,
    region              TEXT NOT NULL,
    bucket              TEXT NOT NULL,
    access_key_id       TEXT NOT NULL,
    secret_access_key   TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'pending',
    failure_reason      TEXT NOT NULL DEFAULT '',
    verified_at         TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
