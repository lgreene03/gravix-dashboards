# Gravix Goal Tree — Horizon 2

**Status:** Ratified — Horizon 2, Phase 7
**Owner:** `cpo`. **Refreshed by:** L1 monthly, L0 quarterly.
**Rule:** every key result has a **numeric target**, a **named measurement source**, and an
**owning role**. A KR whose source is "we think" is deleted at the next L1 tick.

---

## North Star

> **The number of engineering teams who trust a Gravix number enough to page someone on it.**

Not stars. Not downloads. Not signups. Stars measure curiosity; downloads measure a `docker pull`
that may have been abandoned four minutes later. **Trust is the only thing that converts an
observability tool from a toy into infrastructure**, and the sharpest observable proxy for trust is
whether a team will wake a human up because Gravix said so.

Proxy metric (opt-in only, per charter §7.4): **installs with ≥1 alert rule that has fired and been
acknowledged, still running 30 days later.**

Anti-metric — watched to make sure it does *not* rise: **percentage of users who pay for Pro.**
If OSS→Pro conversion climbs while OSS 30-day retention falls, the free product is being degraded
and charter §7.3 has been violated somewhere. L9 investigates.

---

## Goal tree

```
NORTH STAR — teams who trust a Gravix number enough to page on it
│
├── G1  A stranger can trust the code           ─── Phase 7      ─── owner: license-boundary-auditor
├── G2  A stranger can trust the numbers        ─── Phase 8      ─── owner: semantic-modeler
├── G3  A stranger reaches value in 10 minutes  ─── Phase 9      ─── owner: product-designer
├── G4  A stranger can verify the cost claim    ─── Phase 10     ─── owner: perf-cost-engineer
├── G5  Gravix fits the stack they already have ─── Phase 11     ─── owner: senior-engineering-lead
├── G6  Strangers become contributors           ─── Phase 12     ─── owner: oss-steward
├── G7  Paying does not corrupt the free tier   ─── Phase 13     ─── owner: license-boundary-auditor
├── G8  Managed hosting without lock-in         ─── Phase 14     ─── owner: cpo
└── G9  The project outlives its founder        ─── Phase 15     ─── owner: cpo
```

The order is not arbitrary. **Trust is sequential**: nobody evaluates your cost claim until they
believe your numbers, and nobody believes your numbers until they believe the project is real and
will not be relicensed out from under them. Attempting G4 before G1 produces a benchmark that
nobody reads.

---

## G1 — A stranger can trust the code  *(Phase 7)*

*Nobody adopts infrastructure that might be relicensed, or whose free tier is a trial in disguise.*

| KR | Target | Source | Owner |
|---|---|---|---|
| G1.1 | `make build-oss && make test-oss` green with `ee/` deleted, on every PR | CI job `oss-build` | `license-boundary-auditor` |
| G1.2 | 0 core→`ee/` imports | `make check-boundary` | `license-boundary-auditor` |
| G1.3 | 100% of `ee/` features have a recorded Crippleware Test | `boundary.yaml` | `license-boundary-auditor` |
| G1.4 | `LICENSE`, `ee/LICENSE`, `SECURITY.md`, `CONTRIBUTING.md`, `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `TRADEMARK.md` present and accurate | repo | `oss-steward` |
| G1.5 | 100% of releases signed, with a published SBOM | release artefacts | `security-engineer` |
| G1.6 | Reproducible build: same source → identical binary digest | `make verify-reproducible` | `sre-release-manager` |
| G1.7 | DCO enforced on every PR; **CLA never introduced** | CI check | `oss-steward` |

**Exit:** an outside reviewer can determine, from the repo alone in under 10 minutes, exactly what
is free, what is paid, and what could change later.

---

## G2 — A stranger can trust the numbers  *(Phase 8)*

*This is the differentiator. Everything else is table stakes.*

| KR | Target | Source | Owner |
|---|---|---|---|
| G2.1 | 100% of dashboard metrics have a published `METRIC CONTRACT` | `cube/model/` + docs | `semantic-modeler` |
| G2.2 | `gravix recompute` reproduces any historical window **byte-identically** | `TestRecomputeDeterminism` | `senior-engineer` |
| G2.3 | `gravix explain <metric> <bucket>` returns the exact fact set behind any number | `TestExplainLineage` | `senior-engineer` |
| G2.4 | Percentiles exact, or sketch-based with a **published error bound** | metric contracts | `semantic-modeler` |
| G2.5 | Late data lands in its `event_time` bucket and bumps a revision counter; prior value stays reproducible | `TestLateArrivalRevision` | `senior-engineer` |
| G2.6 | A new percentile or dimension can be added and **backfilled over 30 days** | `TestRetroactiveDimension` | `senior-engineer` |
| G2.7 | 0 undisclosed approximations | L9 audit of metric contracts | `semantic-modeler` |
| G2.8 | Cross-partition aggregation is provably correct or explicitly flagged non-aggregatable | `TestMergeability` | `semantic-modeler` |

**Exit:** for any number on the dashboard, a user can ask "why?" and get the facts, the formula and
a command that reproduces it. **No competitor in the field can do this**, because none of them
kept the raw observation.

---

## G3 — A stranger reaches value in 10 minutes  *(Phase 9)*

| KR | Target | Source | Owner |
|---|---|---|---|
| G3.1 | Median time from `git clone` to a populated dashboard | ≤10 min | timed onboarding test | `product-designer` |
| G3.2 | Required manual config steps before first data | **0** | `TestZeroConfigBoot` | `senior-engineer` |
| G3.3 | Services auto-discovered from arriving facts | 100% | `TestServiceAutoDiscovery` | `senior-engineer` |
| G3.4 | Default SLO dashboard generated per service, no YAML | 100% | `TestDefaultSLOGeneration` | `semantic-modeler` |
| G3.5 | `gravix doctor` diagnoses the top 10 setup failures with the exact fix | 10/10 | `TestDoctorDiagnostics` | `support-engineer` |
| G3.6 | Path templates auto-learned; cardinality budget never exceeded | 100% | `TestCardinalityBudget` | `senior-engineer` |
| G3.7 | Every empty state contains the exact command that fills it | 100% | UI review | `product-designer` |

**Exit:** a stranger with Docker and ten minutes ends with a dashboard showing their own traffic
and one alert rule armed — having edited nothing.

---

## G4 — A stranger can verify the cost claim  *(Phase 10)*

| KR | Target | Source | Owner |
|---|---|---|---|
| G4.1 | Benchmark harness in-repo, runnable by an outsider | 1 command | `bench/run.sh` | `perf-cost-engineer` |
| G4.2 | $/million events (ingest+store+query, 30-day retention) published and reproduced | ±10% of published | `COST REPORT` | `perf-cost-engineer` |
| G4.3 | Ingest throughput on the reference box | ≥20,000 ev/s/core | `bench/results/` | `perf-cost-engineer` |
| G4.4 | Storage after rollup + compaction | ≤120 bytes/event at 30-day retention | `bench/results/` | `perf-cost-engineer` |
| G4.5 | Dashboard query p95, warm cache | ≤400 ms | `bench/results/` | `perf-cost-engineer` |
| G4.6 | TCO calculator published, with the at-scale figures **beside** the bootstrap figures | live page | `docs-engineer` |
| G4.7 | Every published cost number carries its harness command and machine spec | 100% | `CLAIM AUDIT` | `market-analyst` |

**Exit:** a skeptic can reproduce our headline cost number on their own hardware in under an hour,
and finds the at-scale caveat published as prominently as the headline.

---

## G5 — Gravix fits the stack they already have  *(Phase 11)*

| KR | Target | Source | Owner |
|---|---|---|---|
| G5.1 | OTLP metrics-subset ingestion accepted without an SDK change | works | `TestOTLPIngest` | `senior-engineer` |
| G5.2 | Prometheus remote-write accepted and converted to facts | works | `TestRemoteWriteIngest` | `senior-engineer` |
| G5.3 | Grafana datasource plugin — Gravix data in existing Grafana dashboards | published | plugin registry | `frontend-engineer` |
| G5.4 | Iceberg table format readable by external engines | Spark + Trino verified | `TestIcebergInterop` | `senior-engineer` |
| G5.5 | Data readable by `duckdb` with **no Gravix process running** | works | `TestBareParquetRead` | `qa-engineer` |
| G5.6 | Export to Parquet/CSV/JSONL, manual and scheduled, in the **free** tier | 100% | `boundary.yaml` | `license-boundary-auditor` |

**Exit:** Gravix can be added to an existing Prometheus/Grafana stack without removing anything,
and removed again without stranding data.

---

## G6 — Strangers become contributors  *(Phase 12)*

| KR | Target | Source | Owner |
|---|---|---|---|
| G6.1 | External share of merged PRs (30-day rolling) | ≥30% | git log | `oss-steward` |
| G6.2 | First-time contributor PR merge rate | ≥60% | GitHub | `oss-steward` |
| G6.3 | PR queue age p90 | ≤5 days | GitHub | `oss-steward` |
| G6.4 | Issues unanswered >72h | 0 | GitHub | `support-engineer` |
| G6.5 | `good first issue` inventory | ≥15 open / ≥5 unclaimed | GitHub | `oss-steward` |
| G6.6 | Third-party plugins (notifiers, exporters, datasources) | ≥5 | plugin registry | `oss-steward` |
| G6.7 | Contributors with merge rights who are not the founder | ≥3 | `GOVERNANCE.md` | `cpo` |
| G6.8 | Public RFCs decided in the open | 100% of charter changes | `docs/oss/rfcs/` | `cpo` |

**Exit:** the project survives its most active contributor taking a two-month break.

---

## G7 — Paying does not corrupt the free tier  *(Phase 13)*

*The goal that most open-core projects fail. It gets the sharpest instrumentation.*

| KR | Target | Source | Owner |
|---|---|---|---|
| G7.1 | Core capabilities moved to `ee/` after being released open | **0, permanently** | L9 audit | `license-boundary-auditor` |
| G7.2 | Upsell UI elements in the OSS dashboard | **0** | L9 audit | `frontend-engineer` |
| G7.3 | Artificial limits in core (retention/seats/services/throughput) | **0** | L9 audit | `license-boundary-auditor` |
| G7.4 | OSS 30-day retention while Pro revenue grows | flat or rising | opt-in telemetry | `growth-analyst` |
| G7.5 | `ee/` licence expiry impact on core function | **none** | `TestLicenseExpiryDegrade` | `pro-engineer` |
| G7.6 | Network calls required for core operation | **0** | `TestOfflineCore` | `security-engineer` |
| G7.7 | Core files modified by `ee/` feature work | **0** | L9 audit | `license-boundary-auditor` |
| G7.8 | Pro MRR | ≥$10k by Phase 13 exit | billing | `cpo` |

**G7.4 is the canary.** If Pro revenue rises while OSS retention falls, we are extracting rather
than adding. That triggers L0 immediately, regardless of the revenue number.

---

## G8 — Managed hosting without lock-in  *(Phase 14)*

| KR | Target | Source | Owner |
|---|---|---|---|
| G8.1 | Cloud runs the **same** OSS core, no private fork | 100% | build provenance | `sre-release-manager` |
| G8.2 | Bring-your-own-bucket: facts land in the **customer's** S3 | supported | `TestBYOB` | `pro-engineer` |
| G8.3 | Cloud → self-host migration | ≤1 documented command | `TestCloudExport` | `sre-release-manager` |
| G8.4 | Self-host → cloud migration | ≤1 documented command | `TestCloudImport` | `sre-release-manager` |
| G8.5 | Cloud uptime | ≥99.9% | dogfood Gravix | `sre-release-manager` |
| G8.6 | Cloud features unavailable to self-hosters | only multi-tenant ops | `boundary.yaml` | `license-boundary-auditor` |

**Exit:** a cloud customer can leave in an afternoon with all their data in open formats. That is
what makes the cloud worth trusting in the first place.

---

## G9 — The project outlives its founder  *(Phase 15)*

| KR | Target | Source | Owner |
|---|---|---|---|
| G9.1 | Maintainers with release authority | ≥3 | `GOVERNANCE.md` | `cpo` |
| G9.2 | LTS branch with a published support window | 12 months | release policy | `sre-release-manager` |
| G9.3 | Bus factor on any critical subsystem | ≥2 | `CODEOWNERS` | `cpo` |
| G9.4 | Revenue covering full infrastructure + 1 FTE | 100% | billing | `cpo` |
| G9.5 | Public roadmap with community input | published | `docs/oss/` | `oss-steward` |
| G9.6 | Documented succession plan for the trademark and signing keys | published | `GOVERNANCE.md` | `cpo` |

**Exit:** if the founder disappears, the project ships its next release on schedule.

---

## KR dashboard (updated at every L1 tick)

`—` means not yet instrumented. Values are filled in monthly; the prior value is retained so
trajectory is visible without archaeology.

| Goal | KR | Target | Current | Prior | Trajectory |
|---|---|---|---|---|---|
| G1 | build-oss green | 100% | — | — | not started |
| G1 | core→ee imports | 0 | — | — | not started |
| G2 | metrics with contracts | 100% | — | — | not started |
| G2 | recompute determinism | pass | — | — | not started |
| G3 | time to first dashboard | ≤10 min | — | — | not started |
| G3 | required config steps | 0 | — | — | not started |
| G4 | $/million events | published | — | — | not started |
| G4 | ingest ev/s/core | ≥20,000 | — | — | not started |
| G5 | OTLP + remote-write | works | — | — | partial (`services/ingestion/otlp.go`) |
| G6 | external PR share | ≥30% | — | — | not started |
| G7 | features re-gated | 0 | — | — | not started |
| G7 | OSS 30-day retention | flat/rising | — | — | not started |
| G8 | cloud on same core | 100% | — | — | not started |
| G9 | maintainers ≥3 | 3 | 1 | 1 | not started |

---

## Goals deliberately NOT set

Recording rejected goals prevents them being re-proposed every quarter as though they were new.

| Not a goal | Why |
|---|---|
| GitHub stars | Vanity. Correlates with launch-day attention, not usage. |
| Feature count vs Datadog | Charter §4. Parity is forbidden, and losing that race is the default outcome. |
| Sub-minute latency | Non-goal §4. Architectural, not aspirational. |
| Supporting every language | SDKs follow demand. Three cover most of it. |
| Being the only tool a team uses | We are one honest instrument, not a platform. Teams should also run tracing — from someone else. |
| Maximising OSS→Pro conversion | The anti-metric. High conversion with falling OSS retention means the free tier is being degraded. |
| Enterprise logos | Lagging indicator. Chasing it distorts the roadmap toward whoever is loudest. |
