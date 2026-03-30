---
title: Alerting
sidebar_position: 5
---

# Alerting

Gravix alerting evaluates metric rules on a one-minute cadence and notifies you through Slack, PagerDuty, or a custom webhook when thresholds are breached.

## Creating a Rule

Rules are defined in your workspace settings at [app.gravix.io/alerting](https://app.gravix.io/alerting), or via the API:

```bash
curl -X POST https://api.gravix.io/v1/alert-rules \
  -H "X-API-Key: $GRAVIX_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "High error rate on payments-api",
    "service": "payments-api",
    "metric": "error_rate",
    "operator": "gt",
    "threshold": 0.05,
    "window_minutes": 5,
    "channel_id": "ch_slack_abc123"
  }'
```

A rule fires when the metric exceeds the threshold for the entire window. It resolves automatically when the metric drops back below threshold.

## Available Metrics

| Metric         | Unit        | Description                                          |
|----------------|-------------|------------------------------------------------------|
| `error_rate`   | ratio 0–1   | Fraction of requests with status_code ≥ 500          |
| `p95_latency`  | milliseconds| 95th-percentile response latency over the window     |
| `p99_latency`  | milliseconds| 99th-percentile response latency over the window     |
| `throughput`   | req/min     | Total requests per minute averaged over the window   |

### Operators

- `gt` — greater than (threshold breach)
- `lt` — less than (throughput drop, useful for detecting service outages)
- `gte` — greater than or equal to
- `lte` — less than or equal to

### Filtering by Path or Method

Rules can be scoped to a specific route template or HTTP method:

```json
{
  "name": "Slow checkout",
  "service": "checkout-api",
  "path_template": "/v1/checkout",
  "method": "POST",
  "metric": "p99_latency",
  "operator": "gt",
  "threshold": 2000,
  "window_minutes": 3,
  "channel_id": "ch_pagerduty_xyz789"
}
```

## Notification Channels

### Slack

1. Go to **Settings → Notification Channels → Add Channel → Slack**.
2. Authorise the Gravix Slack app in your workspace.
3. Choose the channel to post to (e.g., `#alerts`).
4. Copy the resulting `channel_id` for use in rules.

Alert messages include the rule name, current metric value, threshold, and a direct link to the relevant dashboard view.

### PagerDuty

1. Go to **Settings → Notification Channels → Add Channel → PagerDuty**.
2. Enter your PagerDuty **Integration Key** (Events API v2).
3. Copy the resulting `channel_id`.

Gravix opens a PagerDuty incident when the rule fires and resolves it automatically when the metric recovers. Incident severity maps to the configured rule priority (low / high / critical).

### Webhook

Send alert payloads to any HTTPS endpoint:

```bash
curl -X POST https://api.gravix.io/v1/channels \
  -H "X-API-Key: $GRAVIX_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "webhook",
    "name": "My ops webhook",
    "url": "https://hooks.example.com/gravix",
    "secret": "whsec_..."
  }'
```

Gravix signs each request with `X-Gravix-Signature` (HMAC-SHA256 over the raw body using your secret). The payload structure:

```json
{
  "rule_id": "rule_abc123",
  "rule_name": "High error rate on payments-api",
  "service": "payments-api",
  "metric": "error_rate",
  "value": 0.082,
  "threshold": 0.05,
  "state": "firing",
  "fired_at": "2026-03-23T12:00:00Z"
}
```

`state` is either `"firing"` or `"resolved"`.

## Managing Rules

### List Rules

```bash
curl https://api.gravix.io/v1/alert-rules \
  -H "X-API-Key: $GRAVIX_API_KEY"
```

### Disable a Rule

```bash
curl -X PATCH https://api.gravix.io/v1/alert-rules/rule_abc123 \
  -H "X-API-Key: $GRAVIX_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'
```

### Delete a Rule

```bash
curl -X DELETE https://api.gravix.io/v1/alert-rules/rule_abc123 \
  -H "X-API-Key: $GRAVIX_API_KEY"
```

## Alert Evaluation Timing

Rules are evaluated once per minute against the completed preceding window. There is an inherent delay of up to one rollup cycle (one minute) between a metric breach and notification delivery. This is by design — Gravix prioritises correctness and batch efficiency over sub-second alerting.

For sub-minute alerting, use Prometheus (Gravix exposes a `/metrics` endpoint) with your own Alertmanager configuration.
