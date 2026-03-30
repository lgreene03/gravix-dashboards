---
title: Node SDK
sidebar_position: 4
---

# Node SDK

The Gravix Node SDK provides a promise-based client for recording HTTP request facts and drop-in middleware for Express.

## Installation

```bash
npm install @gravix/sdk
```

Or with yarn / pnpm:

```bash
yarn add @gravix/sdk
pnpm add @gravix/sdk
```

Requires Node.js 18+. The SDK has zero production dependencies.

## Creating a Client

```js
const { GravixClient } = require('@gravix/sdk');
// or ESM:
// import { GravixClient } from '@gravix/sdk';

const client = new GravixClient({
  apiKey: 'gvx_live_your_api_key_here',
  service: 'payments-api',
  endpoint: 'https://ingest.gravix.io/api/v1/facts', // default

  // Optional tuning
  batchSize: 100,         // flush after this many events
  flushInterval: 5000,    // or after this many milliseconds
  maxRetries: 3,
});
```

Register a shutdown handler to flush buffered events before the process exits:

```js
process.on('SIGTERM', async () => {
  await client.flush();
  process.exit(0);
});
```

## Sending Facts Manually

```js
await client.send({
  eventId: client.newEventId(),      // generates a UUIDv7
  eventTime: new Date().toISOString(),
  method: 'GET',
  pathTemplate: '/v1/orders/{id}',
  statusCode: 200,
  latencyMs: 55,
  userAgentFamily: 'axios',
});
```

`send()` returns a promise that resolves immediately once the fact is enqueued. It does not wait for network delivery. Use `client.flush()` to await delivery of all buffered events.

## Express Middleware

```js
const express = require('express');
const { GravixClient, gravixMiddleware } = require('@gravix/sdk');

const app = express();

const client = new GravixClient({
  apiKey: 'gvx_live_your_api_key_here',
  service: 'my-express-app',
});

// Register before your routes
app.use(gravixMiddleware(client));

app.get('/v1/users/:id', (req, res) => {
  res.json({ id: req.params.id });
});

app.post('/v1/orders', (req, res) => {
  res.status(201).json({ created: true });
});

app.listen(3000);
```

The middleware reads `req.route.path` after the response is sent to capture the matched route template (e.g., `/v1/users/:id` becomes `/v1/users/{id}`), so numeric IDs and UUIDs are never recorded in your metrics.

## TypeScript

The SDK ships with built-in TypeScript types. No `@types/` package is needed.

```ts
import { GravixClient, Fact, GravixConfig } from '@gravix/sdk';

const config: GravixConfig = {
  apiKey: 'gvx_live_your_api_key_here',
  service: 'my-service',
};

const client = new GravixClient(config);

const fact: Fact = {
  eventId: client.newEventId(),
  eventTime: new Date().toISOString(),
  method: 'DELETE',
  pathTemplate: '/v1/resources/{id}',
  statusCode: 204,
  latencyMs: 12,
  userAgentFamily: 'node-fetch',
};

await client.send(fact);
```

## Configuration Reference

| Option          | Type     | Default     | Description                              |
|-----------------|----------|-------------|------------------------------------------|
| `apiKey`        | `string` | required    | Your Gravix API key                      |
| `service`       | `string` | required    | Service name shown in dashboard          |
| `endpoint`      | `string` | cloud URL   | Ingestion endpoint                       |
| `batchSize`     | `number` | `100`       | Flush after this many buffered events    |
| `flushInterval` | `number` | `5000`      | Flush interval in milliseconds           |
| `maxRetries`    | `number` | `3`         | Retry attempts on transient errors       |
| `timeout`       | `number` | `10000`     | HTTP request timeout in milliseconds     |

## Error Handling

The SDK catches and logs network errors internally. Delivery failures do not throw — Gravix is an observability side-channel and must never affect your critical path. You can optionally attach an error handler:

```js
const client = new GravixClient({
  apiKey: 'gvx_live_your_api_key_here',
  service: 'my-service',
  onError: (err) => {
    // called on repeated delivery failure after all retries
    console.error('gravix delivery error:', err.message);
  },
});
```
