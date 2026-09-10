# SPEC GRVX-1109: The documented exit path, executed in CI

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1109 | **Phase** | 11 | **Goal** | G5.5, G5.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q5 = YES → core. Instructions for leaving cannot be a paid feature; that would be the definition of a hostage situation. |
| **Implementer role** | `senior-engineer`, with `docs-engineer` |
| **Depends on** | GRVX-1101, GRVX-1107 |
| **Blocks** | GRVX-1403 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Publish, and prove in CI, that a user can leave Gravix with everything: every fact, every derived
metric, every alert rule and dashboard definition, in formats readable without any Gravix component.
Publishing the exit path is the cheapest trust purchase available, and a project confident in its
product loses nothing by it.

## 2. Context the implementer needs

- `pkg/export` (GRVX-1107) exports facts, metrics and events to Parquet, CSV and JSONL, free and unrestricted.
- `GRVX-1101` documents reading `data/warehouse/` with DuckDB and no Gravix process running.
- Configuration lives in `pkg/tenantdb` (SQLite or Postgres): alert rules, dashboards, SLOs, API keys, scheduled exports, team and org records.
- `data/raw/` holds JSONL facts; `data/warehouse/` holds Parquet metrics; both are already open formats on the user's own disk.
- `docs/oss/01-competitive-thesis.md` §2 Axis 3 claims format-open data with no rehydration tax. This spec is what makes that claim testable rather than asserted.
- API keys are secrets. An export containing live credentials would turn a backup into a breach.

## 3. Non-goals for this spec

- Do NOT export secret material in plaintext. API keys, password hashes, session tokens and SSO client secrets are listed by name and identifier, never by value.
- Do NOT require a running Gravix to *read* the output. That is the whole point.
- Do NOT make the exit path depend on any `ee/` component.
- Do NOT write an import path back in. GRVX-1404 covers self-host-to-cloud; leaving is one-directional here.
- Do NOT charge, gate, rate-limit, or delay the exit path.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `scripts/export_everything.sh` | The complete exit, one command |
| `pkg/export/config.go` | Configuration export with secret redaction |
| `pkg/export/config_test.go` | Tests |
| `docs-site/docs/leaving-gravix.md` | The published exit guide |
| `tests/e2e/exit_path_test.go` | CI execution of the whole exit |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `cmd/cli/cmd_export.go` | Add `--everything`, invoking the full exit |
| `.github/workflows/ci.yml` | Run the exit-path test in the existing e2e job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/tenantdb/**` | Config is read, never modified |
| `pkg/export/export.go` | Data export is settled by GRVX-1107 |
| `ee/**` | The exit path must work with `ee/` deleted |

## 5. Interface contract

### 5.1 `scripts/export_everything.sh`

```bash
#!/usr/bin/env bash
# Exports everything Gravix holds into open formats in one directory.
# The output is readable with DuckDB, any CSV reader, and any text editor.
# No Gravix process is needed to read it.
# Usage: ./scripts/export_everything.sh --out <dir> [--from <date>] [--to <date>]
set -euo pipefail
```

Output layout:

```
<out>/
  README.md                  how to read every file here, with runnable commands
  MANIFEST.json              what was exported, when, from which version
  facts/                     raw RequestFacts, Parquet, partitioned by day
  metrics/                   rolled-up metrics, Parquet, partitioned by day
  events/                    ServiceEvents, Parquet
  config/
    alert_rules.json         alert definitions
    dashboards.json          dashboard definitions
    slos.json                SLO definitions
    api_keys.json            key NAMES and ids only — never key material
    team.json                users, roles, org membership — no password hashes
    scheduled_exports.json   export schedules
  checksums.txt              SHA-256 of every file
```

Exit codes: `0` success; `1` an export step failed; `2` invalid argument; `3` destination not
writable or not empty.

### 5.2 Secret redaction

```go
// Package export, config.go

// RedactedFields are configuration fields never written to an export, because
// an export is a file a user will copy, email, and store — not a vault.
var RedactedFields = []string{
    "api_key", "key_material", "secret", "password_hash", "token",
    "client_secret", "private_key", "webhook_auth_header", "stripe_key",
}

// ExportConfig writes tenant configuration as JSON with every RedactedFields
// value replaced by the literal string "[redacted]". The field is retained so a
// reader knows it existed; only the value is removed.
func ExportConfig(ctx context.Context, db tenantdb.DB, tenantID, outDir string) error

// ErrSecretLeak is returned when a value matching a redaction rule reaches output.
var ErrSecretLeak = errors.New("export: refusing to write a secret to an export file")
```

Redaction is enforced by a post-write scan, not only by field selection. A field added later that
happens to carry a secret must be caught by the scan rather than silently exported.

### 5.3 The output `README.md`

Generated, not hand-written. Required sections:

1. `## What is in here` — the layout above, each directory explained.
2. `## Read it with DuckDB` — a runnable command per dataset, for example:
   `duckdb -c "SELECT service, count(*) FROM read_parquet('facts/**/*.parquet') GROUP BY 1;"`
3. `## Read it with pandas` — the equivalent Python.
4. `## Read the configuration` — plain JSON; `jq` examples.
5. `## What was redacted, and how to get it` — names every redacted field and states that key
   material must be re-issued rather than recovered, because Gravix stores hashes.
6. `## Verify nothing was lost` — `sha256sum -c checksums.txt`, and the row counts from `MANIFEST.json`.

### 5.4 The CI test

`tests/e2e/exit_path_test.go` must, on every run:

1. Seed a Gravix instance with facts, metrics, alert rules, dashboards and an API key.
2. Run `export_everything.sh`.
3. **Stop every Gravix process.**
4. Read the exported Parquet with a DuckDB client and assert the row counts match `MANIFEST.json`.
5. Parse every config JSON and assert the alert rules and dashboards are present and complete.
6. Grep the entire output tree for the seeded API key's value and assert **zero** matches.
7. Verify `checksums.txt` over every file.

Step 3 is what makes this a test rather than a demonstration. Step 6 is what keeps the export from
becoming a credential leak.

## 6. Behaviour

1. Implement `ExportConfig` with redaction plus the post-write scan.
2. Implement `export_everything.sh` producing the §5.1 layout.
3. Generate the output `README.md` with all six sections and runnable commands.
4. Write `MANIFEST.json` with per-dataset row counts and the Gravix version.
5. Write `checksums.txt`.
6. Add `--everything` to the CLI.
7. Write the CI test with all seven steps, including stopping Gravix before reading.
8. Verify the whole path works with `ee/` deleted.
9. Write `docs-site/docs/leaving-gravix.md` linking to the guide and stating plainly that the exit path is tested on every commit.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Destination not empty | exit 3 | `destination <dir> is not empty; refusing to overwrite an existing export` |
| A secret reaches output | exit 1, delete the file | `refusing to write a secret to an export file: <field> in <file>` |
| An export step fails | exit 1, keep partial output, report | `export step "<name>" failed: <err>; partial output left in <dir>` |
| Checksum mismatch on verify | exit 1 | `checksum mismatch: <file>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | One command exports facts, metrics, events and configuration | `TestExportEverythingCompletes` |
| AC-2 | Exported data is readable with **every Gravix process stopped** | `TestExitPathReadableWithGravixStopped` |
| AC-3 | Row counts in the output match `MANIFEST.json` | `TestExportRowCountsMatchManifest` |
| AC-4 | No API key value appears anywhere in the output | `TestNoSecretInExitPath` |
| AC-5 | Every `RedactedFields` entry is redacted, field retained | `TestAllSecretFieldsRedacted` |
| AC-6 | A secret reaching output aborts and deletes the file | `TestSecretLeakAborts` |
| AC-7 | Alert rules, dashboards and SLOs are complete in the output | `TestConfigExportComplete` |
| AC-8 | The generated README's commands run and return data | `TestReadmeCommandsRun` |
| AC-9 | `checksums.txt` verifies over every file | `TestChecksumsVerify` |
| AC-10 | The exit path works with `ee/` deleted | `TestExitPathWorksWithoutEE` |
| AC-11 | A non-empty destination is refused | `TestNonEmptyDestinationRefused` |
| AC-12 | The exit path runs in CI on every commit | `TestExitPathRunsInCI` |

## 8. Verification

```bash
# 1. The exit
./scripts/export_everything.sh --out /tmp/gravix-exit && ls /tmp/gravix-exit
# expect: the §5.1 layout

# 2. Readable with Gravix stopped — the claim this spec exists to prove
go test ./tests/e2e/... -run TestExitPathReadableWithGravixStopped -v
# expect: PASS

# 3. No credential leak
go test ./tests/e2e/... -run 'TestNoSecretInExitPath|TestAllSecretFieldsRedacted|TestSecretLeakAborts' -v
grep -rc "sk_live\|api_key.*[a-zA-Z0-9]\{20\}" /tmp/gravix-exit/ | grep -v ':0' || echo "no secrets in export"
# expect: PASS; no secrets in export

# 4. Nothing lost
go test ./tests/e2e/... -run 'TestExportRowCountsMatchManifest|TestChecksumsVerify' -v
# expect: PASS

# 5. The README actually works
go test ./tests/e2e/... -run TestReadmeCommandsRun -v
# expect: PASS

# 6. Works without ee/
go test ./tests/e2e/... -run TestExitPathWorksWithoutEE -v
# expect: PASS

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The CI test stops every Gravix process before reading the output
- [ ] Zero secret values anywhere in the exported tree
- [ ] The generated README's commands executed and their output recorded
- [ ] `docs-engineer` delta merged; `leaving-gravix.md` states the path is CI-tested
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any exported file unreadable without Gravix | STOP. `CORRECTNESS DEFECT` — this breaks thesis Axis 3 and the feature's purpose. |
| A secret in the output | STOP. `SECURITY DEFECT: <field> in <file>`. Do not ship; an export is a file people email. |
| Pressure to gate, delay, or charge for the exit path | Refuse, citing charter §2.3. Route to `license-boundary-auditor`. |
