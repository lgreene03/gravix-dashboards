---
title: API Reference
sidebar_position: 8
---

# API Reference

Gravix exposes two HTTP services: the **Ingestion API** (port 8090) for sending request facts and service events, and the **Gateway API** (port 8091) for authentication, billing, alerting, team management, and analytics.

## OpenAPI specification

The full machine-readable API specification is available at:

```
GET http://localhost:8091/api/gateway/openapi.json
```

This returns an OpenAPI 3.1 document covering all endpoints. You can load it into tools like Swagger UI, Postman, or Insomnia for interactive exploration.

## Authentication

Gravix uses two authentication mechanisms:

- **API keys** for ingestion. Pass your API key in the `X-API-Key` header when sending facts or events.
- **JWT Bearer tokens** for gateway endpoints. Obtain a token by calling the login endpoint, then pass it as `Authorization: Bearer <token>`.

## API groups

### Ingestion

These endpoints accept raw observability data. Authenticated with API keys.

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/facts` | Ingest an array of request facts |
| POST | `/api/v1/events` | Ingest an array of service events |

### Auth

User registration, login, password management, and email verification.

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/gateway/register` | Register a new tenant and admin user |
| POST | `/api/gateway/login` | Authenticate and receive a JWT token |
| POST | `/api/gateway/logout` | Revoke the current token |
| POST | `/api/gateway/refresh` | Refresh an expiring JWT token |
| GET | `/api/gateway/me` | Get the current authenticated user |
| POST | `/api/gateway/forgot-password` | Request a password reset email |
| POST | `/api/gateway/reset-password` | Complete a password reset |
| GET | `/api/gateway/verify-email` | Verify an email address |
| PUT | `/api/gateway/password` | Change password |

### Alerting

Create and manage metric-based alert rules and notification channels.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/gateway/alert-rules` | List alert rules |
| POST | `/api/gateway/alert-rules` | Create an alert rule |
| PUT | `/api/gateway/alert-rules/{id}` | Update an alert rule |
| DELETE | `/api/gateway/alert-rules/{id}` | Delete an alert rule |
| GET | `/api/gateway/alert-history` | List alert firing history |
| GET | `/api/gateway/channels` | List notification channels |
| POST | `/api/gateway/channels` | Create a notification channel |
| DELETE | `/api/gateway/channels/{id}` | Delete a notification channel |

### Billing

Stripe-based billing, usage tracking, and invoice management.

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/gateway/billing/checkout` | Create a Stripe Checkout session |
| POST | `/api/gateway/billing/portal` | Get a Stripe billing portal URL |
| GET | `/api/gateway/billing/usage` | Get current billing period usage |
| GET | `/api/gateway/billing/invoices` | List invoices |
| GET | `/api/gateway/billing/usage/history` | Get historical usage data |

### Team

Invite members, manage roles, and list your team.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/gateway/team` | List team members |
| PUT | `/api/gateway/team` | Update a team member's role |
| POST | `/api/gateway/invitations` | Send a team invitation |
| POST | `/api/gateway/invitations/accept` | Accept a team invitation |
| GET | `/api/gateway/invitations` | List pending invitations |

### GDPR

Data access, portability, and consent management for GDPR compliance.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/gateway/gdpr/access` | Export all data associated with a user |
| GET | `/api/gateway/gdpr/portability` | Download data in a portable format |
| GET | `/api/gateway/consent` | List consent records |
| POST | `/api/gateway/consent` | Record a consent decision |
| DELETE | `/api/gateway/account` | Request account deletion |

### API Keys

Manage ingestion API keys programmatically.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/gateway/api-keys` | List API keys |
| POST | `/api/gateway/api-keys` | Create a new API key |
| DELETE | `/api/gateway/api-keys/{id}` | Revoke an API key |
| GET | `/api/gateway/api-keys/expiring` | List keys nearing expiration |

### Additional groups

The API also includes endpoints for **2FA** (TOTP setup and management), **SSO** (SAML configuration), **Sessions** (active session listing and revocation), **Organizations** (multi-org hierarchy), **Referrals** (referral code management), **Analytics** (dashboard data queries), **Traces** (sampled trace inspection), **DLQ** (dead letter queue replay), and **Admin** (audit log, retention policy, data export).

See the full OpenAPI spec for complete details on these endpoints.

## Example requests

### Register a new tenant

```bash
curl -X POST http://localhost:8091/api/gateway/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "admin@example.com",
    "password": "secure-password-here",
    "org_name": "My Company"
  }'
```

### Log in

```bash
curl -X POST http://localhost:8091/api/gateway/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "admin@example.com",
    "password": "secure-password-here"
  }'
```

The response includes a JWT token:

```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "user": { "id": "...", "email": "admin@example.com" }
}
```

### Send request facts

```bash
curl -X POST http://localhost:8090/api/v1/facts \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $GRAVIX_API_KEY" \
  -d '[
    {
      "event_id": "01920c4a-dead-7000-beef-000000000001",
      "event_time": "2026-04-10T12:00:00Z",
      "service": "payments-api",
      "method": "POST",
      "path_template": "/v1/charges/{id}",
      "status_code": 200,
      "latency_ms": 45,
      "user_agent_family": "python-requests"
    }
  ]'
```

A `200` response means all facts were accepted. A `207` response means some facts were rejected -- check the response body for per-fact error details.

### Create an alert rule

```bash
curl -X POST http://localhost:8091/api/gateway/alert-rules \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $JWT_TOKEN" \
  -d '{
    "name": "High error rate on payments-api",
    "service": "payments-api",
    "metric": "error_rate",
    "operator": "gt",
    "threshold": 0.05,
    "window_minutes": 5,
    "channel_id": "ch_slack_abc123"
  }'
```

## Health endpoints

Both services expose health check endpoints that do not require authentication:

| Endpoint | Service | Description |
|----------|---------|-------------|
| `GET /live` | Ingestion (8090), Gateway (8091) | Liveness probe -- returns 200 if the process is running |
| `GET /ready` | Gateway (8091) | Readiness probe -- returns 200 when the service can handle requests |

## Rate limiting

The ingestion API enforces rate limits. If you exceed the limit, the API returns `429 Too Many Requests`. The SDKs handle backoff automatically. If you are sending facts via raw HTTP, implement exponential backoff on 429 responses.
