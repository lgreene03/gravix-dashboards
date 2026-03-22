"""Tests for Gravix client."""

import json
from http.server import HTTPServer, BaseHTTPRequestHandler
import threading

from gravix.client import Client
from gravix.types import RequestFact


class MockHandler(BaseHTTPRequestHandler):
    received = []

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        MockHandler.received.append({
            "path": self.path,
            "body": body,
            "api_key": self.headers.get("X-API-Key"),
        })
        self.send_response(201)
        self.end_headers()
        self.wfile.write(b'{"accepted": 1}')

    def log_message(self, format, *args):
        pass  # suppress logs


def test_send_fact():
    MockHandler.received = []
    server = HTTPServer(("127.0.0.1", 0), MockHandler)
    port = server.server_address[1]
    t = threading.Thread(target=server.handle_request)
    t.daemon = True
    t.start()

    client = Client(f"http://127.0.0.1:{port}", "test-key", service="test-svc")
    fact = RequestFact(
        service="test-svc",
        method="GET",
        path_template="/api/test",
        status_code=200,
        latency_ms=42,
    )
    client.send_fact(fact)
    client.close()
    server.server_close()

    assert len(MockHandler.received) == 1
    req = MockHandler.received[0]
    assert req["path"] == "/api/v1/facts"
    assert req["api_key"] == "test-key"
    data = json.loads(req["body"])
    assert data["method"] == "GET"
    assert data["status_code"] == 200


def test_record_fact_batching():
    MockHandler.received = []
    server = HTTPServer(("127.0.0.1", 0), MockHandler)
    port = server.server_address[1]
    t = threading.Thread(target=server.handle_request)
    t.daemon = True
    t.start()

    client = Client(
        f"http://127.0.0.1:{port}",
        "test-key",
        service="test-svc",
        batch_size=2,
        flush_interval=60,
    )

    # Record 2 facts — should trigger batch at size 2
    for i in range(2):
        client.record_fact(RequestFact(
            service="test-svc",
            method="GET",
            path_template=f"/api/test/{i}",
            status_code=200,
            latency_ms=10,
        ))

    import time
    time.sleep(0.5)
    client.close()
    server.server_close()

    assert len(MockHandler.received) >= 1
    req = MockHandler.received[0]
    assert req["path"] == "/api/v1/facts/batch"


def test_auto_sanitize():
    client = Client("http://localhost:9999", "test-key", service="test")
    fact = RequestFact(
        service="test",
        method="GET",
        path_template="/users/550e8400-e29b-41d4-a716-446655440000",
        status_code=200,
        latency_ms=10,
    )
    client._enrich_fact(fact)
    assert fact.path_template == "/users/{id}"
    client.close()


def test_enrich_sets_defaults():
    client = Client("http://localhost:9999", "test-key", service="default-svc")
    fact = RequestFact(
        service="",
        method="POST",
        path_template="/test",
        status_code=201,
        latency_ms=5,
    )
    client._enrich_fact(fact)
    assert fact.service == "default-svc"
    assert fact.event_id != ""
    assert fact.event_time != ""
    client.close()
