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
