"""Data types for the Gravix API."""

from __future__ import annotations

import uuid
from dataclasses import dataclass, field, asdict
from datetime import datetime, timezone
from typing import Dict, Optional


def _generate_event_id() -> str:
    """Generate a UUIDv7-style ID (falls back to v4 if uuid7 unavailable)."""
    try:
        return str(uuid.uuid7())
    except AttributeError:
        return str(uuid.uuid4())


def _now_iso() -> str:
    """Return current UTC time in ISO 8601 format."""
    return datetime.now(timezone.utc).isoformat()


@dataclass
class RequestFact:
    """A single HTTP request observation.

    ``event_id`` and ``event_time`` are auto-generated if not provided.
    """

    service: str = ""
    method: str = ""
    path_template: str = ""
    status_code: int = 0
    latency_ms: int = 0
    event_id: str = ""
    event_time: str = ""
    user_agent_family: str = ""

    def to_dict(self) -> dict:
        """Serialize to a dict suitable for JSON encoding."""
        d = asdict(self)
        # Omit empty optional fields.
        if not d.get("user_agent_family"):
            del d["user_agent_family"]
        return d


@dataclass
class ServiceEvent:
    """A service lifecycle event (deploy, restart, scale, etc.).

    ``event_id`` and ``event_time`` are auto-generated if not provided.
    """

    service: str = ""
    event_type: str = ""
    event_id: str = ""
    event_time: str = ""
    entity_id: str = ""
    message: str = ""
    properties: Optional[Dict[str, str]] = field(default_factory=dict)

    def to_dict(self) -> dict:
        """Serialize to a dict suitable for JSON encoding."""
        d = asdict(self)
        # Omit empty optional fields.
        for key in ("entity_id", "message"):
            if not d.get(key):
                del d[key]
        if not d.get("properties"):
            del d["properties"]
        return d


@dataclass
class BatchResult:
    """Response from the batch facts endpoint."""

    accepted: int = 0
    rejected: int = 0
    errors: list = field(default_factory=list)


class APIError(Exception):
    """Error returned by the Gravix API."""

    def __init__(self, message: str, status_code: int):
        super().__init__(message)
        self.message = message
        self.status_code = status_code

    def __str__(self) -> str:
        return f"APIError({self.status_code}): {self.message}"
