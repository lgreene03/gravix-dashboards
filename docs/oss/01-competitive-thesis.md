# Competitive Thesis: What "Better Than Datadog" Is Allowed To Mean

**Status:** Ratified — Horizon 2, Phase 7
**Owner:** `market-analyst` (claim provenance) + `cpo` (positioning)
**Audit cadence:** monthly, via Loop **L11**. Every claim below carries a source and a retrieval date.
**Companion:** `00-open-core-charter.md` §4 (what we still refuse to build)

---

## 0. Provenance warning — read before quoting any number from this file

Every competitor price in this document was gathered from **third-party pricing trackers and
vendor documentation as indexed by search**, not from a live fetch of the vendor's pricing page.
Multiple independent trackers converge on these figures, which is adequate for internal planning.

**It is NOT adequate for a public README.** Before any number here appears in public-facing
material, `market-analyst` must verify it directly against `datadoghq.com/pricing` and
`grafana.com/pricing` and stamp it with a retrieval date. Vendors revise list prices without
notice, and a wrong competitor price is the fastest way to lose the argument.

Retrieval date for everything below: **2026-09-09**. Anything unverifiable is marked
**UNVERIFIED** and must not ship.

---

## 1. The honest framing

Gravix cannot beat Datadog on breadth and must never try. Datadog observes hosts, containers,
traces, logs, profiles, browsers, mobile apps, CI pipelines, cloud security posture, and about
forty other things. Gravix observes **HTTP request facts and service events**. That is the whole
product, permanently, by constitution (`docs/04-non-goals.md`).

So "better than Datadog" cannot mean "more". It means this:

> **On the narrow thing Gravix does, its numbers are more trustworthy, its data is more portable,
> and its bill is more predictable — and those three properties are consequences of architecture,
> not of effort, so they cannot be copied without rebuilding the competitor's product.**

And "much better than Grafana" means something sharper, because here the gap is technical:

> **Prometheus throws away the raw observation at scrape time. Gravix keeps it. Everything
> downstream of that one decision — exact percentiles, retroactive dimensions, correct
> cross-instance aggregation, fixing a mislabelled metric after the fact — follows from it.**

That is the thesis. Five axes below make it concrete. Each states the claim, the artefact that
proves it, **and the strongest counter-argument a vendor sales engineer would make** — because a
claim we cannot defend under pushback is worse than no claim at all.

---

## 2. The five axes

### Axis 1 — Cost is immune to cardinality, by construction

**Claim.** Gravix's cost scales with request *volume* — a flat count. It cannot scale with the
number of unique dimension combinations, because unbounded-cardinality dimensions are rejected at
ingestion (`docs/04-non-goals.md` §5, enforced in `schemas/`). Datadog's most expensive units and
Grafana Cloud's primary unit are both cardinality-driven, so one new label value in a deploy
silently multiplies the bill.

**The mechanism, precisely.**

| Vendor | Unit | Rate | Why it is cardinality-driven |
|---|---|---|---|
| Datadog custom metrics | per 100 **indexed** custom metrics/mo — a unique metric-name × tag-value combination counted hourly, averaged monthly **account-wide** | $5.00/100 (≈$0.05/timeseries/mo) | One new tag value = one new timeseries |
| Datadog indexed spans | per million indexed spans, by retention tier | $1.70/M (15-day) → $2.50/M (30-day) | Retention × volume |
| Datadog log indexing | per million indexed events | $1.70/M (15-day) → $2.50/M | Volume, plus $0.10/GB ingest charged whether indexed or not |
| Grafana Cloud metrics | per **active series** (unique series with ≥1 sample in window) | $19/mo platform + $6.50 per 1,000 series above 10,000 free | One new label value = one new active series |
| Grafana Cloud logs/traces | three stacked GB meters on the same bytes | $0.40/GB write + $0.05/GB process + $0.10/GB/mo retain | Volume, metered continuously with no cap |
| **Gravix** | **events ingested** | flat | A dimension must be declared and bounded before it is accepted |

The failure mode this prevents is specific and common: an engineer adds `pod_hash`, `version`, or
`customer_id` as a tag "temporarily", cardinality multiplies, and nobody finds out until the
invoice. Grafana Cloud overage in particular is not a cutoff — it is continuous metering at the
same rate, so a bad deploy flows straight into the bill in near-real time.

**Proof artefact** (`SPEC GRVX-1002`). A load-generator run that submits a `RequestFact` carrying a
high-cardinality field, showing validation rejects it and the cost line stays flat — paired with a
worked dollar calculation of what that same field would have cost as a Datadog indexed custom
metric and as a Grafana Cloud active series.

**Strongest counter-argument.** *"Datadog ships Metrics-without-Limits and ingest-vs-index
controls; you can tag liberally and index selectively. Solved."* — Fair, and we must concede it.
Our narrower true claim: that control is **manual, per-metric, and applied after the fact**,
whereas Gravix's constraint is **structural and applied before ingestion**. A control you must
remember to configure is not the same as a cost that cannot happen.

Sources: [SigNoz — Datadog pricing breakdown](https://signoz.io/blog/datadog-pricing/) ·
[Datadog — custom metrics billing](https://docs.datadoghq.com/account_management/billing/custom_metrics/) ·
[Datadog — APM billing](https://docs.datadoghq.com/account_management/billing/apm_tracing_profiler/) ·
[CloudZero — Grafana Cloud pricing](https://www.cloudzero.com/blog/grafana-cloud-pricing/) — all retrieved 2026-09-09.

---

### Axis 2 — Exact, recomputable percentiles *(scoped to Prometheus/Grafana — NOT to Datadog)*

**Claim.** Within the retention window, Gravix computes percentiles from immutable raw facts on
demand. You can add p99.9 *after* ingestion, group by a dimension you did not think of at write
time, or fix a mislabelled service name, and get an **exact** answer for historical data.
Prometheus cannot, because the raw observation no longer exists.

**The mechanism, precisely.**

1. **Classic Prometheus histograms discard the observation at scrape time.** Only pre-configured
   bucket counts persist. `histogram_quantile()` then linearly interpolates *within* the bucket
   containing the target quantile, assuming a uniform distribution inside it — an assumption that
   is frequently wrong. A documented case: estimated p95 of **443 ms** where the true value was
   **~320 ms**. That error is unfixable after the fact.
2. **Averaging percentiles across instances is mathematically invalid.** The mean of ten servers'
   p95s is not the fleet p95. The only correct method is to sum bucket rates by `le` and re-run
   `histogram_quantile()` — which requires **every instance to share identical bucket boundaries**.
   A per-service custom bucket layout breaks fleet aggregation entirely.
3. **Native histograms help but do not fix this.** They collapse N bucket series into one sparse,
   dynamically-bucketed series — a real cardinality and resolution win. They do **not** restore the
   raw observation, do **not** make `histogram_quantile()` exact, and do **not** let you add a
   dimension that was never emitted or correct a label that was wrong.
4. **Gravix keeps the fact.** `docs/00-system-truth.md` §4 (Recomputability) is not a nicety — it
   is the mechanism behind this entire axis. Percentiles are derived, disposable, and rebuildable.

**Proof artefact** (`SPEC GRVX-801`, `GRVX-806`). Ingest facts, compute p99, then retroactively add
p99.9 **and** a new `path_template` grouping, recompute the historical window, and show the result
is byte-identical to what a from-scratch ingestion would have produced. `gravix explain` then shows
which facts produced any given number.

**Strongest counter-argument — and it is a good one.** *"Datadog does not use classic Prometheus
histograms. Our `distribution` metric type uses DDSketch, which is mergeable and correctly
re-aggregatable. This problem is already solved on our platform."* — **Correct.** This axis is
therefore scoped **explicitly to Prometheus and Grafana**. Claiming it against Datadog APM
distributions is a factual error and an easy rebuttal. What survives against Datadog is narrower
and still true: DDSketch is mergeable but still **lossy and configured at write time**, and
Datadog cannot add a dimension retroactively either — because it never stored your raw request.
State that, and nothing more.

Sources: [Prometheus — histograms and summaries](https://prometheus.io/docs/practices/histograms/) ·
[LinuxCzar — histograms: a tale of woe](https://linuxczar.net/blog/2017/06/15/prometheus-histogram-2/) ·
[Prometheus — native histograms spec](https://prometheus.io/docs/specs/native_histograms/) ·
[Prometheus blog, 2026-02-14 — modernizing Prometheus](https://prometheus.io/blog/2026/02/14/modernizing-prometheus-composite-samples/) — retrieved 2026-09-09.

---

### Axis 3 — Open-format data with no rehydration tax *(scoped to metrics/APM — NOT to logs)*

**Claim.** Gravix raw facts land in **your** bucket as plain JSONL and Parquet at the moment of
ingestion (`data/raw/`, `data/warehouse/`). There is no export step, because Gravix never held a
walled-garden copy. Any Parquet reader — DuckDB, Trino, pandas, Spark, Metabase — already has full
access at zero marginal cost.

**The mechanism, precisely.**

- **Datadog metrics and APM data have no customer-owned raw archive path at all.** That data lives
  in Datadog's index in non-portable form. Log export via the API is capped at 100,000 logs per
  CSV request.
- **Datadog Log Archives to your S3 exist and are genuinely customer-owned** — but they are in
  Datadog's compressed format, and returning them to a searchable state costs **rehydration**:
  $0.10/GB scanned **plus** the normal per-million indexing rate for whatever comes back.
- **Grafana Cloud Logs Export** syncs Loki data to your bucket — and is explicitly marketed against
  lock-in — but in Loki's compressed **chunk** format, which needs Loki-compatible tooling to read.
  No equivalent documented bulk export was found for metrics or traces; Grafana's own guidance for
  those is to dual-write via OpenTelemetry to your own storage instead.
- **UNVERIFIED:** exact contractual post-cancellation retention and export windows for either
  vendor. This needs current ToS/DPA text and must not be claimed until read.

**Proof artefact** (`SPEC GRVX-1101`). A README recipe running unmodified `duckdb` against
`data/warehouse/*.parquet` with no Gravix process running at all — the strongest possible
demonstration that the data is not hostage to the tool.

**Strongest counter-argument.** *"Our Log Archives already go to your S3 in your account. This is
not unique."* — **Correct for logs.** Scope the claim to **metrics and APM data**, where no S3
archive equivalent exists, and to the **absence of a rehydration fee**. Overclaiming here is
unnecessary; the narrow version is strong enough.

Sources: [Datadog — export logs](https://docs.datadoghq.com/logs/explorer/export/) ·
[Grafana Labs — Cloud Logs Export](https://grafana.com/docs/grafana-cloud/send-data/logs/export/) — retrieved 2026-09-09.

---

### Axis 4 — A hosting cost ceiling you know in advance *(MVP-stage claim only)*

**Claim.** The bootstrap stack (`docker-compose.bootstrap.yml`) runs the full
ingestion → rollup → dashboard pipeline in **~800 MB RAM** for **under $20/month** on one VPS —
$0 on Oracle Cloud's always-free ARM tier, ~$4/mo on a Hetzner CX22. That number is knowable before
you start and does not move with traffic shape, cardinality, or dimension count.

**Proof artefact** (`SPEC GRVX-1001`). A documented load test on a $6/mo Hetzner CX22 **and** a $0
Oracle free-tier instance, reporting sustained events/sec, resident memory, and query latency, with
the harness committed to `bench/` so a stranger can rerun it.

**Strongest counter-argument — the strongest on this entire page.** *"Self-hosting moves cost from
a metered SaaS fee to engineering time. You now own uptime, patching, on-call, and scaling, with no
SLA. Your own roadmap says you migrate to $164–527/mo AWS EKS at scale."* — **Entirely correct.**
This claim ships with a mandatory qualifier: it is an **early-stage and small-team** claim, never
an at-scale one. `PRODUCT_ROADMAP.md` documents the EKS migration path, and the public benchmark
page must show the at-scale figures beside the bootstrap figures. Publishing the $20 number without
the $200 number would be the kind of half-truth that costs more credibility than it buys attention.

---

### Axis 5 — No query language to learn, and no lock-in

**Claim.** All Gravix data is reachable through standard **SQL** (DuckDB or Trino) plus a Cube
semantic layer. There is no PromQL, no LogQL, no proprietary DSL — by constitution
(`docs/04-non-goals.md` §6). Any SQL-literate analyst and any off-the-shelf BI tool queries Gravix
directly, and the skill transfers out of Gravix as easily as into it.

**Proof artefact** (`SPEC GRVX-1103`). A side-by-side of "p95 latency by service, last 7 days" in
PromQL and in the exact SQL Gravix runs, plus Metabase connected straight to the DuckDB/Trino
endpoint with no Gravix component in the path.

**Strongest counter-argument.** *"Nobody hand-writes PromQL daily — the UI abstracts it. And our
query language correlates metrics, traces and logs in one expression, which flat SQL over one fact
table cannot express."* — Fair. So the honest claim is **"nothing new to learn, and nothing to
unlearn"** — *not* "more expressive". A single denormalised fact table genuinely does not give
cross-signal correlation, and we do not have traces or logs to correlate with anyway.

---

## 3. What we are worse at — published, in public, in the same document

This section is mandatory and may never be removed. `market-analyst` verifies it is still accurate
at every audit. It exists for two reasons: it is true, and it is the most persuasive content on
the page. A comparison that admits nothing is read as marketing and discounted entirely.

| Where Gravix loses | To whom | Why we accept it |
|---|---|---|
| No distributed tracing | Datadog, Grafana Tempo, SigNoz | Non-goal §1. Use them; we will not compete. |
| No log search | Datadog, Loki, Elastic | Non-goal §2. |
| No infrastructure or host metrics | Datadog, Netdata, Prometheus node-exporter | Non-goal §3. No agents. |
| No sub-minute alerting | Everyone | Non-goal §4. 5–15 min visibility is the design. |
| No per-request lookup | Datadog APM, any tracing tool | Non-goal §5. Bounded cardinality forbids it. |
| No RUM, profiling, session replay, CI visibility, CSPM | Datadog | Non-goal §7. |
| No breadth of integrations | Datadog (~900), Grafana | We have HTTP ingestion and a handful of notifiers. |
| No enterprise support org | Datadog, Grafana Labs | Pro tier offers an SLA; it is not a 24/7 global TAM org. |
| Self-hosting means you carry the pager | All SaaS | Gravix Cloud (Phase 14) is the answer for those who would rather not. |

**If a user needs something in this table, the correct answer is to name the tool that does it.**
Recommending a competitor costs one user and buys the credibility that keeps a hundred.

---

## 4. Positioning against the "cheap observability" field

These are the products Gravix actually competes with for a self-hoster's attention. Datadog and
Grafana are the reference points; these are the alternatives.

| Product | Licence | What is gated | Their weakness | Our differentiator |
|---|---|---|---|---|
| **SigNoz** | Core MIT; `ee/` + `cmd/enterprise/` proprietary (source-available) | SSO/SAML, enterprise features | You operate ClickHouse yourself — schema, compaction, scaling. Active unresolved "is this really open source" community debate. | No database to operate: DuckDB embedded, or plain Parquet in your bucket. Our boundary rules are published and audited. |
| **Uptrace** | Open edition + BSL-style Pro with delayed conversion | Pro-tier features by edition | Smaller ecosystem; same ClickHouse operational burden. | Same. |
| **Coroot** | Apache-2.0, no enterprise carve-out found | UNVERIFIED | eBPF zero-instrumentation **is an agent** — kernel-level, elevated privileges, kernel-version-coupled, Linux-only. | No agents at all. An HTTP POST works on any runtime, including serverless. |
| **HyperDX / ClickStack** | MIT; acquired by ClickHouse Inc. 2026 | Usage-based cloud tiers | No dedicated branded SaaS/SLA post-acquisition; needs ClickHouse literacy; OTel-only. | Runs on a $4 VPS with no database expertise. |
| **OpenObserve** | **Relicensed Apache-2.0 → AGPL-3.0, Nov 2023** | SSO, RBAC, rate limiting — **not in the public repo at all**, closed private crates | AGPL creates real legal-review friction. Paid features are invisible, not merely delayed. | Apache-2.0 core that **cannot** be relicensed (DCO, no CLA). RBAC and rate limiting are permanently free. `ee/` is public, readable source. |
| **Netdata** | Agent GPLv3+; **Cloud UI proprietary (NCUL1, not OSI-approved)** | Multi-node visualisation, support | Per-host agent model — the architectural opposite of Gravix. | No agents; and our dashboard is Apache-2.0, not a proprietary UI over free collectors. |

Two of these — OpenObserve gating RBAC out of the public repo, and Netdata's proprietary UI over a
GPL agent — are precisely the patterns charter §7.3 Q3 and §2.1 exist to prevent. They are useful
public evidence that our boundary is stricter than the field's, not merely differently drawn.

Sources: [SigNoz ee/LICENSE](https://github.com/SigNoz/signoz/blob/develop/ee/LICENSE) ·
[Uptrace editions](https://uptrace.dev/editions) · [Coroot](https://github.com/coroot/coroot) ·
[ClickHouse acquires HyperDX](https://clickhouse.com/blog/clickhouse-acquires-hyperdx-the-future-of-open-source-observability) ·
[OpenObserve FAQs](https://openobserve.ai/faqs/) · [Netdata open source](https://www.netdata.cloud/open-source/) — retrieved 2026-09-09.

---

## 5. Licence-strategy evidence (why `ee/` and why a short Change Date)

This determined the charter's licensing decision, so the evidence is recorded here.

| Company | Move | Outcome |
|---|---|---|
| **Elastic** | Apache-2.0 → SSPL, Jan 2021 | AWS forked 7.10.2 → **OpenSearch**, now Linux-Foundation-governed (400+ orgs, 3,300+ contributors by Sept 2024). Reversed to add AGPLv3 in Sept 2024; many developers publicly say they are not returning. |
| **HashiCorp** | MPL-2.0 → BUSL-1.1, Aug 2023 | Fastest backlash on record: OpenTF manifesto 33,000+ stars within a month; fork Aug 25; Linux Foundation accepted **OpenTofu** Sept 20; 1.0 shipped Jan 2024 — five months announcement to production fork. Later sent OpenTofu a cease-and-desist. BUSL converts to **MPL-2.0**, not Apache-2.0. |
| **Redis** | BSD-3 → SSPL, Mar 2024 | **Valkey** forked within days, backed by AWS, Google Cloud, Oracle, Ericsson, Snap. >70% of surveyed users cited the licence as a reason to evaluate alternatives. Reversed to AGPLv3, May 2025. Migrated users largely stayed migrated. |
| **Grafana Labs** | Apache-2.0 → AGPLv3, Apr 2021 | Landed on corporate banned-licence lists, but stayed **OSI-approved** and avoided SSPL/BSL. **The only case in this table with no consequential fork.** |
| **Sentry** | BSD-3 → BUSL-1.1 (2019) → invented **FSL** (Nov 2023) | Sentry's own published critique of BUSL: a 4-year Change Date is "a really long time… can make it feel like the eventual change to Open Source is only a token effort", and BUSL has "too many parameters" — inconsistent Additional Use Grants across MariaDB/Redis/HashiCorp create legal ambiguity. FSL standardises a **2-year** Change Date to Apache-2.0 or MIT. |
| **CockroachDB** | BUSL-1.1 since Oct 2019, per-version conversion to **Apache-2.0** | The reference `ee/`-style precedent. No consequential fork. |

**The pattern, stated as our own synthesis rather than as a cited industry consensus** (no neutral
survey quantifying a consensus on this specific pattern was found — **UNVERIFIED** as an external
claim):

> Relicensing **existing** code the community already built on provoked a foundation-backed fork in
> **three cases out of three** (Elastic, HashiCorp, Redis). Confining new terms to **new, additive
> code in a carved-out directory**, while the historical core stays permanently permissive
> (SigNoz, CockroachDB), produced **no consequential fork** in the record examined.

Three decisions follow, and they are now charter law:

1. **`ee/` carve-out, never a whole-repo relicence.** Charter §7.3 Q4 — once open, always open.
2. **A 2-year Change Date, not 4.** Sentry's critique is correct: four years reads as a token
   gesture, and an `ee/` feature's competitive value has decayed long before then anyway. We take
   the credibility instead.
3. **DCO, never a CLA.** A CLA would let a future owner do what Elastic, HashiCorp and Redis did.
   Not having one is a binding constraint on our own future behaviour, and the only credible proof
   that §7.1 means something.

Sources: [Grafana relicensing](https://grafana.com/blog/grafana-loki-tempo-relicensing-to-agplv3/) ·
[Elastic licensing FAQ](https://www.elastic.co/pricing/faq/licensing) ·
[OpenSearch to Linux Foundation](https://techcrunch.com/2024/09/16/aws-brings-opensearch-under-linux-foundation-umbrella) ·
[OpenTofu manifesto](https://opentofu.org/manifesto/) ·
[Sentry — introducing FSL](https://blog.sentry.io/introducing-the-functional-source-license-freedom-without-free-riding) ·
[Redis AGPLv3](https://redis.io/blog/agplv3/) ·
[BUSL](https://en.wikipedia.org/wiki/Business_Source_License) — retrieved 2026-09-09.

---

## 6. Claim register — what `market-analyst` audits monthly

| ID | Claim | Scope limit | Proof artefact | Status |
|---|---|---|---|---|
| C1 | Cost immune to cardinality | Concede Metrics-without-Limits exists; ours is structural not manual | `GRVX-1002` | Pending proof |
| C2 | Exact recomputable percentiles | **Prometheus/Grafana only.** vs Datadog: only "cannot add a dimension retroactively" | `scripts/prove_it.sh` | **Proven** — run it yourself, ~10s, no Docker |
| C3 | Open-format data, no rehydration tax | **Metrics/APM only.** Datadog log archives are customer-owned | `GRVX-1101` | Pending proof |
| C4 | Under $20/mo hosting | **MVP-stage only.** Must publish at-scale figures alongside | `GRVX-1001` | Pending proof |
| C5 | No query language lock-in | "Nothing to learn", **not** "more expressive" | `GRVX-1103` | Pending proof |
| C6 | 20× cheaper per million events | Must be a reproducible benchmark or it does not ship | `GRVX-1004` | Not yet claimed |

**No claim in this register may appear in public material until its proof artefact exists and is
runnable by a stranger.** Until then this document is internal positioning, not marketing copy.
That is the difference between a thesis and a press release.
