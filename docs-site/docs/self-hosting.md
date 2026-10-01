---
title: Self-Hosting
sidebar_position: 3
---

# Self-Hosting and Production Hardening

This guide covers the security, storage, and operational settings you should configure before running Gravix in production. The defaults in `.env.example` and the Helm chart are designed for local development and are not safe for production use.

## Authentication and secrets

### JWT secret

The gateway uses `JWT_SECRET` to sign and verify authentication tokens. In production:

- Generate a random value of at least 64 characters.
- Never reuse the same secret across environments.
- Rotate periodically and plan for token invalidation during rotation.

```bash
# Generate a strong JWT secret
openssl rand -base64 64
```

Set it in your `.env` or Helm values:

```
JWT_SECRET=<your-64-char-secret>
```

### TOTP encryption key

Two-factor authentication secrets are encrypted at rest using `TOTP_ENCRYPTION_KEY`. This must be a separate value from `JWT_SECRET` -- if left unset, the gateway falls back to `JWT_SECRET`, which is deprecated and will be removed in a future release.

```bash
# Generate a TOTP encryption key (32+ characters)
openssl rand -base64 32
```

```
TOTP_ENCRYPTION_KEY=<your-unique-totp-key>
```

### API keys

Ingestion API keys (`API_KEY`) must be at least 16 characters. Use a unique key per environment and rotate regularly.

## CORS configuration

By default, `CORS_ALLOWED_ORIGINS` is unset, which allows all origins (`*`) in local development. In production, restrict this to your actual domains:

```
CORS_ALLOWED_ORIGINS=https://app.gravix.io,https://dashboard.gravix.io
```

Never use `*` in production. This prevents cross-origin attacks against your gateway endpoints.

## Storage: S3 instead of MinIO

The Docker Compose setup uses MinIO as an S3-compatible local store. In production, use a managed object storage service such as AWS S3, Google Cloud Storage, or DigitalOcean Spaces.

Update the storage configuration:

```
S3_ENDPOINT=https://s3.us-east-1.amazonaws.com
S3_REGION=us-east-1
S3_BUCKET=your-gravix-bucket
S3_ACCESS_KEY=<iam-access-key>
S3_SECRET_KEY=<iam-secret-key>
```

For the Helm chart, configure under `global.storage`:

```yaml
global:
  storage:
    endpoint: "https://s3.us-east-1.amazonaws.com"
    region: "us-east-1"
    bucket: "your-gravix-bucket"
    accessKey: "<iam-access-key>"
    secretKey: "<iam-secret-key>"
```

For AWS, consider using IAM roles for service accounts (IRSA) instead of static credentials.

## TLS termination

Gravix services do not terminate TLS themselves. Place a reverse proxy or load balancer in front of the ingestion and gateway services to handle HTTPS:

- **Kubernetes**: Use the included Ingress template with cert-manager for automatic certificate provisioning. Enable it in your values file:

```yaml
ingress:
  enabled: true
  hosts:
    - host: api.gravix.io
      paths: ["/"]
  tls:
    - secretName: gravix-tls
      hosts:
        - api.gravix.io
```

- **Docker Compose**: Place nginx, Caddy, or Traefik in front of ports 8090/8091. Example with Caddy:

```
api.gravix.io {
    reverse_proxy localhost:8091
}

ingest.gravix.io {
    reverse_proxy localhost:8090
}
```

## Database

The gateway defaults to SQLite (`DB_DRIVER=sqlite`) with the database file at `data/gravix.db`. For production, switch to PostgreSQL:

```
DB_DRIVER=postgres
DATABASE_URL=postgres://gravix:<password>@your-db-host:5432/gravix?sslmode=require
```

The Docker Compose file includes a PostgreSQL container under the `postgres` profile:

```bash
docker compose --profile postgres up -d
```

## Backup and retention

### Automated data purge

Gravix enforces a 30-day retention policy. The purge container runs daily at 03:00 UTC and removes raw data and warehouse files older than 30 days. This is enabled by default in both Docker Compose and the Helm chart (`retention-job.yaml`).

### Database backups

Back up the tenant database regularly:

- **SQLite**: Copy `data/gravix.db` to a safe location on a schedule.
- **PostgreSQL**: Use `pg_dump` or a managed backup service.

The Helm chart includes a `db-backup-job.yaml` CronJob template for automated PostgreSQL backups.

### Object storage backups

Enable versioning on your S3 bucket so that overwritten or deleted Parquet files can be recovered. Configure lifecycle policies to transition old versions to cheaper storage classes.

## Monitoring and alerting

### Prometheus

Gravix ships a Prometheus configuration in `storage/prometheus/prometheus.yml` with pre-configured scrape targets and alert rules in `storage/prometheus/alert_rules.yml`.

In production, connect Prometheus to Alertmanager for notification delivery:

1. Deploy Alertmanager alongside Prometheus.
2. Configure notification routes (Slack, PagerDuty, email) in the Alertmanager config.
3. The Helm chart includes a `prometheusrule.yaml` template for Prometheus Operator-based alerting rules.

### Grafana

Grafana is pre-provisioned with a Prometheus data source. Default credentials are `admin`/`admin` -- change these immediately in production.

The Helm chart includes `grafana-dashboards.yaml` for provisioning dashboards automatically.

## Dashboard cache warming

The gateway asks Cube for the dashboard's default view, the last seven days, every 30 seconds and
once per active tenant. A view is slow only when nobody has asked for it since Cube started, or
since the date changed at midnight UTC. Once it has been asked, Cube answers from its cache and
refreshes in the background when a rollup writes. Warming means the first person to open the
dashboard after a restart, or on a new day, gets that cached answer too. Measured on the bootstrap
stack's limits, the default view's queries took up to about 3 seconds cold and under 100 ms warm.

It keeps at most one query in Cube at a time. Cube runs two at once, so warming never takes the last
slot from a user. A cycle still running at its time limit sends no further query and is logged as
`cache warm cycle abandoned after <limit>`. The query already running is allowed to finish, because
Cube would keep running it anyway.

| Variable | Default | Meaning |
|---|---|---|
| `CACHE_WARM_ENABLED` | `true` | Set `false` to turn warming off |
| `CACHE_WARM_INTERVAL` | `30s` | Time between cycle starts. It bounds how long the default view stays cold after a Cube restart or a new day |
| `CACHE_WARM_MAX_DURATION` | `60s` | A cycle still running after this is abandoned |

With many tenants, one cycle may not reach them all within the limit. The next cycle resumes at the
tenant the last one stopped on, so every tenant is reached in turn, but not every 30 seconds. Raise
`CACHE_WARM_MAX_DURATION` if each tenant needs to stay warm, or turn warming off.

## External secrets

For production Kubernetes deployments, avoid passing secrets through Helm values. The chart supports External Secrets Operator integration:

```yaml
externalSecrets:
  enabled: true
  secretStoreRef:
    name: "gravix-secret-store"
    kind: "ClusterSecretStore"
  remoteBase: "gravix/prod"
```

This pulls secrets from AWS Secrets Manager, HashiCorp Vault, or GCP Secret Manager instead of storing them in the cluster.

## Email configuration

For password resets, email verification, and team invitations, configure an SMTP provider:

```
EMAIL_PROVIDER=smtp
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USER=<smtp-user>
SMTP_PASS=<smtp-password>
EMAIL_FROM_ADDRESS=noreply@gravix.io
EMAIL_FROM_NAME=Gravix
BASE_URL=https://app.gravix.io
```

## Production checklist

Before going live, verify the following:

- [ ] `JWT_SECRET` is a unique, random 64+ character value
- [ ] `TOTP_ENCRYPTION_KEY` is set separately from `JWT_SECRET`
- [ ] `CORS_ALLOWED_ORIGINS` is set to specific domains (not `*`)
- [ ] `API_KEY` is at least 16 characters and unique per environment
- [ ] S3 storage points to a managed service, not MinIO
- [ ] TLS is configured via reverse proxy or Ingress
- [ ] Database is PostgreSQL with regular backups
- [ ] Alertmanager is connected to Prometheus
- [ ] Grafana default password has been changed
- [ ] Email provider is configured for transactional emails
- [ ] External Secrets Operator is used for Kubernetes deployments
