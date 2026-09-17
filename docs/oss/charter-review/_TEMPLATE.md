# Charter review: YYYY

**Period:** YYYY-MM-DD to YYYY-MM-DD
**Published:** YYYY-MM-DD
**Anniversary:** YYYY-MM-DD (published N days after; the window is 30)

> If this review is late, say so here and carry the fact into the next one:
> `charterreview: YYYY review published N days late`

---

## What we promised

Charter §5's success conditions, verbatim. Do not paraphrase them — a promise restated in the
reviewer's own words is a promise that can drift a little each year until it is a different promise.

| Condition | Target |
|---|---|
| … | … |

## What actually happened

The evidence record from `make charter-evidence`, unedited.

```json
{ … }
```

A field showing `-1` was **not measured**. It is not zero. The `unmeasurable` list beside it says
why, and those reasons belong in section 3, not in a footnote.

## Where we fell short

**Mandatory. Minimum one item.** A review with nothing uncomfortable in it was not a review, and
`charterreview.RequireShortfalls` refuses to publish one — including a section that says "None".

Every target missed. Every metric that could not be measured. Every finding somebody would rather
not have written down.

- **<target>** — measured N against a target of M. Why, and what changes.
- **<unmeasurable metric>** — why there is no number, and whether that is permanent.

If a year genuinely missed no target, the unmeasurable metrics go here. There have always been
some.

## Charter violations

State the answer even when it is none, **with the commands that checked it**. "No violations" with
no evidence is an assertion, which is the thing this document exists not to be.

| Check | Command | Result |
|---|---|---|
| Capabilities moved into `ee/` | `make charter-evidence` → `capabilities_moved_to_ee` | … |
| Core imports `ee/` | `make check-boundary` | … |
| Core builds with `ee/` deleted | `make build-oss && make test-oss` | … |
| Upsell in the free dashboard | `make charter-evidence` → `upsell_elements_found` | … |
| Artificial limits | `make charter-evidence` → `artificial_limits_found` | … |
| A CLA has appeared | `make charter-evidence` → `cla_exists` | … |

If anything moved from core into `ee/`, **this section opens the review** and the finding is
escalated to the License & Boundary Auditor before publication, not at it.

## The canary

G7.4: OSS 30-day retention against Pro monthly recurring revenue.

| | Trend |
|---|---|
| OSS 30-day retention | rising / flat / falling / **unknown** |
| Pro MRR | rising / flat / falling / **unknown** |
| Tripped | yes / no |

`unknown` means it was not checked. Say so plainly — it does not mean fine.

If tripped: the free tier may be being degraded in a way the revenue number rewards. This triggers
Loop L0 **regardless of the revenue figure**, and the reason is in section 6.

## What we are changing

Each proposed amendment as an RFC link, with its tier and window.

- [RFC NNNN](../rfcs/NNNN-slug.md) — what it changes, and why this review prompted it.

**Entrenched clauses are excluded.** §7.1, §7.3 Q4 and §7.4 may be strengthened and never weakened,
and `charterreview.CheckAmendment` refuses a proposal that would before it is published.

If nothing is changing, say so and say why — a year with no proposed change is either a good sign or
a sign nobody looked, and the difference is worth stating.

## What we are not changing, and why

Including everything asked for during the year and declined.

This section is the one a reader learns most from. What a project refuses tells you more about it
than what it accepts, and a review that records only the accepted proposals is a review that has
quietly become a changelog.

- **<request>** — who asked, what they wanted, why it was declined, and what they can do instead.
