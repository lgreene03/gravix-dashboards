# INC-YYYY-NNNN: <one line, no customer identifiers>

**Severity:** SEV1 | SEV2
**Occurred:** YYYY-MM-DD HH:MM UTC
**Duration:** N minutes
**Published:** YYYY-MM-DD

---

## What happened

One paragraph. What broke, what the effect was, how it ended.

Describe scale in bands — "several tenants", "under 1% of ingest", "a minority
of the shared cell" — never as a figure that identifies anyone. No tenant ids,
no organisation names, no domains, no email addresses, no API key fragments, no
IP addresses. `make incident-audit` checks the machine-readable record for all
of those; this prose is on you.

## Timeline

All times UTC.

| Time | Event |
|---|---|
| HH:MM | What started it |
| HH:MM | How we found out |
| HH:MM | What we understood |
| HH:MM | What we did |
| HH:MM | Recovered |

## Root cause

What specifically caused this. Be precise. "A bad deploy" is not a root cause;
"the rollup's leader election lock was taken on a path relative to the working
directory, and the cron job's working directory changed" is.

## Impact

- Data loss: none / N minutes of facts / describe in aggregate
- Ingest: describe in bands
- Dashboards and queries: describe in bands

## Could Gravix diagnose it?

**Yes / No.**

If no — and this is the section that matters most — say exactly what was
missing. Not "monitoring was insufficient", but the capability, in the terms
somebody could build:

> There is no per-partition rollup lag metric. We found the stalled partition by
> listing the Parquet directory by hand and comparing modification times.

This goes into the machine-readable record as `what_was_missing`, and the audit
groups it. The third time the same sentence appears, it is a roadmap item.

## What changed in the open-source project

Link the merged pull request. Say what it does, in one sentence.

> Fixed in #412: the rollup now exposes `rollup_partition_lag_seconds` per
> partition, and the dashboard charts it.

## What did not change, and why

Everything considered and rejected, with the reason. This section is not
optional padding — a reader deciding whether to run Gravix themselves learns
more from what a vendor declined to fix than from what it did.

> We did not add automatic restart of a stalled rollup. A rollup that stalls
> twice in a row is a correctness signal, and restarting it would hide the
> second occurrence.

## Disposition

`improved` (PR #NNN) or `no_change` (reason above).
