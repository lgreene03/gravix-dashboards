# Incidents

What we learn running Gravix Cloud, you get.

Every incident on our infrastructure ends one of two ways: a merged change in
the Apache-2.0 core, or a public note explaining why no change was warranted.
There is no third outcome, and there is no private runbook of fixes we keep for
ourselves.

We also record, for every incident, whether Gravix was sufficient to diagnose
it. When the answer is no, we say what was missing. That is the most useful
thing our own outages produce, and hiding it would waste it.

---

## Why this is structural rather than a promise

A hosted product accumulates operational knowledge that self-hosters do not
have. Left alone, that gap widens: the vendor learns things at scale, keeps
them in a private runbook, and the free product it is built on stops
benefiting from its own popularity.

So this loop is not a policy. It is code, in the Apache-2.0 core
(`pkg/incident/`), and a weekly job that fails when the rules are broken. A
self-hoster runs the same loop over their own incidents with the same code —
charter §7.3 Q1: the mechanism that returns operational learning to the free
product cannot itself be paid.

## The two dispositions

| Disposition | What it means | What it requires |
|---|---|---|
| `improved` | A change landed in the open-source core | A merged pull request number |
| `no_change` | No code change was warranted | A public reason, in writing |

`pending` exists, and is bounded at **30 days**. It is a waiting room, not an
outcome. Past that the weekly audit fails and names the incident.

There is deliberately no `wontfix`, no `cloud_only` and no `acknowledged`. A
third terminal value is where "that is just an operations problem" would go to
live, and most incidents are software problems observed at scale.

## The dogfood question

Every incident records `diagnosed_with_gravix`. When it is `false`,
`what_was_missing` is required and the record is rejected without it.

An observability company discovering that its own tool could not diagnose its
own outage has learned something no user survey will tell it. The audit groups
`what_was_missing` values and reports any that recur, because one incident
Gravix could not explain is bad luck and the same gap three times is a roadmap
item nobody had to guess at.

## Public postmortems

Every **SEV1 or SEV2** incident gets a postmortem in this directory within
five working days, using [`_TEMPLATE.md`](_TEMPLATE.md). It says what happened,
the timeline, the root cause, what was affected in aggregate, whether Gravix
diagnosed it, what changed in the open-source project, and what did not change
and why.

Severity is the SEV1–SEV4 scale from
[`docs/incident-response.md`](../../incident-response.md), not a second
vocabulary for the same thing.

### Redaction

Nothing published here may contain:

- a tenant id
- an organisation name or domain
- an email address
- an API key or fragment of one
- an IP address

`pkg/incident`'s `CheckRedaction` enforces every one of those classes on the
summary, the no-change reason and the what-was-missing note, and it is
deliberately over-eager: a false positive costs somebody a rewording, while a
false negative puts a customer's domain in a git history forever.

Scale is described in bands — "several tenants", "under 1% of ingest" — never
as a figure that identifies anyone.

A near miss counts. An incident does not have to have been customer-visible to
go through this loop; a near miss is the cheapest lesson available.

## The audit

```bash
make incident-audit
```

It reports:

- incidents pending beyond 30 days
- `improved` dispositions with no merged pull request
- `no_change` dispositions with no reason
- SEV1/SEV2 incidents with no postmortem past five working days
- the count of incidents Gravix could not diagnose, and any recurring gap

It runs weekly in CI. **It never auto-dispositions and never auto-closes.** A
machine deciding that an incident produced no learning would defeat the purpose
of the whole loop, which exists precisely because that is the decision people
are tempted to skip.

Exit codes: `0` clean, `1` an enforcement failure, `2` the records could not be
read.

## Adding an incident

1. Copy `_TEMPLATE.md` to `INC-YYYY-NNNN.md` and write the postmortem.
2. Add `INC-YYYY-NNNN.json` beside it with the machine-readable record.
3. Run `make incident-audit` before opening the pull request.

```json
{
  "id": "INC-2026-0001",
  "occurred_at": "2026-09-01T03:12:00Z",
  "severity": "SEV2",
  "summary": "Rollup job stalled for two hours; under 1% of ingest delayed, no facts lost.",
  "disposition": "improved",
  "oss_change_pr": 412,
  "diagnosed_with_gravix": false,
  "what_was_missing": "no per-partition rollup lag metric; we read the Parquet directory by hand",
  "published_at": "2026-09-03T16:00:00Z"
}
```

## The record so far

There are no published incidents yet. That is a statement about how long Gravix
Cloud has been running, not about how reliable it is, and this file will say so
until it is no longer true.
