# Gravix Security Model

## Authentication

### API Key Authentication (Ingestion)
- All ingestion requests require an `X-API-Key` header.
- In single-tenant mode, the key is validated against the `API_KEY` environment variable.
- In multi-tenant mode, keys are looked up in SQLite (`pkg/tenantdb`) with bcrypt-hashed storage.
- Keys support optional expiry (`expires_at` column). Expired keys are rejected at validation time.

### JWT Authentication (Gateway/Dashboard)
- Dashboard sessions use JWT tokens (HS256 signing, `pkg/auth/jwt.go`).
- Tokens contain `tenant_id`, `user_id`, `email`, and `role` claims.
- Token validation rejects non-HMAC signing methods (prevents `alg:none` attacks).
- Tokens have configurable expiry (default: 1 hour).
- The dashboard monitors session expiry and prompts re-login before token expires.

### API Key Rotation
```bash
# Create a new key with 30-day expiry
curl -X POST http://localhost:8091/api/gateway/api-keys \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"rotated-key","expires_in_days":30}'

# List keys expiring within 7 days
curl http://localhost:8091/api/gateway/api-keys/expiring \
  -H "Authorization: Bearer $TOKEN"

# Revoke old key
curl -X DELETE http://localhost:8091/api/gateway/api-keys/$KEY_ID \
  -H "Authorization: Bearer $TOKEN"
```

## CSRF Protection

Gravix API endpoints are protected against CSRF through multiple layers:

1. **Content-Type enforcement**: Ingestion and gateway endpoints require `Content-Type: application/json`. HTML forms cannot submit JSON content types, preventing simple CSRF attacks.
2. **Authorization header**: All authenticated endpoints require `Authorization: Bearer <token>` or `X-API-Key` headers, which cannot be set by cross-origin form submissions.
3. **CORS policy**: The `Access-Control-Allow-Origin` header restricts cross-origin requests. Configure via `CORS_ALLOWED_ORIGIN` environment variable (default: `*` for development).

> **Note**: If HTML form endpoints are added in the future, they must implement CSRF tokens.

## Security Headers

### Gateway (`services/gateway/main.go`)
- `Content-Security-Policy`: Restricts script/style/connect sources to `'self'` and approved CDNs
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `Cache-Control: no-store`
- `Strict-Transport-Security: max-age=31536000; includeSubDomains` (HTTPS only)

### Dashboard (`storage/dashboard/nginx.conf`)
- Mirrors the CSP directives from the gateway
- Serves static files only — no server-side rendering

## Rate Limiting

Per-tenant rate limiting protects against abuse:

| Plan | Requests/second |
|------|----------------|
| Free | 10 |
| Starter | 100 |
| Pro | 1000 |

Rate-limited requests receive `429 Too Many Requests` with `Retry-After` header.

## Path Traversal Protection

The `pkg/storage/local.go` `sanitizeKey()` function prevents path traversal:
- Normalizes paths with `filepath.Clean()`
- Rejects any path containing `..`
- Verifies resolved path stays within the base directory

All storage operations (Put, Get, Delete, List) use this sanitization.

## Input Validation

### Schema Validation (`schemas/`)
- `RequestFact`: Enforces UUIDv7 event IDs, valid status codes (100-599), non-negative latency, path template rules (no raw UUIDs, no numeric IDs >= 4 digits, no query params).
- `ServiceEvent`: Enforces UUIDv7 event IDs, snake_case event types, flat string properties (no nested JSON), property value length limit (1024 chars).
- Schema validation has 100% test coverage plus fuzz tests.

### Request Size Limits
- Ingestion: 1 MB request body limit
- Gateway: 1 MB max header size

## Dependency Scanning

Run `govulncheck ./...` to check for known Go module vulnerabilities. This is included in the golden path test script (`scripts/golden_path_test.sh`).

Install: `go install golang.org/x/vuln/cmd/govulncheck@latest`
