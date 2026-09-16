# Facts and Events Schemas (MVP)

This document defines the strictly enforced schemas for data ingestion.
Any data not conforming to these schemas MUST be rejected at the edge.

## 1. RequestFact

A `RequestFact` represents the completion of an HTTP request served by a service.

### Fields

| Field Name | Type | Nullable | Description |
| :--- | :--- | :--- | :--- |
| `event_time` | `TIMESTAMP` | NO | UTC timestamp when the request completed. |
| `service` | `STRING` | NO | Service name (e.g., `payment-service`). |
| `method` | `STRING` | NO | HTTP method (e.g., `GET`, `POST`). |
| `path_template` | `STRING` | NO | **Low-cardinality** route pattern (e.g., `/users/{id}`). |
| `status_code` | `INTEGER` | NO | HTTP status code (e.g., `200`, `500`). |
| `latency_ms` | `INTEGER` | NO | Request duration in milliseconds. |
| `user_agent_family` | `STRING` | YES | Broad category (e.g., `Chrome`, `Curl`, `Bot`). |

### Constraints

- **NO Query Parameters**: `path_template` must not contain query strings.
- **NO High Cardinality Paths**: `path_template` must be the route definition, NOT the raw URL.
  - REJECT: `/users/12345`
  - ACCEPT: `/users/{id}`
- **NO Headers/Body**: Request/response bodies and headers are strictly forbidden.

### Examples

**Accepted:**

```json
{
  "event_time": "2023-10-27T10:00:00Z",
  "service": "auth-service",
  "method": "POST",
  "path_template": "/login",
  "status_code": 200,
  "latency_ms": 45,
  "user_agent_family": "Firefox"
}
```

**Rejected (High Cardinality Path):**

```json
{
  "path_template": "/users/alice" 
}
```

**Rejected (Raw Data):**

```json
{
  "body": "{\"username\": \"alice\"}"
}
```

---

## 2. ServiceEvent

A `ServiceEvent` represents a significant business or state change event.

### Fields

| Field Name | Type | Nullable | Description |
| :--- | :--- | :--- | :--- |
| `event_time` | `TIMESTAMP` | NO | UTC timestamp when the event occurred. |
| `service` | `STRING` | NO | Service name emitting the event. |
| `event_type` | `STRING` | NO | Snake_case event name (e.g., `order_placed`). |
| `entity_id` | `STRING` | YES | Primary entity ID involved (e.g., `order_123`). |
| `properties` | `MAP<STRING, STRING>` | YES | Flat key-value pairs of context. |

### Constraints

- **Low Cardinality `event_type`**: Must be a defined event class, not a dynamic string.
- **Flat Properties**: Nested JSON objects in `properties` are forbidden.
- **No Large Payloads**: `properties` implies small context, not full state dumps.

### Examples

**Accepted:**

```json
{
  "event_time": "2023-10-27T10:05:00Z",
  "service": "order-service",
  "event_type": "order_shipped",
  "entity_id": "ord-789",
  "properties": {
    "carrier": "FedEx",
    "priority": "high"
  }
}
```

**Rejected (Nested JSON):**

```json
{
  "properties": {
    "details": { "address": "123 Main St" }
  }
}
```

**Rejected (Log Message):**

```json
{
  "event_type": "Error: NullPointerException at Service.java:50"
}
```

## 3. Late-arriving facts

A fact's `event_time` is the only source of truth for ordering
([`00-system-truth.md`](00-system-truth.md) §5). A fact that arrives an hour after it happened belongs
to the bucket its `event_time` names, never to the bucket it turned up in — and the batch it arrived
in may well be filed under a different day, because a delivery just after midnight describes the day
before.

Late is not invalid. Every fact below is accepted and stored; lateness only decides what has to happen
to the derivatives.

### 3.1 Lateness classes

| Class | When | What happens to derivatives |
|---|---|---|
| `on_time` | within one rollup interval (default 5m) of `event_time` | nothing — the bucket has not been built yet |
| `late` | after its bucket was rolled up, within retention | the partition is rebuilt and its revision increments |
| `very_late` | more than 24h after `event_time`, within retention | same as `late`; the separate band exists because a sustained rise here usually means a sender is buffering or a clock is wrong |
| `unprocessable` | `event_time` precedes the 30-day retention window | **none can be built** — see §3.3 |

A fact whose `event_time` is in the *future* is classified `on_time`: its bucket has not been built
either. If it is more than one rollup interval ahead, the ingestion service logs a wrong-clock warning
at most once a minute. The warning never changes the classification or the acceptance.

The counter `gravix_facts_received_by_lateness_total{class,service}` reports the split. Both labels are
bounded — `class` has exactly the four values above — and nothing per-fact may be added to it.
[`04-non-goals.md`](04-non-goals.md) forbids high cardinality in Gravix's own telemetry as firmly as in
the product.

### 3.2 Revisions

When a late fact changes a partition's rows, that partition's manifest records the change:

| Field | Meaning |
|---|---|
| `revision` | increments by one each time the published rows change; `0` means never revised |
| `previous_digest` | the content digest this partition held before the most recent revision |
| `revised_at` | RFC3339 UTC time of that revision |

A non-zero `revision` is a factual statement that a number changed after it was first published. It is
not an error and is not logged as one: late data is the system working as designed. What would be a
defect is a value that changed with nothing to show for it.

`revised_at` is the only wall-clock value in a manifest and is deliberately **excluded from
`content_digest`**, which covers row content only. Recording *when* a revision happened must not make
the output non-reproducible.

The superseded value stays reproducible in the sense that matters: rebuild from the facts as they were
— without the late arrival — and the original digest comes back. Gravix does not archive old bytes; it
keeps the facts that produced them.

### 3.3 Unprocessable facts

A fact whose `event_time` falls before the retention window cannot reach any metric: the partition it
belongs to has already been purged, and facts are never edited to fit ([`00-system-truth.md`](00-system-truth.md) §2).

It is still **accepted and stored** — it is a valid, immutable fact and storage is cheap — and it is
also copied to `dlq/<tenant>/unprocessable/` so an operator can find it. The response says so plainly:

```
202 Accepted
{"accepted":1,"note":"event_time precedes the retention window; stored but not aggregated"}
```

A batch containing any such fact returns `202` rather than `200`, with an `unprocessable` count.

Accepting data that will never appear in a metric, and saying nothing, would be the worst option
available: the sender would have no way of knowing its data had vanished.
