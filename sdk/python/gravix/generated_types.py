"""
Auto-generated from services/gateway/openapi.json
Do not edit manually — run scripts/generate-sdk-types.sh
Generated from OpenAPI spec v1.0.0
"""

from __future__ import annotations
from dataclasses import dataclass, field
from typing import Any


@dataclass
class APIKey:
    id: str | None = None
    name: str | None = None
    key_prefix: str | None = None
    status: str | None = None
    created_at: str | None = None
    expires_at: str | None = None

@dataclass
class AlertHistoryEntry:
    id: str | None = None
    rule_id: str | None = None
    tenant_id: str | None = None
    metric: str | None = None
    threshold: float | None = None
    actual_value: float | None = None
    service: str | None = None
    path_template: str | None = None
    status: str | None = None
    message: str | None = None
    created_at: str | None = None

@dataclass
class AlertRule:
    id: str | None = None
    tenant_id: str | None = None
    name: str | None = None
    metric: str | None = None
    operator: str | None = None
    threshold: float | None = None
    window_minutes: int | None = None
    cooldown_minutes: int | None = None
    service: str | None = None
    path_template: str | None = None
    channel_id: str | None = None
    status: str | None = None
    last_triggered_at: str | None = None
    created_at: str | None = None

@dataclass
class AlertRuleRequest:
    name: str
    metric: str
    operator: str
    threshold: float
    window_minutes: int
    cooldown_minutes: int
    channel_id: str
    service: str | None = None
    path_template: str | None = None

@dataclass
class AuditEntry:
    id: str | None = None
    tenant_id: str | None = None
    user_id: str | None = None
    action: str | None = None
    resource: str | None = None
    resource_id: str | None = None
    detail: str | None = None
    ip_address: str | None = None
    created_at: str | None = None

@dataclass
class ConsentRecord:
    id: str | None = None
    type: str | None = None
    version: str | None = None
    accepted: bool | None = None
    ip_address: str | None = None
    created_at: str | None = None

@dataclass
class CreateAPIKeyRequest:
    name: str
    expires_in_days: int | None = None

@dataclass
class CreateAPIKeyResponse:
    key: str | None = None  # Plaintext key (shown only once)
    key_id: str | None = None
    name: str | None = None
    key_prefix: str | None = None
    expires_at: str | None = None

@dataclass
class DLQEntry:
    timestamp: str | None = None
    tenant_id: str | None = None
    fact_type: str | None = None
    error: str | None = None
    raw_json: dict[str, Any] | None = None

@dataclass
class Invitation:
    id: str | None = None
    email: str | None = None
    role: str | None = None
    status: str | None = None
    invited_by: str | None = None
    created_at: str | None = None
    expires_at: str | None = None

@dataclass
class LoginRequest:
    email: str
    password: str
    totp_code: str | None = None  # Required if 2FA is enabled
    recovery_code: str | None = None  # Alternative to totp_code for 2FA

@dataclass
class LoginResponse:
    token: str | None = None
    tenant_id: str | None = None
    user_id: str | None = None
    email: str | None = None
    role: str | None = None
    plan: str | None = None
    email_verified: bool | None = None
    requires_2fa: bool | None = None  # True when 2FA code is needed to complete login

@dataclass
class MessageResponse:
    message: str | None = None

@dataclass
class NotificationChannel:
    id: str | None = None
    tenant_id: str | None = None
    name: str | None = None
    type: str | None = None
    config: str | None = None
    created_at: str | None = None

@dataclass
class Pagination:
    page: int | None = None
    per_page: int | None = None
    total: int | None = None

@dataclass
class PartialResponse:
    accepted: int | None = None
    rejected: int | None = None
    errors: list[dict[str, Any]] | None = None

@dataclass
class Referral:
    code: str | None = None
    tenant_id: str | None = None
    redeemed_by: str | None = None
    coupon_id: str | None = None
    status: str | None = None
    expires_at: str | None = None
    created_at: str | None = None

@dataclass
class RegisterRequest:
    name: str
    email: str
    password: str
    accept_tos: bool
    captcha_token: str | None = None

@dataclass
class RegisterResponse:
    token: str | None = None
    tenant_id: str | None = None
    user_id: str | None = None
    email: str | None = None
    role: str | None = None
    plan: str | None = None
    email_verification_required: bool | None = None

@dataclass
class RequestFact:
    event_id: str  # UUIDv7 event identifier
    event_time: str
    service: str
    method: str
    path_template: str  # URL path with {id} placeholders, no query params
    status_code: int
    latency_ms: int
    user_agent_family: str | None = None

@dataclass
class RetentionPolicy:
    plan: str | None = None
    min_days: int | None = None
    max_days: int | None = None
    plan_default_facts_days: int | None = None
    facts_days: int | None = None
    metrics_days: int | None = None
    traces_days: int | None = None
    updated_at: str | None = None

@dataclass
class SSOConfigResponse:
    configured: bool | None = None
    provider: str | None = None
    enabled: bool | None = None
    entity_id: str | None = None
    sso_url: str | None = None
    has_certificate: bool | None = None
    client_id: str | None = None
    issuer: str | None = None
    has_secret: bool | None = None

@dataclass
class ServiceEvent:
    event_id: str
    event_time: str
    service: str
    event_type: str
    description: str | None = None
    metadata: dict[str, Any] | None = None

@dataclass
class Session:
    id: str | None = None
    ip_address: str | None = None
    user_agent: str | None = None
    created_at: str | None = None
    expires_at: str | None = None

@dataclass
class TeamMember:
    id: str | None = None
    email: str | None = None
    role: str | None = None
    status: str | None = None
    email_verified: bool | None = None
    last_login_at: str | None = None

@dataclass
class TraceSpan:
    trace_id: str | None = None
    span_id: str | None = None
    parent_span_id: str | None = None
    event_time: str | None = None
    service: str | None = None
    method: str | None = None
    path_template: str | None = None
    status_code: int | None = None
    latency_ms: int | None = None

@dataclass
class TraceSummary:
    trace_id: str | None = None
    root_service: str | None = None
    root_path: str | None = None
    total_latency_ms: int | None = None
    span_count: int | None = None
    event_time: str | None = None

@dataclass
class UsageStats:
    tenant_id: str | None = None
    today: int | None = None
    month_total: int | None = None
    period: str | None = None
    plan: str | None = None
    event_limit: int | None = None
    rate_limit_rps: int | None = None
    daily_counts: list[dict[str, Any]] | None = None

@dataclass
class UserInfo:
    user_id: str | None = None
    email: str | None = None
    role: str | None = None
    tenant_id: str | None = None
    tenant_name: str | None = None
    plan: str | None = None
    email_verified: bool | None = None
    status: str | None = None
    last_login_at: str | None = None

