---
title: Leaving Gravix Cloud
sidebar_position: 4
---

# Move from Gravix Cloud to a self-hosted install

One command. No paid tier, no support ticket, no conversion step.

```bash
gravix migrate export-cloud \
  --tenant-id <id> \
  --since <YYYY-MM-DD> \
  --gateway-endpoint <url> \
  --gateway-token <jwt> \
  --out-dir ./gravix-export
```

That is the whole of it. What lands in `./gravix-export` is not an export format — it is a Gravix
data root. The self-hosted stack reads it directly, because the archive Cloud streams uses the
object-store keys verbatim and those keys are exactly what a local install reads.

## Why there is nothing to convert

Gravix stores **facts**, not metrics. Facts are immutable JSONL, written under keys that look like:

```
raw/<tenant-id>/request_facts/2026-01-14/09/0193f8a0-….jsonl
```

Cloud's export endpoint puts each object into the archive under that same key. Extracting it under
a directory makes that directory a data root. Everything else — the warehouse Parquet, the
percentiles, the dashboard — is **derived** and gets recomputed locally from the facts you now
hold. Nothing has to be migrated because nothing else is the source of truth.

This is also why there is no "export your metrics" step and no metrics transfer. Recomputing them
from the facts you took with you produces the same numbers; see
[Prove it](prove-it) for the test that holds recomputation byte-identical.

## Step 1 — get a session token

The command authenticates with the same JWT your dashboard session uses. Log in and copy the token,
or obtain one from `POST /api/gateway/login`. Put it in the environment rather than on the command
line, where it would be recorded in your shell history and visible in `ps`:

```bash
export GRAVIX_GATEWAY_TOKEN='eyJhbGciOi…'
```

`--gateway-token` also works if you prefer to pass it explicitly.

## Step 2 — export

```bash
gravix migrate export-cloud \
  --tenant-id ten_abc123 \
  --since 2026-01-01 \
  --gateway-endpoint https://cloud.example.com \
  --out-dir ./gravix-export
```

| Flag | Default | What it does |
|---|---|---|
| `--gateway-endpoint` | `http://localhost:8091` | Base URL of the Cloud gateway API |
| `--tenant-id` | *(required)* | The tenant to export |
| `--gateway-token` | *(from `GRAVIX_GATEWAY_TOKEN`)* | Session JWT |
| `--since` | *(required)* | Earliest date, `YYYY-MM-DD` |
| `--until` | today (UTC) | Latest date, inclusive |
| `--out-dir` | `./gravix-export` | Where to extract |
| `--data-types` | `request_facts,service_events` | What to take |

Cloud caps a single export request at 30 days so that no one response has to be unbounded. The
command splits your range into consecutive 30-day windows and issues one request each, so there is
no limit on how much history you can take. A window with no data is skipped, not an error — most
windows are empty for most tenants.

If another export is already running for your tenant, Cloud answers `429`. The command waits five
seconds and tries once more; a second `429` stops it so two exports do not fight each other.

When it finishes:

```
exported 1284 files across 24 requests into ./gravix-export
```

## Step 3 — check the manifest

```bash
cat ./gravix-export/MIGRATION_MANIFEST.json
```

```json
{
  "tenant_id": "ten_abc123",
  "gateway_endpoint": "https://cloud.example.com",
  "since": "2026-01-01",
  "until": "2026-09-16",
  "data_types": ["request_facts", "service_events"],
  "files_exported": 1284,
  "exported_at": "2026-09-16T08:12:44Z"
}
```

It records what you **asked for**, not only what arrived, so a short export is visibly short rather
than quietly incomplete. Compare `since` and `until` against the range you expected before you
decommission anything in Cloud.

## Step 4 — point a self-hosted install at it

There is no import step. The exported directory **is** a Gravix data root, so if you export
straight into where your self-hosted install keeps its data, there is not even a copy:

```bash
gravix migrate export-cloud --tenant-id ten_abc123 --since 2026-01-01 \
  --gateway-endpoint https://cloud.example.com \
  --out-dir ./data
```

If you already exported to the default `./gravix-export`, move it into place:

```bash
mkdir -p ./data
cp -r ./gravix-export/raw ./data/raw
```

Then recompute the warehouse from the facts. Run this from the directory holding `./data` — the
paths are relative to the data root, which is how the job addresses storage whether it is on local
disk or in S3:

```bash
go run ./transforms/request_metrics_minute/ \
  -input-dir ./data/raw/ten_abc123/request_facts \
  -output-dir ./data/warehouse/request_metrics_minute \
  -start-day 2026-01-01 \
  -end-day 2026-09-16
```

Then bring the stack up:

```bash
docker-compose up -d --build
```

The dashboard comes up on the recomputed metrics with no further steps.

## Step 5 — confirm before you cancel

Read the recomputed warehouse with something that is not Gravix. DuckDB is a single binary and has
no knowledge of this project:

```bash
duckdb -csv -c "SELECT count(*) AS rows, sum(request_count) AS requests
FROM read_parquet('data/warehouse/request_metrics_minute/**/*.parquet');"
```

`requests` is the number of requests Gravix recorded in the range you exported. Compare it against
what Cloud showed for the same period before you cancel.

A non-zero count means your facts are yours, on your disk, readable without any Gravix process
running. See [Bare-Parquet access](bare-parquet-access) for what else you can do with them.

## This page is executed, not asserted

`tests/e2e/cloud_to_selfhost_migration_test.go` runs this exact path on every commit: the real
Cloud gateway binary, the real `gravix migrate export-cloud` command, the real rollup binary, and
the stock DuckDB CLI reading the result. A change that broke the exit path would fail CI before it
shipped.

That matters more here than on most pages. A migration guide is read once, at the worst possible
moment, by somebody who has already decided to leave — which is precisely when nobody is going to
file a bug about a stale document.

## What is not here

- **Warehouse Parquet is not transferred.** It is derived and disposable; you recompute it in step 4.
  See [Architecture](architecture-overview).
- **Dashboards and alert rules are configuration, not data.** Export them with
  [`gravix export`](leaving-gravix), which covers the whole install rather than one tenant's facts.
- **The reverse direction** — self-hosted to Cloud — is a separate command.
