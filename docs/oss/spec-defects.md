<!-- Maintained by Loop L3 implementers. Append; never rewrite history. -->
# Spec defect register

When an implementer finds a spec it cannot execute as written, it returns `SPEC DEFECT` rather than
improvising (`11-agent-loops.md` §L3). Each one is recorded here with what was assumed, what is
actually true, and what changed as a result.

This file existing and being non-empty is the process working. A spec corpus with no recorded
defects was either never executed or was executed by someone guessing.

---

## SD-001 — `requirePlan` is dead code, and only one capability is actually plan-gated

**Found by:** `senior-engineer` executing GRVX-703
**Affects:** GRVX-703 §5.4, GRVX-710 (entire premise), `PRODUCT_ROADMAP.md` Phase 6
**Severity:** high — GRVX-710's stated purpose was based on it
**Status:** corrected

### What the specs assumed

GRVX-703 §5.4 and GRVX-710 both stated that **five** capabilities were gated behind `requirePlan`
and should return to the free tier: the public metrics API, custom dashboards, scheduled exports,
per-tenant rate limiting, and the audit log. GRVX-710 was written as a three-person-day task to
remove five gates.

That came from `PRODUCT_ROADMAP.md`'s Phase 4 and Phase 6 entries, which describe those features as
plan-gated. Those entries describe an intent that was never fully implemented.

### What is actually true

Verified by exhaustive grep over non-test Go source:

| Claim | Reality |
|---|---|
| `requirePlan` gates five capabilities | **`requirePlan` has zero non-test callers.** It is defined at `services/gateway/main.go:953` and exercised only by three tests in `main_test.go`. It is dead code. |
| Public metrics API is plan-gated | **True.** `services/gateway/gateway_platform.go:132` returns HTTP 402 `"Public Metrics API requires Pro plan or above"` via a direct `planRank` comparison, not via `requirePlan`. |
| Custom dashboards are plan-gated | **False.** `gateway_dashboards.go` contains zero `planRank`, `requirePlan` or `StatusPaymentRequired` occurrences. Role-gated only. |
| Scheduled exports are plan-gated | **False.** Same for `enterprise.go`. Role-gated only (admin for mutations). |
| Audit log is plan-gated | **False.** Same. Admin-only by role, which is correct and stays. |
| Per-tenant rate limiting is plan-gated | **False, and a category error.** `rateLimitMiddleware` varies the *limit* by plan and returns 429 when exceeded. It applies to every plan including free. A differentiated limit is not a gate. |

There is exactly **one** plan gate in the codebase, and exactly **one** `StatusPaymentRequired` in
non-test code.

### Why this matters beyond bookkeeping

The good news is that four of the five capabilities the charter says must be free **already are**,
so the free tier is in better shape than the roadmap claimed. The bad news is that `PRODUCT_ROADMAP.md`
described a product that was not built, and the Horizon 2 specs inherited that description because
they were written from the roadmap rather than from the code.

That is the drift the boundary map exists to prevent, and it is why `boundary.yaml` is validated
against actual `requirePlan` and `planRank` call sites (GRVX-704 `checkGates`) rather than against
prose.

### What changed

1. **GRVX-703 §5.4** — corrected. `boundary.yaml` records the real gate on `public-metrics-api` and
   no `gate:` field on the four that were never gated.
2. **GRVX-710** — rescoped. It is no longer "remove five gates". It is: remove the one real gate on
   the public metrics API, and delete `requirePlan` and its tests as dead code, or wire it to the
   capabilities that legitimately stay gated once `ee/` exists. Effort revised 3 pd → 1 pd.
3. **`PRODUCT_ROADMAP.md`** — a note added to the Horizon 2 banner recording that its Phase 4 and 6
   gating claims were aspirational.
4. **GRVX-704 `checkGates`** — unchanged, and now more clearly load-bearing: it is the mechanism
   that would have caught this drift automatically.

---

## SD-002 — the boundary map's capability list was incomplete

**Found by:** `senior-engineer` executing GRVX-703
**Affects:** GRVX-703 §5.4 and AC-2
**Severity:** low — an omission, not an error
**Status:** corrected; spec updated to 23

### What the spec said

GRVX-703 §5.4 enumerated **18** capabilities, and AC-2 asserted exactly that count.

### What was missing

All 18 are correct and present. But the list omitted five capabilities that exist in the code and
that GRVX-704's `checkGates` will need mapped, because it cross-references every plan-gate call site
against a `paths` glob in this file:

- `schema-validation` (`schemas/`) — the cardinality budget, which is the mechanism behind the
  competitive thesis's Axis 1 claim
- `compaction-retention` (`transforms/compaction/`, `cmd/purge/`)
- `storage-abstraction` (`pkg/storage/`)
- `terraform-provider` (`terraform-provider-gravix/`)
- `deploy-tooling` (`deploy/gravix/`, the compose stacks)

### Why this was worth deviating for

A boundary map that omits real capabilities cannot do its job. Its purpose is to make it impossible
to gate something without the gate appearing here, and a capability absent from the map is one that
could be gated without the map noticing. Charter §5's "no load-bearing feature is crippled" check is
only as complete as this list.

Omitting `schema-validation` in particular would have been a mistake: cardinality enforcement is the
single capability the cost claim rests on, and it should be explicitly recorded as permanently free.

### What changed

GRVX-703 §5.4 and AC-2 updated from 18 to **23** (20 core, 3 ee). No placement changed; five were
added, all core.
