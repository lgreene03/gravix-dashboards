"""Tests for path sanitization."""

import pytest
from gravix.sanitize import sanitize_path


@pytest.mark.parametrize(
    "input_path,expected",
    [
        ("/api/v1/users/{id}/orders", "/api/v1/users/{id}/orders"),
        (
            "/users/550e8400-e29b-41d4-a716-446655440000/orders",
            "/users/{id}/orders",
        ),
        (
            "/users/550E8400-E29B-41D4-A716-446655440000",
            "/users/{id}",
        ),
        ("/api/v1/products/1234", "/api/v1/products/{id}"),
        ("/api/v1/products/1234/", "/api/v1/products/{id}/"),
        ("/orders/9876543210/items", "/orders/{id}/items"),
        ("/api/v1/health", "/api/v1/health"),
        ("/items/123", "/items/123"),
        (
            "/tenants/550e8400-e29b-41d4-a716-446655440000/users/661f9400-f39c-51e5-b827-557766551111",
            "/tenants/{id}/users/{id}",
        ),
        (
            "/users/550e8400-e29b-41d4-a716-446655440000/posts/99999",
            "/users/{id}/posts/{id}",
        ),
        (
            "/sessions/a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6",
            "/sessions/{id}",
        ),
        ("/", "/"),
        ("", ""),
        ("/api/v1/users", "/api/v1/users"),
        ("/api/v2/orders", "/api/v2/orders"),
    ],
    ids=[
        "already_templated",
        "uuid_in_path",
        "uppercase_uuid",
        "numeric_id_4_digits",
        "numeric_id_trailing_slash",
        "numeric_id_many_digits",
        "short_numeric_preserved",
        "three_digit_preserved",
        "multiple_uuids",
        "mixed_uuid_and_numeric",
        "hex_token",
        "root_path",
        "empty_path",
        "v1_version_preserved",
        "v2_version_preserved",
    ],
)
def test_sanitize_path(input_path, expected):
    assert sanitize_path(input_path) == expected
