# Gravix Open-Core Charter

**Status:** Ratified — Horizon 2, Phase 7
**Supersedes:** nothing. **Amends:** `docs/00-system-truth.md` §7 (new), `docs/04-non-goals.md` §7, `AGENTS.md` (Product section), `CLAUDE.md` (What Is Gravix).
**Owner:** CPO. **Auditor:** License & Boundary Auditor (see `10-agent-roster.md`).

---

## 0. Why this document exists

Gravix was written under a constitution that says two things which are now in tension with the
owner's Horizon 2 directive:

| Existing rule | Source | Horizon 2 directive |
|---|---|---|
| "It is **not** a Datadog replacement and must not attempt feature parity." | `CLAUDE.md`, `AGENTS.md` | "Better than Datadog." |
| "We **WILL NOT** attempt to clone Datadog/NewRelic features … This is a *reporting* system, not an *alerting* system." | `docs/04-non-goals.md` §7 | "Much better than Grafana." |
| Business model: "SaaS managed service." | `PRODUCT_ROADMAP.md` | "Open source, with room for a Pro paid upgrade." |

This charter resolves the tension. It does **not** delete the constraints — it narrows what
"better" is allowed to mean, so that every superiority claim is provable and none of them
require us to become Datadog.

**The resolution, in one sentence:**

> Gravix does not compete on the *number* of things it observes. It competes on whether the
> numbers it reports are **correct, reproducible, owned by you, and billed predictably** — four
> axes on which Datadog and Grafana are structurally, not incidentally, weak.

Feature parity remains forbidden. Superiority on the four axes above is now mandatory.

---

## 1. Amendment to the Constitution: §7 Openness

The following is appended to `docs/00-system-truth.md` as a new invariant section. It has the
same force as the invariants above it.

### 7.1 The core is free software

- The Gravix **core** is licensed **Apache-2.0**. This is irrevocable for any code already
  released under it.
- "Core" means: everything required for one organisation to ingest its own facts, aggregate
  them, query them, alert on them, and operate the system in production, forever, for free,
  with no license key, no phone-home, no seat count, and no expiry.
- The core MUST build, test, run, and pass its full acceptance suite with the `ee/` directory
  **physically deleted from the working tree**. This is enforced in CI (`SPEC GRVX-704`) and is
  the single most important invariant in this charter.

### 7.2 The paid layer is additive, never subtractive

- Commercial code lives **only** under `ee/`, licensed **BUSL-1.1** with a **two-year**
  Change Date to **Apache-2.0**, applied per released version.
- *Why two years and not the BUSL default of four:* Sentry, having used BUSL and then written its
  replacement, published the critique that a four-year Change Date "can make it feel like the
  eventual change to Open Source is only a token effort." That is correct. An `ee/` feature's
  competitive value has decayed long before four years; the credibility is worth more than the
  exclusivity. Evidence and precedents in `01-competitive-thesis.md` §5.
- The Change Licence is stated explicitly as Apache-2.0 in `ee/LICENSE`. It is **not** safe to
  assume "the BUSL conversion licence" — HashiCorp's converts to MPL-2.0, CockroachDB's to
  Apache-2.0. Ambiguity here is how licence disputes start.
- A feature may be placed in `ee/` **only** if it solves a problem that appears when an
  organisation runs Gravix *for other people* or *at organisational scale* — many tenants, many
  installs, many identity providers, many auditors, many invoices.
- A feature MUST NOT be placed in `ee/` if removing it makes the core:
  - report a wrong number,
  - lose data,
  - become unoperable in production,
  - or require a paid tier to satisfy a security or compliance baseline that a single team
    genuinely needs (TLS, authentication, audit logging, backups, retention).

### 7.3 The Crippleware Test

Before any feature is assigned to `ee/`, the License & Boundary Auditor must answer **NO** to all
five questions. A single **YES** forces the feature into the Apache-2.0 core.

1. Would a self-hosting team of 10 engineers, monitoring their own services, notice this is
   missing and consider Gravix incomplete?
2. Does its absence make any number displayed by the core less accurate?
3. Does its absence force the user to accept a worse security posture?
4. Was this capability previously in the Apache-2.0 core? *(Once open, always open. No
   relicensing of shipped functionality — ever.)*
5. Is the only reason to gate it "because people would pay for it"?

Question 5 is the important one. "People would pay for it" is a necessary condition for a
paid feature, never a sufficient one.

### 7.4 No dark patterns

The following are permanently prohibited, in the core and in `ee/`:

- Nag screens, upgrade interstitials, or upsell modals in the OSS dashboard.
- Artificial limits (retention caps, service-count caps, seat caps, ingestion throttles) in the
  core that exist purely to drive upgrades.
- Telemetry that is on by default. Any usage reporting is **opt-in**, documented, shows the
  exact payload, and works when disabled.
- "Open source" branding on anything not under an OSI-approved licence. `ee/` is
  **source-available**, and every document that mentions it must say so.
- Requiring a network call to a Gravix-operated service for the core to start, run, or recover.

### 7.5 Licence-key behaviour (the graceful-degradation rule)

- `ee/` features activate from a **locally verifiable, offline** signed licence token
  (`GRAVIX_LICENSE`). Verification is an Ed25519 signature check against a public key compiled
  into the binary. No network call, ever.
- On expiry, `ee/` features enter **read-only degrade**: existing configuration remains
  readable and exportable, writes are refused with a clear message, and **the core is entirely
  unaffected**. Expiry must never stop ingestion, rollup, alerting, or dashboards.
- A tampered or absent licence is not an error condition. It is the normal state of the OSS
  build, and must produce no warning noise.

---

## 2. The boundary, concretely

The authoritative machine-readable version is `docs/oss/boundary.yaml` (`SPEC GRVX-703`).
This table is the human-readable mirror; if they disagree, the YAML wins and CI fails.

### 2.1 Apache-2.0 core — free forever

| Area | What is free |
|---|---|
| Ingestion | HTTP + OTLP-subset + Prometheus remote-write ingest, fsync durability, DLQ, backpressure, all SDKs |
| Schemas | Protobuf contracts, validation, schema evolution, cardinality budget enforcement |
| Storage | Local disk, S3/MinIO, Parquet, Iceberg tables, compaction, retention, tiering policy *definition* |
| Compute | Rollup jobs, **full recompute/backfill engine**, exact percentiles, late-data revision, lineage |
| Query | DuckDB and Trino engines, Cube semantic layer, SQL access, public metrics API |
| Visualise | Dashboard, SLO views, error budgets, custom dashboards, Grafana datasource plugin |
| Alert | Threshold + burn-rate + statistical-deviation rules, Slack, webhook, PagerDuty, OpsGenie, email |
| Operate | Helm chart, docker-compose, `gravix` CLI, `gravix doctor`, backup/restore, upgrade tooling |
| Secure | TLS, API keys, single-org RBAC (admin/editor/viewer), local audit log, 2FA, rate limiting |
| Automate | Terraform provider, GitHub Action, plugin system, all public APIs |
| Prove | Benchmark harness, TCO calculator, lineage explorer, reproducible-build tooling |

**Note the deliberate inclusions.** RBAC, audit logging, 2FA and TLS are the classic
"open-core paywall" features. They stay free. Charging for the ability to not be breached is
the behaviour this charter exists to prevent.

### 2.2 BUSL-1.1 `ee/` — Pro and Enterprise

| Area | What is paid | Which §7.2 scale problem it solves |
|---|---|---|
| `ee/tenancy/` | Multi-tenant control plane, tenant lifecycle, per-tenant isolation orchestration | Running Gravix *for other people* |
| `ee/billing/` | Metering, Stripe, invoicing, overage, plan enforcement | Charging other people |
| `ee/identity/` | SAML, OIDC federation, SCIM provisioning, directory sync, cross-org SSO | Many identity providers |
| `ee/fleet/` | Managing N Gravix installations from one console, config push, fleet upgrade orchestration | Many installs |
| `ee/compliance/` | Audit-log streaming to SIEM, retention holds, e-discovery export, SOC2 evidence collection | Many auditors |
| `ee/intelligence/` | Seasonal forecasting, capacity projection, multivariate anomaly correlation | Optional, additive analysis |
| `ee/whitelabel/` | Tenant branding, custom domains, embedded white-label dashboards | Reselling |
| `ee/warehouse/` | Continuous sync to Snowflake/BigQuery/Databricks, BI connectors | Enterprise data teams |
| `ee/dr/` | Automated cross-region failover orchestration and drill automation | Multi-region operators |
| `ee/support/` | Not code. SLA, private issue tracker, named engineer. | — |

### 2.3 Borderline cases, decided in advance

These are the arguments that will recur. They are settled here so no future agent relitigates them.

| Feature | Decision | Rationale under §7.3 |
|---|---|---|
| SSO (OIDC) for a single org | **Core** | Q3 = YES. A 10-person team using Google Workspace has a legitimate security need. |
| SAML + SCIM | `ee/` | Q1 = NO. SCIM only matters with an enterprise IdP and headcount churn. |
| Audit log (local, queryable) | **Core** | Q3 = YES. Baseline security. |
| Audit log → Splunk/Datadog SIEM stream | `ee/` | Q1 = NO. Only matters when a compliance team demands it. |
| Anomaly detection (2σ vs trailing baseline) | **Core** | Q4 = YES. Already shipped in Phase 2.5 under Apache terms. Locked open. |
| Seasonal forecasting / capacity projection | `ee/` | Q1/Q2 = NO. Additive analysis, no correctness impact. |
| Unlimited retention | **Core** | Q5 = YES. It is your bucket and your disk. Gating it is rent-seeking. |
| Unlimited services / hosts / seats | **Core** | Q5 = YES. Per-seat pricing on self-hosted OSS is the anti-pattern. |
| High-availability / multi-replica core | **Core** | Q3 = YES. HA is not a luxury. |
| Multi-*tenant* HA control plane | `ee/` | Q1 = NO. |
| Data export (Parquet/CSV/JSONL, manual + scheduled) | **Core** | Q5 = YES. Export is the anti-lock-in guarantee; gating it is hostage-taking. |
| Continuous warehouse sync with schema management | `ee/` | Q1 = NO. Operational convenience for data teams. |

---

## 3. Licensing mechanics

| Path | Licence | Notes |
|---|---|---|
| `/` (everything not listed below) | Apache-2.0 | `LICENSE` |
| `ee/**` | BUSL-1.1, Change Date = release date + **2y**, Change Licence = **Apache-2.0** | `ee/LICENSE` |
| `sdk/**` | Apache-2.0 | Never BUSL. SDKs are the funnel; any friction is self-harm. |
| `docs/**`, `docs-site/**` | CC-BY-4.0 | |
| `proto/**` | Apache-2.0 | Contracts must be freely implementable by third parties. |
| `deploy/**`, `terraform-provider-gravix/**` | Apache-2.0 | |

**Contribution model:** DCO sign-off (`Signed-off-by:`), **not** a CLA.
A CLA would let a future owner relicense contributed code; the DCO cannot. This is a deliberate
constraint on our own future behaviour and is the strongest available signal that §7.1 is real.
Consequence, accepted knowingly: we can never relicense the core. That is the point.

The evidence is unambiguous. Relicensing code the community already built on produced a
foundation-backed fork in three cases out of three — Elastic → OpenSearch, HashiCorp → OpenTofu
(five months from announcement to production fork), Redis → Valkey (days). Confining new terms to
new, additive code in a carved-out directory produced none. See `01-competitive-thesis.md` §5.

**Trademark:** the name "Gravix" and the logo are reserved. Forks may exist and are welcome; they
may not use the name. See `TRADEMARK.md` (`SPEC GRVX-707`).

---

## 4. What we are NOT doing, restated for open-core

Every non-goal in `docs/04-non-goals.md` survives this charter unchanged, with one clarification.

`docs/04-non-goals.md` §7 currently reads "We WILL NOT support complex alerting rules, anomaly
detection, or APM features. This is a *reporting* system, not an *alerting* system."

That sentence is now **partially obsolete** — Phase 2 shipped threshold alerting and 2σ anomaly
detection, and Phase 8 adds burn-rate alerts. §7 is amended to:

> **7. No Feature Parity with Datadog.** We WILL NOT clone Datadog/New Relic's product surface.
> We WILL NOT build APM, profiling, RUM, session replay, or infrastructure metrics.
> Alerting is limited to rules evaluable from **batch-aggregated facts on a ≥1-minute cadence**:
> thresholds, error-budget burn rates, and explainable statistical deviation. Any alerting
> feature requiring sub-minute evaluation, streaming state, or per-request inspection is
> rejected. **Philosophy: this is a reporting system that can page you — not an APM.**

Still forbidden, permanently, at every price point:

- Distributed tracing, span collection, waterfall UIs.
- Log aggregation and log search.
- Host agents and infrastructure metrics.
- Sub-minute dashboards and streaming query engines.
- High-cardinality dimensions (`user_id`, `request_id`, `session_id`, `ip_address`).
- A custom query language.
- RUM, session replay, continuous profiler, CI visibility, security posture management.

If a paying customer demands one of these, the answer is "Gravix is not that product, and here
is the tool that is." Revenue does not purchase an exception to the constitution.

---

## 5. Success conditions for this charter

The charter is working if, at any audit:

| Condition | Measured by | Target |
|---|---|---|
| Core builds without `ee/` | CI job `oss-build` on every PR | 100% green |
| No core package imports `ee/` | `make check-boundary` | 0 violations |
| Paid features solve scale, not function | Crippleware Test recorded per `ee/` feature | 100% documented NO×5 |
| Community trusts the split | External contributors as share of merged PRs | ≥30% by Phase 12 exit |
| No relicensing pressure | DCO-only, no CLA | permanent |
| OSS users are not second class | Median time-to-first-dashboard, OSS install | ≤10 min |

The charter has failed the moment a Gravix user has to pay to get a correct number.

---

## 6. Amendment procedure

This charter may be amended only by:

1. A written RFC in `docs/oss/rfcs/`, open for 14 days of public comment.
2. Explicit approval from the CPO **and** the License & Boundary Auditor.
3. A changelog entry naming what changed and why.

§7.1 (core is Apache-2.0), §7.3 Q4 (once open, always open) and §7.4 (no dark patterns) are
**entrenched**: they may be strengthened, never weakened.
