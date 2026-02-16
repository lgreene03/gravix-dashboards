# API Reference

The Gravix Ingestion Service accepts JSON facts via HTTP POST.

## Authentication

All requests **require** an API Key passed in the `X-API-Key` header.

- **Failures**: `401 Unauthorized` if invalid or missing.
- **Env Var**: The server key is set via `API_KEY` (in `docker-compose.yml`).

## Endpoints

### 1. Ingest Request Fact

Records a single HTTP request event.

**Method**: `POST /api/v1/facts`
**Content-Type**: `application/json`

**Request Body Config**:

```json
{
  "event_id": "018f3a3b-2c5e-7a1d-8b4e-9f0a2c5b3d4e", // UUIDv7 (Required)
  "event_time": "2024-05-01T12:00:00Z",             // ISO 8601 (Required)
  "service": "auth-service",                        // Service Name (Required)
  "method": "POST",                                 // HTTP Method (Required)
  "path_template": "/api/v1/login",                 // Route Template (Required)
  "status_code": 200,                               // HTTP Status Code (Required, 100-599)
  "latency_ms": 125,                                // Latency in ms (Required, Non-negative)
  "user_agent_family": "Chrome"                     // User Agent (Optional)
}
```

**Responses**:

- `201 Created`: Fact explicitly persisted to disk.
- `400 Bad Request`: Validation failure.
- `401 Unauthorized`: Missing API Key.
- `500 Internal Server Error`: Disk write failure.

### 2. Ingest Service Event (Lifecycle)

Records service lifecycle events (start/stop/deploy).

**Method**: `POST /api/v1/events`
**Content-Type**: `application/json`

**Request Body Config**:

```json
{
  "event_id": "UUIDv7",
  "event_time": "ISO 8601",
  "service": "payment-service",
  "event_type": "deploy_start", // "deploy_start", "deploy_end", "pod_crash"
  "metadata": {                 // Optional KV pairs
    "version": "v1.2.3",
    "region": "us-east-1"
  }
}
```

**Responses**:

- `201 Created`
- `400 Bad Request`
- `401 Unauthorized`
