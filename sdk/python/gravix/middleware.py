"""HTTP middleware for automatic request instrumentation.

Supports Flask (via before/after_request) and ASGI (FastAPI, Starlette).
"""

import time
from typing import Optional

from gravix.types import RequestFact
from gravix.sanitize import sanitize_path


def flask_middleware(client, service: str = "", path_fn=None):
    """Register Flask before/after_request hooks for auto-instrumentation.

    Args:
        client: Gravix Client instance.
        service: Override service name (uses client default if empty).
        path_fn: Optional callable(request) -> str for route template extraction.

    Usage:
        app = Flask(__name__)
        gravix_client = gravix.Client(url, key, service="myapp")
        flask_middleware(gravix_client, app=app)
    """
    def decorator(app):
        @app.before_request
        def _gravix_before():
            from flask import g
            g._gravix_start = time.monotonic()

        @app.after_request
        def _gravix_after(response):
            from flask import g, request
            start = getattr(g, "_gravix_start", None)
            if start is None:
                return response

            latency_ms = int((time.monotonic() - start) * 1000)

            path = request.url_rule.rule if request.url_rule else request.path
            if path_fn:
                path = path_fn(request) or path

            fact = RequestFact(
                service=service or client.service,
                method=request.method,
                path_template=path,
                status_code=response.status_code,
                latency_ms=latency_ms,
                user_agent_family=_extract_ua_family(request.user_agent.string),
            )
            client.record_fact(fact)
            return response

        return app
    return decorator


class ASGIMiddleware:
    """ASGI middleware for FastAPI/Starlette auto-instrumentation.

    Usage:
        app = FastAPI()
        client = gravix.Client(url, key, service="myapp")
        app.add_middleware(ASGIMiddleware, client=client)
    """

    def __init__(self, app, client, service: str = ""):
        self.app = app
        self.client = client
        self.service = service or client.service

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
            path = scope.get("path", "/")

            # Try to get route pattern from scope
            route = scope.get("route")
            if route and hasattr(route, "path"):
                path = route.path

            headers = dict(scope.get("headers", []))
            ua = headers.get(b"user-agent", b"").decode("utf-8", errors="replace")

            fact = RequestFact(
                service=self.service,
                method=scope.get("method", "GET"),
                path_template=sanitize_path(path),
                status_code=status_code,
                latency_ms=latency_ms,
                user_agent_family=_extract_ua_family(ua),
            )
            self.client.record_fact(fact)


def _extract_ua_family(ua: str) -> str:
    """Extract user agent family (first token before '/')."""
    if not ua:
        return ""
    idx = ua.find("/")
    if idx > 0:
        return ua[:idx]
    return ua[:50] if len(ua) > 50 else ua
