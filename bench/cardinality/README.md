# Cost immunity to cardinality

This demonstrates Axis 1 of the [competitive thesis](../../docs/oss/01-competitive-thesis.md): a new
high-cardinality field cannot raise a Gravix bill, because it never reaches storage.

```bash
./bench/run.sh --cardinality
```

No network, no account, a couple of seconds.

## What the five steps do

1. **Baseline.** A fixed volume of facts across bounded dimensions — service × method ×
   path_template — and the storage those dimension combinations cost.
2. **A bounded dimension.** `user_agent_family`, 50 values, inside the 1000/day admission threshold.
   It is **accepted**, and it **costs money**: it multiplies the row count at the finer grain. The
   demo prints that increase rather than rounding it to "no change".
3. **An unbounded dimension.** `user_id` is refused twice over — it is in
   `evolve.DeniedDimensions`, and `RequestFact` has no such field, so a fact cannot express it. Zero
   bytes stored, cardinality unchanged.
4. **A high-cardinality path.** `/users/8f14e45f-…` is rejected by schema validation and normalised
   by `pathlearn` to `/users/{id}`. The step also validates a *control* fact with a templated path;
   if the control were rejected too, the rejection above would prove nothing about paths, and the
   demo fails rather than claiming a result.
5. **The comparison** — see below.

## Why step 5 usually prints nothing

Every competitor figure in `competitor_units.yaml` starts `verified: false`, and an unverified entry
is **never printed as a number**. You get a withheld notice instead.

`verified: true` is set only by a `market-analyst` CLAIM AUDIT (Loop L11), against the **vendor's own
pricing page** — never a third-party cost tracker, however well researched. The loader enforces this:

- a `source_url` on a known tracker, *or on any host that is not a vendor domain*, fails validation;
- a verified entry older than 35 days fails, because a price checked once is not checked;
- a verified entry with no `verified_by` fails;
- a Datadog entry whose `concession` omits Metrics-without-Limits fails.

A benchmark that ships a stale competitor price is dismantled publicly within a week, and deservedly.
The gate exists so that cannot happen by accident.

**Current status: no CLAIM AUDIT has been performed.** All four entries are unverified, the numbers
in them came from third-party trackers (recorded in each `provisional_source`), and the demo
withholds the comparison.

## What this does not show

Printed on every run, verified or not:

> What this does NOT show: that Gravix is cheaper than every alternative at every scale. Datadog
> offers Metrics-without-Limits and ingest-versus-index controls that address the same problem —
> manually, per metric, after the fact. That is a real mitigation and a real difference: a control
> you must remember to configure is not the same as a cost that cannot occur.

Three more limits worth stating plainly:

- **This is about the billing unit, not the total bill.** Gravix can still be more expensive than a
  competitor at a given scale. [GRVX-1004](../../docs/oss/specs/GRVX-1004-cost-model-tco-calculator.md)
  is the spec that has to be honest about that.
- **Bounded dimensions are not free.** Step 2 is in the demo precisely so this is not glossed.
- **Datadog's log and APM units are volume-driven, not cardinality-driven.** The log entry in
  `competitor_units.yaml` is marked `cardinality_driven: false` and does not support the claim; it is
  recorded for completeness. Gravix has no logs platform and no tracing, so those rows compare
  billing units, not products.

## Adding or changing an entry

Edit `competitor_units.yaml`, leave `verified: false`, and put the source you actually used in
`provisional_source`. Do not set `verified: true` yourself — that is what the audit is for, and the
whole value of the gate is that the person who wants the number published is not the person who
confirms it.
