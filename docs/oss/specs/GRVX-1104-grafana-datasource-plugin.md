# SPEC GRVX-1104: Grafana datasource plugin — keep Grafana, get Gravix correctness

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1104 |
| **Phase** | 11 |
| **Goal** | G5.3 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 — Visualise row: "Grafana datasource plugin". §7.3 Q1 = YES (a team standardised on Grafana should not have to switch tools to use Gravix) → core. |
| **Implementer role** | `frontend-engineer` |
| **Depends on** | GRVX-1101 |
| **Blocks** | GRVX-1109 |
| **Effort** | 6 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

A Grafana backend datasource plugin, built and versioned as its own Go module, queries
`gravix.raw.request_metrics_minute` through Trino and renders it as a Grafana time-series panel,
so an existing Grafana installation can show Gravix data without Gravix operating its own
dashboard for that team.

## 2. Context the implementer needs

- `storage/trino/init.sql:36-51` — the exact column list of `gravix.raw.request_metrics_minute`:
  `bucket_start` (`VARCHAR`, format `YYYY-MM-DD HH:MM:SS` UTC per
  `transforms/request_metrics_minute/main.go:469`), `service`, `method`, `path_template`,
  `request_count`, `error_count`, `error_rate`, `p50_latency_ms`, `p95_latency_ms`, `p99_latency_ms`,
  `event_day`.
- `CLAUDE.md`'s endpoint table — Trino is reachable at `http://localhost:8081` in the reference
  compose stack; the plugin's default configuration uses this host/port.
- `terraform-provider-gravix/go.mod:1-4` — the existing precedent for a Gravix component that lives
  in this repository but is its **own** Go module (`module github.com/lgreene/terraform-provider-gravix`,
  its own `go.mod`), so its dependency tree is invisible to `go build ./...`/`go test ./...` at the
  repository root and cannot violate `make check-boundary`.
- No `go.work` file exists at the repository root (verified: `test -f go.work` is false), so Go
  tooling at the root never descends into a directory with its own `go.mod` — the same isolation
  `terraform-provider-gravix/` already relies on.

## 3. Non-goals for this spec

- Do NOT add `github.com/grafana/grafana-plugin-sdk-go` or a Trino client library to the root
  `go.mod`. The plugin is its own Go module, per §2.
- Do NOT submit the plugin to the Grafana plugin catalog for signing in this spec. Grafana catalog
  submission requires an organisation account and a signing API key that do not exist in this
  repository's CI. §9 records this as a human step for `oss-steward`, performed after this spec's
  acceptance criteria (which cover build, packaging, and unsigned local installation) pass.
- Do NOT implement alerting, annotations, or variable/template queries. This spec's query type is a
  single time-series panel query against `request_metrics_minute`. Broader query support is future
  work, not part of G5.3's acceptance bar ("Gravix data in existing Grafana dashboards").
- This spec does not cross non-goal §6 (No Custom Query Language): the plugin's `QueryModel` is a
  small structured filter (service/method/path/field); it builds a fixed, parameterized SQL
  statement — it does not expose free-text SQL or invent a query language.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `grafana-plugin/gravix-datasource/go.mod` | Separate Go module, per §2 |
| `grafana-plugin/gravix-datasource/plugin.json` | Grafana plugin manifest |
| `grafana-plugin/gravix-datasource/pkg/main.go` | Backend plugin entrypoint |
| `grafana-plugin/gravix-datasource/pkg/datasource.go` | `Datasource` implementation |
| `grafana-plugin/gravix-datasource/pkg/datasource_test.go` | Tests |
| `grafana-plugin/gravix-datasource/src/module.ts` | Frontend plugin registration |
| `grafana-plugin/gravix-datasource/src/ConfigEditor.tsx` | Datasource connection settings UI |
| `grafana-plugin/gravix-datasource/src/QueryEditor.tsx` | Query builder UI |
| `grafana-plugin/gravix-datasource/src/types.ts` | Shared TypeScript types mirroring `QueryModel` |
| `grafana-plugin/gravix-datasource/package.json` | Frontend build config |
| `docs-site/docs/grafana-plugin.md` | Install and usage guide |
| `docs-site/docs/grafana-plugin-publishing.md` | The manual catalog-submission procedure for `oss-steward` |

### 4.2 Files to modify

None — this is a new, isolated component.

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `go.mod` (repository root) | The plugin is its own module, per §2/§3 |
| `cube/model/**`, `dashboards/**` | This spec bypasses Cube and the native dashboard entirely |
| `storage/trino/**` | The plugin queries the existing catalog; it does not change it |

## 5. Interface contract

### 5.1 `grafana-plugin/gravix-datasource/plugin.json`

```json
{
  "type": "datasource",
  "name": "Gravix",
  "id": "gravix-datasource",
  "backend": true,
  "executable": "gpx_gravix_datasource",
  "info": {
    "description": "Query Gravix request metrics via Trino.",
    "version": "1.0.0"
  },
  "dependencies": {
    "grafanaDependency": ">=10.0.0"
  }
}
```

### 5.2 `grafana-plugin/gravix-datasource/pkg/datasource.go`

```go
package plugin

// Datasource implements the Grafana backend contract for Gravix.
type Datasource struct {
    trinoHost string
    trinoPort int
}

// NewDatasource constructs a Datasource from Grafana's instance settings.
// trinoHost defaults to "localhost" and trinoPort to 8081 when
// settings.JSONData does not carry "trinoHost"/"trinoPort" keys.
func NewDatasource(ctx context.Context, settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error)

// QueryData answers one or more panel queries in a single Grafana request.
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error)

// CheckHealth runs "SELECT 1" against Trino and reports the result as
// Grafana's connection-test outcome.
func (d *Datasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error)

// QueryModel is the JSON shape of one Grafana panel query targeting Gravix.
type QueryModel struct {
    Service      string `json:"service"`
    Method       string `json:"method,omitempty"`
    PathTemplate string `json:"pathTemplate,omitempty"`
    Field        string `json:"field"` // one of: request_count, error_count, error_rate, p50_latency_ms, p95_latency_ms, p99_latency_ms
}

// fieldColumn maps a QueryModel.Field to the literal SQL column name it
// selects. Only names in this map may appear in a generated SELECT clause;
// any other value is rejected before a query is built.
func fieldColumn(field string) (string, error)

var ErrUnsupportedField = errors.New("gravix datasource: unsupported field")
```

## 6. Behaviour

1. `NewDatasource` reads `trinoHost`/`trinoPort` from `settings.JSONData`, falling back to
   `"localhost"`/`8081`, and opens a `database/sql` connection using a pure-Go Trino driver imported
   only by this module's `go.mod` (module path recorded in the implementation report; the driver
   must have zero CGO build constraints — verified by `go build` succeeding with `CGO_ENABLED=0`).
2. `QueryData`, for each query in `req.Queries`, unmarshals `query.JSON` into `QueryModel`. If
   `Service == ""`, the query returns an error frame with message `"service is required"`. Otherwise
   `fieldColumn` maps `Field` to a column name; on `ErrUnsupportedField`, the query returns an error
   frame with that error's message.
3. The handler builds and executes:
   ```sql
   SELECT bucket_start, <column>
   FROM gravix.raw.request_metrics_minute
   WHERE service = ?
     AND (? = '' OR method = ?)
     AND (? = '' OR path_template = ?)
     AND bucket_start >= ? AND bucket_start < ?
   ORDER BY bucket_start ASC
   ```
   with `bucket_start` range bounds formatted `"2006-01-02 15:04:05"` from `req.Queries[i].TimeRange`,
   matching `transforms/request_metrics_minute/main.go:469`'s write format exactly.
4. Each result row becomes one point in a Grafana `data.Frame` with two fields: a `time.Time` field
   parsed from `bucket_start` with that same layout, and a `float64` field named after `Field`.
5. `CheckHealth` executes `SELECT 1`; success returns `backend.HealthStatusOk` with message
   `"gravix: trino reachable"`; any error returns `backend.HealthStatusError` with message
   `fmt.Sprintf("gravix: trino unreachable: %v", err)`.
6. The frontend `QueryEditor.tsx` renders four inputs bound to `QueryModel`'s four fields; `Field`
   is a fixed-option dropdown listing exactly the six values `fieldColumn` accepts.
7. Build: `cd grafana-plugin/gravix-datasource && go build -o dist/gpx_gravix_datasource ./pkg/... &&
   npm install && npm run build`, producing `dist/` containing the compiled backend binary, the
   frontend bundle, and `plugin.json`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `QueryModel.Service == ""` | error frame, no query executed | `service is required` |
| `QueryModel.Field` not in the fixed set | error frame, no query executed | `gravix datasource: unsupported field` |
| Trino unreachable during `CheckHealth` | `backend.HealthStatusError` | `gravix: trino unreachable: <err>` |
| Trino unreachable during `QueryData` | error frame | `gravix: query failed: <err>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `fieldColumn` returns the correct column for each of the six supported field values | `TestFieldColumnSupportedValues` |
| AC-2 | `fieldColumn` returns `ErrUnsupportedField` for an unrecognised value | `TestFieldColumnRejectsUnsupported` |
| AC-3 | `QueryData` returns an error frame with the exact message when `Service` is empty | `TestQueryDataRequiresService` |
| AC-4 | `CheckHealth` against a live Trino returns `backend.HealthStatusOk` | `TestCheckHealthOK` |
| AC-5 | `CheckHealth` against an unreachable host returns `backend.HealthStatusError` with the exact message format | `TestCheckHealthUnreachable` |
| AC-6 | The plugin backend binary builds with `CGO_ENABLED=0` | `TestBuildIsCGOFree` (a shell-invoking Go test asserting `go build` exits 0 under `CGO_ENABLED=0`) |
| AC-7 | A Grafana container with the unsigned plugin volume-mounted and unsigned-loading enabled reports the plugin as installed | `TestGrafanaLoadsPlugin` (`tests/e2e/`, skips if `docker` unavailable) |

## 8. Verification

```bash
# 1. Build the plugin (isolated module, does not touch the root go.mod)
cd grafana-plugin/gravix-datasource
go build -o dist/gpx_gravix_datasource ./pkg/...
CGO_ENABLED=0 go build -o /dev/null ./pkg/...
npm install && npm run build
cd -
# expect: all three commands exit 0

# 2. Backend unit tests
go test ./grafana-plugin/gravix-datasource/pkg/... -v
# expect: PASS

# 3. Grafana load test (requires docker + a running Trino)
docker-compose up -d trino
go test ./tests/e2e/... -run TestGrafanaLoadsPlugin -v
# expect: PASS or SKIP with an actionable message

# 4. Confirm the root module is untouched
git diff --stat go.mod go.sum
# expect: no output

# 5. Open-core integrity
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All seven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1 modified; root `go.mod`/`go.sum` unchanged
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] `docs-site/docs/grafana-plugin-publishing.md` names the exact manual steps and required
      credentials for `oss-steward` to submit the built `dist/` to the Grafana plugin catalog

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| No pure-Go, CGO-free Trino client library exists at implementation time | Return `EXTENSION POINT REQUIRED` naming the constraint; do not add a CGO driver. |
| A criterion that cannot be met without an out-of-scope file | Return `SPEC DEFECT: §4 — needs <path>`. |
