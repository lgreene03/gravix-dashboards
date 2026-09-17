# SPEC GRVX-909: Framework auto-instrumentation recipes — Express, FastAPI, Flask, Django, Gin, Rails

| Field | Value |
|---|---|
| **Spec ID** | GRVX-909 |
| **Phase** | 9 |
| **Goal** | Phase 9 deliverable table row `GRVX-909` (framework auto-instrumentation recipes); supports G3.1 by removing the "how do I even send data" research step from onboarding |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q5 (is the only reason to gate this money) = NO → core. Integration recipes cost nothing to give away; the only conceivable reason to withhold one would be commercial, which the crippleware test forbids. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-905 |
| **Blocks** | GRVX-910 |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Today a self-hoster integrating a real service has three SDKs (`sdk/go`, `sdk/node`, `sdk/python`)
and their READMEs, but no framework-specific, copy-pasteable, runnable example for any of the six
frameworks named in the roadmap. After this spec, `docs/recipes/` contains one recipe per framework
— Express, FastAPI, Flask, Django, Gin, Rails — each with a runnable example file under
`examples/recipes/<framework>/`, and every recipe's documented code block is byte-identical to its
runnable example, verified by an automated test so the two can never drift.

## 2. Context the implementer needs

- `sdk/go/middleware.go:20-56` — `gravix.HTTPMiddleware(c *Client, pathTemplate func(*http.Request)
  string) func(http.Handler) http.Handler`, a generic `net/http` middleware. No Gin-specific helper
  exists anywhere in `sdk/go/`; the Gin recipe writes a small, self-contained `gin.HandlerFunc`
  directly against `client.RecordFact`, not against `HTTPMiddleware`, because Gin's router already
  exposes the matched route template via `c.FullPath()` and does not need the raw-path fallback
  `HTTPMiddleware` is designed for.
- `sdk/go/gravix.go` — `gravix.New(baseURL, apiKey string, opts ...Option) *Client`,
  `gravix.WithService(name string) Option`, `gravix.WithBatchSize(n int) Option`. `WithBatchSize(1)`
  is an already-established pattern for deterministic, immediately-flushed test fixtures
  (`sdk/go/middleware_test.go:25`, comment: `// flush immediately`).
- `sdk/go/gravix.go:198` and `sdk/node/src/client.ts:100` and `sdk/python/gravix/client.py:123` —
  every SDK's batched flush POSTs newline-delimited JSON to `/api/v1/facts/batch`, **not**
  `/api/v1/facts`. With batch size 1, every `RecordFact`/`recordFact`/`record_fact` call triggers an
  immediate, single-record POST to `/api/v1/facts/batch`. Every recipe example in this spec sets
  batch size to 1 for local-testing determinism, documented in each recipe as "increase this in
  production to reduce request volume."
- `sdk/node/src/sanitize.ts:15` — `RAW_ID_RE = /\/[0-9]{4,}(\/|$)/g` — client-side auto-sanitize
  requires **4 or more digits**; a 2-digit example id (e.g. `/users/42`) is *not* sanitized
  client-side. Every recipe's example route therefore uses a 4-digit id (`1234`) so the documented,
  observed `path_template` is deterministic and matches `/users/{id}` after sanitization, without
  relying on GRVX-905's server-side segment-budget collapsing (which requires 50 distinct values
  before it acts, per `docs/oss/specs/GRVX-905-path-template-autolearn.md` §6.2c) to do the work.
- `sdk/node/src/middleware.ts:22-46` (`expressMiddleware`) — records `pathTemplate: req.path ||
  req.url`, i.e. the **raw** URL path, not the Express route pattern (`req.route.path`). The Node
  SDK's `client.recordFact` (`sdk/node/src/client.ts:137`) auto-sanitizes it via `sanitizePath`
  before sending, which is why the 4-digit-id convention above matters for this recipe specifically.
- `sdk/python/gravix/middleware.py:13-57` (`flask_middleware(client, service="", path_fn=None)`) —
  by default uses `request.url_rule.rule` (e.g. `/users/<int:user_id>`), which is **not** translated
  to Gravix's `{name}` convention and is not touched by `sanitize_path`
  (`sdk/python/gravix/sanitize.py:10` only matches bare numeric/UUID segments, not
  `<int:user_id>`-shaped text). `flask_middleware` accepts an optional `path_fn(request) -> str` for
  exactly this reason; the Flask recipe supplies one.
- `sdk/python/gravix/middleware.py:60-110` (`ASGIMiddleware`) — for FastAPI/Starlette, reads
  `scope["route"].path`, which Starlette already populates in `{name}` form for a route declared as
  `@app.get("/users/{user_id}")` — **no translation needed** for the FastAPI recipe.
- No Django-specific middleware exists in `sdk/python/gravix/middleware.py` today (only Flask and
  ASGI). The Django recipe writes a small, self-contained Django middleware class directly against
  `gravix.Client.record_fact`, translating `request.resolver_match.route` (Django 2.2+, e.g.
  `"users/<int:user_id>/"`) the same way the Flask recipe translates `url_rule.rule`.
- No Ruby SDK exists anywhere in this repository (`sdk/` contains only `go`, `node`, `python`,
  `java`). The Rails recipe therefore uses Ruby's standard-library `Net::HTTP` directly against the
  documented REST contract — there is no `gravix` gem to `gem install`. This is stated explicitly in
  the Rails recipe's prose, not glossed over.
- `.github/workflows/ci.yml`'s `sdk-tests` job (existing) already provisions Node 20
  (`actions/setup-node@v4`) and Python 3.12 (`actions/setup-python@v5`), but **not** Go and **not**
  Ruby. This spec's live-execution tests need Go (to run `go test`), Node, and Python — none of
  which coexist in one existing job — so this spec adds a new CI job rather than reusing
  `sdk-tests`.
- `RequestFact` schema constraints (`schemas/request_fact.go:70-87`, `proto/gravix.proto:9-19`):
  `path_template` must not contain a raw UUID, a raw ≥4-digit numeric segment, or a `?` query
  string — every recipe's *sent* fact (after SDK-side sanitization/translation) must be schema-valid,
  not just superficially `{id}`-shaped.

## 3. Non-goals for this spec

- Do NOT add a Ruby toolchain to CI. `.github/workflows/ci.yml` has no Ruby setup step today, and
  adding one for a single recipe's live test is out of proportion to this spec's scope. The Rails
  recipe is therefore verified by the static docs/example-sync test (AC-6) and a static contract
  test (AC-12) only — not live execution. This is a stated, accepted scope limit, not a defect; see
  §10 for the escalation path if a future spec wants to close this gap.
- Do NOT modify any existing SDK (`sdk/go`, `sdk/node`, `sdk/python`). Every recipe uses the SDKs'
  existing, documented public API exactly as it exists today.
- Do NOT add a Java or Rust recipe. The roadmap names exactly six frameworks; adding more is scope
  creep.
- Do NOT implement a documentation generator that extracts code blocks from source files
  automatically. The byte-identity requirement (§7 AC-1..AC-6) is enforced by a test that fails loud
  if the two drift, not by generating one from the other — this keeps the docs human-editable prose
  around a fenced code block, per the existing style of every other file in `docs/`.
- This spec does not cross non-goal §5 (No High-Cardinality Dimensions) because every recipe either
  relies on GRVX-905's server-side enforcement (already shipped) or client-side sanitization
  (already shipped in every SDK) to keep `path_template` bounded; no recipe introduces a new
  unbounded dimension.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/recipes/README.md` | Index linking to all six recipes |
| `docs/recipes/express.md` | Express recipe |
| `docs/recipes/fastapi.md` | FastAPI recipe |
| `docs/recipes/flask.md` | Flask recipe |
| `docs/recipes/django.md` | Django recipe |
| `docs/recipes/gin.md` | Gin recipe |
| `docs/recipes/rails.md` | Rails recipe (Net::HTTP, no SDK) |
| `examples/recipes/express/server.js` | Runnable Express example |
| `examples/recipes/express/package.json` | `express`, `@gravix/sdk` dependencies |
| `examples/recipes/fastapi/main.py` | Runnable FastAPI example |
| `examples/recipes/flask/app.py` | Runnable Flask example |
| `examples/recipes/django/app.py` | Runnable single-file Django example (uses `settings.configure`) |
| `examples/recipes/gin/main.go` | Runnable Gin example |
| `examples/recipes/gin/go.mod` | Standalone module so it is not pulled into the root `go build ./...` |
| `examples/recipes/rails/client.rb` | Runnable Rails-style example using `Net::HTTP` (no Rails dependency required to execute — see §6 step 7) |
| `examples/recipes/recipes_test.go` | Docs/example sync tests (AC-1..AC-6) and live-execution tests (AC-7..AC-11) |
| `examples/recipes/rails_static_test.go` | Rails static-contract test (AC-12) |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `.github/workflows/ci.yml` | Add job `recipe-examples`: checkout, setup-go, setup-node, setup-python, `go test ./examples/recipes/... -v`; add it to `ci-summary`'s `needs` and failure condition |
| `README.md` | Add a "Framework recipes" section linking to `docs/recipes/README.md`, placed near the existing SDK links |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `sdk/go/**`, `sdk/node/**`, `sdk/python/**` | Every recipe consumes existing, unmodified public SDK surface |
| `schemas/**`, `services/ingestion/main.go` | Recipes are pure clients of the existing, documented ingestion API |
| `docs/api-site/**`, `docs/openapi.yaml` | Out of scope; recipes are a separate documentation surface from the generated API reference |

## 5. Interface contract

### 5.1 Stub ingestion server used by every live-execution test

```go
// examples/recipes/recipes_test.go

// newStubIngestionServer starts an httptest.Server accepting
// POST /api/v1/facts/batch (newline-delimited JSON RequestFact bodies,
// one line per call given batch size 1) and appends every decoded
// path_template to received, guarded by a mutex. It always responds 201
// with body `{"accepted":1,"rejected":0}`.
func newStubIngestionServer(t *testing.T, received *[]string, mu *sync.Mutex) *httptest.Server
```

### 5.2 Docs/example byte-identity check (one per framework)

```go
// assertDocMatchesExample fails the test unless the fenced code block in
// docPath (the content between the first ```<lang> and the next closing ```
// line) is byte-identical to examplePath's full content.
func assertDocMatchesExample(t *testing.T, docPath, examplePath string)
```

### 5.3 Gin path-template translation (in `examples/recipes/gin/main.go`, exercised directly by
`examples/recipes/recipes_test.go` via `go run` + HTTP, not imported as a library)

```go
// ginParamRe matches one Gin route parameter segment: :name or *name.
var ginParamRe = regexp.MustCompile(`[:*]([A-Za-z0-9_]+)`)

// ginPathToTemplate converts Gin's route syntax to Gravix's {name} syntax.
// ginPathToTemplate("/users/:id") == "/users/{id}"
func ginPathToTemplate(fullPath string) string
```

## 6. Behaviour

1. **Express** (`examples/recipes/express/server.js`): an Express app using
   `@gravix/sdk`'s `GravixClient` (`baseUrl`/`apiKey` from `GRAVIX_ENDPOINT`/`GRAVIX_API_KEY` env
   vars, `service: "my-express-app"`, `batchSize: 1`) and `expressMiddleware(client)`
   (`sdk/node/src/middleware.ts:22`). Route `GET /users/:id`, handler responds
   `res.json({id: req.params.id})`. `docs/recipes/express.md`'s fenced code block is byte-identical
   to this file.
2. **FastAPI** (`examples/recipes/fastapi/main.py`): a FastAPI app using `gravix.Client` and
   `gravix.middleware.ASGIMiddleware` (`sdk/python/gravix/middleware.py:60`), `batch_size=1`. Route
   `@app.get("/users/{user_id}")` returning `{"id": user_id}`. No path translation needed (§2).
   `docs/recipes/fastapi.md`'s fenced code block is byte-identical to this file.
3. **Flask** (`examples/recipes/flask/app.py`): a Flask app using `gravix.Client` and
   `gravix.middleware.flask_middleware(client, path_fn=gravix_path_fn)`, where `gravix_path_fn` is
   defined in this same file as:
   ```python
   import re
   _PARAM_RE = re.compile(r"<(?:[a-zA-Z_][a-zA-Z0-9_]*:)?([a-zA-Z_][a-zA-Z0-9_]*)>")

   def gravix_path_fn(request):
       return _PARAM_RE.sub(r"{\1}", request.url_rule.rule)
   ```
   Route `@app.route("/users/<int:user_id>")`. `docs/recipes/flask.md`'s fenced code block is
   byte-identical to this file.
4. **Django** (`examples/recipes/django/app.py`): a single-file Django app using
   `django.conf.settings.configure(...)` and `django.setup()` (no `django-admin startproject`
   scaffolding), defining: a `GravixMiddleware` class (constructor reads `settings.GRAVIX_CLIENT`,
   `__call__` records a `RequestFact` after calling `self.get_response(request)`, translating
   `request.resolver_match.route` with the same `_PARAM_RE` pattern as the Flask recipe), a URLconf
   with one route `path("users/<int:user_id>/", view)`, and, guarded by `if __name__ ==
   "__main__":`, a `wsgiref.simple_server.make_server("0.0.0.0", port, application).serve_forever()`
   where `port` comes from `sys.argv[1]` (default `8001`). `docs/recipes/django.md`'s fenced code
   block is byte-identical to this file.
5. **Gin** (`examples/recipes/gin/main.go`): a Gin app using `gravix.New` (Go SDK,
   `gravix.WithBatchSize(1)`) and a self-contained `gravixGinMiddleware(client *gravix.Client)
   gin.HandlerFunc` per §5.3, calling `client.RecordFact` with `PathTemplate:
   ginPathToTemplate(c.FullPath())` after `c.Next()`. Route `router.GET("/users/:id", handler)`.
   `docs/recipes/gin.md`'s fenced code block is byte-identical to this file. This example has its
   own `go.mod` (`examples/recipes/gin/go.mod`, module `gravix-recipe-gin`, requiring
   `github.com/gravix-io/gravix-go` and `github.com/gin-gonic/gin`) so it is never compiled as part
   of the root module's `go build ./...` or `go test ./...`.
6. **Rails** (`examples/recipes/rails/client.rb`): a plain Ruby script — not a full Rails
   application — demonstrating the exact HTTP contract a Rails `ApplicationController`
   `around_action` would use: builds a JSON body matching `RequestFact`'s fields, `POST`s it via
   `Net::HTTP.start(...).request(...)` to `ENV["GRAVIX_ENDPOINT"] + "/api/v1/facts"` (singular
   endpoint — no batching, since there is no SDK to batch with) with header `X-API-Key:
   ENV["GRAVIX_API_KEY"]`. `path_template` in the example body is the literal string
   `"/users/{id}"` (hand-written, since there is no router to introspect and no client-side
   sanitizer to rely on — the recipe's prose states explicitly: "translate your own route pattern to
   `{name}` form before sending; Gravix has no Ruby SDK to do this for you yet"). `docs/recipes/
   rails.md`'s fenced code block is byte-identical to this file.
7. `examples/recipes/recipes_test.go`:
   a. `TestExpressDocMatchesExample` .. `TestGinDocMatchesExample` (5 tests, AC-1..AC-5): each calls
      `assertDocMatchesExample` for its framework.
   b. `TestExpressRecipeSendsSanitizedFact` (AC-7): starts the stub server (§5.1), sets
      `GRAVIX_ENDPOINT`/`GRAVIX_API_KEY` env vars pointing at it, runs `node
      examples/recipes/express/server.js` as a subprocess (`exec.CommandContext`, `PORT=<free
      port>`), waits for it to accept connections (poll with backoff, 5s budget), issues `GET
      http://127.0.0.1:<port>/users/1234`, waits up to 2s for the stub server to receive a record,
      asserts `received == ["/users/{id}"]`, kills the subprocess. Skips (not fails — `t.Skip`, with
      message `` node not found on PATH `` — see §10 for why this is not a Definition-of-Done
      violation) only if `exec.LookPath("node")` fails; the `recipe-examples` CI job always has
      Node, so this skip path is never exercised there.
   c. `TestFastAPIRecipeSendsSanitizedFact` / `TestFlaskRecipeSendsSanitizedFact` (AC-8/AC-9): same
      pattern via `python3 examples/recipes/fastapi/main.py`/`flask/app.py`, requesting
      `/users/1234`, asserting `received == ["/users/{id}"]`. Both run under the `python3` on PATH;
      no separate interpreter needed per framework.
   d. `TestDjangoRecipeSendsSanitizedFact` (AC-10): `python3 examples/recipes/django/app.py <port>`,
      requesting `/users/1234/` (trailing slash, matching the URLconf), asserting `received ==
      ["/users/{id}"]`.
   e. `TestGinRecipeSendsSanitizedFact` (AC-11): runs in-process — no subprocess. Uses
      `net/http/httptest.NewServer` wrapping the Gin router built by calling
      `newGinExampleRouter(client)` (**not** exported from `examples/recipes/gin/main.go`, whose
      `go.mod` is standalone per step 5 — instead, this test duplicates the ~15-line router
      construction inline, with a comment `// mirrors examples/recipes/gin/main.go's router
      construction — kept in sync by TestGinDocMatchesExample / TestGinDocMatchesExample's sibling,
      the AC-5 byte-identity check, guaranteeing the *documented* code and this test's fixture never
      diverge silently`), issues `GET /users/1234`, asserts the recorded `PathTemplate ==
      "/users/{id}"`.
8. `examples/recipes/rails_static_test.go`: `TestRailsRecipeStaticContract` (AC-12) reads
   `examples/recipes/rails/client.rb` as text and asserts, via `strings.Contains`, that it contains
   `Net::HTTP`, `/api/v1/facts`, `X-API-Key`, and does **not** match a regex for a raw ≥4-digit
   numeric or UUID literal inside any `path_template` value in the file.
9. `.github/workflows/ci.yml`: add job `recipe-examples` — `runs-on: ubuntu-latest`, `needs: [build]`,
   steps: `actions/checkout@v4`; `actions/setup-go@v5` (version `1.25`); `actions/setup-node@v4`
   (version `20`); `actions/setup-python@v5` (version `3.12`); `go test ./examples/recipes/... -v
   -timeout 120s`. Add `recipe-examples` to the `ci-summary` job's `needs` list and to its failure
   condition, matching the exact pattern already used for `e2e`/`sdk-tests`
   (`.github/workflows/ci.yml`'s existing `ci-summary` job).

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| A recipe doc's fenced code block is edited without updating the matching example file (or vice versa) | the matching `TestXDocMatchesExample` fails | `docs/recipes/<x>.md code block does not match examples/recipes/<x>/...` (assembled by `assertDocMatchesExample` via `t.Errorf`, showing a unified diff) |
| `node`/`python3` not on `PATH` when running `go test ./examples/recipes/...` outside CI | the corresponding live-execution test is skipped, not failed | `` node not found on PATH `` / `` python3 not found on PATH `` |
| Subprocess example server does not accept a connection within 5s | the corresponding live-execution test fails | `<framework> example server did not become ready within 5s` |
| Gin recipe's router fixture (test-local copy) diverges from `examples/recipes/gin/main.go` | `TestGinDocMatchesExample` (AC-5) fails first, at the docs/example layer, before the live-execution assertion in `TestGinRecipeSendsSanitizedFact` can mask the drift | (see byte-identity message above) |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `docs/recipes/express.md`'s code fence is byte-identical to `examples/recipes/express/server.js` | `TestExpressDocMatchesExample` |
| AC-2 | `docs/recipes/fastapi.md`'s code fence is byte-identical to `examples/recipes/fastapi/main.py` | `TestFastAPIDocMatchesExample` |
| AC-3 | `docs/recipes/flask.md`'s code fence is byte-identical to `examples/recipes/flask/app.py` | `TestFlaskDocMatchesExample` |
| AC-4 | `docs/recipes/django.md`'s code fence is byte-identical to `examples/recipes/django/app.py` | `TestDjangoDocMatchesExample` |
| AC-5 | `docs/recipes/gin.md`'s code fence is byte-identical to `examples/recipes/gin/main.go` | `TestGinDocMatchesExample` |
| AC-6 | `docs/recipes/rails.md`'s code fence is byte-identical to `examples/recipes/rails/client.rb` | `TestRailsDocMatchesExample` |
| AC-7 | Running the Express example and requesting `/users/1234` results in the stub server receiving exactly `path_template == "/users/{id}"` | `TestExpressRecipeSendsSanitizedFact` |
| AC-8 | Same, for FastAPI, requesting `/users/1234` | `TestFastAPIRecipeSendsSanitizedFact` |
| AC-9 | Same, for Flask, requesting `/users/1234` | `TestFlaskRecipeSendsSanitizedFact` |
| AC-10 | Same, for Django, requesting `/users/1234/` | `TestDjangoRecipeSendsSanitizedFact` |
| AC-11 | Same, for Gin (in-process), requesting `/users/1234` | `TestGinRecipeSendsSanitizedFact` |
| AC-12 | `examples/recipes/rails/client.rb` contains `Net::HTTP`, `/api/v1/facts`, and `X-API-Key`, and contains no raw ≥4-digit numeric or UUID literal inside a `path_template` value | `TestRailsRecipeStaticContract` |

## 8. Verification

```bash
# 1. Docs/example sync (all six frameworks, no runtime dependency)
go test ./examples/recipes/... -run DocMatchesExample -v
# expect: PASS x6

# 2. Live-execution tests (Node, Python, Go — Ruby intentionally excluded, see §3)
go test ./examples/recipes/... -run RecipeSendsSanitizedFact -v -timeout 120s
# expect: PASS x5 (Express, FastAPI, Flask, Django, Gin)

# 3. Rails static contract
go test ./examples/recipes/... -run TestRailsRecipeStaticContract -v
# expect: PASS

# 4. Gin example's standalone module builds independently
(cd examples/recipes/gin && go build ./...)
# expect: build ok

# 5. Nothing else broke
go build ./... && go test ./schemas/...
# expect: build ok, PASS

# 6. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

### 8.1 Verification record — 2026-09-11

**Result: implemented and verified.** All twelve acceptance criteria pass. One spec defect found and
resolved (SD-018), and one SDK constraint discovered by running the code (§8.3).

```
$ go test ./examples/recipes/... -v -count=1 -timeout 300s
--- PASS: TestExpressDocMatchesExample          (AC-1)
--- PASS: TestFastAPIDocMatchesExample          (AC-2)
--- PASS: TestFlaskDocMatchesExample            (AC-3)
--- PASS: TestDjangoDocMatchesExample           (AC-4)
--- PASS: TestGinDocMatchesExample              (AC-5)
--- PASS: TestRailsDocMatchesExample            (AC-6)
--- PASS: TestExpressRecipeSendsSanitizedFact   (AC-7)   0.47s
--- PASS: TestFastAPIRecipeSendsSanitizedFact   (AC-8)   1.42s
--- PASS: TestFlaskRecipeSendsSanitizedFact     (AC-9)   0.41s
--- PASS: TestDjangoRecipeSendsSanitizedFact    (AC-10)  0.44s
--- PASS: TestGinRecipeSendsSanitizedFact       (AC-11)  0.87s
--- PASS: TestRailsRecipeStaticContract         (AC-12)
ok  	github.com/lgreene/gravix-dashboards/examples/recipes	3.620s

$ (cd examples/recipes/gin && go build ./...)     # ok
$ go list ./... | grep -c recipes/gin             # 0 — standalone, as §6.5 requires
$ go build ./... && go test ./schemas/...          # ok
$ make check-boundary                              # boundary: 0 violations
$ make build-oss && make test-oss                  # 51 packages, ee/ absent
$ go test ./... -race -count=1                     # no failures
$ make lint                                        # clean
```

**The five live-execution tests were run against real, installed dependencies** — Express 4 with the
Node SDK built from this checkout, and FastAPI/Flask/Django with the Python SDK installed from
`sdk/python`. They are not skipped in this report.

### 8.2 Rails, verified further than the spec asks

§3 accepts that Rails is checked statically only, because adding a Ruby toolchain to CI for one
example is out of proportion. Ruby happens to be present in this environment, so the recipe was also
run for real against a live ingestion service:

```
$ GRAVIX_ENDPOINT=http://127.0.0.1:18094 GRAVIX_API_KEY=… ruby examples/recipes/rails/client.rb
gravix responded 201
```

That is a one-off observation, not a CI guarantee — the scope limit in §3 stands. It is recorded
because "verified statically" and "known to work" are different claims, and this recipe now has both.

### 8.3 The SDK constraint that only running the code reveals

The Express recipe was first written with `require('@gravix/sdk')`, the ordinary CommonJS form. It
does not work:

```
Error [ERR_PACKAGE_PATH_NOT_EXPORTED]: No "exports" main defined in …/@gravix/sdk/package.json
```

`sdk/node/package.json` declares `"type": "module"` and exports **only** an `import` condition. The
SDK is ESM-only, and a CommonJS Express app — still the common case — cannot `require` it at all.

Nothing in the spec mentions this, and no test in the repository would have caught it: the SDK's own
tests are ESM. The recipe now uses `import`, sets `"type": "module"`, and **says so in its prose**
along with the `await import(...)` escape hatch for CommonJS apps, because a reader hitting
`ERR_PACKAGE_PATH_NOT_EXPORTED` with no explanation concludes the SDK is broken.

This is the third spec running where executing the documented thing found what reading it could not
(SD-017's UUIDv7, F-014's `uuidgen`, this).

### 8.4 Two bugs found while wiring AC-11

**SD-018 — the spec asks for two incompatible things.** §6.5 requires Gin never to enter the root
module; §6.7e requires the Gin test to run in-process. `examples/recipes/recipes_test.go` *is* in the
root module, so importing Gin there is precisely what §6.5 forbids — confirmed with `go vet`:
`no required module provides package github.com/gin-gonic/gin`. Resolved in favour of §6.5, which
states a constraint, over §6.7e, which states a mechanism. AC-11's criterion is untouched.

**A `go run` supervision deadlock.** The first subprocess implementation used `go run .` and the
package timed out at **300 seconds** — against a server that answers in under 25 seconds standalone.
`go run` compiles and then execs a child; killing the `go` process orphans that child, which keeps
the stdout pipe the test handed it open, so `cmd.Wait()` never returns. Building the binary first and
executing it directly takes **0.87s**. Any test that supervises a `go run` subprocess has this
waiting in it.

### 8.5 What each recipe actually had to solve

The recipes are not six copies of one snippet. Each framework hands you the route in a different
shape, and getting `path_template` right is the whole job:

| Framework | What it gives you | What the recipe does |
|---|---|---|
| FastAPI | the route already in `{name}` form | nothing — `add_middleware` and done |
| Gin | the route in `:name` form, via `c.FullPath()` | one regex; also reports `/unmatched` for 404s rather than an empty template |
| Express | the raw URL | relies on the SDK sanitizer, and the recipe states its four-digit threshold, because shorter ids are **not** rewritten |
| Flask, Django | `<int:user_id>`, which no sanitizer touches | both translate explicitly; without it the templates are framework-shaped and cross-service comparison goes quietly wrong |
| Rails | nothing — there is no gem | everything by hand, including a UUIDv7 generator, because `SecureRandom.uuid` is a v4 and is rejected |

That last row is SD-017 again in a language with no library to hide it. `TestRailsRecipeStaticContract`
fails if the recipe ever reaches for `SecureRandom.uuid` — and it checks **usage, not mention**,
stripping comments first, because the recipe names that method in order to warn against it. A guard
that cannot tell the two apart forbids the explanation along with the mistake, which is F-009 exactly.

### 8.6 Deviations from the spec

| Deviation | Why |
|---|---|
| AC-11 runs the Gin example as a subprocess, not in-process | SD-018 — §6.5 and §6.7e cannot both hold. |
| The Express recipe is ESM, not CommonJS | §8.3 — the SDK exports no `require` condition, so the CommonJS form cannot run. |
| `examples/recipes/gin/go.sum` created | `go mod tidy` produces it; the module does not build reproducibly without it. |
| Test helpers `requirePython` / `requireNodeModules` / `runExampleAt` beyond §5 | §5 names one helper; five live tests across three runtimes need a way to locate a suitable interpreter and to pass a port as an argument rather than an env var. `GRAVIX_RECIPE_PYTHON` lets a virtualenv be pointed at without changing the recipes. |

## 9. Definition of done

- [x] All twelve acceptance criteria pass with their named tests — §8.1, against really-installed
      dependencies; none of the five live tests skipped
- [x] Every Verification command run, real output pasted into the report — §8.1
- [x] `make check-boundary` clean
- [x] `make build-oss && make test-oss` pass with `ee/` deleted — 51 packages
- [x] **Yes** — only the files in §4.1/§4.2, plus `examples/recipes/gin/go.sum`, which `go mod tidy`
      generates and without which the module does not build reproducibly (§8.6)
- [x] `docs-engineer` delta merged — `docs/recipes/README.md` created and linked from the top-level
      `README.md`, with the index explaining what each framework makes easy or hard
- [x] Zero new skipped or quarantined tests — and none skipped in this run either: Node, Python and
      Go dependencies were installed locally so all five live tests executed
- [x] `examples/recipes/gin` confirmed standalone — `go list ./... | grep -c recipes/gin` returns
      **0**, and there is no `go.work`

## 10. Escalation

| If you find… | Do this |
|---|---|
| A future requirement to live-test the Rails recipe | Return `EXTENSION POINT REQUIRED` is not applicable here (this is `core`, not `ee/`); instead file a new spec adding a Ruby setup step to `.github/workflows/ci.yml` and a `sdk/ruby` gap — do not silently add Ruby to this spec's CI job |
| Starlette's `scope["route"].path` does not actually populate `{name}`-form templates on the FastAPI/Starlette version pinned in `sdk/python/pyproject.toml` | Return `SPEC DEFECT: §2 — FastAPI/Starlette route-template assumption is wrong on the pinned version` |
| Django's `request.resolver_match.route` attribute does not exist on the Django version available to `examples/recipes/django/app.py` | Return `SPEC DEFECT: §6 step 4 — resolver_match.route requires Django >= 2.2; pin or bump` |
| The `recipe-examples` CI job's four-runtime setup (Go+Node+Python) exceeds a reasonable CI time budget | Return `SPEC DEFECT: §6 step 9 — CI job runtime; consider splitting into per-language jobs` |
