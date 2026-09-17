<!-- Copyright 2026 The Gravix Authors -->
<!-- SPDX-License-Identifier: BUSL-1.1 -->

# `ee/intelligence` — method, limits, and what stays free

```
The free 2σ anomaly detection is not going anywhere.

It shipped in Horizon 1 under Apache-2.0 and charter §7.3 Q4 makes that
permanent. It tells you something is unusual right now, which is the question
most teams need answered. This package answers a different question — where a
trend is heading — and it is additive. If you never buy it, nothing you have
today gets worse.
```

## Two different questions

| | Free, Apache-2.0 | This package, BUSL-1.1 |
|---|---|---|
| Question | "Is this minute unusual?" | "Where is this heading, and when do I run out?" |
| Method | Current bucket against the trailing 7-day average for the same time-of-day and day-of-week, flagged beyond 2σ | Seasonal-trend decomposition, triple exponential smoothing, or a fitted linear trend — whichever measures best on held-out history |
| Where | `pkg/gatewaycore`, alert operator `anomaly` | `ee/intelligence`, mounted at `/ee/intelligence/` |
| If you do not have it | You have it. It is in the free product. | You keep every number you have today, unchanged |

## Nothing here is a black box

Each method is one an operator can check by hand on a window they pick:

- **STL** — LOESS-smoothed seasonal-trend decomposition. `Explanation.Components` carries the
  seasonal cycle, the trend and the residual separately, so "why is it predicting that" has an
  answer made of numbers.
- **Holt-Winters** — additive triple exponential smoothing. α, β and γ are fixed at 0.3, 0.1 and
  0.3 and reported in every explanation. They are not grid-searched: parameters fitted to the data
  would make the model a function of the data in a way nobody could reproduce.
- **Linear trend** — ordinary least squares on the STL trend, with the seasonal component added
  back. Slope and intercept are in the explanation.

The method is not chosen by preference. All three are fitted against a held-out final season and
the one with the lowest mean absolute error wins; every method's score is in `MethodScores`, so the
choice can be checked rather than believed.

## Nothing here is a bare number

Every `Prediction` carries a lower bound, an upper bound, a confidence, the method that produced
it, and `IsPrediction: true` — always set, always serialised, so no dashboard, export or
third-party tool can render a forecast as an observation. Capacity projections return a **range**,
never a date:

```
between 2027-01-14 and 2027-03-02 (80%), growing 0.4213/day (0.2011 to 0.6415)
```

A single date implies a precision a slope estimated from four weeks of noisy history does not
have, and it is the number somebody will put in a plan.

## The confidence is 80%, not 95%

Because 80% is what the back-test achieves. `TestForecastAccuracyBackTest` holds out the final
season of five differently-shaped series, forecasts it, and measures how often the true value falls
inside the interval over five differently-shaped series and three seeds each — 360 held-out points.
The measured coverage is **0.875**. GRVX-1309 §10 is explicit: *"Lower the stated confidence to the
measured value and report it. Never state a confidence the back-test does not support."* 0.875
supports 80% with margin and does not support 90%, so the stated confidence is 80%. The per-series
figures are printed by that test on every run.

## It refuses

| Condition | Answer |
|---|---|
| Fewer than 3 complete seasons of history | `ErrInsufficientHistory`, naming how much is needed and how much there is |
| A horizon longer than a third of the training window | `ErrHorizonTooLong`, naming the horizon that is supported |
| Residual noise larger than the seasonal amplitude | `ErrNonStationary` — there is no pattern to project |
| A metric that is not moving toward its threshold | `ErrNoCrossing`, with the growth interval that shows why |

A forecaster that always produces an answer is producing noise for some inputs and can be trusted
for none of them. Refusing is a feature, and over HTTP it is a `422` — the request was fine, the
answer is "no, and here is why".

## Uncertainty compounds visibly

A forecast over p95 latency inherits the sketch's error bound (GRVX-804), and says so in as many
words:

> This forecast is built on p95 latency, which is computed from a mergeable sketch with a relative
> error of up to 1%. That uncertainty is in addition to the prediction interval shown, not included
> in it.

Compounding uncertainty silently is how a plausible-looking projection becomes a bad capacity
decision.

## What it reads

The same Parquet the free product writes and the free product reads:
`data/warehouse/request_metrics_minute/`. There is no separate paid data path, no separate store,
and nothing an Enterprise install records that an open-source one does not. A minute with no
traffic has no p95, so gaps are carried forward rather than interpolated — inventing a value
between two neighbours would put a number in the history that never happened.

## What it does not need

No GPU. No external inference service. No network call of any kind, in any state —
`TestNoExternalInference` asserts it over the package's own source, the same way `pkg/license`
proves that licence verification cannot dial.

## What this STL is not

The inner loop only. The published algorithm's robustness iteration, which down-weights outliers,
is not implemented, so a single large spike in the training window pulls the trend toward it. That
is disclosed in every explanation's caveats rather than left for somebody to discover from a bad
forecast.
