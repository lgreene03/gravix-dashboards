# SPEC GRVX-1402: Bring-your-own-bucket — facts land in the customer's own S3

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1402 |
| **Phase** | 14 |
| **Goal** | G8.2 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §7.2/§2.2 `ee/tenancy/` — bring-your-own-bucket exists only because Gravix Cloud runs many installations for other people; a self-hoster already owns their own disk. Crippleware Test, all NO: Q1 NO (a self-hoster's data already sits in their own storage; nothing is missing), Q2 NO, Q3 NO, Q4 NO, Q5 NO (the feature is unbuildable outside a multi-tenant context, not merely unpriced there). |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1401 |
| **Blocks** | GRVX-1403 |
| **Effort** | 8 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

After this spec, a Gravix Cloud tenant can register their own S3 (or S3-compatible) bucket and
credentials, have Gravix verify the bucket is writable, readable and deletable, and have that
tenant's ingestion and rollup traffic routed to write facts and warehouse Parquet **only** into
that customer-owned bucket — using the unmodified OSS `ingestion-service` and rollup binaries,
configured entirely through the environment variables they already read. No core file changes
by this spec.

## 2. Context the implementer needs

- `services/ingestion/main.go:717-736` — the ingestion binary builds exactly one
  `storage.ObjectStore` per process at startup, from `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`,
  `S3_ACCESS_KEY`, `S3_SECRET_KEY` (falls back to `LocalStore` if `S3_ENDPOINT` is unset). There
  is one store per process; the binary has no per-request bucket routing.
- `transforms/request_metrics_minute/main.go:174`, `transforms/service_events_daily/main.go:161`,
  `transforms/service_events_detail/main.go:159` — the same five `S3_*` environment variables
  configure the rollup binaries' object store, one store per process.
- `pkg/storage/s3.go:39` — `func NewS3Store(ctx context.Context, endpoint, region, bucket, accessKey, secretKey string) (*S3Store, error)` is exported and already used exactly this way by `services/ingestion/main.go:721-727`.
- `pkg/storage/storage.go:17-25` — the `ObjectStore` interface (`Put`, `PutWithStorageClass`, `Get`, `Delete`, `Exists`, `List`).
- `deploy/gravix/values.yaml:23-28` — `global.storage.{endpoint,region,bucket,accessKey,secretKey}` map directly to the five `S3_*` environment variables via the Helm templates (`deploy/gravix/templates/ingestion.yaml:47-58`, `.../rollup-job.yaml`, `.../events-rollup-job.yaml`, `.../events-detail-job.yaml`, `.../retention-job.yaml`).
- `pkg/auth/jwt.go:62` — `func NewTokenService(secret string, duration time.Duration) *TokenService`, `.Validate(tokenStr string) (*Claims, error)`; `pkg/auth/jwt.go` (`Claims.TenantID`, `Claims.Role`, `Claims.HasRole`). This is the same JWT scheme the core gateway issues from `POST /api/gateway/login`.
- Gravix Cloud's control plane runs one dedicated ingestion + rollup deployment per BYOB tenant (a "cell"), each parameterised by that tenant's own `S3_*` values, following the existing single-store-per-process model exactly — this is Cloud infrastructure topology, not a code change to any binary.
- No BYOB configuration store exists anywhere in the repository today.

## 3. Non-goals for this spec

- Do NOT modify `services/ingestion/main.go`, any file under `transforms/`, or `pkg/storage/`.
  The unmodified OSS binaries and the unmodified `storage.NewS3Store` constructor are used
  exactly as they exist today.
- Do NOT implement the Kubernetes controller that provisions a per-tenant cell deployment. That
  is `ee/fleet/` (Phase 13, `GRVX-1307`) — this spec produces the bucket configuration, its
  validation, and the exact environment variable map that feeds such a controller; it does not
  build the controller.
- Do NOT change how non-BYOB (shared-bucket) Cloud tenants are routed. Only tenants with a
  registered `BucketConfig` are affected.
- Do NOT implement billing implications of BYOB (some plans may price it differently). That is
  `ee/billing/`, out of scope here.
- This spec does not cross non-goal §5 (No High-Cardinality Dimensions) — bucket configuration
  is one row per tenant, not a per-request dimension.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/tenancy/byob/config.go` | `BucketConfig`, `ValidateBucket`, `ProvisionEnv` |
| `ee/tenancy/byob/config_test.go` | Tests for validation and env mapping |
| `ee/tenancy/byob/store.go` | `Store` interface, `SQLiteStore` |
| `ee/tenancy/byob/store_test.go` | Tests for the store |
| `ee/tenancy/byob/migrations/0001_bucket_configs.up.sql` | Embedded schema for the `bucket_configs` table |
| `ee/tenancy/byob/cmd/byob-api/main.go` | Standalone HTTP API for registering and verifying BYOB buckets |
| `ee/tenancy/byob/cmd/byob-api/main_test.go` | HTTP handler tests |

### 4.2 Files to modify

None. This spec touches zero core files.

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/main.go` | Reused unmodified; BYOB is a deployment-time parameterisation, not a code change |
| `transforms/**` | Same reason |
| `pkg/storage/**` | `storage.NewS3Store` is imported, never modified |
| `deploy/gravix/**` | Cloud's per-tenant cell deployment is Cloud-only infrastructure outside the OSS Helm chart; the OSS chart's single-tenant values are unaffected |

## 5. Interface contract

### 5.1 `ee/tenancy/byob/config.go`

```go
// Package byob lets a Gravix Cloud tenant register their own S3-compatible
// bucket, verifies Gravix can write, read and delete objects in it, and
// produces the exact environment variables that let the unmodified OSS
// ingestion and rollup binaries write into that bucket instead of the
// shared Cloud bucket.
package byob

import (
	"context"
	"errors"
	"time"
)

// BucketConfig is one tenant's bring-your-own-bucket registration.
type BucketConfig struct {
	TenantID        string
	Endpoint        string // e.g. "https://s3.us-east-1.amazonaws.com"
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Status          string // "pending", "verified", "failed"
	FailureReason   string // non-empty only when Status == "failed"
	VerifiedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const (
	StatusPending  = "pending"
	StatusVerified = "verified"
	StatusFailed   = "failed"
)

// ValidateBucket writes a marker object, reads it back, confirms the bytes
// round-trip, then deletes it and confirms the delete took effect. It
// constructs its own storage.ObjectStore from cfg via storage.NewS3Store —
// it never reuses a process-wide store.
func ValidateBucket(ctx context.Context, cfg BucketConfig) error

// ProvisionEnv returns the exact five environment variables that, injected
// into an ingestion or rollup deployment, make the unmodified OSS binaries
// read and write cfg's bucket. Keys match services/ingestion/main.go:717-727.
func ProvisionEnv(cfg BucketConfig) map[string]string

var (
	ErrBucketNotWritable     = errors.New("byob: bucket rejected a write")
	ErrBucketNotReadable     = errors.New("byob: object written but could not be read back")
	ErrBucketContentMismatch = errors.New("byob: object content did not round-trip")
	ErrBucketNotDeletable    = errors.New("byob: bucket rejected a delete of the marker object")
	ErrBucketDeleteNotVisible = errors.New("byob: deleted marker object is still visible")
)
```

`ProvisionEnv` returns exactly:

```go
map[string]string{
	"S3_ENDPOINT":   cfg.Endpoint,
	"S3_REGION":     cfg.Region,
	"S3_BUCKET":     cfg.Bucket,
	"S3_ACCESS_KEY": cfg.AccessKeyID,
	"S3_SECRET_KEY": cfg.SecretAccessKey,
}
```

### 5.2 `ee/tenancy/byob/store.go`

```go
package byob

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Store persists BucketConfig rows.
type Store interface {
	Put(ctx context.Context, cfg BucketConfig) error
	Get(ctx context.Context, tenantID string) (BucketConfig, error)
	MarkVerified(ctx context.Context, tenantID string, at time.Time) error
	MarkFailed(ctx context.Context, tenantID string, reason string) error
}

// SQLiteStore implements Store on a dedicated SQLite database. It owns its
// own migrations under ee/tenancy/byob/migrations and never touches
// pkg/tenantdb's schema or database file.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens db and applies embedded migrations idempotently.
func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error)

func (s *SQLiteStore) Put(ctx context.Context, cfg BucketConfig) error
func (s *SQLiteStore) Get(ctx context.Context, tenantID string) (BucketConfig, error)
func (s *SQLiteStore) MarkVerified(ctx context.Context, tenantID string, at time.Time) error
func (s *SQLiteStore) MarkFailed(ctx context.Context, tenantID string, reason string) error

var ErrNotFound = errors.New("byob: tenant has no bucket configuration")
```

`0001_bucket_configs.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS bucket_configs (
    tenant_id           TEXT PRIMARY KEY,
    endpoint            TEXT NOT NULL,
    region              TEXT NOT NULL,
    bucket              TEXT NOT NULL,
    access_key_id       TEXT NOT NULL,
    secret_access_key   TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'pending',
    failure_reason      TEXT NOT NULL DEFAULT '',
    verified_at         TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
```

### 5.3 `ee/tenancy/byob/cmd/byob-api/main.go` — HTTP contract

Deployed as its own service in Gravix Cloud's control plane, reachable at a path routed by
Cloud's ingress (outside this repository's OSS Helm chart). Authenticated with the same JWT
scheme the core gateway issues.

**`POST /byob/buckets`**

Request header: `Authorization: Bearer <jwt>`, validated with `auth.NewTokenService(secret, 0).Validate(token)`; the request is rejected unless `claims.HasRole(auth.RoleAdmin)` and `claims.TenantID` equals the body's `tenant_id`.

Request body:

```json
{
  "tenant_id": "ten_abc123",
  "endpoint": "https://s3.us-east-1.amazonaws.com",
  "region": "us-east-1",
  "bucket": "acme-gravix-facts",
  "access_key_id": "AKIA...",
  "secret_access_key": "..."
}
```

Responses:

| Status | Body | Condition |
|---|---|---|
| 201 | `{"tenant_id":"ten_abc123","status":"verified"}` | `ValidateBucket` succeeds; row stored with `status=verified`, `verified_at=now` |
| 422 | `{"error":"<ValidateBucket error message>","status":"failed"}` | `ValidateBucket` fails; row stored with `status=failed`, `failure_reason=<message>` |
| 400 | `{"error":"invalid JSON body"}` | body does not decode |
| 400 | `{"error":"tenant_id, endpoint, region, bucket, access_key_id, and secret_access_key are all required"}` | any field empty |
| 401 | `{"error":"missing or invalid Authorization header"}` | header absent or malformed |
| 403 | `{"error":"admin role required for the target tenant"}` | claims fail the role/tenant check |
| 405 | `{"error":"POST required"}` | wrong method |

**`GET /byob/buckets/{tenant_id}`** — same auth rule. 200 with the stored `BucketConfig` (secret fields redacted: `access_key_id` shows only its first 4 and last 4 characters, `secret_access_key` is omitted entirely from the response body). 404 `{"error":"byob: tenant has no bucket configuration"}` when `Store.Get` returns `ErrNotFound`.

**`POST /byob/buckets/{tenant_id}/verify`** — same auth rule. Re-runs `ValidateBucket` against the already-stored configuration and updates its status. Same response shapes as `POST /byob/buckets`.

## 6. Behaviour

1. `ValidateBucket` builds `store, err := storage.NewS3Store(ctx, cfg.Endpoint, cfg.Region, cfg.Bucket, cfg.AccessKeyID, cfg.SecretAccessKey)`. If `err != nil`, return it wrapped as `ErrBucketNotWritable`.
2. Generate a marker key `fmt.Sprintf("gravix-byob-verify/%s", uuid.New())` and marker content, a random 32-byte payload.
3. `store.Put(ctx, markerKey, bytes.NewReader(content))`. On error, return `ErrBucketNotWritable` wrapped with the underlying error.
4. `rc, err := store.Get(ctx, markerKey)`. On error, return `ErrBucketNotReadable` wrapped with the underlying error.
5. Read all of `rc` and compare byte-for-byte with `content`. On mismatch, return `ErrBucketContentMismatch`.
6. `store.Delete(ctx, markerKey)`. On error, return `ErrBucketNotDeletable` wrapped with the underlying error.
7. `exists, err := store.Exists(ctx, markerKey)`. If `err == nil && exists`, return `ErrBucketDeleteNotVisible`.
8. If every step passed, return `nil`.
9. `byob-api`'s handlers call `ValidateBucket` synchronously within the request (no background job); a slow or unreachable bucket surfaces as a `422` to the caller within the request's lifetime, capped by a 15-second `context.WithTimeout` set by the handler.
10. `ProvisionEnv` is a pure function with no I/O; it does not read or write the `Store`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Bucket rejects the marker `Put` (bad credentials, wrong region, bucket does not exist) | `ValidateBucket` returns `ErrBucketNotWritable` | `byob: bucket rejected a write` |
| `Put` succeeds but `Get` fails | `ValidateBucket` returns `ErrBucketNotReadable` | `byob: object written but could not be read back` |
| `Get` returns different bytes than were written | `ValidateBucket` returns `ErrBucketContentMismatch` | `byob: object content did not round-trip` |
| `Delete` fails | `ValidateBucket` returns `ErrBucketNotDeletable` | `byob: bucket rejected a delete of the marker object` |
| Marker still visible after a successful `Delete` call | `ValidateBucket` returns `ErrBucketDeleteNotVisible` | `byob: deleted marker object is still visible` |
| `GET /byob/buckets/{tenant_id}` for an unregistered tenant | 404 | `byob: tenant has no bucket configuration` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `ValidateBucket` returns `nil` against a fake S3 backend that accepts put/get/delete and round-trips content | `TestValidateBucketAcceptsHealthyBucket` |
| AC-2 | `ValidateBucket` returns `ErrBucketNotWritable` when `Put` fails | `TestValidateBucketRejectsUnwritableBucket` |
| AC-3 | `ValidateBucket` returns `ErrBucketContentMismatch` when `Get` returns altered bytes | `TestValidateBucketRejectsContentMismatch` |
| AC-4 | `ProvisionEnv` returns exactly the five keys `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` with the config's values | `TestProvisionEnvMatchesIngestionEnvVars` |
| AC-5 | `SQLiteStore.Get` after `Put` returns a byte-for-byte-equal `BucketConfig` (aside from timestamps) | `TestSQLiteStoreRoundTrips` |
| AC-6 | `POST /byob/buckets` returns 201 and `status: verified` for a valid bucket and a caller with the admin role for the target tenant | `TestByobAPIRegistersVerifiedBucket` |
| AC-7 | `POST /byob/buckets` returns 403 when the caller's JWT tenant differs from the body's `tenant_id` | `TestByobAPIRejectsCrossTenantRegistration` |
| AC-8 | `GET /byob/buckets/{tenant_id}` never includes `secret_access_key` in its response body | `TestByobAPIRedactsSecretInResponse` |
| AC-9 | Deleting `ee/` and running `make build-oss && make test-oss` succeeds | verified by the §8 command |

## 8. Verification

```bash
# 1. Package tests
go test ./ee/tenancy/byob/... -v -cover
# expect: PASS, coverage reported

# 2. HTTP API tests
go test ./ee/tenancy/byob/cmd/byob-api/... -v
# expect: PASS

# 3. Secret redaction is enforced, not assumed
go test ./ee/tenancy/byob/cmd/byob-api/... -run TestByobAPIRedactsSecretInResponse -v
# expect: PASS

# 4. Open-core integrity
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All nine acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1 modified (§4.2 is empty for this spec)
- [ ] `docs-engineer` delta merged (a "Bring your own bucket" setup guide), or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] The five keys returned by `ProvisionEnv` are shown, in the implementation report, to match `services/ingestion/main.go:717-727` verbatim

## 10. Escalation

| If you find… | Do this |
|---|---|
| A criterion that cannot be met without modifying a file outside `ee/` | Return `SPEC DEFECT: §4 — needs <path>` |
| `pkg/auth.NewTokenService` or `Claims` change shape before this spec is built | Return `SPEC DEFECT: §5.3 — pkg/auth signature mismatch` |
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| A needed capability that only a core file change can provide | Return `EXTENSION POINT REQUIRED` with the proposed interface |
