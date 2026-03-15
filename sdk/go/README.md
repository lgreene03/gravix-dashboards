# Gravix Go SDK

Go client library for the [Gravix](https://github.com/gravix-io/gravix) observability platform. Send HTTP request metrics and service lifecycle events with automatic batching, retry, and path sanitization.

## Installation

```bash
go get github.com/gravix-io/gravix-go
```

## Quick Start

```go
package main

import (
    "context"

    gravix "github.com/gravix-io/gravix-go"
)

func main() {
    client := gravix.New(
        "http://localhost:8090",
        "your-api-key",
        gravix.WithService("my-api"),
    )
    defer client.Close() // flushes remaining buffered facts

    // Record a request fact (batched automatically).
    client.RecordFact(context.Background(), gravix.RequestFact{
        Method:       "GET",
        PathTemplate: "/api/v1/users/{id}",
        StatusCode:   200,
        LatencyMs:    42,
    })
}
```

## Features

### Automatic Batching

Facts are buffered in memory and flushed to the batch API endpoint (`/api/v1/facts/batch`) either when the buffer reaches the configured size or on a timer interval. This reduces HTTP overhead significantly compared to sending each fact individually.

```go
client := gravix.New(url, key,
    gravix.WithBatchSize(200),               // flush every 200 facts (default: 100)
    gravix.WithFlushInterval(10*time.Second), // or every 10s (default: 5s)
)
```

Call `client.Flush(ctx)` to force an immediate flush, or `client.Close()` to flush and shut down.

### Path Sanitization

The SDK automatically replaces raw UUIDs, numeric IDs (4+ digits), and hex tokens in path templates with `{id}` placeholders. This prevents high-cardinality metric explosion.

```go
// Automatic: "/users/550e8400-e29b-41d4-a716-446655440000/orders"
//         -> "/users/{id}/orders"

// Disable if you handle sanitization yourself:
client := gravix.New(url, key, gravix.WithAutoSanitize(false))
```

Use `gravix.SanitizePath(path)` directly if you need standalone sanitization.

### Retry with Backoff

Failed requests are retried with exponential backoff and jitter. The client retries on:
- `429 Too Many Requests` (respects `Retry-After` header)
- `500`, `502`, `503`, `504` server errors

It does **not** retry on `400` (validation error) or `401` (auth error).

```go
client := gravix.New(url, key,
    gravix.WithMaxRetries(5), // default: 3, set 0 to disable
)
```

### Service Events

Service events (deploys, restarts, scale operations) are sent synchronously since they are low-volume and time-sensitive.

```go
err := client.RecordEvent(ctx, gravix.ServiceEvent{
    Service:   "my-api",
    EventType: "deploy_completed",
    Message:   "Deployed v1.2.3 to production",
    Properties: map[string]string{
        "version": "1.2.3",
        "branch":  "main",
        "commit":  "abc1234",
    },
})
```

### Error Handling

For batched facts, errors are delivered via the `WithOnError` callback:

```go
client := gravix.New(url, key,
    gravix.WithOnError(func(err error) {
        log.Printf("gravix batch error: %v", err)
    }),
)
```

For synchronous calls (`SendFact`, `RecordEvent`), errors are returned directly. API errors are returned as `*gravix.APIError` with the HTTP status code:

```go
err := client.RecordEvent(ctx, event)
if apiErr, ok := err.(*gravix.APIError); ok {
    fmt.Printf("API error %d: %s\n", apiErr.StatusCode, apiErr.Message)
}
```

## HTTP Middleware

Wrap your HTTP handler to automatically record request facts:

```go
func gravixMiddleware(client *gravix.Client) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            rec := &statusRecorder{ResponseWriter: w, statusCode: 200}
            next.ServeHTTP(rec, r)

            client.RecordFact(r.Context(), gravix.RequestFact{
                Method:       r.Method,
                PathTemplate: r.URL.Path, // auto-sanitized
                StatusCode:   rec.statusCode,
                LatencyMs:    int(time.Since(start).Milliseconds()),
            })
        })
    }
}

type statusRecorder struct {
    http.ResponseWriter
    statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
    r.statusCode = code
    r.ResponseWriter.WriteHeader(code)
}
```

## Configuration Reference

| Option | Default | Description |
|--------|---------|-------------|
| `WithService(name)` | `""` | Default service name for facts/events |
| `WithBatchSize(n)` | `100` | Max facts buffered before auto-flush |
| `WithFlushInterval(d)` | `5s` | Max time between auto-flushes |
| `WithHTTPClient(c)` | `10s timeout` | Custom `*http.Client` |
| `WithAutoSanitize(b)` | `true` | Auto-replace UUIDs/IDs in paths |
| `WithMaxRetries(n)` | `3` | Max retry attempts (0 = disable) |
| `WithOnError(fn)` | `nil` | Callback for batch flush errors |

## Types

### RequestFact

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `EventID` | `string` | Auto | UUIDv7 (auto-generated if empty) |
| `EventTime` | `string` | Auto | RFC3339 timestamp (auto-generated if empty) |
| `Service` | `string` | Yes* | Service name (*falls back to WithService) |
| `Method` | `string` | Yes | HTTP method (GET, POST, etc.) |
| `PathTemplate` | `string` | Yes | URL path with `{id}` placeholders |
| `StatusCode` | `int` | Yes | HTTP status code (100-599) |
| `LatencyMs` | `int` | Yes | Response time in milliseconds |
| `UserAgentFamily` | `string` | No | User agent family |

### ServiceEvent

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `EventID` | `string` | Auto | UUIDv7 (auto-generated) |
| `EventTime` | `string` | Auto | RFC3339 timestamp (auto-generated) |
| `Service` | `string` | Yes | Service name |
| `EventType` | `string` | Yes | Snake-case type (e.g., `deploy_completed`) |
| `EntityID` | `string` | No | Related entity identifier |
| `Message` | `string` | No | Human-readable message |
| `Properties` | `map[string]string` | No | Flat key-value metadata |
