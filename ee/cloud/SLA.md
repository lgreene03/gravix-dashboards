<!--
Copyright 2026 The Gravix Authors
SPDX-License-Identifier: BUSL-1.1

This file documents Gravix Enterprise Edition, which is NOT open source.
Licensed under the Business Source License 1.1. See ee/LICENSE.
Change Date: two years from this version's publication. Change License: Apache-2.0.
-->

# Gravix Cloud Service Level Agreement

**Applies to:** Gravix Cloud, the hosted service.
**Does not apply to:** self-hosted Gravix. Your uptime on your own hardware is yours; nothing in
this document is a commitment about it, and nothing in it is a capability withheld from you.

## The number, and where it comes from

Gravix Cloud's uptime is measured **by Gravix**.

Every health check against Cloud's own infrastructure is written as an ordinary `RequestFact`,
through the same public ingestion API every customer uses, into a Gravix tenant we operate on
ourselves. The uptime figure below is then a query against that tenant's `error_rate` metric,
through the same public metrics API we sell:

```
uptime% = 100 - mean(error_rate) over the month, daily granularity
```

There is no separate monitoring pipeline behind the number we are contractually bound by. If
Gravix's aggregation is wrong, our own SLA figure is wrong with it, and the people who would owe
you a credit are the people who would have to fix it.

That is the whole reason the measurement works this way. A vendor whose SLA is measured by a
different system than the one it sells has not staked anything on that system being right.

## Uptime targets

Sourced from [`docs/sla.md`](../docs/sla.md) §1, unchanged:

| Plan | Monthly uptime target | Monthly allowed downtime |
|---|---|---|
| Free | No SLA | — |
| Team | 99.5% | ~3.6 hours |
| Business | 99.9% | ~43 minutes |
| Scale | 99.9% | ~43 minutes |
| Enterprise | 99.95% | ~22 minutes |

**Uptime** is the percentage of the month during which the Gravix Ingestion API
(`POST /api/v1/facts`) returns a 2xx or 429 within **5 seconds**. The prober's own timeout is 5
seconds for exactly that reason, and a test fails if the two ever disagree.

## Service credits

Sourced from [`docs/sla.md`](../docs/sla.md) §3, unchanged, and independent of plan:

| Measured monthly uptime | Credit (% of monthly fee) |
|---|---|
| 99.9% and above | 0% |
| 99.0% – 99.9% | 10% |
| 95.0% – 99.0% | 25% |
| below 95.0% | 50% |

The bands are half-open at the top: exactly 99.9% **meets** the target and owes no credit. Every
boundary is pinned by test from both sides, because getting it wrong one way costs us money every
good month and the other way denies a credit that was owed.

## Checking the number yourself

```bash
curl -H "Authorization: Bearer $GRAVIX_TOKEN" \
  'https://cloud.example.com/sla/uptime/2026-08?plan=business'
```

```json
{
  "year_month": "2026-08",
  "plan": "business",
  "measured_uptime_pct": 99.94,
  "target_pct": 99.9,
  "credit_pct": 0
}
```

Any valid session can read it. Platform uptime is the same number for every customer and is
published on a page with no login at all, so restricting it by role would only make it harder to
check a claim we make in public.

The free plan gets `target_pct: 0` — no SLA — but still sees `measured_uptime_pct`. Hiding the
number from the people who have not paid would mean hiding how bad a month was from everyone who
was not billed for it.

## The public status page

[status.gravix.io](https://status.gravix.io) runs `cmd/status_page`, the **unmodified
Apache-2.0 core binary**, pointed at the same target list the prober polls. It is configured, not
built, for Cloud: `STATUS_ENDPOINTS`, `STATUS_PORT`, `STATUS_POLL_INTERVAL`, `STATUS_DATA_FILE`.

It is deliberately an independent display rather than a second view of the same query. If a real
outage also stopped the prober, the fact stream would show a month with no data — which this SLA
scores as 100% — and the status page is what would say otherwise.

## Exclusions

As [`docs/sla.md`](../docs/sla.md) §2: announced maintenance, force majeure, customer-side
network or SDK misconfiguration, abuse or traffic exceeding rate limits by more than 10x, and
third-party outages.

## Claiming a credit

Submit a support request within 30 days, naming the month. Credits apply to the next billing cycle
and are not refundable as cash. Computing the credit is automatic; applying it to an invoice is a
billing operation and is not part of this system.

## What a self-hoster has instead

All of it, minus the contract:

- `cmd/status_page` is Apache-2.0 and in the core. Point it at your own endpoints.
- Your facts are already in Gravix, so `error_rate` over your own services is a query you can
  already run.

What lives behind the commercial licence here is the part that only exists because there is a
contract: a tenant we run on ourselves, and a credit owed to somebody else. There is no uptime
measurement a paying customer gets that a self-hoster cannot perform.
