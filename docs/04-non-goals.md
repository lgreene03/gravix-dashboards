# Non-Goals (MVP)

This document explicitly lists features and capabilities that **WILL NOT** be built.
Requests for these features MUST be rejected.

## 1. No Distributed Tracing

- We **WILL NOT** implement span collection, parent/child context propagation, or waterfall visualizations.
- We **WILL NOT** support OpenTelemetry trace ingestion.
- **Alternative**: Use structured `ServiceEvent`s to log key milestones if needed, correlated by a shared ID in `properties` (at low volume only).

## 2. No Log Aggregation

- We **WILL NOT** build a log search engine (like ELK/Splunk).
- We **WILL NOT** ingest raw text logs or `stdout`/`stderr` streams.
- **Alternative**: `ServiceEvent`s can capture *structured* business events, but never debug logs.

## 3. No Agents

- We **WILL NOT** build, distribute, or support sidecar agents or host-level daemons.
- We **WILL NOT** collect system metrics (CPU, Memory, Disk) from hosts.
- **Design logic**: Integration must be via simple HTTP libraries/clients only.

## 4. No Real-Time Dashboards

- We **WILL NOT** support sub-minute latency.
- We **WILL NOT** support streaming query engines.
- **Constraint**: Data visibility latency of 5-15 minutes is acceptable and expected.

## 5. No High-Cardinality Dimensions

- We **WILL NOT** index `user_id`, `request_id`, `session_id`, or `ip_address` as dimensions.
- **Constraint**: All dimension columns must have bounded cardinality (e.g., < 1000 unique values per day).

## 6. No Custom Query Language

- We **WILL NOT** invent a Domain Specific Language (DSL) like PromQL or LogQL.
- **Constraint**: All data access is via standard SQL only.

## 7. No Feature Parity with Datadog

- We **WILL NOT** attempt to clone Datadog/NewRelic features.
- We **WILL NOT** support complex alerting rules, anomaly detection, or APM features.
- **Philosophy**: This is a *reporting* system, not an *alerting* system.
