// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package fixtures generates deterministic RequestFact datasets and computes
// metrics from them by a path that shares nothing with the rollup.
//
// The second half is the point. A test oracle that calls the code under test
// proves only that the code agrees with itself, which it always will. Everything
// in this file — the bucketing, the counting, the percentile — is written out
// again from the contract rather than imported, so that when the two disagree it
// is genuine information.
//
// The rule is enforced, not merely stated: TestOracleIsIndependent parses this
// package's imports and fails if any of them is an implementation package.
package fixtures

import (
	"bufio"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Origin is the instant every generated dataset starts from. Fixed, because a
// dataset that moves with the wall clock is not a fixture.
var Origin = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

// Spec describes a dataset. The same Spec always produces the same facts, so a
// failure is reproducible from the spec alone.
type Spec struct {
	Seed            int64
	Days            int
	ServicesCount   int
	PathsPerService int
	FactsPerMinute  int
	// MinutesPerDay limits how much of each day is populated. 0 means all 1,440,
	// which is usually far more than a test needs.
	MinutesPerDay int
	LatencyDist   string  // "uniform"|"normal"|"lognormal"|"bimodal"|"pareto"
	ErrorRate     float64 // 0..1
	// LateFraction is the share of facts whose event_time falls in an earlier
	// bucket than the file they are written to, which is what "late" means here.
	LateFraction float64
	MaxLateness  time.Duration
	// UserAgents, when set, is drawn from round-robin so a retroactive dimension
	// has something bounded to split rows by.
	UserAgents []string
}

// String makes a failure message reproducible by quoting the spec back.
func (s Spec) String() string {
	return fmt.Sprintf("fixtures.Spec{Seed:%d, Days:%d, ServicesCount:%d, PathsPerService:%d, "+
		"FactsPerMinute:%d, MinutesPerDay:%d, LatencyDist:%q, ErrorRate:%g, LateFraction:%g, MaxLateness:%s}",
		s.Seed, s.Days, s.ServicesCount, s.PathsPerService, s.FactsPerMinute, s.MinutesPerDay,
		s.LatencyDist, s.ErrorRate, s.LateFraction, s.MaxLateness)
}

func (s Spec) minutesPerDay() int {
	if s.MinutesPerDay <= 0 || s.MinutesPerDay > 1440 {
		return 1440
	}
	return s.MinutesPerDay
}

// serviceNames and pathNames are alphabetic on purpose: a path template
// containing four or more consecutive digits is rejected by schema validation as
// a raw id, and a fixture that cannot be ingested is not a fixture.
func serviceName(i int) string { return fmt.Sprintf("svc-%s", letters(i)) }

func pathName(i int) string { return fmt.Sprintf("/%s/{id}", letters(i)) }

func letters(i int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	if i < len(alphabet) {
		return string(alphabet[i])
	}
	return string(alphabet[i/len(alphabet)-1]) + string(alphabet[i%len(alphabet)])
}

var methods = []string{"GET", "POST", "PUT", "DELETE"}

// latency draws one latency from the named distribution.
//
// The distributions are here because the properties being tested behave
// differently under each: a percentile sketch's error is worst on a heavy tail,
// and max-of-percentiles is wrongest there too.
func latency(rng *rand.Rand, dist string) int32 {
	var v float64
	switch dist {
	case "normal":
		v = 120 + rng.NormFloat64()*35
	case "lognormal":
		v = math.Exp(3.9 + rng.NormFloat64()*0.55)
	case "bimodal":
		if rng.Float64() < 0.8 {
			v = 25 + rng.NormFloat64()*6
		} else {
			v = 900 + rng.NormFloat64()*120
		}
	case "pareto":
		v = 10 / math.Pow(rng.Float64(), 1/1.5)
	default: // uniform
		v = rng.Float64() * 500
	}
	if v < 0 {
		v = 0
	}
	if v > math.MaxInt32 {
		v = math.MaxInt32
	}
	return int32(v)
}

// statusCode returns an error status for the given share of requests.
func statusCode(rng *rand.Rand, errorRate float64) int32 {
	if rng.Float64() < errorRate {
		return []int32{500, 502, 503}[rng.Intn(3)]
	}
	return []int32{200, 201, 204, 301, 404}[rng.Intn(5)]
}

// Generate writes JSONL facts under dir, partitioned by event day and hour the
// way ingestion writes them, and returns the exact facts written so a test can
// compute ground truth without re-reading anything.
//
// A late fact is written into the file for the day it ARRIVED while carrying an
// event_time in an earlier bucket. That is what makes it late, and it is the
// case a rollup that trusts the file path gets wrong.
func Generate(dir string, s Spec) ([]*gravixv1.RequestFact, error) {
	if s.Days <= 0 || s.ServicesCount <= 0 || s.PathsPerService <= 0 || s.FactsPerMinute <= 0 {
		return nil, fmt.Errorf("fixtures: %s has a non-positive dimension", s)
	}

	rng := rand.New(rand.NewSource(s.Seed))
	// uuidSeq keeps event ids deterministic: uuid.NewV7 reads the clock and the
	// system entropy pool, neither of which a fixture may depend on.
	uuidSeq := rand.New(rand.NewSource(s.Seed ^ 0x5eed))

	byFile := map[string][]*gravixv1.RequestFact{}
	var all []*gravixv1.RequestFact

	for day := 0; day < s.Days; day++ {
		dayStart := Origin.AddDate(0, 0, day)
		for m := 0; m < s.minutesPerDay(); m++ {
			bucket := dayStart.Add(time.Duration(m) * time.Minute)
			for i := 0; i < s.FactsPerMinute; i++ {
				svc := serviceName(rng.Intn(s.ServicesCount))
				path := pathName(rng.Intn(s.PathsPerService))
				method := methods[rng.Intn(len(methods))]

				eventTime := bucket.Add(time.Duration(rng.Intn(60)) * time.Second)
				arrival := eventTime

				if s.LateFraction > 0 && rng.Float64() < s.LateFraction && s.MaxLateness > 0 {
					// Shift the event backwards; the file it lands in is chosen by
					// the arrival time, which stays put.
					lateBy := time.Duration(rng.Int63n(int64(s.MaxLateness)))
					eventTime = eventTime.Add(-lateBy)
				}

				f := &gravixv1.RequestFact{
					EventId:      deterministicUUID(uuidSeq),
					EventTime:    timestamppb.New(eventTime.UTC()),
					Service:      svc,
					Method:       method,
					PathTemplate: path,
					StatusCode:   statusCode(rng, s.ErrorRate),
					LatencyMs:    latency(rng, s.LatencyDist),
				}
				if len(s.UserAgents) > 0 {
					f.UserAgentFamily = s.UserAgents[rng.Intn(len(s.UserAgents))]
				}

				key := fmt.Sprintf("%s/%02d/facts_%02d.jsonl",
					arrival.UTC().Format("2006-01-02"), arrival.UTC().Hour(), arrival.UTC().Minute()/15)
				byFile[key] = append(byFile[key], f)
				all = append(all, f)
			}
		}
	}

	for key, facts := range byFile {
		full := filepath.Join(dir, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, fmt.Errorf("fixtures: mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := writeJSONL(full, facts); err != nil {
			return nil, err
		}
	}
	return all, nil
}

// deterministicUUID produces a syntactically valid UUIDv7 from a seeded source,
// so the same Spec yields the same event ids.
func deterministicUUID(rng *rand.Rand) string {
	var b [16]byte
	for i := range b {
		b[i] = byte(rng.Intn(256))
	}
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return uuid.UUID(b).String()
}

func writeJSONL(path string, facts []*gravixv1.RequestFact) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("fixtures: create %s: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, fact := range facts {
		data, err := protojson.Marshal(fact)
		if err != nil {
			return fmt.Errorf("fixtures: marshal: %w", err)
		}
		w.Write(data)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("fixtures: flush %s: %w", path, err)
	}
	return f.Sync()
}

// Key identifies one aggregation group. It mirrors the metric's declared
// dimensions and nothing else — adding a field here would be adding a dimension.
type Key struct {
	Bucket       time.Time
	Service      string
	Method       string
	PathTemplate string
	// UserAgentFamily is set only when GroundTruthBy is asked for it, which is
	// how the retroactive-dimension property gets an oracle.
	UserAgentFamily string
}

// Metrics is one row's worth of answers, computed exactly.
type Metrics struct {
	RequestCount int64
	ErrorCount   int64
	ErrorRate    float64
	P50          float64
	P95          float64
	P99          float64
	// Latencies is retained so a test can compute any other quantile exactly.
	Latencies []float64
}

// GroundTruth computes exact metrics straight from the facts.
//
// Written from contracts/request_metrics_minute.v2.yaml, not from the rollup:
// bucket by truncating event_time to the grain, group by the four declared
// dimensions, an error is status >= 500, and the percentile is the linear
// interpolation the contract names. If the rollup ever disagrees, exactly one of
// the two is wrong and this file is the one with no performance constraints.
func GroundTruth(facts []*gravixv1.RequestFact, bucket time.Duration) map[Key]Metrics {
	return GroundTruthBy(facts, bucket, false)
}

// GroundTruthBy optionally adds user_agent_family to the grouping key, which is
// the dimension GRVX-806 adds retroactively.
func GroundTruthBy(facts []*gravixv1.RequestFact, bucket time.Duration, byUserAgent bool) map[Key]Metrics {
	groups := map[Key][]*gravixv1.RequestFact{}
	for _, f := range facts {
		k := Key{
			Bucket:       f.EventTime.AsTime().UTC().Truncate(bucket),
			Service:      f.Service,
			Method:       f.Method,
			PathTemplate: f.PathTemplate,
		}
		if byUserAgent {
			k.UserAgentFamily = f.UserAgentFamily
		}
		groups[k] = append(groups[k], f)
	}

	out := make(map[Key]Metrics, len(groups))
	for k, group := range groups {
		var errors int64
		latencies := make([]float64, 0, len(group))
		for _, f := range group {
			if f.StatusCode >= 500 {
				errors++
			}
			latencies = append(latencies, float64(f.LatencyMs))
		}
		sort.Float64s(latencies)

		m := Metrics{
			RequestCount: int64(len(group)),
			ErrorCount:   errors,
			Latencies:    latencies,
		}
		if m.RequestCount > 0 {
			m.ErrorRate = float64(errors) / float64(m.RequestCount)
		}
		m.P50 = Quantile(latencies, 0.50)
		m.P95 = Quantile(latencies, 0.95)
		m.P99 = Quantile(latencies, 0.99)
		out[k] = m
	}
	return out
}

// Quantile is the exact linear-interpolation quantile over a sorted slice.
//
// The rule is stated here rather than assumed, because "the 95th percentile" is
// not one thing: this is the C = 1 / nearest-rank-with-interpolation definition,
// index = q*(n-1), interpolating between neighbours. The metric contract names
// the same rule.
func Quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := q * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if hi >= len(sorted) {
		hi = len(sorted) - 1
	}
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// AllLatencies returns every latency across the given keys, sorted — the input a
// window percentile's ground truth needs.
func AllLatencies(truth map[Key]Metrics, match func(Key) bool) []float64 {
	var out []float64
	for k, m := range truth {
		if match == nil || match(k) {
			out = append(out, m.Latencies...)
		}
	}
	sort.Float64s(out)
	return out
}
