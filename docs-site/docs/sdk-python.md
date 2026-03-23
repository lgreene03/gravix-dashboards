---
title: Python SDK
sidebar_position: 3
---

# Python SDK

The Gravix Python SDK provides a thread-safe client for recording HTTP request facts, plus ready-made middleware for Flask and FastAPI.

## Installation

```bash
pip install gravix
```

Requires Python 3.9+. The SDK has no mandatory dependencies beyond the standard library. Optional extras:

```bash
pip install gravix[flask]    # includes Flask integration helpers
pip install gravix[fastapi]  # includes FastAPI/Starlette integration helpers
```

## Creating a Client

```python
from gravix import GravixClient, GravixConfig

client = GravixClient(
    GravixConfig(
        api_key="gvx_live_your_api_key_here",
        service="payments-api",
        endpoint="https://ingest.gravix.io/api/v1/facts",  # default

        # Optional tuning
        batch_size=100,          # flush after this many events
        flush_interval=5.0,      # or after this many seconds
        max_retries=3,
    )
)
```

Call `client.shutdown()` (or use it as a context manager) to flush buffered events before process exit.

```python
with GravixClient(config) as client:
    # client flushes and shuts down automatically on exit
    run_app(client)
```

## Sending Facts Manually

```python
import time
from gravix import Fact

client.send(Fact(
    event_id=client.new_event_id(),   # generates a UUIDv7
    event_time=time.time(),
    method="POST",
    path_template="/v1/charges/{id}",
    status_code=200,
    latency_ms=42,
    user_agent_family="python-httpx",
))
```

`send()` is non-blocking and enqueues the fact for batched delivery.

## Flask Middleware

```python
from flask import Flask
from gravix import GravixClient, GravixConfig
from gravix.flask import GravixMiddleware

app = Flask(__name__)

client = GravixClient(GravixConfig(
    api_key="gvx_live_your_api_key_here",
    service="my-flask-app",
))

# Wrap the Flask app
app.wsgi_app = GravixMiddleware(app.wsgi_app, client)

@app.route("/v1/users/<int:user_id>")
def get_user(user_id):
    return {"id": user_id}

@app.route("/v1/orders/<int:order_id>", methods=["GET", "POST"])
def orders(order_id):
    return {"order": order_id}
```

The Flask middleware reads `request.url_rule` to extract the route template (e.g., `/v1/users/<int:user_id>` becomes `/v1/users/{id}`), so raw IDs never appear in your metrics.

## FastAPI Middleware

```python
from fastapi import FastAPI
from gravix import GravixClient, GravixConfig
from gravix.fastapi import GravixMiddleware

app = FastAPI()

client = GravixClient(GravixConfig(
    api_key="gvx_live_your_api_key_here",
    service="my-fastapi-app",
))

app.add_middleware(GravixMiddleware, client=client)

@app.get("/v1/products/{product_id}")
async def get_product(product_id: int):
    return {"product_id": product_id}

@app.post("/v1/checkout")
async def checkout():
    return {"status": "ok"}
```

The FastAPI middleware hooks into Starlette's routing to capture the matched route path, not the instantiated URL.

## Shutdown and Graceful Cleanup

For long-running servers, register a shutdown hook to flush any buffered events:

```python
import atexit

atexit.register(client.shutdown)
```

Or with uvicorn lifespan events:

```python
from contextlib import asynccontextmanager

@asynccontextmanager
async def lifespan(app: FastAPI):
    yield
    client.shutdown()

app = FastAPI(lifespan=lifespan)
```

## Configuration Reference

| Parameter        | Type    | Default         | Description                            |
|------------------|---------|-----------------|----------------------------------------|
| `api_key`        | `str`   | required        | Your Gravix API key                    |
| `service`        | `str`   | required        | Service name shown in dashboard        |
| `endpoint`       | `str`   | cloud URL       | Ingestion endpoint                     |
| `batch_size`     | `int`   | `100`           | Flush after this many buffered events  |
| `flush_interval` | `float` | `5.0`           | Flush after this many seconds          |
| `max_retries`    | `int`   | `3`             | Retry attempts on transient errors     |
| `timeout`        | `float` | `10.0`          | HTTP request timeout in seconds        |

## Thread Safety

The client is fully thread-safe. A single `GravixClient` instance can be shared across all threads in a multi-threaded WSGI server (gunicorn, uWSGI). No locking is needed at the call site.
