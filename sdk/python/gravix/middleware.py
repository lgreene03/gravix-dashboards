"""Framework middleware for automatic request fact recording.

Supports Flask, FastAPI/Starlette, and Django.
"""

from __future__ import annotations

import time
from typing import TYPE_CHECKING

from gravix.types import RequestFact

if TYPE_CHECKING:
    from gravix.client import GravixClient


# ---------------------------------------------------------------------------
# Flask
# ---------------------------------------------------------------------------

def flask_middleware(client: "GravixClient"):
    """Register a Flask ``after_request`` handler that records request facts.

    Usage::

        from flask import Flask
        from gravix import GravixClient
        from gravix.middleware import flask_middleware

        app = Flask(__name__)
        client = GravixClient("http://localhost:8090", "key", service="my-app")
        flask_middleware(client)(app)

    Args:
        client: A :class:`GravixClient` instance.

    Returns:
        A callable that takes a Flask app and registers the middleware.
    """

    def _register(app):
        @app.before_request
        def _start_timer():
            from flask import g
            g._gravix_start = time.monotonic()

        @app.after_request
        def _record(response):
            from flask import request, g

            start = getattr(g, "_gravix_start", None)
            latency_ms = 0
            if start is not None:
                latency_ms = int((time.monotonic() - start) * 1000)

            ua = request.headers.get("User-Agent", "")
            family = ua.split("/")[0] if "/" in ua else ua.split(" ")[0] if ua else ""

            client.record_fact(RequestFact(
                method=request.method,
                path_template=request.path,
                status_code=response.status_code,
                latency_ms=latency_ms,
                user_agent_family=family[:64],
            ))
            return response

        return app

    return _register


# ---------------------------------------------------------------------------
# FastAPI / Starlette
# ---------------------------------------------------------------------------

class FastAPIMiddleware:
    """ASGI middleware for FastAPI/Starlette that records request facts.

    Usage::

        from fastapi import FastAPI
        from gravix import GravixClient
        from gravix.middleware import FastAPIMiddleware

        app = FastAPI()
        client = GravixClient("http://localhost:8090", "key", service="my-app")
        app.add_middleware(FastAPIMiddleware, client=client)

    Args:
        app: The ASGI application.
        client: A :class:`GravixClient` instance.
    """

    def __init__(self, app, client: "GravixClient"):
        self.app = app
        self.client = client

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return

        start = time.monotonic()
        status_code = 500  # default in case of error

        async def send_wrapper(message):
            nonlocal status_code
            if message["type"] == "http.response.start":
                status_code = message["status"]
            await send(message)

        try:
            await self.app(scope, receive, send_wrapper)
        finally:
            latency_ms = int((time.monotonic() - start) * 1000)
            method = scope.get("method", "GET")
            path = scope.get("path", "/")

            headers = dict(scope.get("headers", []))
            ua_bytes = headers.get(b"user-agent", b"")
            ua = ua_bytes.decode("utf-8", errors="replace") if isinstance(ua_bytes, bytes) else str(ua_bytes)
            family = ua.split("/")[0] if "/" in ua else ua.split(" ")[0] if ua else ""

            self.client.record_fact(RequestFact(
                method=method,
                path_template=path,
                status_code=status_code,
                latency_ms=latency_ms,
                user_agent_family=family[:64],
            ))


# ---------------------------------------------------------------------------
# Django
# ---------------------------------------------------------------------------

class DjangoMiddleware:
    """Django middleware that records request facts.

    Usage::

        # settings.py
        MIDDLEWARE = [
            "gravix.middleware.DjangoMiddleware",
            ...
        ]

        GRAVIX_CLIENT = GravixClient("http://localhost:8090", "key", service="my-app")

    Then in your middleware configuration, the client is picked up from
    ``settings.GRAVIX_CLIENT``.

    Or instantiate directly::

        from gravix import GravixClient
        from gravix.middleware import DjangoMiddleware

        client = GravixClient("http://localhost:8090", "key", service="my-app")
        # In MIDDLEWARE list, use the result of DjangoMiddleware.factory(client)
    """

    def __init__(self, get_response, client: "GravixClient" = None):
        self.get_response = get_response
        if client is not None:
            self.client = client
        else:
            # Try to get from Django settings.
            try:
                from django.conf import settings
                self.client = getattr(settings, "GRAVIX_CLIENT", None)
            except ImportError:
                self.client = None

    def __call__(self, request):
        start = time.monotonic()
        response = self.get_response(request)
        latency_ms = int((time.monotonic() - start) * 1000)

        if self.client is not None:
            ua = request.META.get("HTTP_USER_AGENT", "")
            family = ua.split("/")[0] if "/" in ua else ua.split(" ")[0] if ua else ""

            self.client.record_fact(RequestFact(
                method=request.method,
                path_template=request.path,
                status_code=response.status_code,
                latency_ms=latency_ms,
                user_agent_family=family[:64],
            ))

        return response

    @classmethod
    def factory(cls, client: "GravixClient"):
        """Return a middleware class bound to the given client."""

        class BoundMiddleware(cls):
            def __init__(self, get_response):
                super().__init__(get_response, client=client)

        return BoundMiddleware
