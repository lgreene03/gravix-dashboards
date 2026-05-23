"""Tests for data types."""

from gravix.types import RequestFact, ServiceEvent


def test_request_fact_to_dict():
    f = RequestFact(
        service="api",
        method="GET",
        path_template="/users/{id}",
        status_code=200,
        latency_ms=42,
    )
    d = f.to_dict()
    assert d["service"] == "api"
    assert d["method"] == "GET"
    assert d["status_code"] == 200
    assert "event_id" not in d  # empty, omitted


def test_request_fact_with_optional_fields():
    f = RequestFact(
        service="api",
        method="POST",
        path_template="/items",
        status_code=201,
        latency_ms=10,
        event_id="test-id",
        user_agent_family="curl",
    )
    d = f.to_dict()
    assert d["event_id"] == "test-id"
    assert d["user_agent_family"] == "curl"


def test_service_event_to_dict():
    e = ServiceEvent(
        service="api",
        event_type="deploy",
        message="v1.2.3 deployed",
        properties={"version": "1.2.3"},
    )
    d = e.to_dict()
    assert d["service"] == "api"
    assert d["event_type"] == "deploy"
    assert d["properties"]["version"] == "1.2.3"


def test_service_event_minimal():
    e = ServiceEvent(service="api", event_type="restart")
    d = e.to_dict()
    assert d == {"service": "api", "event_type": "restart"}
