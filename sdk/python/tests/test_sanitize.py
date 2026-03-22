"""Tests for path sanitization."""

from gravix.sanitize import sanitize_path


def test_uuid_replacement():
    assert sanitize_path("/users/550e8400-e29b-41d4-a716-446655440000") == "/users/{id}"


def test_numeric_id_replacement():
    assert sanitize_path("/orders/12345/items") == "/orders/{id}/items"


def test_short_number_kept():
    assert sanitize_path("/v2/api") == "/v2/api"


def test_query_params_stripped():
    assert sanitize_path("/users?page=1&limit=10") == "/users"


def test_already_templated():
    assert sanitize_path("/users/{id}") == "/users/{id}"


def test_multiple_ids():
    path = "/users/550e8400-e29b-41d4-a716-446655440000/orders/99999"
    result = sanitize_path(path)
    assert result == "/users/{id}/orders/{id}"


def test_no_ids():
    assert sanitize_path("/api/health") == "/api/health"


def test_root_path():
    assert sanitize_path("/") == "/"
