"""Path template sanitization.

Replaces raw UUIDs, numeric IDs (4+ digits), and hex tokens in URL paths
with ``{id}`` placeholders to prevent high-cardinality metric explosion.
"""

from __future__ import annotations

import re

# Matches standard UUID format (any version).
_UUID_RE = re.compile(
    r"[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}"
)

# Matches numeric IDs with 4 or more digits in path segments.
_RAW_ID_RE = re.compile(r"/[0-9]{4,}(/|$)")

# Matches hex strings of 16+ characters in path segments.
_HEX_TOKEN_RE = re.compile(r"/[a-fA-F0-9]{16,}(/|$)")


def _replace_segment(match: re.Match) -> str:
    """Replace a matched path segment, preserving trailing slash."""
    if match.group(0).endswith("/"):
        return "/{id}/"
    return "/{id}"


def sanitize_path(path: str) -> str:
    """Replace raw IDs in a URL path with ``{id}`` placeholders.

    Examples::

        >>> sanitize_path("/users/550e8400-e29b-41d4-a716-446655440000/orders")
        '/users/{id}/orders'
        >>> sanitize_path("/api/v1/products/12345")
        '/api/v1/products/{id}'
        >>> sanitize_path("/api/v1/users/{id}")
        '/api/v1/users/{id}'
    """
    # Replace UUIDs first (most specific).
    result = _UUID_RE.sub("{id}", path)
    # Replace numeric IDs (4+ digits).
    result = _RAW_ID_RE.sub(_replace_segment, result)
    # Replace long hex tokens.
    result = _HEX_TOKEN_RE.sub(_replace_segment, result)
    return result
