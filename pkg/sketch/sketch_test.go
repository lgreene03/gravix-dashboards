// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package sketch

import (
	"bytes"
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// ─── fixtures ───

// distributions are the five shapes the accuracy bound is measured over. Latency
// is rarely normal: lognormal and pareto are the shapes that actually show up,
// and they are where naive percentile combination fails worst.
var distributions = []string{"uniform", "normal", "lognormal", "bimodal", "pareto"}

// quantiles are the points the bound is claimed at.
var quantiles = []float64{0.5, 0.9, 0.95, 0.99}

// generate produces n observations from the named distribution, deterministically
// for a given seed so a failure can be reproduced exactly.
func generate(name string, n int, rng *rand.Rand) []float64 {
	out := make([]float64, n)
	for i := range out {
		switch name {
		case "uniform":
			out[i] = rng.Float64() * 1000
		case "normal":
			out[i] = 500 + rng.NormFloat64()*100
		case "lognormal":
			out[i] = math.Exp(rng.NormFloat64()*1.2 + 3)
		case "bimodal":
			// A fast path and a slow path, which is what a service with a cache
			// actually looks like.
			if rng.Float64() < 0.7 {
				out[i] = 50 + rng.NormFloat64()*10
			} else {
				out[i] = 900 + rng.NormFloat64()*50
			}
		case "pareto":
			out[i] = 10 / math.Pow(rng.Float64(), 1/1.5)
		default:
			panic("unknown distribution " + name)
		}
		if out[i] < 0 {
			out[i] = 0
		}
	}
	return out
}

// trueQuantile is the exact answer, from the fully sorted data. It is what the
// sketch is measured against.
func trueQuantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := q * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if hi >= len(sorted) {
		hi = len(sorted) - 1
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func sortedCopy(v []float64) []float64 {
	out := append([]float64(nil), v...)
	sort.Float64s(out)
	return out
}

// splitIntoBuckets cuts a day of observations into per-minute buckets, each
// sorted as the rollup would sort them.
func splitIntoBuckets(data []float64, buckets int) [][]float64 {
	out := make([][]float64, buckets)
	size := len(data) / buckets
	for i := 0; i < buckets; i++ {
		lo := i * size
		hi := lo + size
		if i == buckets-1 {
			hi = len(data)
		}
		out[i] = sortedCopy(data[lo:hi])
	}
	return out
}

// ─── AC-1: the accuracy bound, measured not assumed ───

func TestAccuracyWithinBound(t *testing.T) {
	const n = 1_000_000

	worst := 0.0
	for _, dist := range distributions {
		t.Run(dist, func(t *testing.T) {
			rng := rand.New(rand.NewSource(42))
			sorted := sortedCopy(generate(dist, n, rng))
			s := FromSorted(sorted)

			if s.Count() != int64(n) {
				t.Fatalf("Count = %d, want %d", s.Count(), n)
			}

			for _, q := range quantiles {
				want := trueQuantile(sorted, q)
				got, err := s.Quantile(q)
				if err != nil {
					t.Fatalf("Quantile(%v): %v", q, err)
				}
				rel := math.Abs(got-want) / want
				if rel > worst {
					worst = rel
				}
				t.Logf("q=%.2f true=%.4f sketch=%.4f relative error=%.6f", q, want, got, rel)
				if rel > MaxRelativeError {
					t.Errorf("q=%.2f: relative error %.6f exceeds MaxRelativeError %.6f "+
						"(true %.4f, sketch %.4f)", q, rel, MaxRelativeError, want, got)
				}
			}
		})
	}
	t.Logf("worst single-sketch relative error across all distributions and quantiles: %.6f", worst)
}

// ─── AC-2: the operation that actually matters ───

func TestMergedDaySketchWithinBound(t *testing.T) {
	const n = 1_000_000
	const buckets = 1440 // one day of minutes

	worst := 0.0
	for _, dist := range distributions {
		t.Run(dist, func(t *testing.T) {
			rng := rand.New(rand.NewSource(42))
			data := generate(dist, n, rng)
			sorted := sortedCopy(data)

			var parts []*Sketch
			for _, bucket := range splitIntoBuckets(data, buckets) {
				parts = append(parts, FromSorted(bucket))
			}
			merged, err := MergeAll(parts)
			if err != nil {
				t.Fatalf("MergeAll: %v", err)
			}

			if merged.Count() != int64(n) {
				t.Errorf("Count = %d, want %d — merging lost observations", merged.Count(), n)
			}

			for _, q := range quantiles {
				want := trueQuantile(sorted, q)
				got, err := merged.Quantile(q)
				if err != nil {
					t.Fatalf("Quantile(%v): %v", q, err)
				}
				rel := math.Abs(got-want) / want
				if rel > worst {
					worst = rel
				}
				t.Logf("q=%.2f true=%.4f merged=%.4f relative error=%.6f", q, want, got, rel)
				if rel > MaxRelativeError {
					t.Errorf("q=%.2f: merging %d buckets gave relative error %.6f, exceeding %.6f",
						q, buckets, rel, MaxRelativeError)
				}
			}
		})
	}
	t.Logf("worst merged-day relative error across all distributions and quantiles: %.6f", worst)
}

// ─── AC-3: better than what it replaces ───

func TestSketchBeatsMaxOfPercentiles(t *testing.T) {
	const n = 1_000_000
	const buckets = 1440
	const q = 0.95

	for _, dist := range distributions {
		t.Run(dist, func(t *testing.T) {
			rng := rand.New(rand.NewSource(42))
			data := generate(dist, n, rng)
			sorted := sortedCopy(data)
			want := trueQuantile(sorted, q)

			split := splitIntoBuckets(data, buckets)

			// What the Cube model does today: the maximum of the per-minute p95s.
			maxOfP95 := 0.0
			var parts []*Sketch
			for _, bucket := range split {
				if v := trueQuantile(bucket, q); v > maxOfP95 {
					maxOfP95 = v
				}
				parts = append(parts, FromSorted(bucket))
			}

			merged, err := MergeAll(parts)
			if err != nil {
				t.Fatalf("MergeAll: %v", err)
			}
			sketchP95, err := merged.Quantile(q)
			if err != nil {
				t.Fatalf("Quantile: %v", err)
			}

			maxErr := math.Abs(maxOfP95-want) / want
			sketchErr := math.Abs(sketchP95-want) / want

			t.Logf("true p95=%.4f | max-of-per-minute-p95=%.4f (error %.4f) | merged sketch=%.4f (error %.6f)",
				want, maxOfP95, maxErr, sketchP95, sketchErr)

			if sketchErr >= maxErr {
				t.Errorf("the sketch is no better than max-of-percentiles: %.6f vs %.6f", sketchErr, maxErr)
			}
			if maxErr <= MaxRelativeError {
				t.Logf("note: on this distribution max-of-p95 happens to land within the bound too; "+
					"it is still not a percentile and carries no bound (error %.4f)", maxErr)
			}
		})
	}
}

// ─── AC-4, AC-5, AC-6: determinism ───

func TestSerialisationDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	data := generate("lognormal", 20_000, rng)
	sorted := sortedCopy(data)

	first, err := FromSorted(sorted).MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	// Repeated builds of the same multiset.
	for i := 0; i < 5; i++ {
		again, err := FromSorted(sortedCopy(data)).MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary: %v", err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("build %d produced different bytes for the same multiset", i)
		}
	}

	// The same multiset shuffled before sorting. This is the real case: the object
	// store gives no ordering guarantee, so the rollup may read a day's facts in
	// any order. After sorting, the bytes must not know the difference.
	for i := 0; i < 5; i++ {
		shuffled := append([]float64(nil), data...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		again, err := FromSorted(sortedCopy(shuffled)).MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary: %v", err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("shuffle %d changed the serialised bytes; output depends on read order", i)
		}
	}
}

func TestSketchGoldenFormat(t *testing.T) {
	goldenPath := filepath.Join("testdata", "golden_sketch.bin")

	got, err := goldenSketch().MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("serialised sketch does not match %s (%d bytes vs %d)\n"+
			"The wire format changed. Bump Version and regenerate the fixture in the same commit — "+
			"every stored sketch is written in this format.", goldenPath, len(got), len(want))
	}

	// And it must round-trip.
	var back Sketch
	if err := back.UnmarshalBinary(want); err != nil {
		t.Fatalf("the golden fixture does not parse: %v", err)
	}
	if back.Count() != goldenSketch().Count() {
		t.Errorf("round-tripped Count = %d, want %d", back.Count(), goldenSketch().Count())
	}
	original, _ := goldenSketch().Quantile(0.95)
	restored, _ := back.Quantile(0.95)
	if original != restored {
		t.Errorf("round-tripped p95 = %v, want %v", restored, original)
	}
}

// goldenSketch is a small, fixed sketch that pins the wire format.
func goldenSketch() *Sketch {
	values := make([]float64, 0, 500)
	for i := 0; i < 500; i++ {
		values = append(values, float64(i)*1.5+1)
	}
	return FromSorted(values)
}

func TestMergeIsAssociativeAndCommutative(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	var parts []*Sketch
	for i := 0; i < 8; i++ {
		parts = append(parts, FromSorted(sortedCopy(generate("lognormal", 5_000, rng))))
	}

	reference, err := MergeAll(parts)
	if err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	want, err := reference.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	// Commutative: any permutation of the same parts.
	for i := 0; i < 10; i++ {
		shuffled := append([]*Sketch(nil), parts...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		got, err := MergeAll(shuffled)
		if err != nil {
			t.Fatalf("MergeAll: %v", err)
		}
		gotBytes, _ := got.MarshalBinary()
		if !bytes.Equal(gotBytes, want) {
			t.Fatalf("permutation %d produced different bytes; Merge is not commutative", i)
		}
	}

	// Associative: ((a+b)+(c+d)) must equal (((a+b)+c)+d).
	left, err := MergeAll(parts[:4])
	if err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	right, err := MergeAll(parts[4:])
	if err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	if err := left.Merge(right); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	gotBytes, _ := left.MarshalBinary()
	if !bytes.Equal(gotBytes, want) {
		t.Error("regrouping the merges changed the result; Merge is not associative")
	}
}

// ─── AC-7, AC-8: refusing what it cannot read ───

func TestUnmarshalRejectsVersionMismatch(t *testing.T) {
	data, err := goldenSketch().MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	tampered := append([]byte(nil), data...)
	copy(tampered[0:versionFieldSize], make([]byte, versionFieldSize))
	copy(tampered[0:], "tdigest-v99")

	var s Sketch
	err = s.UnmarshalBinary(tampered)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
	if want := "sketch: serialised version mismatch: file tdigest-v99, binary " + Version; err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestMergeRejectsCompressionMismatch(t *testing.T) {
	a := goldenSketch()
	b := goldenSketch()
	b.compression = Compression * 2

	err := a.Merge(b)
	if !errors.Is(err, ErrCompressionMismatch) {
		t.Fatalf("err = %v, want ErrCompressionMismatch", err)
	}
	want := "sketch: cannot merge sketches with different compression: 100 vs 200"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestUnmarshalRejectsMalformedPayloads(t *testing.T) {
	good, err := goldenSketch().MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	t.Run("too short for a header", func(t *testing.T) {
		var s Sketch
		if err := s.UnmarshalBinary(good[:10]); !errors.Is(err, ErrMalformed) {
			t.Errorf("err = %v, want ErrMalformed", err)
		}
	})

	t.Run("truncated centroids", func(t *testing.T) {
		var s Sketch
		if err := s.UnmarshalBinary(good[:len(good)-8]); !errors.Is(err, ErrMalformed) {
			t.Errorf("err = %v, want ErrMalformed", err)
		}
	})

	t.Run("count disagrees with weights", func(t *testing.T) {
		tampered := append([]byte(nil), good...)
		tampered[20]++ // nudge the count in the header
		var s Sketch
		if err := s.UnmarshalBinary(tampered); !errors.Is(err, ErrMalformed) {
			t.Errorf("err = %v, want ErrMalformed — a count that disagrees with its centroids is corrupt", err)
		}
	})
}

// ─── AC-14: what it costs ───

// MeasuredBytesPerRow is the mean serialised size of the sketch column over
// realistic bucket sizes, measured by TestSketchSizeBudget. The measured mean is
// 1,623 bytes; the constant sits just above it so ordinary variation does not
// fail the build while a real regression does.
//
// GRVX-804 AC-14 set a budget of 40 bytes per row. No quantile sketch can meet
// that: the fixed header alone is 32 bytes and a one-observation sketch is 48.
// Lowering Compression is not a way out either — at 50 the merged-day error is
// 4.7% and at 20 it is 38%, against the 1% this spec exists to deliver. See
// docs/oss/spec-defects.md SD-007 for the full measurement and the options.
const MeasuredBytesPerRow = 1700

func TestSketchSizeBudget(t *testing.T) {
	rng := rand.New(rand.NewSource(7))

	// The floor: even one observation costs more than the budget allowed.
	single, err := FromSorted([]float64{42}).MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	t.Logf("a one-observation sketch is %d bytes (header %d + one centroid %d)",
		len(single), headerSize, centroidSize)

	// Realistic bucket sizes: a per-minute, per-endpoint bucket holds tens to
	// hundreds of requests.
	var total int
	const rows = 2000
	for i := 0; i < rows; i++ {
		n := 5 + rng.Intn(500)
		values := make([]float64, n)
		for j := range values {
			values[j] = 10 + rng.ExpFloat64()*50
		}
		b, err := FromSorted(sortedCopy(values)).MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary: %v", err)
		}
		total += len(b)
	}

	perRow := total / rows
	t.Logf("mean serialised sketch over %d realistic buckets: %d bytes/row", rows, perRow)

	if perRow > MeasuredBytesPerRow {
		t.Errorf("sketch costs %d bytes/row, above the measured budget of %d. "+
			"If this is a deliberate change, re-measure and update MeasuredBytesPerRow and SD-007.",
			perRow, MeasuredBytesPerRow)
	}
}

// ─── behaviour ───

func TestQuantileRejectsOutOfRange(t *testing.T) {
	s := goldenSketch()
	for _, q := range []float64{-0.1, 1.1, math.NaN()} {
		_, err := s.Quantile(q)
		if !errors.Is(err, ErrQuantileRange) {
			t.Errorf("Quantile(%v) err = %v, want ErrQuantileRange", q, err)
		}
	}
	// The endpoints are valid.
	for _, q := range []float64{0, 1} {
		if _, err := s.Quantile(q); err != nil {
			t.Errorf("Quantile(%v) = %v, want it accepted", q, err)
		}
	}
}

func TestEmptySketchHasNoQuantile(t *testing.T) {
	s := New()
	if s.Count() != 0 {
		t.Errorf("Count = %d, want 0", s.Count())
	}
	if _, err := s.Quantile(0.95); !errors.Is(err, ErrEmptySketch) {
		t.Errorf("err = %v, want ErrEmptySketch", err)
	}
	if _, err := FromSorted(nil).Quantile(0.5); !errors.Is(err, ErrEmptySketch) {
		t.Errorf("err = %v, want ErrEmptySketch for an empty build", err)
	}
}

func TestEmptySketchSerialisesAndRestores(t *testing.T) {
	data, err := New().MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	if len(data) != headerSize {
		t.Errorf("empty sketch is %d bytes, want just the %d-byte header", len(data), headerSize)
	}
	var back Sketch
	if err := back.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if back.Count() != 0 {
		t.Errorf("Count = %d, want 0", back.Count())
	}
}

func TestMergeWithEmptyIsIdentity(t *testing.T) {
	base := goldenSketch()
	want, _ := base.MarshalBinary()

	if err := base.Merge(New()); err != nil {
		t.Fatalf("Merge(empty): %v", err)
	}
	if err := base.Merge(nil); err != nil {
		t.Fatalf("Merge(nil): %v", err)
	}
	got, _ := base.MarshalBinary()
	if !bytes.Equal(got, want) {
		t.Error("merging an empty sketch changed the result")
	}

	// And empty-merge-nonempty adopts the other side.
	empty := New()
	if err := empty.Merge(goldenSketch()); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got, _ = empty.MarshalBinary()
	if !bytes.Equal(got, want) {
		t.Error("merging into an empty sketch did not adopt the other side exactly")
	}
}

func TestAddMatchesFromSortedForOneValue(t *testing.T) {
	s := New()
	s.Add(42)
	if s.Count() != 1 {
		t.Fatalf("Count = %d, want 1", s.Count())
	}
	q, err := s.Quantile(0.5)
	if err != nil {
		t.Fatalf("Quantile: %v", err)
	}
	if q != 42 {
		t.Errorf("Quantile(0.5) = %v, want 42", q)
	}
}

func TestNaNObservationsAreDropped(t *testing.T) {
	s := FromSorted([]float64{1, 2, math.NaN(), 3})
	if s.Count() != 3 {
		t.Errorf("Count = %d, want 3 — NaN is not an observation", s.Count())
	}
	s2 := New()
	s2.Add(math.NaN())
	if s2.Count() != 0 {
		t.Errorf("Count = %d, want 0", s2.Count())
	}
}

func TestMergeAllOfNothing(t *testing.T) {
	s, err := MergeAll(nil)
	if err != nil {
		t.Fatalf("MergeAll(nil): %v", err)
	}
	if s.Count() != 0 {
		t.Errorf("Count = %d, want 0", s.Count())
	}
}

func TestMergeAllPropagatesMismatch(t *testing.T) {
	odd := goldenSketch()
	odd.compression = Compression * 3
	if _, err := MergeAll([]*Sketch{goldenSketch(), odd}); !errors.Is(err, ErrCompressionMismatch) {
		t.Errorf("err = %v, want ErrCompressionMismatch", err)
	}
}

func TestCompressionDefaultsWhenZero(t *testing.T) {
	var s Sketch
	if got := s.Compression(); got != Compression {
		t.Errorf("Compression() = %d on a zero value, want %d", got, Compression)
	}
}

func TestCentroidsAreCanonicallyOrdered(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	s := FromSorted(sortedCopy(generate("bimodal", 50_000, rng)))

	for i := 1; i < len(s.centroids); i++ {
		prev, cur := s.centroids[i-1], s.centroids[i]
		if cur.mean < prev.mean {
			t.Fatalf("centroid %d mean %v is below its predecessor %v", i, cur.mean, prev.mean)
		}
		if cur.mean == prev.mean && cur.weight < prev.weight {
			t.Fatalf("centroid %d ties on mean but its weight %d is below %d; the tiebreak is not applied",
				i, cur.weight, prev.weight)
		}
	}
}

func TestCountSurvivesMerge(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	var parts []*Sketch
	var want int64
	for i := 0; i < 20; i++ {
		n := 100 + rng.Intn(900)
		parts = append(parts, FromSorted(sortedCopy(generate("normal", n, rng))))
		want += int64(n)
	}
	merged, err := MergeAll(parts)
	if err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	if merged.Count() != want {
		t.Errorf("Count = %d, want %d — merging must not lose or invent observations", merged.Count(), want)
	}
}
