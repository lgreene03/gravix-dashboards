// Auto-generated from services/gateway/openapi.json
// Do not edit manually — run scripts/generate-sdk-types.sh
// Generated from OpenAPI spec v1.0.0

export interface APIKey {
  id?: string;
  name?: string;
  key_prefix?: string;
  status?: "active" | "revoked";
  created_at?: string;
  expires_at?: string;
}

export interface AlertHistoryEntry {
  id?: string;
  rule_id?: string;
  tenant_id?: string;
  metric?: string;
  threshold?: number;
  actual_value?: number;
  service?: string;
  path_template?: string;
  status?: string;
  message?: string;
  created_at?: string;
}

export interface AlertRule {
  id?: string;
  tenant_id?: string;
  name?: string;
  metric?: string;
  operator?: string;
  threshold?: number;
  window_minutes?: number;
  cooldown_minutes?: number;
  service?: string;
  path_template?: string;
  channel_id?: string;
  status?: "active" | "paused";
  last_triggered_at?: string;
  created_at?: string;
}

export interface AlertRuleRequest {
  name: string;
  service?: string;
  metric: "error_rate" | "p50_latency" | "p95_latency" | "p99_latency" | "throughput";
  operator: "gt" | "lt" | "anomaly";
  threshold: number;
  window_minutes: number;
  cooldown_minutes: number;
  path_template?: string;
  channel_id: string;
}

export interface AuditEntry {
  id?: string;
  tenant_id?: string;
  user_id?: string;
  action?: string;
  resource?: string;
  resource_id?: string;
  detail?: string;
  ip_address?: string;
  created_at?: string;
}

export interface ConsentRecord {
  id?: string;
  type?: string;
  version?: string;
  accepted?: boolean;
  ip_address?: string;
  created_at?: string;
}

export interface CreateAPIKeyRequest {
  name: string;
  expires_in_days?: number;
}

export interface CreateAPIKeyResponse {
    /** Plaintext key (shown only once) */
  key?: string;
  key_id?: string;
  name?: string;
  key_prefix?: string;
  expires_at?: string;
}

export interface DLQEntry {
  timestamp?: string;
  tenant_id?: string;
  fact_type?: string;
  error?: string;
  raw_json?: Record<string, unknown>;
}

export interface Invitation {
  id?: string;
  email?: string;
  role?: string;
  status?: "pending" | "accepted" | "expired";
  invited_by?: string;
  created_at?: string;
  expires_at?: string;
}

export interface LoginRequest {
  email: string;
  password: string;
    /** Required if 2FA is enabled */
  totp_code?: string;
    /** Alternative to totp_code for 2FA */
  recovery_code?: string;
}

export interface LoginResponse {
  token?: string;
  tenant_id?: string;
  user_id?: string;
  email?: string;
  role?: string;
  plan?: string;
  email_verified?: boolean;
    /** True when 2FA code is needed to complete login */
  requires_2fa?: boolean;
}

export interface MessageResponse {
  message?: string;
}

export interface NotificationChannel {
  id?: string;
  tenant_id?: string;
  name?: string;
  type?: "slack" | "webhook";
  config?: string;
  created_at?: string;
}

export interface Pagination {
  page?: number;
  per_page?: number;
  total?: number;
}

export interface PartialResponse {
  accepted?: number;
  rejected?: number;
  errors?: {
  index?: number;
  error?: string;
}[];
}

export interface Referral {
  code?: string;
  tenant_id?: string;
  redeemed_by?: string;
  coupon_id?: string;
  status?: string;
  expires_at?: string;
  created_at?: string;
}

export interface RegisterRequest {
  name: string;
  email: string;
  password: string;
  captcha_token?: string;
  accept_tos: boolean;
}

export interface RegisterResponse {
  token?: string;
  tenant_id?: string;
  user_id?: string;
  email?: string;
  role?: string;
  plan?: string;
  email_verification_required?: boolean;
}

export interface RequestFact {
    /** UUIDv7 event identifier */
  event_id: string;
  event_time: string;
  service: string;
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE" | "HEAD" | "OPTIONS";
    /** URL path with {id} placeholders, no query params */
  path_template: string;
  status_code: number;
  latency_ms: number;
  user_agent_family?: string;
}

export interface RetentionPolicy {
  plan?: string;
  min_days?: number;
  max_days?: number;
  plan_default_facts_days?: number;
  facts_days?: number;
  metrics_days?: number;
  traces_days?: number;
  updated_at?: string;
}

export interface SSOConfigResponse {
  configured?: boolean;
  provider?: "saml" | "oidc";
  enabled?: boolean;
  entity_id?: string;
  sso_url?: string;
  has_certificate?: boolean;
  client_id?: string;
  issuer?: string;
  has_secret?: boolean;
}

export interface ServiceEvent {
  event_id: string;
  event_time: string;
  service: string;
  event_type: "deploy" | "rollback" | "scale" | "config_change" | "incident" | "alert" | "maintenance";
  description?: string;
  metadata?: Record<string, string>;
}

export interface Session {
  id?: string;
  ip_address?: string;
  user_agent?: string;
  created_at?: string;
  expires_at?: string;
}

export interface TeamMember {
  id?: string;
  email?: string;
  role?: string;
  status?: string;
  email_verified?: boolean;
  last_login_at?: string;
}

export interface TraceSpan {
  trace_id?: string;
  span_id?: string;
  parent_span_id?: string;
  event_time?: string;
  service?: string;
  method?: string;
  path_template?: string;
  status_code?: number;
  latency_ms?: number;
}

export interface TraceSummary {
  trace_id?: string;
  root_service?: string;
  root_path?: string;
  total_latency_ms?: number;
  span_count?: number;
  event_time?: string;
}

export interface UsageStats {
  tenant_id?: string;
  today?: number;
  month_total?: number;
  period?: string;
  plan?: string;
  event_limit?: number;
  rate_limit_rps?: number;
  daily_counts?: {
  day?: string;
  count?: number;
}[];
}

export interface UserInfo {
  user_id?: string;
  email?: string;
  role?: string;
  tenant_id?: string;
  tenant_name?: string;
  plan?: string;
  email_verified?: boolean;
  status?: string;
  last_login_at?: string;
}

