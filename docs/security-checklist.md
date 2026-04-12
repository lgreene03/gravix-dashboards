# Gravix Pre-GA Security Checklist

Operator checklist for hardening a Gravix deployment before going to production.

## Secrets and Keys

- [ ] **JWT_SECRET**: Set to a cryptographically random value of 64+ characters.
      Generate with: `openssl rand -base64 48`
      Do NOT reuse across environments.

- [ ] **TOTP_ENCRYPTION_KEY**: Set to a separate cryptographically random value, distinct from JWT_SECRET.
      Generate with: `openssl rand -base64 32`
      This key encrypts TOTP secrets at rest; rotating it invalidates all enrolled 2FA.

- [ ] **API_KEY**: Set to a cryptographically random value of 32+ characters.
      Generate with: `openssl rand -hex 32`

## CORS

- [ ] **CORS_ALLOWED_ORIGINS**: Set to the specific domain(s) that host your dashboard (comma-separated).
      Do NOT leave as `*` in production. The gateway logs a warning at startup if this is misconfigured.

## TLS

- [ ] **Enable TLS termination** in front of the gateway (via reverse proxy, load balancer, or Kubernetes Ingress).
      All traffic between clients and the gateway must be encrypted in transit.

## Rate Limiting

- [ ] **Review rate limit thresholds** configured via environment variables or defaults in the gateway.
      Ensure they are appropriate for your expected traffic volume.
      The login endpoint enforces account lockout after 5 failed attempts within 15 minutes.

## Monitoring and Alerting

- [ ] **Configure Prometheus alerting for auth failures.**
      Set up alerts on the `gateway_requests_total` metric filtered by the login path and 401/429 status codes.
      Monitor `gateway_audit_errors_total` for audit log write failures.
      Example alert rule:

      ```yaml
      - alert: HighAuthFailureRate
        expr: rate(gateway_requests_total{path="/api/gateway/login", status=~"401|429"}[5m]) > 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Elevated authentication failure rate"
      ```

## Token Lifecycle

- [ ] **Access token TTL** is set to 1 hour. Clients must use the `/api/gateway/refresh` endpoint to obtain fresh tokens.
      Ensure your frontend or SDK implements token refresh before expiry.

## Environment

- [ ] **GRAVIX_ENV**: Set to `production` (or any value other than `development`/`dev`) so that security warnings are active at startup.
