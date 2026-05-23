"""Data types for Gravix SDK."""

from dataclasses import dataclass, field
from typing import Optional


@dataclass
class RequestFact:
    """A single HTTP request observation."""
    service: str
    method: str
    path_template: str
    status_code: int
    latency_ms: int
    event_id: str = ""
    event_time: str = ""
    user_agent_family: str = ""

    def to_dict(self) -> dict:
        d = {
            "service": self.service,
            "method": self.method,
            "path_template": self.path_template,
            "status_code": self.status_code,
            "latency_ms": self.latency_ms,
        }
        if self.event_id:
            d["event_id"] = self.event_id
        if self.event_time:
            d["event_time"] = self.event_time
        if self.user_agent_family:
            d["user_agent_family"] = self.user_agent_family
        return d


@dataclass
class ServiceEvent:
    """A lifecycle event (deploy, restart, scale, etc.)."""
    service: str
    event_type: str
    event_id: str = ""
    event_time: str = ""
    entity_id: str = ""
    message: str = ""
    properties: dict = field(default_factory=dict)

    def to_dict(self) -> dict:
        d = {
            "service": self.service,
            "event_type": self.event_type,
        }
        if self.event_id:
            d["event_id"] = self.event_id
        if self.event_time:
            d["event_time"] = self.event_time
        if self.entity_id:
            d["entity_id"] = self.entity_id
        if self.message:
            d["message"] = self.message
        if self.properties:
            d["properties"] = self.properties
        return d
