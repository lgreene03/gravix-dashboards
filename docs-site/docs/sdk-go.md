---
title: Go SDK
sidebar_position: 2
---

# Go SDK

The Gravix Go SDK is a small client for sending request facts and service events, with batching,
retries, path sanitisation, and `net/http` middleware. Every example on this page uses only
identifiers the SDK exports; `tests/governance/sdk_docs_test.go` fails the build if one stops
existing.

## Installation

The SDK's module path is `github.com/gravix-io/gravix-go` (it has its own `go.mod` under `sdk/go`).

:::warning Not yet fetchable with `go get`

The project's canonical import path is not settled — see **F-050** in
[`docs/oss/findings.md`](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/oss/findings.md).
Until it is, use the SDK from a clone with a `replace` directive. This is exactly how the tested Gin
recipe in `examples/recipes/gin` consumes it.

:::

```bash
git clone https://github.com/lgreene03/gravix-dashboards
cd your-service
go mod edit -require=github.com/gravix-io/gravix-go@v0.0.0 \
            -replace=github.com/gravix-io/gravix-go=../gravix-dashboards/sdk/go
go mod tidy
```

Requires Go 1.22+.

## Creating a client

```go
package main

import (
    "time"

    gravix "github.com/gravix-io/gravix-go"
)

func main() {
    client := gravix.New(
        "http://localhost:8090", // the ingestion API
        "your-api-key",
        gravix.WithService("payments-api"),
        gravix.WithBatchSize(100),
        gravix.WithFlushInterval(5*time.Second),
        gravix.WithMaxRetries(3),
    )
    defer client.Close() // flushes anything still buffered
}
```

| Option | Default | Effect |
|---|---|---|
| `WithService(name)` | none | Service name for facts and events that do not set their own |
| `WithBatchSize(n)` | `100` (`DefaultBatchSize`) | Flush after this many buffered facts |
| `WithFlushInterval(d)` | `5s` (`DefaultFlushInterval`) | Flush at least this often |
| `WithMaxRetries(n)` | `3` | Retry attempts on 429, 500, 502, 503 and 504; `0` disables retries |
| `WithAutoSanitize(bool)` | `true` | Replace raw UUIDs and 4+ digit IDs in `PathTemplate` with `{id}` |
| `WithHTTPClient(hc)` | 10 s timeout (`DefaultHTTPTimeout`) | Use your own `*http.Client` |
| `WithOnError(fn)` | none | Called with errors from background batch sends |

## Recording facts

`RecordFact` queues a fact for the next batch and returns immediately. Missing `EventID` (a UUIDv7),
`EventTime` and `Service` are filled in for you.

```go
client.RecordFact(context.Background(), gravix.RequestFact{
    Method:       "GET",
    PathTemplate: "/v1/users/{id}",
    StatusCode:   200,
    LatencyMs:    38,
})
```

Errors from a background batch send go to the `WithOnError` callback, not to the caller. When you need
to know the fact was accepted, send it synchronously instead:

```go
if err := client.SendFact(ctx, gravix.RequestFact{
    Method: "POST", PathTemplate: "/v1/orders", StatusCode: 201, LatencyMs: 54,
}); err != nil {
    log.Printf("gravix: fact not accepted: %v", err)
}
```

`client.Flush(ctx)` sends whatever is buffered now; `client.Close()` flushes and stops the client.

## Recording events

Service events — deploys, restarts, scaling — are sent synchronously, because they are rare and you
usually want to know they arrived.

```go
err := client.RecordEvent(ctx, gravix.ServiceEvent{
    Service:    "payments-api",
    EventType:  "deploy_completed",
    Message:    "Deployed v1.2.3",
    Properties: map[string]string{"version": "1.2.3"},
})
```

## net/http middleware

`HTTPMiddleware` records one fact per request. Its second argument returns the route template for a
request; pass `nil` to record the raw path with sanitisation applied.

```go
mux := http.NewServeMux()
mux.HandleFunc("/v1/users/{id}", handleUser)

handler := gravix.HTTPMiddleware(client, nil)(mux)
http.ListenAndServe(":8080", handler)
```

`TraceMiddleware(client)` additionally records trace spans for sampled requests.

## Gin

The SDK has no Gin dependency. `examples/recipes/gin/main.go` is a complete, tested middleware: it
reads `c.FullPath()`, turns `:id` into `{id}`, and calls `client.RecordFact` after the handler runs.
Copy it; CI runs it on every pull request.

## OpenTelemetry

If you already use OpenTelemetry, the exporter module `github.com/gravix-io/gravix-go/otel` turns
completed HTTP spans into request facts and skips the rest. It is a separate module with its own
`go.mod`, so it needs its own `replace` beside the SDK's:

```bash
go mod edit -require=github.com/gravix-io/gravix-go/otel@v0.0.0 \
            -replace=github.com/gravix-io/gravix-go/otel=../gravix-dashboards/sdk/go/otel
go mod tidy
```

```go
exporter := otel.NewExporter(client)
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
```

## Path sanitisation

`gravix.SanitizePath` is what `WithAutoSanitize` applies, and it is exported so you can use it in your
own middleware:

```go
gravix.SanitizePath("/users/550e8400-e29b-41d4-a716-446655440000/orders") // "/users/{id}/orders"
gravix.SanitizePath("/api/v1/products/12345")                             // "/api/v1/products/{id}"
```
