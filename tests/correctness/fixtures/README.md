<!-- Copyright 2026 The Gravix Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Correctness fixtures

There are no fixture files checked in. `Generate` writes a dataset into a
temporary directory from a `Spec`, and the same `Spec` always produces the same
facts — same event ids, same timestamps, same latencies, same order.

That is deliberate. A checked-in fixture is a photograph of a decision nobody
remembers making; a seed is the decision itself. When a property fails, the
failure message quotes the `Spec` back, and pasting it into a new test
reproduces the dataset exactly.

## Regenerating

Nothing to regenerate. If you need to look at a dataset:

```go
facts, err := fixtures.Generate(t.TempDir(), fixtures.Spec{
    Seed: 42, Days: 1, ServicesCount: 2, PathsPerService: 2,
    FactsPerMinute: 20, MinutesPerDay: 60, LatencyDist: "pareto",
    ErrorRate: 0.05,
})
```

## Determinism, and what it rests on

Three things had to be pinned for the same `Spec` to produce the same bytes:

- **Event ids.** `uuid.NewV7` reads the wall clock and the system entropy pool.
  `deterministicUUID` draws all sixteen bytes from a seeded source and then sets
  the version and variant bits by hand, so the ids are valid UUIDv7 in shape and
  reproducible in value.
- **Draw order.** Every random draw comes from one `rand.Rand` in a fixed
  sequence. Adding a draw in the middle changes every fact after it — which is
  fine, as long as it is a deliberate change to the fixture and not a surprise.
- **Latency distributions.** Five are available because the properties under test
  behave differently under each. A sketch's error is worst on a heavy tail, and
  `max`-of-percentiles is most wrong there too, so `pareto` is the honest choice
  for anything about percentile accuracy and `uniform` is the flattering one.

## Late facts

`LateFraction` and `MaxLateness` shift a fact's `event_time` backwards while
leaving the file it is written to where the arrival time put it. That is what
makes a fact late in the only sense that matters here: a rollup that files a
fact by its path instead of its `event_time` gets it wrong, and one that reads
the whole partition gets it right.

## The oracle

`GroundTruth` computes metrics directly from the facts, and it imports nothing
from `pkg/`. That rule is enforced by `TestOracleIsIndependent`, which parses
this package's imports rather than trusting the comment.

It matters more than it looks. An oracle that calls the code under test proves
that the code agrees with itself, which it will do just as happily when it is
wrong. The percentile rule, the bucketing rule and the definition of an error
are all written out again here from
`contracts/request_metrics_minute.v2.yaml` — so when the two disagree, the
disagreement is information.
