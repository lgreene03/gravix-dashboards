"""Path template sanitization — port of Go sanitize logic."""

import re

# Matches UUIDs (v1-v7)
_UUID_RE = re.compile(
    r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"
)

# Matches numeric IDs (4+ digits)
_NUMERIC_ID_RE = re.compile(r"\b\d{4,}\b")


def sanitize_path(path: str) -> str:
    """Replace raw UUIDs and numeric IDs in a URL path with {id} placeholders.

    Examples:
        /users/550e8400-e29b-41d4-a716-446655440000 -> /users/{id}
        /orders/12345/items -> /orders/{id}/items
    """
    # Strip query parameters
    if "?" in path:
        path = path.split("?", 1)[0]

    # Replace UUIDs first (more specific pattern)
    path = _UUID_RE.sub("{id}", path)

    # Replace numeric IDs in path segments
    parts = path.split("/")
    for i, part in enumerate(parts):
        if _NUMERIC_ID_RE.fullmatch(part):
            parts[i] = "{id}"
    path = "/".join(parts)

    # Collapse consecutive {id} placeholders
    while "/{id}/{id}" in path:
        path = path.replace("/{id}/{id}", "/{id}")

    return path
