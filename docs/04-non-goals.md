# Non-Goals (MVP)

This document explicitly lists features and capabilities that **WILL NOT** be built.
Requests for these features MUST be rejected.

Each non-goal names **an alternative tool that does do the thing**. That is not a courtesy: declining
a request without saying where to go leaves someone stuck, and `GOVERNANCE.md` already promises to
"say so kindly, cite the section, and name the tool that does do it". `scripts/gen_roadmap_board.py`
fails the build if any non-goal on this page names none.

## 1. No Distributed Tracing

- We **WILL NOT** implement span collection, parent/child context propagation, or waterfall visualizations.
- We **WILL NOT** support OpenTelemetry trace ingestion.
- **Alternative**: Use structured `ServiceEvent`s to log key milestones if needed, correlated by a shared ID in `properties` (at low volume only).
- **Alternative tool**: **Jaeger** or **Grafana Tempo** if you self-host, **Honeycomb** if you would rather not. Tracing is a genuinely hard problem that these solve well, and half-solving it here would help nobody.

## 2. No Log Aggregation

- We **WILL NOT** build a log search engine (like ELK/Splunk).
- We **WILL NOT** ingest raw text logs or `stdout`/`stderr` streams.
- **Alternative**: `ServiceEvent`s can capture *structured* business events, but never debug logs.
- **Alternative tool**: **Grafana Loki** if you want something small, **OpenSearch** or **Elasticsearch** if you want search. Logs and metrics have different storage economics, and pretending otherwise is how observability bills get surprising.

## 3. No Agents

- We **WILL NOT** build, distribute, or support sidecar agents or host-level daemons.
- We **WILL NOT** collect system metrics (CPU, Memory, Disk) from hosts.
- **Design logic**: Integration must be via simple HTTP libraries/clients only.
- **Alternative tool**: **Prometheus node_exporter** for host metrics, or **Telegraf** if you want an agent that collects many things. Both do this properly and neither needs us.

## 4. No Real-Time Dashboards

- We **WILL NOT** support sub-minute latency.
- We **WILL NOT** support streaming query engines.
- **Constraint**: Data visibility latency of 5-15 minutes is acceptable and expected.
- **Alternative tool**: **Prometheus** with a short scrape interval, or **Grafana Live** for a streaming panel. Sub-minute is an architecture, not a setting, and theirs is built for it.

## 5. No High-Cardinality Dimensions

- We **WILL NOT** index `user_id`, `request_id`, `session_id`, or `ip_address` as dimensions.
- **Constraint**: All dimension columns must have bounded cardinality (e.g., < 1000 unique values per day).
- **Alternative tool**: **ClickHouse** if you genuinely need per-user or per-request dimensions at scale. It is built for that cardinality and priced for it; we are neither.

## 6. No Custom Query Language

- We **WILL NOT** invent a Domain Specific Language (DSL) like PromQL or LogQL.
- **Constraint**: All data access is via standard SQL only.
- **Alternative tool**: **Trino**, **DuckDB** or any SQL client, pointed at `data/warehouse/`. The files are plain Parquet and readable with no Gravix process running — see [Bare-Parquet access](../docs-site/docs/bare-parquet-access.md).

## 7. No Feature Parity with Datadog

- We **WILL NOT** attempt to clone Datadog/NewRelic features.
- We **WILL NOT** support complex alerting rules, anomaly detection, or APM features.
- **Philosophy**: This is a *reporting* system, not an *alerting* system.
- **Alternative tool**: **Datadog**, **New Relic** or **Grafana Cloud**, and we mean that without irony. If you need the full platform, buy the full platform; Gravix is one honest instrument, and running both is a reasonable thing to do.
