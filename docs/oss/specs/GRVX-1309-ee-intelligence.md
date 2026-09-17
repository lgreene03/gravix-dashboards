# SPEC GRVX-1309: `ee/intelligence/` — seasonal forecasting and capacity projection

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1309 | **Phase** | 13 | **Goal** | G7.7 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §2.3 settles this: the existing **2σ anomaly detection is core** because Q4 = YES — it shipped open in Horizon 1 Phase 2.5. Seasonal forecasting and capacity projection are **ee** because Q1 and Q2 are NO: they are additive analysis with no correctness impact. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1302, GRVX-1303, GRVX-804 |
| **Blocks** | none |
| **Effort** | 7 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Forecast where a metric is heading and when capacity will be exhausted — as **additive** analysis
sitting beside the free anomaly detection, never replacing it, and never producing a number a user
cannot interrogate.

## 2. Context the implementer needs

- Horizon 1 Phase 2.5 shipped statistical deviation detection: current bucket versus trailing 7-day average for the same time-of-day and day-of-week, flagged beyond 2σ. It is Apache-2.0 and permanently free.
- `GRVX-804` provides mergeable sketches with a published error bound; any forecast over percentiles inherits that bound and must say so.
- `GRVX-803` requires every metric to declare `exactness` and, when approximate, a `known_defect`. A forecast is not a metric, but the same disclosure discipline applies.
- `docs/04-non-goals.md` §7, as amended by charter §4, permits "explainable statistical deviation" and forbids anything requiring sub-minute evaluation or streaming state.
- `docs/oss/01-competitive-thesis.md` §3 lists what Gravix is worse at; a forecast that cannot be explained would belong on that list.

## 3. Non-goals for this spec

- Do NOT gate, degrade, or replace the free 2σ anomaly detection. It shipped open; §7.3 Q4 is permanent.
- Do NOT build an unexplainable model. Every prediction must be traceable to the inputs and method that produced it, in the same spirit as `gravix explain`.
- Do NOT require GPU, external inference services, or a network call.
- Do NOT present a forecast as a fact. A prediction is labelled a prediction, with its interval, everywhere it appears.
- Do NOT edit any core file.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/intelligence/forecast.go` | Seasonal decomposition and forecasting |
| `ee/intelligence/forecast_test.go` | Tests, including accuracy bounds |
| `ee/intelligence/capacity.go` | Capacity exhaustion projection |
| `ee/intelligence/capacity_test.go` | Tests |
| `ee/intelligence/explain.go` | Prediction lineage |
| `ee/intelligence/explain_test.go` | Tests |
| `ee/intelligence/register.go` | Extension-point registration |
| `ee/intelligence/README.md` | Method, limits, and what stays free |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| The Phase 2.5 anomaly detector | Free, and staying free |
| `pkg/sketch/**` | Sketch semantics are settled by GRVX-804 |
| Every path outside `ee/` | `pro-engineer` cannot edit core |

## 5. Interface contract

### 5.1 The explainability requirement

```go
// Package intelligence provides Gravix Enterprise forecasting and capacity
// projection. Every prediction it produces can be explained: the method, the
// inputs, the parameters, and the interval are all retrievable, because a
// number an operator cannot interrogate is a number they will not act on.
package intelligence

// Method is a forecasting technique. Each is chosen to be explainable; none is
// a black box.
type Method string

const (
    // MethodSTL is seasonal-trend decomposition using LOESS. The seasonal,
    // trend and residual components are all separately retrievable.
    MethodSTL Method = "stl"
    // MethodHoltWinters is triple exponential smoothing. Its level, trend and
    // seasonal coefficients are retrievable.
    MethodHoltWinters Method = "holt_winters"
    // MethodLinearTrend is ordinary least squares on the trend component.
    MethodLinearTrend Method = "linear_trend"
)

// Prediction is one forecast point. It is never a bare number.
type Prediction struct {
    At          time.Time `json:"at"`
    Value       float64   `json:"value"`
    LowerBound  float64   `json:"lower_bound"`
    UpperBound  float64   `json:"upper_bound"`
    Confidence  float64   `json:"confidence"`   // e.g. 0.95
    Method      Method    `json:"method"`
    IsPrediction bool     `json:"is_prediction"` // always true; present so a consumer cannot mistake it for an observation
}

// Explanation is how a prediction was produced.
type Explanation struct {
    Method         Method    `json:"method"`
    TrainingWindow string    `json:"training_window"`
    TrainingPoints int       `json:"training_points"`
    Parameters     map[string]float64 `json:"parameters"`
    SeasonalPeriod string    `json:"seasonal_period"`
    ResidualStdDev float64   `json:"residual_std_dev"`
    InputMetric    string    `json:"input_metric"`      // name@version
    InputExactness string    `json:"input_exactness"`   // inherited from the metric contract
    Caveats        []string  `json:"caveats"`
}

// Forecast produces predictions with their explanation.
func Forecast(ctx context.Context, metric string, horizon time.Duration) ([]Prediction, *Explanation, error)

var (
    ErrInsufficientHistory = errors.New("intelligence: not enough history to forecast")
    ErrHorizonTooLong      = errors.New("intelligence: horizon exceeds what this history supports")
    ErrNonStationary       = errors.New("intelligence: series is too irregular to forecast responsibly")
)
```

`IsPrediction` is always `true` and always serialised. It exists so no consumer — a dashboard, an
export, a third-party tool — can render a forecast as though it were an observation.

### 5.2 Refusing to forecast

The honest failure modes are as important as the feature:

| Condition | Behaviour |
|---|---|
| Fewer than 3 full seasonal periods of history | `ErrInsufficientHistory`, naming how much is needed |
| Horizon longer than one third of the training window | `ErrHorizonTooLong`, naming the supported horizon |
| Residual standard deviation exceeds the seasonal amplitude | `ErrNonStationary` — the series has no pattern to project |

A forecasting feature that always produces an answer is producing noise for some inputs and cannot
be trusted for any of them. Refusing is a feature.

### 5.3 Inherited uncertainty

A forecast over a sketch-based metric inherits the sketch's error bound (GRVX-804). `Explanation`
records `InputExactness`, and `Caveats` gains, verbatim:

```
This forecast is built on p95 latency, which is computed from a mergeable sketch
with a relative error of up to 1%. That uncertainty is in addition to the
prediction interval shown, not included in it.
```

Compounding uncertainty silently is how a plausible-looking projection becomes a bad capacity
decision.

### 5.4 Capacity projection

Given a metric, a threshold, and current growth, project when the threshold will be crossed —
returning a **range**, never a date. Output shape: `between 2027-01-14 and 2027-03-02 (95%)`, plus
the growth rate and its confidence interval, plus every §5.2 refusal condition.

`ee/intelligence/README.md` states verbatim:

```
The free 2σ anomaly detection is not going anywhere.

It shipped in Horizon 1 under Apache-2.0 and charter §7.3 Q4 makes that
permanent. It tells you something is unusual right now, which is the question
most teams need answered. This package answers a different question — where a
trend is heading — and it is additive. If you never buy it, nothing you have
today gets worse.
```

## 6. Behaviour

1. Implement STL, Holt-Winters and linear trend, all with retrievable components.
2. Implement the §5.2 refusal conditions with their exact messages.
3. Implement `Explanation` including inherited exactness and the verbatim caveat.
4. Implement capacity projection returning ranges.
5. Verify no code path requires a network call or a GPU.
6. Verify the free anomaly detector is untouched and works with `ee/` deleted.
7. Write accuracy tests: back-test each method over held-out history and assert the true value falls inside the interval at the stated confidence, on at least five real-shaped series.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Insufficient history | `ErrInsufficientHistory` | `intelligence: need at least <n> of history to forecast <metric>, have <m>` |
| Horizon too long | `ErrHorizonTooLong` | `intelligence: <d> history supports a horizon of at most <h>` |
| Series too irregular | `ErrNonStationary` | `intelligence: <metric> has no detectable seasonal pattern; a forecast would be noise` |
| Write in `StateReadOnly` | 402 | the GRVX-1303 payload |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | The free 2σ detector works unchanged with `ee/` deleted | `TestFreeAnomalyDetectionUnaffected` |
| AC-2 | Every prediction carries an interval and `IsPrediction: true` | `TestPredictionsAreLabelled` |
| AC-3 | Every prediction has a retrievable explanation | `TestEveryPredictionExplainable` |
| AC-4 | Insufficient history is refused, naming what is needed | `TestRefusesInsufficientHistory` |
| AC-5 | An over-long horizon is refused, naming the supported one | `TestRefusesLongHorizon` |
| AC-6 | An irregular series is refused rather than forecast | `TestRefusesNonStationarySeries` |
| AC-7 | Sketch-based input inherits its error bound in the caveats | `TestInheritedExactnessDisclosed` |
| AC-8 | Back-tested intervals contain the true value at the stated confidence | `TestForecastAccuracyBackTest` |
| AC-9 | Capacity projection returns a range, never a single date | `TestCapacityReturnsRange` |
| AC-10 | No network call or GPU is required | `TestNoExternalInference` |
| AC-11 | The free-detector statement appears verbatim | `TestFreeDetectorStatementPresent` |
| AC-12 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-13 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. Nothing free got worse
go test ./ee/intelligence/... -run TestFreeAnomalyDetectionUnaffected -v
# expect: PASS

# 2. Refusing is a feature
go test ./ee/intelligence/... -run 'TestRefusesInsufficientHistory|TestRefusesLongHorizon|TestRefusesNonStationarySeries' -v
# expect: PASS

# 3. Nothing is a bare number
go test ./ee/intelligence/... -run 'TestPredictionsAreLabelled|TestEveryPredictionExplainable|TestCapacityReturnsRange' -v
# expect: PASS

# 4. Uncertainty compounds visibly
go test ./ee/intelligence/... -run TestInheritedExactnessDisclosed -v
# expect: PASS

# 5. Accuracy, back-tested
go test ./ee/intelligence/... -run TestForecastAccuracyBackTest -v
# expect: PASS, with per-series coverage printed

# 6. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All thirteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Back-test coverage recorded per method and per series shape
- [ ] The free anomaly detector demonstrated unchanged with `ee/` deleted
- [ ] The verbatim statement and caveat present
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A method whose predictions cannot be explained | Do not ship it. Return `SPEC DEFECT: §5.1`. An unexplainable number in an observability tool will not be acted on. |
| Back-test coverage below the stated confidence | Lower the stated confidence to the measured value and report it. Never state a confidence the back-test does not support. |
| Pressure to degrade the free detector | Refuse, citing §7.3 Q4. Route to `license-boundary-auditor`. |
