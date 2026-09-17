-- Copyright 2026 The Gravix Authors
-- SPDX-License-Identifier: BUSL-1.1
--
-- This file is part of Gravix Enterprise Edition and is NOT open source.
-- Licensed under the Business Source License 1.1. See ee/LICENSE.
-- Change Date: two years from this version's publication. Change License: Apache-2.0.

CREATE TABLE IF NOT EXISTS spend_caps (
    tenant_id  TEXT PRIMARY KEY,
    cap_cents  INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS spend_periods (
    tenant_id       TEXT NOT NULL,
    year_month      TEXT NOT NULL,
    spent_cents     INTEGER NOT NULL DEFAULT 0,
    rejected_events INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, year_month)
);
