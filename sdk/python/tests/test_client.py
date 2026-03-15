"""Tests for the Gravix client."""

import json
import threading
import time
from http.server import HTTPServer, BaseHTTPRequestHandler
from unittest.mock import MagicMock

import pytest

from gravix.client import GravixClient
from gravix.types import RequestFact, ServiceEvent, APIError


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

class MockHandler(BaseHTTPRequestHandler):
    """Simple HTTP handler that records requests and returns configurable responses."""

    server_responses = {}  # path -> (status_code, body_dict)
    received_requests = []  # list of (method, path, body, headers)
    request_count = 0
    lock = threading.Lock()

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length).decode("utf-8") if length else ""

        with MockHandler.lock:
            MockHandler.received_requests.append(
                (self.command, self.path, body, dict(self.headers))
            )
            MockHandler.request_count += 1
            count = MockHandler.request_count

        # Check for custom response.
        resp_info = MockHandler.server_responses.get(self.path)
        if callable(resp_info):
            status, resp_body = resp_info(count)
        elif resp_info:
            status, resp_body = resp_info
        elif self.path in ("/api/v1/facts", "/api/v1/events"):
            status, resp_body = 201, None
        elif self.path == "/api/v1/facts/batch":
            status, resp_body = 200, {"accepted": 1, "rejected": 0}
        else:
            status, resp_body = 404, {"error": "not found"}

        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        if resp_body is not None:
            self.wfile.write(json.dumps(resp_body).encode())

    def log_message(self, format, *args):
        pass  # Suppress request logs.


@pytest.fixture()
def mock_server():
    """Start a local HTTP server for testing."""
    MockHandler.received_requests = []
    MockHandler.request_count = 0
    MockHandler.server_responses = {}

    server = HTTPServer(("127.0.0.1", 0), MockHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    yield f"http://127.0.0.1:{port}", MockHandler

    server.shutdown()


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

class TestSendFact:
    def test_sends_fact_with_auto_fields(self, mock_server):
        url, handler = mock_server
        client = GravixClient(url, "test-key", service="auth-svc", max_retries=0)

        client.send_fact(RequestFact(
            method="GET",
            path_template="/api/v1/users/{id}",
            status_code=200,
            latency_ms=42,
        ))
        client.close()

        assert len(handler.received_requests) == 1
        method, path, body, headers = handler.received_requests[0]
        assert method == "POST"
        assert path == "/api/v1/facts"
        # BaseHTTPRequestHandler normalizes header case; use case-insensitive lookup.
        headers_lower = {k.lower(): v for k, v in headers.items()}
        assert headers_lower["x-api-key"] == "test-key"
        assert headers_lower["content-type"] == "application/json"

        data = json.loads(body)
        assert data["event_id"] != ""
        assert data["event_time"] != ""
        assert data["service"] == "auth-svc"
        assert data["method"] == "GET"
        assert data["status_code"] == 200

    def test_service_override(self, mock_server):
        url, handler = mock_server
        client = GravixClient(url, "key", service="default-svc", max_retries=0)

        client.send_fact(RequestFact(
            service="override-svc",
            method="POST",
            path_template="/login",
            status_code=200,
            latency_ms=10,
        ))
        client.close()

        data = json.loads(handler.received_requests[0][2])
        assert data["service"] == "override-svc"


class TestRecordEvent:
    def test_sends_event(self, mock_server):
        url, handler = mock_server
        client = GravixClient(url, "key", service="deploy-svc", max_retries=0)

        client.record_event(ServiceEvent(
            event_type="deploy_completed",
            message="v1.2.3 deployed",
            properties={"version": "1.2.3", "branch": "main"},
        ))
        client.close()

        assert len(handler.received_requests) == 1
        _, path, body, _ = handler.received_requests[0]
        assert path == "/api/v1/events"

        data = json.loads(body)
        assert data["event_type"] == "deploy_completed"
        assert data["service"] == "deploy-svc"
        assert data["properties"]["version"] == "1.2.3"


class TestAutoSanitize:
    def test_sanitizes_path(self, mock_server):
        url, handler = mock_server
        client = GravixClient(url, "key", service="svc", max_retries=0)

        client.send_fact(RequestFact(
            method="GET",
            path_template="/users/550e8400-e29b-41d4-a716-446655440000/orders",
            status_code=200,
            latency_ms=10,
        ))
        client.close()

        data = json.loads(handler.received_requests[0][2])
        assert data["path_template"] == "/users/{id}/orders"

    def test_sanitize_disabled(self, mock_server):
        url, handler = mock_server
        raw = "/users/550e8400-e29b-41d4-a716-446655440000"
        client = GravixClient(url, "key", auto_sanitize=False, max_retries=0)

        client.send_fact(RequestFact(
            service="svc",
            method="GET",
            path_template=raw,
            status_code=200,
            latency_ms=10,
        ))
        client.close()

        data = json.loads(handler.received_requests[0][2])
        assert data["path_template"] == raw


class TestBatching:
    def test_flush_on_size(self, mock_server):
        url, handler = mock_server
        client = GravixClient(
            url, "key", service="svc", batch_size=3,
            flush_interval=60.0, max_retries=0,
        )

        for _ in range(3):
            client.record_fact(RequestFact(
                method="GET", path_template="/health",
                status_code=200, latency_ms=10,
            ))

        time.sleep(0.2)
        client.close()

        # Should have flushed to batch endpoint.
        batch_reqs = [r for r in handler.received_requests if r[1] == "/api/v1/facts/batch"]
        assert len(batch_reqs) >= 1

        # Count lines in JSONL body.
        body = batch_reqs[0][2]
        lines = [l for l in body.strip().split("\n") if l]
        assert len(lines) == 3

    def test_flush_on_close(self, mock_server):
        url, handler = mock_server
        client = GravixClient(
            url, "key", service="svc", batch_size=100,
            flush_interval=60.0, max_retries=0,
        )

        client.record_fact(RequestFact(
            method="GET", path_template="/health",
            status_code=200, latency_ms=10,
        ))
        client.close()

        batch_reqs = [r for r in handler.received_requests if r[1] == "/api/v1/facts/batch"]
        assert len(batch_reqs) >= 1


class TestRetry:
    def test_retry_on_429(self, mock_server):
        url, handler = mock_server

        def respond(count):
            if count == 1:
                return 429, {"error": "rate limit", "code": 429}
            return 201, None

        handler.server_responses["/api/v1/facts"] = respond

        client = GravixClient(url, "key", max_retries=2)
        client.send_fact(RequestFact(
            service="svc", method="GET", path_template="/health",
            status_code=200, latency_ms=10,
        ))
        client.close()

        fact_reqs = [r for r in handler.received_requests if r[1] == "/api/v1/facts"]
        assert len(fact_reqs) == 2

    def test_no_retry_on_400(self, mock_server):
        url, handler = mock_server
        handler.server_responses["/api/v1/facts"] = (400, {"error": "bad request", "code": 400})

        client = GravixClient(url, "key", max_retries=3)

        with pytest.raises(APIError) as exc_info:
            client.send_fact(RequestFact(
                service="svc", method="GET", path_template="/health",
                status_code=200, latency_ms=10,
            ))

        assert exc_info.value.status_code == 400
        client.close()

        fact_reqs = [r for r in handler.received_requests if r[1] == "/api/v1/facts"]
        assert len(fact_reqs) == 1

    def test_no_retry_on_401(self, mock_server):
        url, handler = mock_server
        handler.server_responses["/api/v1/facts"] = (401, {"error": "unauthorized", "code": 401})

        client = GravixClient(url, "key", max_retries=3)

        with pytest.raises(APIError) as exc_info:
            client.send_fact(RequestFact(
                service="svc", method="GET", path_template="/health",
                status_code=200, latency_ms=10,
            ))

        assert exc_info.value.status_code == 401
        client.close()

        fact_reqs = [r for r in handler.received_requests if r[1] == "/api/v1/facts"]
        assert len(fact_reqs) == 1


class TestContextManager:
    def test_context_manager(self, mock_server):
        url, handler = mock_server

        with GravixClient(url, "key", service="svc", batch_size=100,
                          flush_interval=60.0, max_retries=0) as client:
            client.record_fact(RequestFact(
                method="GET", path_template="/health",
                status_code=200, latency_ms=10,
            ))

        # Should have flushed on exit.
        batch_reqs = [r for r in handler.received_requests if r[1] == "/api/v1/facts/batch"]
        assert len(batch_reqs) >= 1


class TestOnError:
    def test_on_error_callback(self, mock_server):
        url, handler = mock_server
        handler.server_responses["/api/v1/facts/batch"] = (500, {"error": "server error", "code": 500})

        errors = []
        client = GravixClient(
            url, "key", service="svc", batch_size=1,
            flush_interval=60.0, max_retries=0,
            on_error=lambda e: errors.append(e),
        )

        client.record_fact(RequestFact(
            method="GET", path_template="/health",
            status_code=200, latency_ms=10,
        ))

        time.sleep(0.3)
        client.close()

        assert len(errors) >= 1
