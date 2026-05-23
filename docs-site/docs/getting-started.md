---
title: Getting Started
sidebar_position: 1
---

# Getting Started with Gravix

Gravix is a lightweight, data-first HTTP observability platform. It captures raw request events, aggregates them into p50/p95/p99 latency metrics and error rates, and surfaces them on a live dashboard — without agents, sidecars, or complex configuration.

## Prerequisites

- Docker and Docker Compose v2+
- An API key (sign up at [app.gravix.io](https://app.gravix.io))
- One of the following for SDK usage:
  - Go 1.21+
  - Python 3.9+
  - Node.js 18+

## Quick Start with Docker Compose

The fastest way to run Gravix locally is with Docker Compose. This starts the ingestion service, rollup ETL, Trino query engine, and dashboard.

```bash
git clone https://github.com/lgreene/gravix-dashboards.git
cd gravix-dashboards
cp .env.example .env
# Edit .env and set GRAVIX_API_KEY to your API key
docker-compose up -d --build
```

Once running, open the dashboard at [http://localhost:8000/index.html](http://localhost:8000/index.html).

| Service       | URL                          |
|---------------|------------------------------|
| Dashboard     | http://localhost:8000        |
| Ingestion API | http://localhost:8090/api/v1/facts |
| Prometheus    | http://localhost:9090        |
| Grafana       | http://localhost:3000        |

## Sending Your First Event

Gravix ingests **facts** — immutable records of individual HTTP requests. Send one with `curl`:

```bash
curl -X POST http://localhost:8090/api/v1/facts \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $GRAVIX_API_KEY" \
  -d '{
    "event_id": "01920c4a-dead-7000-beef-000000000001",
    "event_time": "2026-03-23T12:00:00Z",
    "service": "payments-api",
    "method": "POST",
    "path_template": "/v1/charges/{id}",
    "status_code": 200,
    "latency_ms": 45,
    "user_agent_family": "curl"
  }'
```

Key field rules:

- `event_id` must be a UUIDv7
- `path_template` must use `{id}` placeholders — never raw UUIDs or numeric IDs
- `status_code` must be 100–599
- `latency_ms` must be ≥ 0

## Installing an SDK

For production use, instrument your service with an SDK rather than raw HTTP calls. The SDKs handle batching, retries, and middleware integration.

**Go**

```bash
go get github.com/lgreene/gravix-dashboards/sdk/go
```

**Python**

```bash
pip install gravix
```

**Node.js**

```bash
npm install @gravix/sdk
```

See the SDK guides for full usage:

- [Go SDK](/sdk-go)
- [Python SDK](/sdk-python)
- [Node SDK](/sdk-node)

## Viewing the Dashboard

After sending a few events, the rollup ETL job (runs every minute) will aggregate them into metrics. Open [http://localhost:8000/index.html](http://localhost:8000/index.html) to see:

- **Error rate** by service over time
- **p50 / p95 / p99 latency** histograms
- **Throughput** (requests per minute)
- **Status code distribution**

The dashboard auto-refreshes every 30 seconds. No login is required in local mode.

## Next Steps

- Set up [alerting rules](/alerting) to get notified on error rate spikes
- Review the [Go SDK](/sdk-go), [Python SDK](/sdk-python), or [Node SDK](/sdk-node) for middleware integration
- Explore the [Billing FAQ](/billing-faq) to understand event pricing before scaling up
