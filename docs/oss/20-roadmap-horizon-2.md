# Horizon 2 Roadmap: Open Core — Phases 7 through 15

**Status:** Ratified. **Owner:** `cpo`. **Supersedes nothing** — Horizon 1 (Phases 0–6) shipped and
stands. **Governed by:** `00-open-core-charter.md`. **Measured by:** `12-goal-tree.md`.
**Executed by:** the loops in `11-agent-loops.md`.

---

## 0. What changed between horizons

Horizon 1 built a working multi-tenant SaaS: ingestion, rollups, DuckDB/Trino, Cube, dashboard,
alerting, SDKs, RBAC, SSO, billing, Terraform, Helm. All of it exists and works.

Horizon 2 does not add much product. It does three things Horizon 1 skipped:

1. **Makes it credible** — a licence, a boundary, a governance model, and proof that the free tier
   is not a trial.
2. **Makes the differentiator real** — Horizon 1 *stored* facts. Horizon 2 makes recomputability a
   user-facing capability that no competitor can match, because none of them kept the raw data.
3. **Turns a product into a project** — contributors, an ecosystem, and a business that funds it
   without corrupting it.

Sequencing note: **G1 before G2 before G4.** Nobody evaluates a cost benchmark from a project they
suspect will be relicensed next year. Credibility is the prerequisite for every claim after it.

---

## Phase 7 — Open the Core

**Theme:** "A stranger can trust the code."
**Goal:** G1. **Effort:** ~4 person-weeks. **Duration:** Month 0–1.
**Loops introduced:** L9 (Boundary), L7 (Community).

The repository currently has **no LICENSE file at all**. Until that changes, Gravix is legally
all-rights-reserved and nobody may use it. Everything else in this roadmap is blocked on this phase.

### Deliverables

| ID | Deliverable | Placement |
|---|---|---|
| `GRVX-701` | `LICENSE` — Apache-2.0; per-directory licence map; headers on every source file | core |
| `GRVX-702` | `ee/` scaffold + `ee/LICENSE` (BUSL-1.1, 2-year Change Date → Apache-2.0) + header file | boundary |
| `GRVX-703` | `boundary.yaml` — machine-readable core/`ee` map with a Crippleware Test record per entry | boundary |
| `GRVX-704` | `make build-oss` / `make test-oss` / `make check-boundary` + the CI job that runs them with `ee/` deleted | core |
| `GRVX-705` | `CONTRIBUTING.md` + DCO check + issue/PR templates | core |
| `GRVX-706` | `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `MAINTAINERS.md`, `ADOPTERS.md` | core |
| `GRVX-707` | `TRADEMARK.md` — fork-friendly, name-reserved | core |
| `GRVX-708` | `SECURITY.md` + disclosure SLA + advisory process | core |
| `GRVX-709` | Release signing (cosign) + SBOM (CycloneDX) + reproducible-build verification | core |
| `GRVX-710` | Migrate existing plan-gated features to `boundary.yaml`; move anything failing the Crippleware Test back to core | boundary |
| `GRVX-711` | README rewrite: what is free, what is paid, what we refuse to build | core |

### `GRVX-710` is the phase's real work

Horizon 1 gated features behind `requirePlan` for SaaS reasons. Several of them fail the
Crippleware Test and must be **returned to the free tier**:

| Currently gated | Ruling | Why |
|---|---|---|
| Public metrics API (`requirePlan("pro")`) | → **core** | Q5. Reading your own data is not a premium feature. |
| Custom dashboards | → **core** | Q1. Any team notices this missing. |
| Scheduled exports | → **core** | Q5. Export is the anti-lock-in guarantee. |
| Per-tenant rate limiting | → **core** | Q3. A safety control, not a feature. |
| Audit log | → **core** | Q3. Baseline security. |
| Tenant branding | stays `ee/` | Q1–Q5 all NO. Only matters when reselling. |
| Multi-tenancy + billing | stays `ee/` | Only matters when running Gravix for others. |

Giving away shipped paid features is uncomfortable and correct. Charter §7.3 Q4 makes it
non-negotiable once released open — doing it now, deliberately, costs far less than being forced
to later.

### Exit criteria

- [ ] `make build-oss && make test-oss` pass on a tree with `ee/` **deleted**
- [ ] `make check-boundary` reports 0 violations
- [ ] Every `requirePlan` call maps to a `boundary.yaml` entry with all five Crippleware answers
- [ ] Every source file carries the correct licence header
- [ ] A release is signed, has an SBOM, and its build is reproducible
- [ ] The README states the free/paid split in the first screenful

---

## Phase 8 — The Correctness Moat  ⭐

**Theme:** "A stranger can trust the numbers."
**Goal:** G2. **Effort:** ~10 person-weeks. **Duration:** Month 1–4.
**Loops introduced:** L6 (Benchmark), L10 (Dogfood).

**This is the phase that makes the competitive thesis true.** `docs/00-system-truth.md` §4 has
promised recomputability since day one; it has never been exposed to a user. Making it a
first-class, demonstrable capability is the one thing in this roadmap that Datadog and Grafana
**cannot copy without rebuilding their storage layer**, because they discarded the raw observation.

### Deliverables

| ID | Deliverable | Placement |
|---|---|---|
| `GRVX-801` | Recompute engine: `gravix recompute --from --to --metric` — deterministic, idempotent, resumable | core |
| `GRVX-802` | Idempotency keys on rollup outputs; re-running a window produces a byte-identical file | core |
| `GRVX-803` | Metric contract registry: versioned definitions, `metric@vN`, never redefined in place | core |
| `GRVX-804` | Exact percentiles from raw facts + mergeable t-digest sketches with published error bounds | core |
| `GRVX-805` | Late-data revisions: `event_time` bucketing, revision counter, prior values reproducible | core |
| `GRVX-806` | Retroactive dimensions: add a dimension or percentile and backfill 30 days | core |
| `GRVX-807` | `gravix explain <metric> <bucket>` — full lineage from number to source facts | core |
| `GRVX-808` | Mergeability proofs: every metric is aggregatable-with-proof or flagged non-aggregatable | core |
| `GRVX-809` | Dashboard lineage UI: click any number, see the facts behind it | core |
| `GRVX-810` | Correctness test suite: determinism, late arrival, retroactive backfill, merge correctness | core |
| `GRVX-811` | SLO engine + error-budget burn-rate alerts computed from facts | core |
| `GRVX-812` | Public "prove it" demo: add p99.9 to historical data, live, in the docs | core |

### Why this beats the alternatives

| | Prometheus/Grafana | Datadog | Gravix after Phase 8 |
|---|---|---|---|
| Add a percentile retroactively | Impossible — raw observation discarded at scrape | Impossible — raw request not stored | `gravix recompute --add-percentile p99.9` |
| Add a dimension retroactively | Impossible | Impossible | `gravix recompute --add-dimension` |
| Fix a mislabelled service historically | Impossible | Impossible | Append a correcting fact, recompute |
| Cross-instance p99 | Invalid unless buckets match exactly | Correct (DDSketch, mergeable) | Exact from facts, or sketch with a stated bound |
| Show which data produced a number | No | Partially | `gravix explain` |

**Scope discipline:** the "impossible" claims are about *retroactive* changes only, and Datadog's
DDSketch column is a genuine strength we concede in public. See `01-competitive-thesis.md` Axis 2.

### Exit criteria

- [ ] `gravix recompute` over any 30-day window is byte-identical to a from-scratch ingestion
- [ ] Every dashboard metric has a published `METRIC CONTRACT`
- [ ] Adding p99.9 and backfilling 30 days is demonstrated in the public docs
- [ ] Late-arriving facts land in their `event_time` bucket, bump a revision, prior values reproducible
- [ ] `gravix explain` returns the exact fact set behind any number
- [ ] 0 undisclosed approximations across all metric contracts

---

## Phase 9 — Zero-Config Value

**Theme:** "Ten minutes, no YAML."
**Goal:** G3. **Effort:** ~7 person-weeks. **Duration:** Month 4–6.
**Loops introduced:** L12 (Adoption).

An open-source tool is judged in the first ten minutes. Horizon 1's quick-start wizard assumed a
signed-up SaaS tenant; a self-hoster starts from `git clone` and a blank screen.

### Deliverables

| ID | Deliverable | Placement |
|---|---|---|
| `GRVX-901` | Single-command boot: `docker compose up` → working stack with synthetic traffic, zero edits | core |
| `GRVX-902` | Service auto-discovery from arriving facts — no registration step | core |
| `GRVX-903` | Auto-generated SLO dashboard per discovered service | core |
| `GRVX-904` | Default alert rules proposed from observed baselines, armed on one click | core |
| `GRVX-905` | Path-template auto-learning with a hard cardinality budget and a clear rejection path | core |
| `GRVX-906` | `gravix doctor` — diagnoses the top 10 setup failures, each with the exact fix command | core |
| `GRVX-907` | Empty states that carry the exact curl command, pre-filled with the real endpoint and key | core |
| `GRVX-908` | First-run countdown: "first rollup completes in ~4 min" instead of a blank chart | core |
| `GRVX-909` | Framework auto-instrumentation recipes: Express, FastAPI, Flask, Django, Gin, Rails | core |
| `GRVX-910` | Timed onboarding test in CI — fails the build if the 10-minute budget regresses | core |

**`GRVX-910` is what makes this phase durable.** A time-to-value target that is not enforced by CI
decays within two releases, because every new required config step looks individually reasonable.

### Exit criteria

- [ ] Median clone → populated dashboard ≤10 min, enforced by CI
- [ ] 0 required config steps before first data
- [ ] `gravix doctor` resolves 10/10 of the known setup failures
- [ ] Every empty state contains a runnable command

---

## Phase 10 — Cost Proof

**Theme:** "Verify it yourself."
**Goal:** G4. **Effort:** ~5 person-weeks. **Duration:** Month 6–7.

Cost is Gravix's loudest claim and currently its least substantiated. `scripts/perf_baseline.json`
exists; a public, reproducible, adversarially-honest benchmark does not.

### Deliverables

| ID | Deliverable | Placement |
|---|---|---|
| `GRVX-1001` | `bench/` harness: one command, fixed dataset generator, stated machine spec | core |
| `GRVX-1002` | Cardinality-immunity demo: high-cardinality field rejected, cost line flat, vs. the priced competitor units | core |
| `GRVX-1003` | Storage-efficiency work: encoding, ZSTD tuning, sketch rollups — target ≤120 bytes/event | core |
| `GRVX-1004` | Cost model + public TCO calculator, bootstrap **and** at-scale figures shown together | core |
| `GRVX-1005` | Ingest throughput optimisation — target ≥20,000 ev/s/core | core |
| `GRVX-1006` | Query performance: pre-aggregation strategy, cache warming, p95 ≤400 ms | core |
| `GRVX-1007` | Public benchmark page, regenerated by CI on every release | core |
| `GRVX-1008` | Third-party reproduction guide: run our benchmark on your hardware | core |

**Honesty requirement (charter-level).** The benchmark publishes where Gravix is **slower or more
expensive**, and the at-scale AWS figures sit beside the $20 VPS figure. A benchmark that only
flatters us will be dismantled publicly within a week, and deservedly.

### Exit criteria

- [ ] An outsider reproduces the headline $/million figure within ±10% in under an hour
- [ ] ≥20,000 ev/s/core, ≤120 bytes/event, ≤400 ms query p95 on the reference box
- [ ] The public page shows unfavourable results alongside favourable ones
- [ ] Every published number carries its harness command and machine spec

---

## Phase 11 — Interop, Not Lock-in

**Theme:** "Fits the stack you already have."
**Goal:** G5. **Effort:** ~8 person-weeks. **Duration:** Month 7–9.

Nobody rips out Prometheus to try an unknown tool. The adoption path is *additive*.

### Deliverables

| ID | Deliverable | Placement |
|---|---|---|
| `GRVX-1101` | Bare-Parquet access guide — query `data/warehouse/` with DuckDB, no Gravix running | core |
| `GRVX-1102` | Prometheus remote-write receiver → facts, with cardinality budget enforcement | core |
| `GRVX-1103` | SQL-vs-PromQL guide + verified Metabase/Superset connection | core |
| `GRVX-1104` | Grafana datasource plugin — keep Grafana, get Gravix correctness | core |
| `GRVX-1105` | OTLP metrics-subset hardening (builds on `services/ingestion/otlp.go`) | core |
| `GRVX-1106` | Apache Iceberg table format, readable by Spark and Trino | core |
| `GRVX-1107` | Export in the free tier: Parquet/CSV/JSONL, manual and scheduled | core |
| `GRVX-1108` | Migration importers: backfill facts from Prometheus TSDB and Datadog metric exports | core |
| `GRVX-1109` | Documented exit path: how to leave Gravix and take everything | core |

**`GRVX-1109` is deliberate.** Publishing the exit path is the cheapest trust-purchase available,
and a project confident in its product loses nothing by it.

**Non-goal check:** ingesting Prometheus remote-write and OTLP **metrics** does not create a
tracing or logs platform. Trace and log signals are rejected at the receiver with a clear message
citing `docs/04-non-goals.md`. The receiver must not become a side door around the constitution.

### Exit criteria

- [ ] Gravix runs alongside an existing Prometheus/Grafana stack with nothing removed
- [ ] Grafana plugin published and installable from the registry
- [ ] Iceberg tables readable by Spark and Trino
- [ ] The documented exit path is tested in CI

---

## Phase 12 — The Community Machine

**Theme:** "Strangers become contributors."
**Goal:** G6. **Effort:** ~6 person-weeks. **Duration:** Month 9–12.

### Deliverables

| ID | Deliverable | Placement |
|---|---|---|
| `GRVX-1201` | Plugin system v2: notifiers, exporters, ingestion adapters — stable ABI, versioned | core |
| `GRVX-1202` | Plugin registry + scaffold generator (`gravix plugin new`) | core |
| `GRVX-1203` | Contribution ladder: contributor → reviewer → maintainer, with published criteria | core |
| `GRVX-1204` | RFC process + `docs/oss/rfcs/` + a public decision log | core |
| `GRVX-1205` | `good first issue` pipeline: every one states the file, the change, the verification | core |
| `GRVX-1206` | Dev-container + one-command dev setup; contributor test suite under 5 minutes | core |
| `GRVX-1207` | Public roadmap board fed by L1, with community voting | core |
| `GRVX-1208` | `ADOPTERS.md` + case studies + monthly community call notes | core |
| `GRVX-1209` | Automated release notes crediting every contributor by name | core |
| `GRVX-1210` | Grant merge rights to ≥3 non-founder maintainers | core |

**`GRVX-1206` is the highest-leverage item.** A contributor test suite over five minutes loses
drive-by contributors, who are most of them.

### Exit criteria

- [ ] ≥30% of merged PRs from outside contributors (30-day rolling)
- [ ] PR queue age p90 ≤5 days
- [ ] ≥5 third-party plugins exist
- [ ] ≥3 non-founder maintainers hold merge rights
- [ ] The project survives its most active contributor taking two months off

---

## Phase 13 — The Pro Upgrade Path

**Theme:** "Paying adds; it never subtracts."
**Goal:** G7. **Effort:** ~12 person-weeks. **Duration:** Month 12–16.
**This is where the money comes from, and where most open-core projects break their charter.**

### Deliverables

| ID | Deliverable | Placement |
|---|---|---|
| `GRVX-1301` | Offline Ed25519 licence verification; no network call, ever | core (verifier) |
| `GRVX-1302` | Extension-point framework: the core interfaces `ee/` registers against | core |
| `GRVX-1303` | Graceful degrade: expiry → `ee/` read-only, core entirely unaffected | `ee/` |
| `GRVX-1304` | `ee/tenancy/` — multi-tenant control plane (migrated from Horizon 1) | `ee/` |
| `GRVX-1305` | `ee/billing/` — metering, Stripe, invoicing, overage | `ee/` |
| `GRVX-1306` | `ee/identity/` — SAML, SCIM, directory sync *(single-org OIDC stays free)* | `ee/` |
| `GRVX-1307` | `ee/fleet/` — manage N installations from one console | `ee/` |
| `GRVX-1308` | `ee/compliance/` — SIEM streaming, retention holds, SOC2 evidence | `ee/` |
| `GRVX-1309` | `ee/intelligence/` — seasonal forecasting, capacity projection | `ee/` |
| `GRVX-1310` | `ee/whitelabel/` — branding, custom domains, embeds | `ee/` |
| `GRVX-1311` | `ee/warehouse/` — continuous Snowflake/BigQuery/Databricks sync | `ee/` |
| `GRVX-1312` | Pro packaging, pricing, and a public "what you get" page listing what stays free | `ee/` |

### The rule that makes this phase safe

Every `ee/` item is built by `pro-engineer`, which **cannot edit core files**. Needing a core hook
returns `EXTENSION POINT REQUIRED`, and the hook is specced and built separately as core.
That handshake is slower, and it is the mechanism that keeps the OSS build genuinely independent
rather than nominally so.

### Exit criteria

- [ ] `make build-oss && make test-oss` still green with `ee/` deleted
- [ ] 0 core files modified by any `ee/` feature
- [ ] Licence expiry demonstrably does not affect ingestion, rollup, alerting or dashboards
- [ ] 0 capabilities moved from core to `ee/`
- [ ] OSS 30-day retention flat or rising while Pro MRR grows *(the G7.4 canary)*
- [ ] Pro MRR ≥$10k

---

## Phase 14 — Gravix Cloud

**Theme:** "Managed, not captured."
**Goal:** G8. **Effort:** ~10 person-weeks. **Duration:** Month 16–20.

### Deliverables

| ID | Deliverable | Placement |
|---|---|---|
| `GRVX-1401` | Cloud on the **same** OSS core + `ee/` — no private fork, verified by build provenance | `ee/` |
| `GRVX-1402` | Bring-your-own-bucket: facts land in the customer's own S3 | `ee/` |
| `GRVX-1403` | One-command cloud → self-host migration | core |
| `GRVX-1404` | One-command self-host → cloud migration | core |
| `GRVX-1405` | Cloud SLA + status page, monitored by Gravix itself | `ee/` |
| `GRVX-1406` | Usage-based pricing with a hard spend cap the customer sets *(the anti-bill-shock feature)* | `ee/` |
| `GRVX-1407` | SOC 2 Type II for the cloud offering | `ee/` |
| `GRVX-1408` | Cloud → OSS feedback loop: every cloud incident becomes an OSS improvement | core |

**`GRVX-1406` earns the Axis-1 claim.** If we criticise unpredictable metered billing and then ship
unpredictable metered billing, the entire competitive thesis collapses. A customer-set hard cap is
the product-level expression of that argument.

### Exit criteria

- [ ] Cloud runs unmodified OSS core, provable from build provenance
- [ ] A customer can leave in an afternoon with all data in open formats
- [ ] BYOB works — customers keep ownership even on cloud
- [ ] 99.9% uptime, measured by our own dogfood Gravix
- [ ] A customer can set a spend cap that is actually enforced

---

## Phase 15 — Sustainability

**Theme:** "Outlives its founder."
**Goal:** G9. **Effort:** ~5 person-weeks. **Duration:** Month 20–24.

### Deliverables

| ID | Deliverable | Placement |
|---|---|---|
| `GRVX-1501` | LTS branch policy — 12-month support window, published | core |
| `GRVX-1502` | Governance maturity: maintainer council, voting, conflict resolution | core |
| `GRVX-1503` | Succession plan for trademark, signing keys, domains, registry accounts | core |
| `GRVX-1504` | Paid support tiers separate from feature licensing | `ee/` |
| `GRVX-1505` | Partner/reseller programme | `ee/` |
| `GRVX-1506` | Foundation-donation evaluation (with an explicit recommendation either way) | core |
| `GRVX-1507` | `CODEOWNERS` ensuring bus factor ≥2 on every critical subsystem | core |
| `GRVX-1508` | Annual charter review + a published transparency report | core |

### Exit criteria

- [ ] ≥3 maintainers with release authority
- [ ] Bus factor ≥2 on every critical subsystem
- [ ] Revenue covers infrastructure + 1 FTE
- [ ] The project ships its next release on schedule if the founder disappears

---

## Investment summary

These figures are **derived from the specs**, not estimated at phase level. Each phase's effort is
the sum of its specs' `Effort` fields, so the roadmap cannot drift from the work actually specified.
Regenerate with `python3 scripts/gen_spec_index.py`.

| Phase | Theme | Duration | Specs | Effort | Goal | Cumulative |
|---|---|---|---|---|---|---|
| 7 | Open the Core | M0–1 | 11 | 16 pd | G1 | 16 pd |
| 8 | Correctness Moat | M1–4 | 12 | 48 pd | G2 | 64 pd |
| 9 | Zero-Config Value | M4–6 | 10 | 41 pd | G3 | 105 pd |
| 10 | Cost Proof | M6–7 | 8 | 29 pd | G4 | 134 pd |
| 11 | Interop | M7–9 | 9 | 43 pd | G5 | 177 pd |
| 12 | Community Machine | M9–12 | 10 | 35 pd | G6 | 212 pd |
| 13 | Pro Upgrade Path | M12–16 | 12 | 77 pd | G7 | 289 pd |
| 14 | Gravix Cloud | M16–20 | 8 | 48 pd | G8 | 337 pd |
| 15 | Sustainability | M20–24 | 8 | 27 pd | G9 | 364 pd |

**88 specs, 364 person-days ≈ 73 person-weeks** — about 17 months for two engineers, or 26 months
for one with the loops carrying the recurring work.

That is ~9% above the phase-level estimate this document originally carried (67 pw). The specs are
the truth; the estimate was optimistic, and Phase 13 is where it was most optimistic — writing the
`ee/` specs surfaced that every one of them needs its own zero-core-files verification and its own
degrade behaviour, which the phase-level number did not account for. Keeping the larger figure
rather than trimming specs to fit it is the point of deriving it.

Phases 7–12 are **entirely Apache-2.0**. The first line of paid code is written in month 12, after
the free product is complete, proven, and adopted. That ordering is the charter made into a
schedule: you cannot degrade a free tier you have not finished building, and you cannot sell a Pro
tier to a community you do not yet have.

---

## Revenue expectation

| Month | Phase | Free installs | Pro MRR | Note |
|---|---|---|---|---|
| 1 | 7 | 10s | $0 | Licence exists; project is legally usable |
| 4 | 8 | 100s | $0 | The correctness demo is the marketing |
| 6 | 9 | 100s | $0 | Ten-minute onboarding compounds |
| 7 | 10 | 500+ | $0 | Benchmark drives technical credibility |
| 9 | 11 | 1,000+ | $0 | Additive adoption alongside Prometheus |
| 12 | 12 | 2,000+ | $0 | Contributors carry part of the roadmap |
| 16 | 13 | 4,000+ | $10k | First paid code, twelve months in |
| 20 | 14 | 6,000+ | $30k | Cloud converts self-hosters who want out of ops |
| 24 | 15 | 10,000+ | $60k | Support and partners layer on |

Deliberately slower than Horizon 1's projection. Open-source adoption compounds and cannot be
rushed; asking for money in month 2 forecloses the compounding entirely.

---

## Phase-to-spec index

| Phase | Spec range | Count | Status |
|---|---|---|---|
| 7 | `GRVX-701` … `GRVX-711` | 11 | specs written |
| 8 | `GRVX-801` … `GRVX-812` | 12 | specs written |
| 9 | `GRVX-901` … `GRVX-910` | 10 | specs written |
| 10 | `GRVX-1001` … `GRVX-1008` | 8 | specs written |
| 11 | `GRVX-1101` … `GRVX-1109` | 9 | specs written |
| 12 | `GRVX-1201` … `GRVX-1210` | 10 | specs written |
| 13 | `GRVX-1301` … `GRVX-1312` | 12 | specs written |
| 14 | `GRVX-1401` … `GRVX-1408` | 8 | specs written |
| 15 | `GRVX-1501` … `GRVX-1508` | 8 | specs written |
| | **Total** | **88** | |

See `specs/SPEC-INDEX.md`.
