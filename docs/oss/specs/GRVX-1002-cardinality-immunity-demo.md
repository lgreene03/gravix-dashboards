# SPEC GRVX-1002: Demonstrate cost immunity to cardinality

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1002 | **Phase** | 10 | **Goal** | G4.1, G4.7 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q5 = YES → core. This demonstrates a property of the free product; gating it would be incoherent. |
| **Implementer role** | `perf-cost-engineer`, with `market-analyst` |
| **Depends on** | GRVX-1001, GRVX-905 |
| **Blocks** | GRVX-1007 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Prove Axis 1 of the competitive thesis: a new high-cardinality field cannot raise Gravix's bill,
because it is rejected at ingestion. Show the flat cost line, and set the mechanism beside the
cardinality-driven billing units competitors use — with every competitor number gated behind
`market-analyst` verification.

## 2. Context the implementer needs

- `docs/oss/01-competitive-thesis.md` §2 Axis 1 states the claim, the mechanism table, and the
  concession we must make: *"Datadog ships Metrics-without-Limits and ingest-vs-index controls."*
  Our narrower true claim is that their control is manual and after-the-fact, ours is structural
  and pre-ingestion.
- `docs/oss/01-competitive-thesis.md` §0 requires every competitor price be re-verified against the
  vendor's own page with a retrieval date before it appears publicly. The figures currently in the
  thesis came from third-party trackers and are **not** publication-ready.
- `docs/04-non-goals.md` §5 bounds dimensions at <1000 unique values per day.
- `pkg/evolve/cardinality.go` (GRVX-806) has `DeniedDimensions` and `MaxDistinctValuesPerDay = 1000`.
- `GRVX-905` enforces the path-template cardinality budget at ingestion.
- `bench/` (GRVX-1001) provides the harness and `Result` schema.

## 3. Non-goals for this spec

- Do NOT publish an unverified competitor price. Every one is a placeholder until `market-analyst` returns a `CLAIM AUDIT` with a primary source and retrieval date.
- Do NOT claim the cost advantage is unconditional. It applies to the billing **unit**, not to total cost at every scale — GRVX-1004 handles at-scale honesty.
- Do NOT omit the Metrics-without-Limits concession.
- Do NOT change any ingestion behaviour. GRVX-905 owns enforcement; this spec measures it.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `bench/cardinality/demo.go` | The demonstration |
| `bench/cardinality/demo_test.go` | Tests |
| `bench/cardinality/competitor_units.yaml` | Competitor billing units, each with a provenance block |
| `bench/cardinality/README.md` | What this shows and what it does not |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `bench/run.sh` | Add a `--cardinality` mode invoking the demo |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/**`, `pkg/evolve/**` | Enforcement is GRVX-905 and GRVX-806; this measures it |
| `docs/oss/01-competitive-thesis.md` | The register is updated only when the proof lands, by `market-analyst` |

## 5. Interface contract

### 5.1 The demonstration

Five steps, each printing its command and result:

1. **Baseline.** Ingest 1,000,000 facts across 5 services and 20 path templates. Record bytes stored, ingest cost, and the count of distinct dimension combinations.
2. **Add a bounded dimension.** Add `user_agent_family` (≤50 values). Ingest the same volume. Record the same figures. Cost rises with the row count at the finer grain, and the spec must show that honestly rather than claiming zero change.
3. **Attempt an unbounded dimension.** Submit facts carrying `user_id`. Show the rejection, the exact error, and that stored bytes are unchanged.
4. **Attempt a high-cardinality path.** Submit `/users/8f14e45f-ceea-167a-5a36-dedd4bea2543` rather than `/users/{id}`. Show the template normalisation or rejection, and the unchanged cardinality count.
5. **The comparison.** Print what the same field would cost as a Datadog indexed custom metric and as a Grafana Cloud active series — **only if** `competitor_units.yaml` entries are marked `verified: true`. Otherwise print the placeholder from §5.3.

### 5.2 `bench/cardinality/competitor_units.yaml`

```yaml
version: 1
units:
  - vendor: string              # "datadog" | "grafana_cloud"
    product: string
    unit: string                # the exact billing unit, e.g. "indexed custom metric"
    price_usd: float            # per the unit_quantity below
    unit_quantity: int
    cardinality_driven: bool
    source_url: string          # the VENDOR'S OWN page, never a third-party tracker
    retrieved: string           # YYYY-MM-DD
    verified: bool              # set true only by market-analyst
    verified_by: string
    concession: string          # the strongest vendor counter-argument, verbatim
```

Every Datadog entry's `concession` must include Metrics-without-Limits. An entry with
`verified: false` may not be printed as a number.

### 5.3 Unverified placeholder, printed verbatim

```
Competitor comparison withheld.
  <n> competitor billing unit(s) in competitor_units.yaml are not yet verified
  against the vendor's own pricing page. Gravix does not publish a competitor
  number it has not checked itself on a stated date.
  To verify: dispatch market-analyst (Loop L11). See docs/oss/01-competitive-thesis.md §0.
```

This is the spec's most important behaviour. A benchmark that ships a stale competitor price is
dismantled publicly within a week, and deservedly.

### 5.4 What the demo must concede, printed verbatim

```
What this shows: a high-cardinality dimension cannot raise your Gravix bill,
because it never reaches storage. The constraint is structural, applied before
ingestion.

What this does NOT show: that Gravix is cheaper than every alternative at every
scale. Datadog offers Metrics-without-Limits and ingest-versus-index controls
that address the same problem — manually, per metric, after the fact. That is a
real mitigation and a real difference: a control you must remember to configure
is not the same as a cost that cannot occur.
```

## 6. Behaviour

1. Implement the five steps.
2. Populate `competitor_units.yaml` from `docs/oss/01-competitive-thesis.md` §2 Axis 1, with every entry `verified: false` and the third-party source recorded in a `provisional_source` comment.
3. Implement the §5.3 gate: an unverified entry prints the placeholder, never a number.
4. Print the §5.4 concession unconditionally, verified or not.
5. Add `--cardinality` to `bench/run.sh`.
6. Dispatch `market-analyst` for the `CLAIM AUDIT`, and record its verdict in the report. Do not set `verified: true` yourself.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Unverified entry would print | placeholder, exit 0 | the §5.3 block |
| `source_url` is a third-party tracker | validation error, exit 1 | `competitor_units: <vendor> source must be the vendor's own page, got <url>` |
| `retrieved` older than 35 days on a verified entry | exit 1 | `competitor_units: <vendor> was verified <n> days ago; re-run Loop L11` |
| An unbounded dimension is accepted | exit 1 | `DEMO FAILED: <field> was accepted; cardinality enforcement is broken` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | A bounded dimension is accepted and its cost effect reported honestly | `TestBoundedDimensionCostReported` |
| AC-2 | `user_id` is rejected and stored bytes are unchanged | `TestUnboundedDimensionRejected` |
| AC-3 | A raw UUID path is normalised or rejected | `TestHighCardinalityPathHandled` |
| AC-4 | An unverified competitor entry prints the placeholder, never a number | `TestUnverifiedCompetitorWithheld` |
| AC-5 | A third-party `source_url` fails validation | `TestThirdPartySourceRejected` |
| AC-6 | A verified entry older than 35 days fails | `TestStaleVerificationRejected` |
| AC-7 | The concession block prints verbatim, verified or not | `TestConcessionAlwaysPrinted` |
| AC-8 | Metrics-without-Limits appears in every Datadog concession | `TestDatadogConcessionComplete` |
| AC-9 | Accepting an unbounded dimension fails the demo | `TestDemoFailsIfEnforcementBroken` |

## 8. Verification

```bash
# 1. The demo
./bench/run.sh --cardinality
# expect: five steps, the concession block, exit 0

# 2. The verification gate holds
go test ./bench/cardinality/... -run 'TestUnverifiedCompetitorWithheld|TestThirdPartySourceRejected|TestStaleVerificationRejected' -v
# expect: PASS

# 3. Honesty is unconditional
./bench/run.sh --cardinality | grep -c "a control you must remember to configure"
# expect: 1

# 4. No unverified number leaks
./bench/run.sh --cardinality | grep -cE '\$[0-9]+\.[0-9]+ per' || true
# expect: 0 while entries are unverified

# 5. Enforcement is really tested
go test ./bench/cardinality/... -run TestDemoFailsIfEnforcementBroken -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All nine acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `market-analyst` `CLAIM AUDIT` verdict recorded; no entry self-verified
- [ ] Every competitor entry `verified: false` unless the audit says otherwise
- [ ] The concession block prints unconditionally
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Cardinality enforcement is not actually working | STOP. `CORRECTNESS DEFECT` against GRVX-905. The claim is false until enforcement holds. |
| Pressure to publish an unverified competitor price | Refuse, citing thesis §0. Route to `market-analyst`. |
| A vendor pricing page that cannot be reached | Leave `verified: false` and report it. An unreachable source is not a verified one. |

---

## 11. Implementation report

All nine acceptance criteria pass. Coverage 91.8%.

**One Definition-of-Done item is open and cannot be closed by the implementer:** no `market-analyst`
CLAIM AUDIT has been performed, so every entry in `competitor_units.yaml` is `verified: false` and
the demo withholds the comparison. §9 requires "no entry self-verified", and the implementer
verifying their own competitor figures is exactly what the gate exists to prevent. Dispatching Loop
L11 is the remaining work.

### 11.1 Verification

```
$ go test ./bench/cardinality/... -v -cover
--- PASS: TestBoundedDimensionCostReported     (AC-1)
--- PASS: TestUnboundedDimensionRejected       (AC-2)  7 subtests, one per denied field
--- PASS: TestHighCardinalityPathHandled       (AC-3)  3 subtests
--- PASS: TestUnverifiedCompetitorWithheld     (AC-4)
--- PASS: TestThirdPartySourceRejected         (AC-5)  5 subtests
--- PASS: TestStaleVerificationRejected        (AC-6)
--- PASS: TestConcessionAlwaysPrinted          (AC-7)
--- PASS: TestDatadogConcessionComplete        (AC-8)
--- PASS: TestDemoFailsIfEnforcementBroken     (AC-9)
coverage: 91.8% of statements

$ ./bench/run.sh --cardinality ; echo "exit=$?"
five steps, the withheld notice, the concession block, exit=0

$ ./bench/run.sh --cardinality | grep -c "a control you must remember to configure"
1

$ ./bench/run.sh --cardinality | grep -cE '\$[0-9]+\.[0-9]+ per'
0

$ make check-boundary && make build-oss && make test-oss
boundary: 0 violations; both succeed with ee/ absent
```

### 11.2 A step that passed while proving nothing

Step 4's first version built a `RequestFact` with no `event_time`. The schema duly rejected it — for
the missing timestamp:

```
schema rejected it: event_time is required; pathlearn normalised it to "/users/{id}"
```

The step reported a rejection, the demo printed a flat cost line, and the whole thing would have kept
passing with the UUID check deleted from `schemas/`. It was visible only by reading the output.

Two changes, both in AC-3 now:

1. Every other field on the fact is valid, so the schema error names the actual constraint:
   `path_template appears to contain a raw UUID; use {id} placeholders`.
2. The step validates a **control** fact, identical but with `/users/{id}`. If the control is also
   rejected, the fact was invalid for some unrelated reason and the step returns `DEMO INVALID`
   rather than claiming a result. AC-3 additionally asserts the rejection text does *not* mention
   `event_time`, `event_id`, `status_code`, `latency_ms` or a missing service.

This is the fifth guard this session that passed while the thing it guarded was broken.

### 11.3 The verification gate, and where it is stricter than §5.2

§6.1 row 2 requires rejecting a third-party `source_url`. A blocklist of known trackers is not
enough — the next tracker nobody listed walks straight through. So the loader also requires the host
to be on the vendor's **own** domain allowlist (`datadoghq.com`, `grafana.com` and subdomains), and
AC-5 includes `https://some-new-cost-blog.example/datadog`, which is on no blocklist and still fails.

Staleness binds only `verified: true` entries. An unverified entry of any age is already withheld,
and dating it is not what makes it unpublishable — rejecting old unverified entries would have meant
the file could not be committed at all with provisional figures in it.

`Metrics-without-Limits` is matched after case-folding, hyphen-flattening and whitespace collapse, so
"metrics without limits" and a line-wrapped "Metrics\nwithout\nLimits" both count, while "there are
no limits on spending" does not. The requirement is that the argument is present, not that one exact
string is.

### 11.4 What the demo concedes, in the demo

Printed unconditionally, verified or not — AC-7 checks it with a verified entry too, which is where
the temptation to drop it would be:

- a bounded dimension **is** accepted and **does** cost money, with the row-count multiplier shown;
- Datadog's Metrics-without-Limits and ingest-versus-index controls address the same problem;
- the difference claimed is only that theirs is manual and after the fact while ours is structural
  and pre-ingestion.

`competitor_units.yaml` also records Datadog log management with `cardinality_driven: false`, which
does not support the claim, because leaving it out would have made the file read as though every
competitor unit were cardinality-driven.

### 11.5 Definition of done

- [x] All nine acceptance criteria pass with their named tests
- [x] Every Verification command run, real output above
- [ ] **`market-analyst` CLAIM AUDIT verdict recorded** — not done. Requires dispatching Loop L11;
      the implementer must not self-verify, so this stays open.
- [x] Every competitor entry `verified: false` — all four, since no audit has said otherwise
- [x] The concession block prints unconditionally
- [x] `docs-engineer` delta merged — `bench/cardinality/README.md`
- [x] Zero new skipped tests
