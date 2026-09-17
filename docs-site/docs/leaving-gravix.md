---
title: Leaving Gravix
sidebar_position: 5
---

# Leaving Gravix

One command exports everything Gravix holds — every fact, every derived metric, every alert rule and
dashboard definition — into formats you can read with no Gravix component installed.

```bash
./scripts/export_everything.sh --out ~/gravix-export
```

There is no plan gate, no volume cap, no rate limit and no delay on this path. Gating the exit would
be the definition of a hostage situation, and a project confident in its product loses nothing by
publishing the way out.

## This page is tested, not asserted

`tests/e2e/exit_path_test.go` runs on every commit. It seeds a Gravix instance, runs the script
above, **confirms no Gravix process is listening on any of its ports**, and only then reads the
exported Parquet with a stock DuckDB client and checks the row counts against `MANIFEST.json`.

It also runs every command the generated README publishes and fails if any of them returns nothing.
An instruction that does not work is worse than no instruction, because you would believe it.

## What you get

```
<out>/
  README.md                  how to read every file here, with runnable commands
  MANIFEST.json              what was exported, when, from which version, row counts
  facts/                     raw request events, Parquet, partitioned by day
  metrics/                   rolled-up metrics, Parquet, partitioned by day
  events/                    service events, Parquet
  config/
    alert_rules.json         alert definitions
    dashboards.json          dashboard definitions
    slos.json                SLO definitions
    api_keys.json            key names and ids only — never key material
    team.json                users, roles, org membership — no password hashes
    scheduled_exports.json   export schedules
  checksums.txt              SHA-256 of every file
```

The `facts/` directory is the important one. It holds the raw events every metric was derived from,
so whatever you move to can recompute the same numbers — or different ones — from the same source.
You are not leaving with a summary of your history; you are leaving with the history.

## What is deliberately missing

Secrets. API keys, password hashes, 2FA secrets, webhook auth headers and SSO client secrets appear
in the files by **name**, with their values replaced by `[redacted]`.

This is not Gravix withholding something. It stores those values as hashes, so the originals do not
exist anywhere to export — re-issue them in your new system. And an export is a file people copy to a
laptop, attach to an email and leave in a downloads folder; shipping live credentials in it would
turn a backup into a breach.

The redaction is enforced twice: once when the file is written, and again by a scan of what was
actually written. The second pass is what catches a field added next year that nobody remembered to
redact — it aborts the export and deletes the file rather than leaving it on disk.

## Verifying it

```bash
cd ~/gravix-export
sha256sum -c checksums.txt
duckdb -c "SELECT service, count(*) FROM read_parquet('facts/**/*.parquet') GROUP BY 1;"
cat MANIFEST.json
```

Compare the counts you get to the ones in `MANIFEST.json`. If they differ, something was lost, and
that is a bug we want reported.

## Reading it somewhere else

Anything that reads Parquet reads this: DuckDB, pandas, Polars, Spark, ClickHouse, BigQuery,
Snowflake, Athena. The configuration is plain JSON. See
[Bare-Parquet access](./bare-parquet-access.md) for the same guarantee applied to a live
`data/warehouse/` directory.
