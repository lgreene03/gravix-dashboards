<!--
Copyright 2026 The Gravix Authors
SPDX-License-Identifier: BUSL-1.1

This file documents Gravix Enterprise Edition, which is NOT open source.
Licensed under the Business Source License 1.1. See ee/LICENSE.
Change Date: two years from this version's publication. Change License: Apache-2.0.
-->

# The spend cap

**Set a number. Gravix will not spend past it.**

If you are on Gravix Cloud usage-based billing, you can set a hard monthly limit on what your
ingestion can cost. It is a cutoff, not a warning threshold and not a throttle. When you reach it,
Gravix stops accepting new data and tells you so.

## Why it is hard rather than soft

We have been openly critical of metered observability billing that has no ceiling — in
`docs/oss/01-competitive-thesis.md` we single out overage that "is not a cutoff, it is continuous
metering at the same rate", where a bad deploy flows straight into the invoice in near-real time.

Having said that in public, we are not going to ship it ourselves. So the cap here actually stops
things. That is less convenient than silently continuing and billing you, and it is the entire
point.

## What happens at the cap

**Stops:**

- New facts and events are refused. `POST /api/v1/facts`, `POST /api/v1/facts/batch` and
  `POST /api/v1/events` answer `402 Payment Required`.

**Keeps working, unchanged:**

- Every dashboard.
- Every alert rule, and every alert that fires.
- Every fact already ingested — nothing is deleted, hidden, downsampled or archived.
- Every query, export and API read. The gate only stands in front of the three write paths above;
  it has no involvement in reads at all.
- Recomputation, percentile queries and the exit path. Leaving is never gated.

**Never happens:**

- Nothing already accepted is deleted.
- Nothing already accepted is re-priced or billed twice.
- Nothing is dropped silently. There is no state in which Gravix accepts your data, charges you
  nothing, and quietly discards it.

## What you see

A refused request gets a response that says what happened and what to do:

```json
{
  "error": "monthly spend cap reached",
  "cap_cents": 50000,
  "spent_cents": 50012,
  "raise_cap_url": "/spendcap/caps/ten_abc123"
}
```

Your SDK or agent will surface this as an HTTP error rather than accepting the write. That is the
design: an error your pipeline can see beats a gap in a dashboard that somebody notices on Thursday.

Every refused event is also counted. Ask for your cap status at any time:

```bash
curl -H "Authorization: Bearer $GRAVIX_TOKEN" \
  https://cloud.example.com/spendcap/caps/ten_abc123
```

```json
{
  "tenant_id": "ten_abc123",
  "cap_cents": 50000,
  "spent_cents": 50012,
  "period": "2026-09",
  "rejected_events": 184203
}
```

`rejected_events` is the number you were going to ask for anyway: *how much did I lose, and when
did it start*. It is a number, not an absence.

## How to raise it

```bash
curl -X PUT -H "Authorization: Bearer $GRAVIX_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"cap_cents": 100000}' \
  https://cloud.example.com/spendcap/caps/ten_abc123
```

It takes effect immediately for new requests. The enforcer caches each tenant's cap for up to 30
seconds to avoid a database read per request, but raising the cap through this endpoint clears that
entry — because you are almost certainly doing it while data is being refused right now.

`cap_cents` must be a positive integer. There is no way to set a cap of zero, because a zero cap
and no cap are the same value internally and a typo should not silently turn your limit off.

The caller must hold the `admin` role **for the tenant named in the URL**. An admin token for one
tenant cannot read or change another tenant's cap.

## Spend counts only what was accepted

Spend is recorded after ingestion returns a `2xx`, never before.

If ingestion rejects a request — a malformed fact, a path template with a raw ID, a schema
violation — it is not billed and it does not count against your cap. You are not charged for data
Gravix declined to store, and your month is not shortened by it.

## The billing period

The cap is monthly, and the period is the calendar month in UTC (`2026-09`). A new month starts at
zero spend. A cap reached in September does not refuse anything in October.

## Self-hosted installs

None of this exists for you, and none of it needs to. There is no metered bill on hardware you own,
so there is nothing to cap. The `ingestion-service` binary in the open-source core has no awareness
that a cap exists and cannot be configured to enforce one: the cap is a separate process in Gravix
Cloud's ingress that Cloud runs in front of the identical, unmodified binary.
