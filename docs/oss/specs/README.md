# Gravix Horizon 2 Specifications

88 specifications covering Phases 7–15 of `../20-roadmap-horizon-2.md`.

## How to use these

Each spec is a **self-contained work order**. The implementing agent reads exactly one spec plus
the files that spec names — not the roadmap, not the charter, not this README. Everything needed
to execute correctly is inside the spec.

That is a deliberate design constraint, not a convenience. Requiring product context at
implementation time is how scope creep, philosophy violations and undocumented design decisions
enter a codebase. If a spec cannot be executed from its own contents, the spec is defective and
goes back to `senior-engineering-lead` (Loop L2) — the implementer does not improvise a fix.

## Files

| File | Purpose |
|---|---|
| `_TEMPLATE.md` | The mandatory structure. Every spec follows it. |
| `SPEC-INDEX.md` | All 88 specs: ID, title, phase, goal, placement, dependencies, effort. |
| `GRVX-<nnn>-<slug>.md` | The specs themselves. |

## Dispatch

```
Goal: <spec objective, one sentence>
Role: senior-engineer | pro-engineer | frontend-engineer
Spec: docs/oss/specs/GRVX-<nnn>-<slug>.md
Read only: the spec, and the files it names in §4
Exit Criteria: every §7 acceptance criterion passes; §8 verification output pasted verbatim
```

Never attach conversation history. See `../11-agent-loops.md` §L3.

## The Readiness Gate

No spec is dispatched until it passes all 12 checks in `../11-agent-loops.md` §L2. The result is
recorded in each spec's header table. Check 12 is the one that matters: *could someone who has
never heard of Gravix execute this correctly?*
