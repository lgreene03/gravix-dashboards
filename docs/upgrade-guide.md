# Upgrade and Migration Guide

This guide covers upgrading Gravix across Docker Compose and Kubernetes environments, including database migrations, rollback procedures, and post-upgrade verification.

For initial deployment instructions, see [Deployment Guide](deployment-guide.md). For disaster recovery, see [Disaster Recovery Runbook](disaster-recovery.md).

---

## Pre-Upgrade Checklist

Complete every item before starting an upgrade.

1. **Verify current version.** Record the running version so you can roll back to it.

   ```bash
   # Docker Compose
   docker-compose exec ingestion cat /app/VERSION 2>/dev/null || docker inspect gravix-ingestion --format '{{.Config.Image}}'

   # Kubernetes
   helm list -o json | jq '.[].app_version'
   kubectl get deployment gravix-ingestion -o jsonpath='{.spec.template.spec.containers[0].image}'
   ```

2. **Review the changelog.** Read the release notes for every version between your current version and the target. Pay special attention to entries marked "BREAKING" or "MIGRATION REQUIRED".

3. **Back up the tenant database.**

   ```bash
   # SQLite (local / single-node)
   cp data/gravix.db data/gravix.db.bak-$(date +%Y%m%d%H%M%S)

   # PostgreSQL (production)
   pg_dump -Fc gravix > gravix-$(date +%Y%m%d%H%M%S).dump
   ```

4. **Back up Helm values** (Kubernetes only).

   ```bash
   helm get values gravix -o yaml > gravix-values-backup-$(date +%Y%m%d).yaml
   ```

5. **Check disk space.** Migrations and new Parquet files require free space. Ensure at least 20% free on the data volume.

   ```bash
   df -h data/
   ```

6. **Drain traffic** (optional, for zero-downtime-sensitive environments). Redirect the load generator away from the instance being upgraded, or scale it down temporarily.

---

## Docker Compose Upgrades

### Step 1: Pull New Images

```bash
docker-compose pull
```

Or rebuild from source if running from a local checkout:

```bash
git pull origin main
docker-compose build
```

### Step 2: Stop Services

```bash
docker-compose down
```

Data volumes are preserved by default. Do not pass `-v` unless you intend a full reset.

### Step 3: Run Database Migrations

Migrations run automatically when the gateway or ingestion service starts -- the `RunMigrations` function in `pkg/tenantdb/migrate.go` applies all pending `.up.sql` files on boot. If you prefer to run migrations manually before starting services:

```bash
go run ./cmd/seed_tenants/ -db ./data/gravix.db -migrate-only
```

Verify the current schema version:

```bash
sqlite3 data/gravix.db "SELECT * FROM schema_migrations ORDER BY version;"
```

### Step 4: Start Services

```bash
docker-compose up -d
```

### Step 5: Verify Health

Follow the verification steps in the "Verification" section below.

---

## Kubernetes / Helm Upgrades

### Step 1: Diff the Changes

Preview what Helm will change before applying:

```bash
helm diff upgrade gravix ./deploy/gravix \
  --values ./deploy/gravix/values.yaml \
  --values ./deploy/gravix/values-prod.yaml
```

If `helm-diff` is not installed, use `helm upgrade --dry-run` instead.

### Step 2: Run the Upgrade

```bash
helm upgrade gravix ./deploy/gravix \
  --values ./deploy/gravix/values.yaml \
  --values ./deploy/gravix/values-prod.yaml \
  --set global.imageRegistry="ghcr.io/<org>/gravix-dashboards" \
  --set ingestion.image.tag="<new-tag>"
```

The order of `--values` flags matters: `values-prod.yaml` must come after `values.yaml`.

### Step 3: Monitor the Rollout

```bash
kubectl rollout status deployment/gravix-ingestion
kubectl rollout status deployment/gravix-cube
kubectl rollout status deployment/gravix-gateway
```

### Step 4: Verify

Follow the verification steps in the "Verification" section below.

### Blue-Green Considerations

Gravix does not ship a built-in blue-green deployment mechanism. If you need blue-green upgrades:

1. Deploy the new version to a separate namespace (e.g., `gravix-green`).
2. Point it at the same S3/MinIO bucket (read-only) and a cloned tenant database.
3. Run smoke tests against the green deployment.
4. Switch ingress traffic from blue to green.
5. Tear down the blue deployment after confirming stability.

Be aware that two deployments writing to the same S3 bucket concurrently can cause data conflicts. The green environment should be read-only until the cutover.

---

## Database Migrations

### How Migrations Work

Gravix uses a custom migration runner (`pkg/tenantdb/migrate.go`), not golang-migrate. Key details:

- Migration files live in `pkg/tenantdb/migrations/sqlite/` and `pkg/tenantdb/migrations/postgres/`.
- Files follow the naming convention `NNNNNN_description.up.sql` (e.g., `000004_phase31_enterprise.up.sql`).
- The version number is extracted from the filename prefix. Migrations run in sorted order.
- Applied versions are tracked in the `schema_migrations` table.
- Migrations run automatically on service startup via `RunMigrations()`.
- Pre-existing databases (those with a `tenants` table but no `schema_migrations` table) are detected and force-set to version 1.

### Current Migrations

| Version | File | Description |
|---------|------|-------------|
| 1 | `000001_initial_schema.up.sql` | Baseline schema (tenants, api_keys, users, alerts, audit) |
| 2 | `000002_rename_plans.up.sql` | Renames legacy plan names to 5-tier naming |
| 3 | `000003_add_trial_fields.up.sql` | Adds trial_started_at and trial_ends_at to tenants |
| 4 | `000004_phase31_enterprise.up.sql` | SSO configs, sessions, API key scopes, 2FA, multi-org |

### Checking Current Version

```bash
# SQLite
sqlite3 data/gravix.db "SELECT version, applied_at FROM schema_migrations ORDER BY version;"

# PostgreSQL
psql gravix -c "SELECT version, applied_at FROM schema_migrations ORDER BY version;"
```

### Running Migrations Manually

Migrations are embedded in the Go binary via `//go:embed` and run at startup. To apply them without starting the full service, use the seed command or start and immediately stop the service:

```bash
# Docker Compose -- start just the gateway briefly
docker-compose up gateway --no-deps -d
docker-compose logs gateway | grep "migrations complete"
docker-compose stop gateway
```

### Migration Rollback

There are no automatic down-migrations. To roll back a migration:

1. Restore from the database backup taken during the pre-upgrade checklist.
2. Alternatively, write and apply a manual reversal SQL script.

This is why backing up the database before every upgrade is mandatory.

---

## Breaking Changes

### How to Identify Breaking Changes

1. **Release notes.** Check for entries marked "BREAKING" in the changelog.
2. **Migration files.** Review new `.up.sql` files for `ALTER TABLE`, `DROP`, or `UPDATE` statements that change existing columns or data.
3. **Protobuf changes.** If `proto/gravix.proto` has changed, regenerate client code and check for field renumbering or removed fields.
4. **Helm values.** Compare `values.yaml` across versions for renamed, removed, or newly required fields:
   ```bash
   diff <(git show OLD_TAG:deploy/gravix/values.yaml) <(git show NEW_TAG:deploy/gravix/values.yaml)
   ```
5. **API changes.** Check `docs/07-api-reference.md` for endpoint or payload changes.

### Common Migration Issues

| Issue | Symptom | Resolution |
|-------|---------|------------|
| Schema version mismatch | Service logs show "migration failed" | Check `schema_migrations` table; restore backup if corrupted |
| Column already exists | `ALTER TABLE ... ADD COLUMN` fails | Migration may have partially applied; manually insert the version into `schema_migrations` |
| Plan name migration skipped | Tenants stuck on old plan names | Run migration 2 manually: `UPDATE tenants SET plan = 'team' WHERE plan = 'starter';` |
| Disk full during migration | Write errors in logs | Free disk space, restore backup, retry |
| Locked database (SQLite) | "database is locked" errors | Stop all services accessing the database, then retry |

---

## Version-Specific Notes

This section will be updated with instructions for specific version transitions as they arise.

### v0.x to v1.0 (placeholder)

_No version-specific notes yet. This section will be populated as breaking changes are introduced in future releases._

<!--
Template for future entries:

### vX.Y to vX.Z

**Breaking changes:**
- Description of breaking change

**Migration steps:**
1. Step-by-step instructions

**Rollback notes:**
- Any version-specific rollback considerations
-->

---

## Rollback Procedure

### Docker Compose Rollback

1. Stop the current services:
   ```bash
   docker-compose down
   ```

2. Restore the database backup:
   ```bash
   cp data/gravix.db.bak-<timestamp> data/gravix.db
   ```

3. Check out the previous version:
   ```bash
   git checkout <previous-tag>
   ```

4. Rebuild and start:
   ```bash
   docker-compose up -d --build
   ```

5. Verify health (see "Verification" below).

### Kubernetes Rollback

1. List release history:
   ```bash
   helm history gravix
   ```

2. Roll back to the previous release:
   ```bash
   helm rollback gravix
   ```

   Or roll back to a specific revision:
   ```bash
   helm rollback gravix <revision-number>
   ```

   The repository also provides a rollback script with confirmation prompts:
   ```bash
   ./scripts/rollback.sh gravix <revision>
   ```

3. If the database was migrated, restore from backup:
   ```bash
   # PostgreSQL
   pg_restore -d gravix --clean gravix-<timestamp>.dump
   ```

4. Verify all pods are running:
   ```bash
   kubectl get pods -l app.kubernetes.io/instance=gravix
   kubectl rollout status deployment/gravix-ingestion
   ```

5. Verify health (see "Verification" below).

### Important Rollback Notes

- Helm rollback reverts Kubernetes resources but does not revert database schema changes. Always restore from a database backup if the upgrade included migrations.
- If the rollup job wrote Parquet files in a new format, old Trino queries may fail. Clear the affected warehouse partition and let the rollup regenerate it.
- Rollback of plan name changes (migration 2) requires manual SQL or a backup restore.

---

## Verification

Run these checks after every upgrade or rollback.

### Health Check Endpoints

```bash
# Ingestion liveness
curl -sf http://localhost:8090/live && echo "OK" || echo "FAIL"

# Dashboard health
curl -sf http://localhost:8000/healthz && echo "OK" || echo "FAIL"

# Cube readiness
curl -sf http://localhost:4000/readyz && echo "OK" || echo "FAIL"

# Trino status
curl -sf http://localhost:8081/v1/info | jq .starting

# Prometheus health
curl -sf http://localhost:9090/-/healthy && echo "OK" || echo "FAIL"

# Grafana health
curl -sf http://localhost:3000/api/health | jq .database
```

For Kubernetes, replace `localhost:<port>` with the appropriate service URL or use `kubectl port-forward`.

### Smoke Test

Run the golden path smoke test, which validates end-to-end data flow without requiring Docker:

```bash
./scripts/golden_path_test.sh
```

### Data Flow Verification

```bash
# Check raw data ingestion (MinIO/S3)
docker exec gravix-minio mc ls myminio/gravix/raw/request_facts/ --recursive | head

# Check warehouse Parquet files
docker exec gravix-minio mc ls myminio/gravix/warehouse/request_metrics_minute/ --recursive | head

# Query Trino
docker exec gravix-trino trino --execute "SELECT count(*) FROM gravix.raw.request_metrics_minute"
```

### Database Schema Verification

Confirm the schema version matches expectations after migration:

```bash
sqlite3 data/gravix.db "SELECT version, applied_at FROM schema_migrations ORDER BY version DESC LIMIT 1;"
```

### Monitoring Dashboards

After upgrade, check:

- **Grafana** (http://localhost:3000): Verify dashboards load and show current data.
- **Prometheus** (http://localhost:9090): Confirm scrape targets are up under Status > Targets.
- **Cube Playground** (http://localhost:4000): Run a test query to confirm the semantic layer is functional.

---

## Related Documentation

- [Deployment Guide](deployment-guide.md) -- initial deployment and configuration
- [Operations](operations.md) -- day-to-day operational procedures
- [Disaster Recovery Runbook](disaster-recovery.md) -- RTO/RPO targets and restore procedures
- [System Truth](00-system-truth.md) -- core design principles and constraints
- [API Reference](07-api-reference.md) -- ingestion API endpoints and payloads
