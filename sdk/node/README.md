# Gravix Node.js SDK

Node.js/TypeScript client library for the [Gravix](https://github.com/gravix-io/gravix) observability platform. Send HTTP request metrics and service lifecycle events with automatic batching, retry, and path sanitization. Zero runtime dependencies.

## Installation

```bash
npm install @gravix/sdk
```

## Quick Start

```typescript
import { GravixClient } from "@gravix/sdk";

const client = new GravixClient({
  baseUrl: "http://localhost:8090",
  apiKey: "your-api-key",
  service: "my-api",
});

// Record a request fact (batched automatically).
await client.recordFact({
  method: "GET",
  pathTemplate: "/api/v1/users/{id}",
  statusCode: 200,
  latencyMs: 42,
});

// Flush and shut down when done.
await client.close();
```

## Features

### Automatic Batching

Facts are buffered in memory and flushed to the batch API endpoint (`/api/v1/facts/batch`) either when the buffer reaches the configured size or on a timer interval. This reduces HTTP overhead significantly.

```typescript
const client = new GravixClient({
  baseUrl: "http://localhost:8090",
  apiKey: "your-api-key",
  batchSize: 200,          // flush every 200 facts (default: 100)
  flushIntervalMs: 10000,  // or every 10s (default: 5000)
});
```

Call `await client.flush()` to force an immediate flush, or `await client.close()` to flush and shut down. The background timer is unreffed so it won't prevent process exit.

### Path Sanitization

The SDK automatically replaces raw UUIDs, numeric IDs (4+ digits), and hex tokens in path templates with `{id}` placeholders. This prevents high-cardinality metric explosion.

```typescript
// Automatic: "/users/550e8400-e29b-41d4-a716-446655440000/orders"
//         -> "/users/{id}/orders"

// Disable if you handle sanitization yourself:
const client = new GravixClient({ ..., autoSanitize: false });
```

Use `sanitizePath()` directly for standalone sanitization:

```typescript
import { sanitizePath } from "@gravix/sdk";

sanitizePath("/api/v1/products/12345");
// => "/api/v1/products/{id}"
```

### Retry with Backoff

Failed requests are retried with exponential backoff and jitter. The client retries on:
- `429 Too Many Requests` (respects `Retry-After` header)
- `500`, `502`, `503`, `504` server errors

It does **not** retry on `400` (validation error) or `401` (auth error).

```typescript
const client = new GravixClient({ ..., maxRetries: 5 }); // default: 3, set 0 to disable
```

### Service Events

Service events (deploys, restarts, scale operations) are sent synchronously since they are low-volume and time-sensitive.

```typescript
await client.recordEvent({
  service: "my-api",
  eventType: "deploy_completed",
  message: "Deployed v1.2.3 to production",
  properties: { version: "1.2.3", branch: "main", commit: "abc1234" },
});
```

### Error Handling

For batched facts, errors are delivered via the `onError` callback:

```typescript
const client = new GravixClient({
  ...,
  onError: (err) => console.error("gravix batch error:", err),
});
```

For synchronous calls (`sendFact`, `recordEvent`), errors are thrown directly. API errors are instances of `APIError` with the HTTP status code:

```typescript
import { APIError } from "@gravix/sdk";

try {
  await client.recordEvent(event);
} catch (err) {
  if (err instanceof APIError) {
    console.error(`API error ${err.statusCode}: ${err.message}`);
  }
}
```

## Framework Middleware

### Express

```typescript
import express from "express";
import { GravixClient, expressMiddleware } from "@gravix/sdk";

const app = express();
const client = new GravixClient({
  baseUrl: "http://localhost:8090",
  apiKey: "your-api-key",
  service: "my-app",
});

app.use(expressMiddleware(client));
```

### Koa

```typescript
import Koa from "koa";
import { GravixClient, koaMiddleware } from "@gravix/sdk";

const app = new Koa();
const client = new GravixClient({
  baseUrl: "http://localhost:8090",
  apiKey: "your-api-key",
  service: "my-app",
});

app.use(koaMiddleware(client));
```

### Fastify

```typescript
import Fastify from "fastify";
import { GravixClient, fastifyPlugin } from "@gravix/sdk";

const app = Fastify();
const client = new GravixClient({
  baseUrl: "http://localhost:8090",
  apiKey: "your-api-key",
  service: "my-app",
});

app.register(fastifyPlugin(client));
```

## Configuration Reference

| Option | Default | Description |
|--------|---------|-------------|
| `baseUrl` | — | Ingestion service URL (required) |
| `apiKey` | — | X-API-Key value (required) |
| `service` | `""` | Default service name for facts/events |
| `batchSize` | `100` | Max facts buffered before auto-flush |
| `flushIntervalMs` | `5000` | Milliseconds between auto-flushes |
| `autoSanitize` | `true` | Auto-replace UUIDs/IDs in paths |
| `maxRetries` | `3` | Max retry attempts (0 = disable) |
| `timeoutMs` | `10000` | HTTP request timeout in milliseconds |
| `onError` | — | Callback for batch flush errors |

## Types

### RequestFact

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `eventId` | `string` | Auto | UUIDv7 (auto-generated if omitted) |
| `eventTime` | `string` | Auto | ISO 8601 timestamp (auto-generated if omitted) |
| `service` | `string` | Yes* | Service name (*falls back to constructor) |
| `method` | `string` | Yes | HTTP method (GET, POST, etc.) |
| `pathTemplate` | `string` | Yes | URL path with `{id}` placeholders |
| `statusCode` | `number` | Yes | HTTP status code (100-599) |
| `latencyMs` | `number` | Yes | Response time in milliseconds |
| `userAgentFamily` | `string` | No | User agent family |

### ServiceEvent

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `eventId` | `string` | Auto | UUIDv7 (auto-generated) |
| `eventTime` | `string` | Auto | ISO 8601 timestamp (auto-generated) |
| `service` | `string` | Yes | Service name |
| `eventType` | `string` | Yes | Snake-case type (e.g., `deploy_completed`) |
| `entityId` | `string` | No | Related entity identifier |
| `message` | `string` | No | Human-readable message |
| `properties` | `Record<string, string>` | No | Flat key-value metadata |
