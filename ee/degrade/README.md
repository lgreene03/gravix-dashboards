<!-- Copyright 2026 The Gravix Authors -->
<!-- SPDX-License-Identifier: BUSL-1.1 -->

# `ee/degrade` — what happens when a licence lapses

**Nothing that matters.**

A Gravix Pro licence is a commercial arrangement. It is not a kill switch, and this package is the
code that makes that true rather than a promise. Its guarantee is negative: there is no mechanism
by which an expired, absent, forged or corrupt licence can change a number, stop a job, or take a
dashboard away.

## The table

| Capability | Licensed | Grace | Read-only | Absent |
|---|---|---|---|---|
| Ingestion, rollup, alerting, dashboards, export (**core**) | ✅ | ✅ | ✅ | ✅ |
| Read `ee/` configuration | ✅ | ✅ | ✅ | ✅ |
| Export `ee/` configuration | ✅ | ✅ | ✅ | ✅ |
| Create or modify `ee/` configuration | ✅ | ✅ | ❌ | ❌ |
| `ee/` background jobs already scheduled | ✅ | ✅ | ✅ | ✅ |
| Schedule new `ee/` background jobs | ✅ | ✅ | ❌ | ❌ |
| Notice displayed | none | factual, dismissible | factual, dismissible | **none** |

The first row is the spec. It is the same row in all four columns, and
`TestCoreUnaffectedInEveryState` proves it by running the whole pipeline — ingest, roll up to
Parquet, evaluate an SLO, compute the percentile a dashboard renders, export the tenant's
configuration — under each state and comparing the bytes. If a licence state could ever reach a
core number, that test fails.

## The states

| State | When | Notice |
|---|---|---|
| `licensed` | A valid licence, or up to 24 hours past expiry | none |
| `grace` | 24 hours to 14 days past expiry | factual, dated, dismissible |
| `read_only` | More than 14 days past expiry | factual, dated, dismissible |
| `absent` | No licence, or one that fails verification | **none, ever** |

Fourteen days of full function covers a lapsed card, a purchasing cycle and a holiday, which are
the actual reasons licences expire. The 24-hour tolerance before the notice appears exists because
a machine whose clock has drifted is a likelier explanation than a customer who lapsed by an hour;
it moves only the licensed→grace boundary, so the fourteen days a customer is told about are
fourteen days.

## Why `absent` is silent

It is the permanent, correct state of every open-source installation, and the overwhelming
majority of them. Charter §7.5: a tampered or absent licence "is not an error condition. It is the
normal state of the OSS build, and must produce no warning noise." So in `StateAbsent` this package
returns an empty notice, writes no log record at any level, registers no metric, and adds no
header. An OSS user must not be able to tell from anything their install does that `ee/` code paths
exist at all.

Treating a *forged* licence as `absent` rather than as a distinct "invalid" state is the same
decision seen from the other side: it gives somebody editing a token no signal to iterate against.

## Why already-scheduled jobs keep running

Stopping a customer's warehouse sync mid-month because a card expired would destroy data
continuity to make a billing point. Their data would have a hole in it that renewing does not fill.
So `read_only` refuses *new* configuration and *new* scheduling; work already scheduled continues.

## Why the refusal says what it says

```json
{
  "error": "licence_expired",
  "message": "Your Gravix Enterprise licence expired on 2026-11-04. Existing configuration is readable and exportable. Renew to make changes.",
  "expired_at": "2026-11-04T00:00:00Z",
  "core_unaffected": true,
  "export_endpoint": "/api/gateway/export"
}
```

`core_unaffected` is in the payload, not only in the documentation, because the person reading it
was woken by an alert and the first thing they need to know is that monitoring did not stop.
`export_endpoint` is there because leaving should never be something a customer has to ask how to
do. HTTP 402, not 403, so that an operator grepping for authorisation failures does not find a
billing state.

## Using it from an `ee/` feature

Every mutation goes through `Guard`. Reads and exports do not — deliberately.

```go
func (s *Store) SetRetention(ctx context.Context, days int) error {
    return degrade.Guard(ctx, s.state, "set retention", func() error {
        return s.db.Update(ctx, days)
    })
}
```

`TestEveryEEMutationIsGuarded` scans `ee/` for writes that do not pass through `Guard` and fails
on them, so the rule survives the specs that have not been written yet.

## What this package must never contain

No network call, in any state (`TestNoNetworkInAnyState`). No upsell copy, in any state
(`TestNoNagUI`). No import from anything in core that would let core import back
(`TestNoCoreFilesModified`). Verification itself lives in `pkg/license`, which is Apache-2.0,
offline, and free — a customer can read exactly how their licence is checked.
