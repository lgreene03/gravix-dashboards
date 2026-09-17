---
title: Moving to Gravix Cloud
sidebar_position: 5
---

# Move a self-hosted install to Gravix Cloud

One command replays every fact and service event already on your disk into a Cloud tenant.

```bash
export GRAVIX_API_KEY='grvx_…'

gravix migrate import-cloud \
  --data-dir ./data \
  --tenant-dir-name ten_abc123 \
  --ingestion-endpoint https://ingest.example.com
```

There is no special import API behind this and no server-side work to enable. The command replays
your history through `POST /api/v1/facts/batch` — the same public endpoint every Gravix SDK already
calls. It works because Gravix has never had a staleness check on `event_time`: a fact from
eighteen months ago is accepted exactly as one from a second ago.

Bringing your own history with you is free, for the same reason taking it away is. See
[Leaving Gravix Cloud](cloud-to-selfhost-migration) for the other direction.

## Before you start: the one limitation

**The importer does not de-duplicate.** It does not check whether the destination already has a
fact, because it has no way to ask. If you run it twice over the same range, you get every fact
twice, and the duplicates are indistinguishable from real traffic — your request counts double and
your percentiles shift.

So:

- Run it **once** per range.
- If a run fails partway, **do not simply re-run the whole thing**. Narrow `--data-dir` to the
  files that did not land, or accept the duplicates knowingly.
- Do a `--dry-run` first (below) so a mistake in the flags costs you nothing.

This is a real limitation, not a caveat we expect you to skim. It is stated here rather than only
in the spec because it is the one way this command can quietly give you a wrong answer.

## Step 1 — see what would be sent

`--dry-run` walks your data and counts it without making a single HTTP request. It needs no API
key, so you can run it before you have a Cloud account at all:

```bash
gravix migrate import-cloud \
  --data-dir ./data \
  --tenant-dir-name ten_abc123 \
  --dry-run
```

```json
{
  "files_read": 1284,
  "facts_accepted": 41203884,
  "facts_rejected": 0,
  "events_accepted": 902,
  "events_failed": 0,
  "imported_at": "2026-09-16T08:41:02Z"
}
```

On a dry run, `facts_accepted` is what *would* be sent. Check it against what you expect before
spending the bandwidth.

## Step 2 — find your tenant directory name

`--tenant-dir-name` is the path segment under `<data-dir>/raw/`:

```bash
ls ./data/raw
# ten_abc123
```

A single-tenant install has no such segment — its facts are at `./data/raw/request_facts/` — so
move them under a name first:

```bash
mkdir -p ./data/raw/ten_abc123
mv ./data/raw/request_facts ./data/raw/service_events ./data/raw/ten_abc123/
```

## Step 3 — import

```bash
export GRAVIX_API_KEY='grvx_…'

gravix migrate import-cloud \
  --data-dir ./data \
  --tenant-dir-name ten_abc123 \
  --ingestion-endpoint https://ingest.example.com
```

| Flag | Default | What it does |
|---|---|---|
| `--data-dir` | `./data` | Your self-hosted data directory |
| `--tenant-dir-name` | *(required)* | The segment under `<data-dir>/raw/` to read |
| `--ingestion-endpoint` | `http://localhost:8090` | The Cloud ingestion API |
| `--api-key` | *(from `GRAVIX_API_KEY`)* | Your Cloud tenant's ingestion key |
| `--batch-lines` | `500` | JSONL lines per batch request |
| `--dry-run` | `false` | Count without sending |

Files are replayed in chronological order — the `YYYY-MM-DD/HH` segments in the path sort that way
— so your history arrives in the order it happened.

Put the key in the environment rather than on the command line, where it would be recorded in your
shell history and visible in `ps`.

## What the summary tells you

```json
{
  "files_read": 1284,
  "facts_accepted": 41203201,
  "facts_rejected": 683,
  "events_accepted": 902,
  "events_failed": 0,
  "imported_at": "2026-09-16T09:12:44Z"
}
```

- **`facts_rejected`** are facts the destination refused, usually because they would fail
  validation there too — a `path_template` with a raw ID, say, from before you tightened it. They
  were not stored and they are not billed.
- **`events_failed`** are service events that were refused individually. A bad event does not stop
  the run; it is counted and the import continues.

A non-zero `facts_rejected` is worth reading, not ignoring. It usually means the two installs
disagree about what is valid, which is a thing to understand before you decommission the source.

## What happens if you hit a quota

Cloud answers `429` with a `Retry-After` when a tenant's monthly event quota is exhausted. The
importer waits the interval the server asks for and retries once. If the second attempt is also
refused, that batch is counted in `facts_rejected` and the run **continues** rather than aborting.

That is deliberate. Hitting a quota partway through replaying a year should leave you with the
months that fit and a number telling you what did not, rather than an aborted run and nothing at
all. Raise your quota (or your [spend cap](billing-faq)) and import the remainder.

## Batching

Facts are grouped into batches of `--batch-lines` lines, capped at 900 KB per request to stay
inside the ingestion API's 1 MB body limit. Service events are sent one per request, because there
is no batch endpoint for them — this command works inside the API the server already offers rather
than asking for one of its own.

## This page is executed, not asserted

`tests/e2e/selfhost_to_cloud_migration_test.go` runs this path on every commit with two real,
unmodified ingestion binaries — one standing in for your install, one for Cloud — and the real
`gravix` binary between them. It asserts that the destination accepts exactly as many facts as the
source held, and that they are durably on the destination's disk afterwards. A change that lost
facts in transit would fail CI before it shipped.
