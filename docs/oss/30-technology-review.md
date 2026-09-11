<!-- Reviewed 2026-09-11. Re-date this file whenever a version claim below is rechecked. -->
# Technology review

Whether each thing Gravix depends on is still the right choice, and where it is not.

Every version claim here was checked against the upstream registry on **2026-09-11**. A claim with no
date is worthless, so if you are reading this months later, treat the numbers as stale and recheck
before acting.

The verdicts are deliberately blunt. A dependency nobody re-examines is a dependency nobody
understands.

---

## 1. Summary

| Verdict | Count | What |
|---|---|---|
| Keep — still the right choice | 6 | Go, `net/http`, parquet-go, aws-sdk-go-v2, protobuf, the plain-HTML dashboard |
| Keep, but upgrade | 5 | Trino, Cube, Prometheus, Grafana, MinIO |
| Replace | 2 | `montanaflynn/stats`, `influxdata/tdigest` |
| Question the architecture | 1 | Trino **and** DuckDB **and** Cube **and** Grafana for one dashboard |

The Go code is in good shape and uses current, well-chosen libraries. **The problems are in the
infrastructure tier**, where pinned images are one to three major versions behind, and in one
architectural decision that contradicts the project's own pitch.

---

## 2. The Go code — mostly right

| Dependency | Version | Verdict |
|---|---|---|
| Go | 1.24.9 | **Keep.** Current. |
| `net/http` (stdlib, no framework) | — | **Keep, and say so louder.** Since Go 1.22 the stdlib router handles method and path patterns, so Gin, Chi and Echo no longer buy much. Zero HTTP framework dependencies in an observability tool is a feature. |
| `parquet-go/parquet-go` | v0.27.0 | **Keep.** This is the maintained fork; the original `segmentio/parquet-go` is archived. Picking the fork was correct. |
| `aws-sdk-go-v2` | v1.41.1 | **Keep.** v2, not the deprecated v1. Correct. |
| `google.golang.org/protobuf` | v1.36.8 | **Keep.** The current API, not the retired `github.com/golang/protobuf`. Correct. |
| `prometheus/client_golang` | v1.23.2 | **Keep.** Current. |

### 2.1 Replace: `montanaflynn/stats`

Used for exactly one thing — `stats.Percentile` over a bucket's latencies.

Two reasons to drop it:

1. **It is ~10 lines of stdlib.** `slices.Sort` plus linear interpolation. A dependency that saves ten
   lines costs more than it saves, in review surface and in supply chain.
2. **Its interpolation rule is nonstandard and undocumented here.** For `[10,20,30,40]` it returns
   `20` at p50, because it indexes at `percent/100 * len` and takes `c[i-1]` on a whole index — not
   the midpoint `25` that most readers expect. That is a legitimate definition, but nothing in the
   repo says which definition is in force, and `contracts/request_metrics_minute.v1.yaml` says only
   "50th percentile". Owning the ten lines means the contract can state the rule exactly.

Not urgent, but it is a small, well-contained win: one function, already covered by tests.

### 2.2 Replace: `influxdata/tdigest`

Added by GRVX-804 and it works — the measured bound is 0.92% over a merged day, which is what the
contract publishes. But it is **v0.0.1 and effectively unmaintained**, which is a poor foundation for
the number the competitive thesis rests on.

Two better options, in order:

1. **`DataDog/sketches-go` (DDSketch).** Actively maintained, and its guarantee is *relative-error*
   rather than rank-error — which is the guarantee you actually want for latency. "p99 is within 1% of
   the true value" is a stronger and more intuitive promise than "p99 is within 1% of the true rank".
2. **`caio/go-tdigest/v4`.** Actively maintained, same algorithm, smallest possible change.

**The swap is cheap by design.** `pkg/sketch` owns the wire format (§5.2 of GRVX-804 is our own
fixed little-endian layout), so the engine is swappable behind `Version = "tdigest-v1"`. A change
bumps that constant and the contract to v3; nothing else in the codebase knows which library is
underneath. That was the point of not serialising the library's own format.

---

## 3. Infrastructure — this is where the debt is

Checked against Docker Hub on 2026-09-11:

| Component | Pinned in `docker-compose.yml` | Current upstream | Gap |
|---|---|---|---|
| Trino | `435` | **483** | 48 releases |
| Prometheus | `v2.51.0` | **v3.13.3** | a **major** version |
| Grafana | `10.4.0` | **13.0.8** | **three** major versions |
| Cube | `v0.35` | **v1.7.37** | pre-1.0 → 1.x |
| MinIO | `RELEASE.2024-03-15T01-07-19Z` | `RELEASE.2025-09-07T16-13-09Z` | ~18 months |
| nginx | `1.25-alpine` | `1.30.4` stable / `1.31.5` mainline | 5 minor |
| Postgres | `16-alpine` | `16.15` (17 and 18 exist) | patch only — fine |

Pinning is right. **Pinning and then never moving is not pinning, it is rot.** Every one of these
carries accumulated security fixes.

Three need care rather than a version bump:

- **Prometheus 2 → 3** is a major release with breaking changes to some config and to the UI. It needs
  reading, not a find-and-replace.
- **Cube 0.35 → 1.x** crossed a 1.0 boundary. The data model syntax in `cube/model/` will need
  checking against the current schema format.
- **MinIO** has moved its community console behind a reduced feature set and its licensing posture has
  hardened. It is still fine as an S3-compatible target for local development, which is all Gravix
  uses it for — but it should not be recommended for production. The `pkg/storage` S3 backend talks to
  real S3, so nothing is locked in.

**Recommendation:** one spec per upgrade, each with its own smoke test, rather than a single "upgrade
everything" change nobody can review or bisect. Grafana and nginx are near-mechanical. Prometheus,
Cube and Trino are not.

---

## 4. The architectural question

Gravix currently ships **four** ways to read or display the same data:

```
Parquet ──> Trino (JVM, coordinator + worker)  ──> SQL clients
       ├──> DuckDB (in Cube's process)         ──> Cube ──> Dashboard
       ├──> Prometheus                          ──> Grafana
       └──> Cube semantic layer                 ──> Dashboard
```

For a project whose first sentence is *"low-cost, data-first"* and whose charter forbids operational
weight, that is hard to defend. Trino alone is a JVM coordinator plus workers; it is built for
federating queries across many large sources at multi-terabyte scale. Gravix reads Parquet files off
one object store.

**`cube/model/schema/RequestMetricsMinute.js` already tells the real story.** When
`CUBEJS_DB_TYPE=duckdb` it reads the warehouse directly:

```js
SELECT * FROM read_parquet('/cube/data/warehouse/request_metrics_minute/**/*.parquet', union_by_name=true)
```

DuckDB reads the Parquet perfectly well, in-process, with no coordinator and no JVM. Trino's value
appears at a scale this project has explicitly said it is not targeting.

**Recommendation:** make DuckDB the only query engine in the default deployment and move Trino to an
optional profile for people who want SQL federation. That is a roadmap decision — it changes what is
shipped and what is documented — so it belongs to `senior-engineering-lead` and needs a spec. It is
recorded here because it is the single biggest gap between what Gravix says about itself and what it
installs.

Prometheus and Grafana are a different case: they monitor *Gravix itself* (the rollup job's own
counters), not user data. That is a reasonable separation and worth keeping, though it should be an
optional profile too rather than part of the default `docker-compose up`.

---

## 5. The dashboard — keep it

`dashboards/` is plain HTML, CSS and JavaScript with no build step and no framework.

This reads at first glance like something to modernise. It is not. A dashboard that is three static
files can be served by anything, audited by anyone, and has no supply chain. For a tool whose point is
that you can verify what it shows you, a 200-dependency build pipeline in front of the UI would be an
argument against the product.

Keep it, and state it as a deliberate choice in the README rather than leaving a reader to assume it
is neglect.

---

## 6. What was fixed while writing this

Repository hygiene, not technology, but it is what a reader sees first:

| Problem | Size | Action |
|---|---|---|
| `service_events_detail` compiled binary committed | 27.5 MB | untracked |
| `cli` compiled binary committed — a **Mach-O arm64 macOS** binary, in a Linux-deployed project | 8.7 MB | untracked |
| `checkboundary` compiled binary committed (by GRVX-704; `go build ./cmd/...` with no `-o` writes into the working directory) | 4.3 MB | untracked |
| `sdk/node/node_modules/` committed, 268 files, while `package-lock.json` sits beside it | 26 MB | untracked |
| `.claude/worktrees/jovial-chaum` committed as a gitlink with no `.gitmodules`, which breaks `git submodule` and was already warning in CI | — | removed |
| 34 Go files not `gofmt`-formatted | — | formatted |

`.gitignore` now covers every command in `cmd/` by name, because the trap that produced three of those
binaries is `go build ./cmd/foo` with no `-o`.

**Still outstanding, and the owner's call:** the blobs are gone from `HEAD` but remain in history, so
a clone still pays for them (`.git` is ~90 MB). Purging them needs `git filter-repo` and a force-push
over rewritten history — a destructive operation on a published branch that should be a deliberate
decision, not a side effect of a tidy-up.

---

## 7. Priority

1. **Grafana 10 → 13, nginx, MinIO, Postgres patch.** Near-mechanical, pure security benefit.
2. **Prometheus 2 → 3.** Breaking changes; needs reading.
3. **Cube 0.35 → 1.x.** Data model syntax may have moved.
4. **Drop `montanaflynn/stats`.** Ten lines, and it lets the percentile contract state its exact rule.
5. **Swap the t-digest for DDSketch.** Better guarantee, maintained upstream, and `pkg/sketch` was
   built to make it a contained change.
6. **Decide about Trino.** The largest simplification available, and a product decision rather than an
   engineering one.
7. **Trino 435 → 483**, if it survives item 6.
