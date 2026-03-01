# API Reference

The Gravix Ingestion Service accepts JSON facts and events via HTTP POST.

## Authentication

All data endpoints require an API key passed in the `X-API-Key` header.

- **Failures**: `401 Unauthorized` if the key is missing or invalid.
- **Comparison**: Constant-time (`crypto/subtle.ConstantTimeCompare`) to prevent timing attacks.
- **Configuration**: Set via the `API_KEY` environment variable.

## Rate Limiting

All data endpoints are rate-limited using a token-bucket algorithm.

| Parameter | Value |
|-----------|-------|
| Rate | 100 requests/second |
| Burst | 200 requests |
| Algorithm | Token-bucket with atomic CAS (lock-free) |

When the limit is exceeded, the server returns:

```
429 Too Many Requests
{"error": "rate limit exceeded, try again later", "code": 429}
```

## Error Response Format

All error responses return JSON:

```json
{
  "error": "description of what went wrong",
  "code": 400
}
```

## Request Size Limit

All endpoints enforce a **1 MB** maximum request body. Requests exceeding this limit receive:

```
413 Request Entity Too Large
{"error": "request body too large (max 1MB)", "code": 413}
```

---

## Data Endpoints

### 1. Ingest Request Fact

Records a single HTTP request event.

**Method**: `POST /api/v1/facts`
**Content-Type**: `application/json`

**Request Body**:

```json
{
  "eventId": "018f3a3b-2c5e-7a1d-8b4e-9f0a2c5b3d4e",
  "eventTime": "2024-05-01T12:00:00Z",
  "service": "auth-service",
  "method": "POST",
  "pathTemplate": "/api/v1/login",
  "statusCode": 200,
  "latencyMs": 125,
  "userAgentFamily": "Chrome"
}
```

**Field Constraints**:

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `eventId` | string | Yes | Must be a valid UUIDv7 |
| `eventTime` | string | Yes | ISO 8601 / RFC 3339 timestamp |
| `service` | string | Yes | Non-empty |
| `method` | string | Yes | Non-empty (e.g., GET, POST, PUT, DELETE) |
| `pathTemplate` | string | Yes | No query parameters (`?`), no raw UUIDs, no numeric IDs with 4+ digits. Use `{id}` placeholders. |
| `statusCode` | integer | Yes | 100 - 599 |
| `latencyMs` | integer | Yes | >= 0 |
| `userAgentFamily` | string | No | Browser/client family (e.g., "Chrome", "Safari") |

**Responses**:

| Status | Meaning |
|--------|---------|
| `201 Created` | Fact persisted to disk (fsync'd). |
| `400 Bad Request` | Validation failure. Body contains `error` field with details. |
| `401 Unauthorized` | Missing or invalid API key. |
| `413 Request Entity Too Large` | Body exceeds 1 MB. |
| `429 Too Many Requests` | Rate limit exceeded. |
| `500 Internal Server Error` | Disk write or marshal failure. |

**Example**:

```bash
curl -X POST http://localhost:8090/api/v1/facts \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "eventId": "018f3a3b-2c5e-7a1d-8b4e-9f0a2c5b3d4e",
    "eventTime": "2024-05-01T12:00:00Z",
    "service": "auth-service",
    "method": "POST",
    "pathTemplate": "/api/v1/login",
    "statusCode": 200,
    "latencyMs": 125
  }'
```

### 2. Ingest Request Facts (Batch)

Records multiple request facts in a single request using JSONL (newline-delimited JSON) format.

**Method**: `POST /api/v1/facts/batch`
**Content-Type**: `application/json`

**Request Body**: One JSON object per line (JSONL format):

```
{"eventId":"018f3a3b-0001-7000-8000-000000000001","eventTime":"2024-05-01T12:00:00Z","service":"auth-service","method":"GET","pathTemplate":"/api/health","statusCode":200,"latencyMs":5}
{"eventId":"018f3a3b-0002-7000-8000-000000000002","eventTime":"2024-05-01T12:00:01Z","service":"auth-service","method":"POST","pathTemplate":"/api/v1/login","statusCode":200,"latencyMs":125}
{"eventId":"invalid-uuid","eventTime":"2024-05-01T12:00:02Z","service":"auth-service","method":"GET","pathTemplate":"/api/health","statusCode":200,"latencyMs":3}
```

Each line is validated independently. Valid facts are persisted; invalid lines are reported as errors.

**Response** (`200 OK`):

```json
{
  "accepted": 2,
  "rejected": 1,
  "errors": [
    "line 3: invalid RequestFact: event_id must be a valid UUIDv7"
  ]
}
```

The `errors` field is omitted when `rejected` is 0. All valid records in a batch are written with a single fsync for efficiency.

| Status | Meaning |
|--------|---------|
| `200 OK` | Batch processed. Check `accepted`/`rejected` counts. |
| `400 Bad Request` | Empty request body. |
| `401 Unauthorized` | Missing or invalid API key. |
| `413 Request Entity Too Large` | Body exceeds 1 MB. |
| `429 Too Many Requests` | Rate limit exceeded. |
| `500 Internal Server Error` | Disk write failure (none of the valid records were persisted). |

### 3. Ingest Service Event

Records a service lifecycle event (deploy, restart, health check, scale event, etc.).

**Method**: `POST /api/v1/events`
**Content-Type**: `application/json`

**Request Body**:

```json
{
  "eventId": "018f3a3b-3d7f-7b2e-9c5a-1a2b3c4d5e6f",
  "eventTime": "2024-05-01T14:30:00Z",
  "service": "payment-service",
  "eventType": "deploy_started",
  "entityId": "deployment/payment-v1.2.3",
  "message": "Rolling update initiated",
  "properties": {
    "version": "v1.2.3",
    "region": "us-east-1",
    "replicas": "3"
  }
}
```

**Field Constraints**:

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `eventId` | string | Yes | Must be a valid UUIDv7 |
| `eventTime` | string | Yes | ISO 8601 / RFC 3339 timestamp |
| `service` | string | Yes | Non-empty |
| `eventType` | string | Yes | Must be snake_case (pattern: `^[a-z]+(_[a-z0-9]+)*$`) |
| `entityId` | string | No | Specific resource identifier |
| `message` | string | No | Human-readable description |
| `properties` | object | No | Flat key-value string pairs. No nested objects. Max 1024 characters per value. |

**Common Event Types**: `deploy_started`, `deploy_completed`, `health_check_failed`, `scale_up`, `scale_down`, `pod_crash`, `restart`

**Responses**:

| Status | Meaning |
|--------|---------|
| `201 Created` | Event persisted to disk (fsync'd). |
| `400 Bad Request` | Validation failure. Body contains `error` field with details. |
| `401 Unauthorized` | Missing or invalid API key. |
| `413 Request Entity Too Large` | Body exceeds 1 MB. |
| `429 Too Many Requests` | Rate limit exceeded. |
| `500 Internal Server Error` | Disk write or marshal failure. |

---

## Health & Observability Endpoints

These endpoints do **not** require authentication or rate limiting.

### 4. Liveness Probe

**Method**: `GET /live`

Returns `200 OK` with body `ok` if the service process is running. Used by Kubernetes liveness probes and Docker health checks.

### 5. Readiness Probe

**Method**: `GET /ready`

Returns `200 OK` with body `ok` if the service is ready to accept traffic. Used by Kubernetes readiness probes and load balancers.

### 6. Prometheus Metrics

**Method**: `GET /metrics`

Returns Prometheus-formatted metrics. Key metrics exposed:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `ingestion_requests_total` | Counter | `path`, `status` | Total ingestion requests by endpoint and status code |
| `ingestion_batch_size_bytes` | Histogram | `topic` | Size of data written to disk per write |
| `ingestion_fsync_duration_seconds` | Histogram | `topic` | Duration of fsync operations |

Standard Go runtime metrics (`go_goroutines`, `go_memstats_*`, `process_*`) are also exposed.

---

## Validation Rules

### Path Template Rules

The `pathTemplate` field prevents high-cardinality explosion. These are **rejected**:

| Example | Reason |
|---------|--------|
| `/api/users/550e8400-e29b-41d4-a716-446655440000` | Raw UUID in path |
| `/api/orders/12345` | Raw numeric ID (4+ digits) |
| `/api/search?q=shoes` | Query parameters not allowed |

Use `{id}` placeholders instead:

| Rejected | Accepted |
|----------|----------|
| `/api/users/550e8400-...` | `/api/users/{id}` |
| `/api/orders/12345` | `/api/orders/{id}` |
| `/api/orders/12345/items/67890` | `/api/orders/{id}/items/{id}` |

### Event ID Rules

The `eventId` must be a UUIDv7 (time-ordered UUID). Standard v4 UUIDs are rejected. UUIDv7 enables efficient deduplication in the rollup pipeline.

### Event Type Rules

The `eventType` field must be snake_case: lowercase letters and digits separated by underscores. Examples: `deploy_started`, `health_check_failed`, `scale_up`.
