# Horizon 3 — the goal for the next iteration

**Status:** Set 2026-11-03 at the project owner's request. It replaces nothing in
[`12-goal-tree.md`](12-goal-tree.md): the North Star stays the same, and this document makes it a
number other than zero.
**Owner:** `cpo`. **Reviewed:** at every L1 tick until 2027-05-31.
**Research date:** every external fact below was gathered on **2026-11-03** and carries its source.
Two hosts (`grafana.com`, `businesswire.com`) could not be reached from the environment that did the
research, so the Grafana survey figures come from the search index of its press coverage, not from a
fetch of the report. That is good enough for planning. It is not good enough for a public claim.

---

## 1. The goal

> **By 2027-05-31, ten teams that are not us run Gravix 2.0 for at least 30 days and have paged
> someone on a Gravix alert. They got there on an install a stranger can stand up in ten minutes, and
> every number on it can show its proof.**

The North Star has always been *the number of teams who trust a Gravix number enough to page someone
on it*. Today that number is zero. That is not because Gravix tried to earn trust and failed: nothing
Horizon 2 built has been released, so no stranger has had the chance to try it. Horizon 3 is the
iteration that gives them the chance, and counts what happens.

---

## 2. Where Horizon 2 left the product

Horizon 2 built a great deal, and it proved most of its claims in tests. It did not put any of it in
anyone's hands.

| Fact | Evidence |
|---|---|
| 69 of 88 specs are `done`; 8 `partial`, 7 `planned`, 4 `blocked` | `docs/oss/spec-status.json` |
| **No release since v1.0.0** (2026-03-23). Everything in the changelog's `[Unreleased]` section, which is all of Horizon 2, has never shipped in a tagged release | `CHANGELOG.md`, `docs/oss/releases.json` (empty) |
| **No adopter** has ever been listed | `ADOPTERS.md` |
| **One maintainer.** Every design-tier decision needs two approvals, so RFC 0003 has waited since its window closed on 2026-10-08 | `MAINTAINERS.md`, `docs/oss/open-decisions.md` |
| **The first outside contributor** opened ten pull requests (#53–#62) against the good-first issues on 2026-10-28 and 29. None has been reviewed. At six days, that is past the five-day target in G6.3 | GitHub |
| **29 of 83 findings are a path that had never worked**: an API that never returned a number (F-077), a stack whose dashboard could never load data (F-016), a Helm chart with no tables to read (F-079), a network policy that cut the gateway off (F-083), an image scan that never scanned (F-078) | `docs/oss/findings.md` |
| The full stack runs **19 services**: Trino (a JVM query engine), Cube, MinIO, Postgres, Prometheus, Grafana, the rollups and their init jobs, with four ways to read the same data. The bootstrap stack runs 10 | `docker-compose.yml`, `docker-compose.bootstrap.yml`, `30-technology-review.md` §4 |
| The goal tree's KR dashboard was never filled in | `12-goal-tree.md` |
| **The North Star cannot be observed.** Its proxy is an alert that has fired *and been acknowledged*, and Gravix has no way to acknowledge an alert. A team running it could not tell us the number even if it wanted to | `pkg/gatewaycore` (no acknowledge action) |

The pattern in the findings matters most. Each of those 29 was a component built to its spec and passing
its unit tests, inside a deployment nobody had stood up end to end. Horizon 2 measured progress in specs
completed. A stranger measures it in whether the thing works on their machine.

---

## 3. What changed outside

| What happened | Source | What it means for Gravix |
|---|---|---|
| **Datadog Infinite Cardinality Metrics went GA** (blog dated 2026-06-09): "A metric is now priced by its metric name, not by the number of unique time series created by tag combinations." Datadog pitches it explicitly for agentic querying | [Datadog blog](https://www.datadoghq.com/blog/infinite-cardinality-metrics/), fetched | **Claim C1, cost immune to cardinality, no longer separates Gravix from Datadog.** Datadog now offers a cardinality-insensitive price. C1 must be re-scoped before it appears anywhere, and it can no longer be the headline |
| **Prometheus native histograms are stable** (v3.8). OpenTelemetry exponential histograms map onto them, and Amazon Managed Prometheus added support in June 2026 | [Greptime on Prometheus 3](https://greptime.com/blogs/2026-05-09-prometheus-3-and-greptimedb), [AWS](https://aws.amazon.com/about-aws/whats-new/2026/06/amazon-managed-service-prometheus-native-histograms/) | Bounded-error percentiles are becoming normal. What remains Gravix's alone on Axis 2 is **recomputing history from raw facts**: a new percentile, a new grouping, a corrected label, with the facts behind every number. That holds; "more accurate percentiles" alone does not |
| **Parquet on object storage is becoming the default shape for open telemetry stores**: Parseable, a DuckDB community extension that receives OTLP and writes Parquet or Iceberg, single-binary DuckDB backends, and Iceberg-based stores in the user's own bucket | [Parseable](https://www.parseable.com/docs/introduction), [duckdb otlp extension](https://duckdb.org/community_extensions/extensions/otlp), [ducktel](https://github.com/davidgeorgehope/ducktel), [Siglake](https://siglake.dev/) | **Axis 3, your data in open formats, is now table stakes.** It stays true, and it stops being a reason to choose Gravix |
| **Complexity is the top concern** in Grafana Labs' 2026 survey (38%), ahead of signal-to-noise (34%) and cost (31%). Self-managed teams name complexity most often. Cost is still the top selection criterion, at 65%, for the third year. 1,363 respondents | [press coverage](https://www.hpcwire.com/bigdatawire/this-just-in/grafana-labs-survey-highlights-ai-adoption-cost-pressures-and-complexity-in-observability/), via search index | **A 19-service default install argues against Gravix's own first sentence.** For a self-hoster, simplicity is now the purchase criterion; cost is assumed |
| **OpenTelemetry is in production**: metrics 57%, traces 50%, logs 48% of respondents, same survey | same | The cheapest path for a new team is to point an exporter they already run at Gravix. Today Gravix accepts its own `RequestFact` and a metrics subset, and refuses spans at the door |
| **AI agents now read observability data directly.** Datadog's MCP server went GA on 2026-03-09, and Grafana ships one. A later Grafana release reports **85% of IT leaders comfortable with AI investigating incidents, 50% with it deploying changes**, under the headline "AI adoption is outpacing trust" | [Datadog MCP GA](https://www.barchart.com/story/news/641478/datadog-launches-mcp-server-to-provide-ai-agents-with-secure-real-time-access-to-unified-observability-data), [Grafana release](https://natlawreview.com/press-releases/grafana-labs-survey-ai-adoption-outpacing-trust-enterprises-push-toward), via search index | **The gap is trust, and trust is the North Star.** An agent that cites a number it cannot prove is the failure everyone is worried about. A Gravix number carries its facts, its contract and the command that rebuilds it. Other stores keep raw telemetry too; what they do not keep is the record of which facts made each derived number, and a recompute that reproduces it byte for byte |
| Datadog: Q2 2026 revenue $1.12B, up 36%, but only ~200 net new customers in the quarter, and it disclosed that its largest customer began reducing usage in Q3 | [earnings highlights](https://finance.yahoo.com/markets/stocks/articles/datadog-inc-ddog-q2-2026-231928268.html), secondary | Cost scrutiny is real and buyers are looking. That is an opening for alternatives in general, not for Gravix in particular, until Gravix can be installed |

---

## 4. What Gravix's edge is now

Of the five axes in [`01-competitive-thesis.md`](01-competitive-thesis.md), the market has caught up
on two (cardinality cost against Datadog, open formats) and narrowed a third (percentiles). The other
two, a small fixed hosting cost and plain SQL, are still true and are not Gravix's alone. What is
Gravix's alone is the thing the project was built on:

> **Gravix's numbers can prove themselves.** For any value it shows, Gravix can produce the facts,
> the formula and a command that rebuilds it byte for byte, for a person or for an agent. That
> follows from keeping the raw observation and the lineage of every derived number. A product that
> discarded either cannot add it later for the history it already holds.

Horizon 3 leads with that, and adds the one thing that currently counts against Gravix most:
**simplicity.** An honest instrument that takes a JVM, a semantic layer and a metastore to stand up is
not one a ten-person team will try.

---

## 5. Key results

Every key result has a numeric target, a named source and an owner, as in the goal tree.

| KR | Target | By | Source | Owner |
|---|---|---|---|---|
| **H3.1 Ship** | `v2.0.0` tagged, signed, with SBOM and upgrade notes; then at least one release a month | 2027-01-31 | `docs/oss/releases.json`, release workflow | `sre-release-manager` |
| **H3.2 Every install path proven end to end** | Each documented install (bootstrap Compose, full Compose, Helm with default values, Helm with production values in a kind cluster) boots in CI. Each is asserted to return a non-zero number through every public read path: dashboard query, `/api/v1/metrics`, `/api/v1/percentile`, lineage, export, and an alert that fires. **24 of 24 green**, the Helm production path nightly and the rest on every pull request | 2027-01-15 | CI job | `qa-engineer` |
| **H3.3 One engine by default** | The default install runs no JVM, at most **5 long-running services**, under **1 GB** resident memory, with DuckDB as its only query engine. Trino, Prometheus, Grafana and Iceberg become opt-in profiles | 2027-03-31 (in 2.1) | Compose file; memory measured in `timed-onboarding` | `senior-engineering-lead` |
| **H3.4 Time to first page** | An alert can be acknowledged, from the dashboard or from a link in its notification, and the acknowledgement is recorded. From the install command to an alert that fires on the user's own traffic and is acknowledged: **≤10 minutes** on Compose, **≤20** on Helm | 2027-03-31 | `timed-onboarding`, extended | `product-designer` |
| **H3.5 Proof travels with the number** | 100% of numbers returned by the dashboard and the public API carry a lineage reference that `gravix explain` resolves | 2027-03-31 | contract test | `semantic-modeler` |
| **H3.6 The North Star, counted** | **≥10** independent installs, each running ≥30 days with ≥1 alert rule that has fired and been acknowledged. Counted only from what teams choose to report (charter §7.4: no telemetry): verified `ADOPTERS.md` entries and an opt-in `gravix doctor --report` that sends only the counts this KR needs, and only when run (to be built). Interim: **≥3** | 2027-05-31 (interim 2027-03-31) | `scripts/verify_adopters.sh`, opt-in reports | `growth-analyst` |
| **H3.7 Contributors are answered** | Every first-time contributor's pull request gets a first review within **72 hours**; ≥60% are merged (G6.2); queue age p90 ≤5 days (G6.3). It starts with #53–#62 | now, continuously | GitHub | `oss-steward` |
| **H3.8 A second and third maintainer** | **≥2** maintainers who are not the founder, through the [contribution ladder](contribution-ladder.md) and nothing else | 2027-05-31 | `MAINTAINERS.md` | `cpo` |
| **H3.9 Claims that are still true** | Every claim in the register re-audited against §3 before any announcement: C1 re-scoped against Infinite Cardinality Metrics, Axis 2 against native histograms, Axis 3 against Parquet stores. **0** public claims without a proof artefact | before the 2.0 announcement | thesis §6 | `market-analyst` |

H3.2 comes before H3.1 on purpose. Shipping a release whose install paths have never been booted
would publish the next batch of "never worked" findings to strangers instead of to CI.

H3.8 is not decoration. Two maintainers is what turns the waiting design-tier decisions in §7 from
blocked into decided. Maintainers come from contributors, contributors come from users, and that is
why H3.6 and H3.7 sit in the same iteration.

---

## 6. What Horizon 3 does not do

- **No new `ee/` capability.** GRVX-1304 to 1310 stay `planned`, and G7.8's revenue target is deferred
  until H3.6 reaches its interim. A paid tier with no free users has nothing to protect and no one to
  sell to. This is sequencing, not a change to the charter.
- **No new ingestion protocol**, apart from the decision on OpenTelemetry spans in §7.
- **No new deployment target**, region or values file.
- **No new feature specs** until H3.2 is green. Specs in Horizon 3 serve its key results.
- **Every non-goal stands**: no tracing product, no log search, no agents, nothing sub-minute, no
  high-cardinality dimensions, no query language.

---

## 7. Decisions Horizon 3 needs

| # | Decision | Tier | Recommendation |
|---|---|---|---|
| D1 | **DuckDB as the only engine in the default install**, Trino an opt-in profile (H3.3) | product, with a spec | **Yes.** It is the single largest gap between what Gravix says and what it installs (`30-technology-review.md` §4). The serving views (F-070) stay for the Trino profile |
| D2 | **Accept OpenTelemetry HTTP server spans as `RequestFact`s.** Keep only the fields a fact has; drop trace and span IDs; store no trace and draw no waterfall | design tier: it changes `docs/04-non-goals.md` §1, which today says "We WILL NOT support OpenTelemetry trace ingestion" | **Yes, by RFC.** A fact is exactly an HTTP server span without its trace context, and half of the survey's respondents already run OpenTelemetry traces in production. For them it is a path to data with no code change. It needs two approvals, so it waits on H3.8 |
| D3 | **A read-only MCP endpoint over metrics and `explain`**, where every answer carries its fact count, contract version and reproduce command | design tier: a new public API | **Yes, by RFC**, after H3.5. It is the trust argument in §3 made usable. Also waits on H3.8 |
| D4 | **RFC 0003**, compressing raw facts at rest | design tier | **Accept** when a second maintainer can approve (already recommended in `open-decisions.md`) |
| D5 | **2.0 as the version** | `sre-release-manager` | **Yes.** 1.0.0 carried no licence, and Horizon 2 changed what the project is and how it installs. A major version says so. `sre-release-manager` checks it against the semver policy before tagging |

---

## 8. Sequence

| When | What |
|---|---|
| **Nov–Dec 2026** | Review the ten outside pull requests (H3.7). Build the end-to-end install matrix (H3.2) and fix what it finds: the archive and DLQ store, the iceberg sync's tenants, the Trino workers' catalog. Re-audit the claim register (H3.9). Dry-run the release workflow |
| **Jan 2027** | **v2.0.0** on the current architecture, once H3.2 is green (H3.1). Ship what works first, then simplify |
| **Feb–Mar 2027** | **2.1, with one engine by default** (H3.3). Time to first page (H3.4), lineage on every number (H3.5), the opt-in adopter report, and a launch post that says only what the claim register has proved. **Interim: 3 adopters** |
| **Apr–May 2027** | With two maintainers, decide D2 and D3. **10 adopters at 30 days** (H3.6) |

---

## 9. How we will know this is wrong

- **If 2.0 and a launch produce no adopter by 2027-03-31,** the problem is demand or positioning, not
  the product. L0 meets early and asks the harder question: whether Gravix's value is better delivered
  as a component, its recompute and lineage engine, than as a stack.
- **If adopters arrive and are gone by day 30,** the problem is operating it. H3.3 and H3.4 come
  first, before any new feature.
- **If contributors arrive and leave unanswered,** H3.7 has failed and H3.8 cannot happen. That one is
  entirely within our control, and it is failing now.

---

## Sources

All retrieved 2026-11-03. "Fetched" means the page itself was read. "Search index" means the figure
came from a search engine's index of the page, because the page could not be fetched from the research
environment. Treat those as provisional until someone reads them at source.

- Datadog, *Infinite Cardinality Metrics* — https://www.datadoghq.com/blog/infinite-cardinality-metrics/ — fetched
- Datadog MCP server GA, press release — https://www.barchart.com/story/news/641478/datadog-launches-mcp-server-to-provide-ai-agents-with-secure-real-time-access-to-unified-observability-data — search index
- Datadog Q2 2026 earnings call highlights — https://finance.yahoo.com/markets/stocks/articles/datadog-inc-ddog-q2-2026-231928268.html — search index
- Grafana Labs, Observability Survey 2026 — https://grafana.com/observability-survey/2026/ — not reachable; figures from https://www.hpcwire.com/bigdatawire/this-just-in/grafana-labs-survey-highlights-ai-adoption-cost-pressures-and-complexity-in-observability/ — search index
- Grafana Labs, AI adoption outpacing trust — https://natlawreview.com/press-releases/grafana-labs-survey-ai-adoption-outpacing-trust-enterprises-push-toward — search index
- Prometheus 3 and native histograms — https://greptime.com/blogs/2026-05-09-prometheus-3-and-greptimedb — search index
- Amazon Managed Service for Prometheus, native histograms — https://aws.amazon.com/about-aws/whats-new/2026/06/amazon-managed-service-prometheus-native-histograms/ — search index
- Parseable — https://www.parseable.com/docs/introduction — search index
- DuckDB `otlp` community extension — https://duckdb.org/community_extensions/extensions/otlp — search index
- ducktel — https://github.com/davidgeorgehope/ducktel — search index
- Siglake — https://siglake.dev/ — search index
