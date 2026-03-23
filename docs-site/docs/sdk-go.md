---
title: Go SDK
sidebar_position: 2
---

# Go SDK

The Gravix Go SDK provides a lightweight client for sending request facts, plus drop-in middleware for `net/http`, Gin, and Echo.

## Installation

```bash
go get github.com/lgreene/gravix-dashboards/sdk/go
```

Requires Go 1.21+.

## Creating a Client

```go
package main

import (
    gravix "github.com/lgreene/gravix-dashboards/sdk/go"
)

func main() {
    client, err := gravix.NewClient(gravix.Config{
        APIKey:   "gvx_live_your_api_key_here",
        Endpoint: "https://ingest.gravix.io/api/v1/facts", // or http://localhost:8090 locally
        Service:  "payments-api",

        // Optional tuning
        BatchSize:     100,           // flush after this many events
        FlushInterval: 5 * time.Second, // or after this duration
        MaxRetries:    3,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer client.Flush() // flush buffered events on shutdown
}
```

## Sending Facts Manually

If you need to record facts outside of middleware (background jobs, async handlers, etc.):

```go
err := client.Send(context.Background(), gravix.Fact{
    EventID:         gravix.NewEventID(), // generates a UUIDv7
    EventTime:       time.Now().UTC(),
    Method:          "GET",
    PathTemplate:    "/v1/users/{id}",
    StatusCode:      200,
    LatencyMs:       38,
    UserAgentFamily: "Go-http-client",
})
if err != nil {
    log.Printf("gravix send error: %v", err)
}
```

`Send` is non-blocking — it enqueues the fact into an internal buffer. Call `client.Flush()` to drain the buffer synchronously (e.g., during graceful shutdown).

## net/http Middleware

Wrap your `http.Handler` with `gravix.Middleware` to automatically record every request:

```go
package main

import (
    "net/http"

    gravix "github.com/lgreene/gravix-dashboards/sdk/go"
)

func main() {
    client, _ := gravix.NewClient(gravix.Config{
        APIKey:  "gvx_live_your_api_key_here",
        Service: "my-service",
    })
    defer client.Flush()

    mux := http.NewServeMux()
    mux.HandleFunc("/v1/users/{id}", handleUser)
    mux.HandleFunc("/v1/orders/{id}", handleOrder)

    // Wrap the mux — uses the matched pattern as path_template automatically
    http.ListenAndServe(":8080", gravix.Middleware(client)(mux))
}
```

The middleware captures the matched route pattern (e.g., `/v1/users/{id}`), not the raw path, so no UUIDs leak into your metrics.

## Gin Middleware

```go
package main

import (
    "github.com/gin-gonic/gin"
    gravix "github.com/lgreene/gravix-dashboards/sdk/go"
)

func main() {
    client, _ := gravix.NewClient(gravix.Config{
        APIKey:  "gvx_live_your_api_key_here",
        Service: "my-service",
    })
    defer client.Flush()

    r := gin.Default()
    r.Use(gravix.GinMiddleware(client))

    r.GET("/v1/products/:id", getProduct)
    r.POST("/v1/orders", createOrder)

    r.Run(":8080")
}
```

The Gin middleware reads `c.FullPath()` for the path template, so parameterized routes like `/v1/products/:id` are recorded correctly.

## Echo Middleware

```go
package main

import (
    "github.com/labstack/echo/v4"
    gravix "github.com/lgreene/gravix-dashboards/sdk/go"
)

func main() {
    client, _ := gravix.NewClient(gravix.Config{
        APIKey:  "gvx_live_your_api_key_here",
        Service: "my-service",
    })
    defer client.Flush()

    e := echo.New()
    e.Use(gravix.EchoMiddleware(client))

    e.GET("/v1/invoices/:id", getInvoice)

    e.Start(":8080")
}
```

## Configuration Reference

| Field           | Type          | Default    | Description                              |
|-----------------|---------------|------------|------------------------------------------|
| `APIKey`        | `string`      | required   | Your Gravix API key                      |
| `Endpoint`      | `string`      | cloud URL  | Ingestion endpoint                       |
| `Service`       | `string`      | required   | Service name shown in dashboard          |
| `BatchSize`     | `int`         | `100`      | Flush after this many buffered events    |
| `FlushInterval` | `time.Duration` | `5s`    | Flush after this duration                |
| `MaxRetries`    | `int`         | `3`        | Retry attempts on transient errors       |
| `Timeout`       | `time.Duration` | `10s`   | HTTP request timeout per attempt         |

## Error Handling

The SDK never panics. Errors from `Send` indicate the internal queue is full (backpressure). In that case, log and drop — Gravix is designed for sampling, not guaranteed delivery.

```go
if err := client.Send(ctx, fact); err != nil {
    // queue full — log and continue, do not block the request path
    log.Printf("gravix: dropped fact: %v", err)
}
```
