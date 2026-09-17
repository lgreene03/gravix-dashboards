// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package sketch provides a deterministic, mergeable quantile sketch over
// latency observations, so a percentile over many buckets is computed by
// merging sketches rather than by combining per-bucket percentiles.
//
// Combining per-bucket percentiles is the thing this package exists to replace.
// The maximum of sixty one-minute p95s is not the hour's p95 and its error is
// unbounded: it is whatever the worst minute happened to be. Merging sketches
// gives the hour's p95 with a measured bound instead.
//
// # Determinism
//
// A sketch's serialised bytes are part of a Parquet row, and GRVX-801 requires a
// rebuilt partition to be byte-identical to the original. So the same multiset of
// observations must always produce the same bytes, whatever order they arrived
// in. Two rules achieve that:
//
//   - Observations enter through FromSorted, in ascending order, so the digest's
//     internal merging sees the same sequence every time.
//   - Centroids are held and written in a canonical order — ascending mean, ties
//     broken by ascending weight — rather than in whatever order the underlying
//     library last left them.
//
// Merge is built on the same rule: it unions the centroid multisets and
// re-derives from the canonical order, which is what makes it commutative and
// associative rather than merely usually-so.
package sketch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/influxdata/tdigest"
)

// Version identifies the serialisation format. Bump on any format change.
const Version = "tdigest-v1"

// Compression is the t-digest compression parameter. Fixed, because changing it
// changes every serialised sketch and therefore every content digest.
const Compression = 100

// MaxRelativeError is the guaranteed relative error at the tails, as a fraction.
// This value is asserted by TestAccuracyWithinBound and is published in the
// metric contract. It is a measured bound, not an aspiration.
const MaxRelativeError = 0.01

// headerSize is the fixed-layout prefix: version, compression, count, centroid
// count.
const headerSize = 32

// centroidSize is one serialised centroid: float64 mean then int64 weight.
const centroidSize = 16

// versionFieldSize is the NUL-padded width of the version string on disk.
const versionFieldSize = 16

var (
	// ErrEmptySketch is returned when a quantile is asked of no observations.
	ErrEmptySketch = errors.New("sketch: no observations")
	// ErrVersionMismatch is returned when a payload was written by another format
	// version.
	ErrVersionMismatch = errors.New("sketch: serialised version mismatch")
	// ErrCompressionMismatch is returned when two sketches were built with
	// different compression and so cannot be merged.
	ErrCompressionMismatch = errors.New("sketch: cannot merge sketches with different compression")
	// ErrQuantileRange is returned for a quantile outside [0,1].
	ErrQuantileRange = errors.New("sketch: quantile must be in [0,1]")
	// ErrMalformed is returned when a payload is truncated or internally
	// inconsistent.
	ErrMalformed = errors.New("sketch: malformed payload")
)

// centroid is one cluster of observations.
type centroid struct {
	mean   float64
	weight int64
}

// Sketch is a quantile sketch over float64 observations.
type Sketch struct {
	compression uint32
	centroids   []centroid // canonical order, see package docs
	count       int64

	// compressedCache holds the bounded form derived from centroids. Deriving it
	// costs a pass over every accumulated centroid, and a merged day holds
	// hundreds of thousands, so it is computed once and reused until a mutation
	// invalidates it.
	compressedCache []centroid
	cacheValid      bool
}

// New returns an empty sketch.
func New() *Sketch {
	return &Sketch{compression: Compression}
}

// FromSorted builds a sketch from observations that the caller has already sorted
// ascending. Sorting is required: it is what makes serialisation deterministic for
// a given multiset, independent of the order facts were read from storage.
//
// The input is not re-sorted — checking would cost as much as sorting, and the
// caller is the rollup, which has already sorted for its exact percentiles.
func FromSorted(sorted []float64) *Sketch {
	s := New()
	if len(sorted) == 0 {
		return s
	}

	digest := tdigest.NewWithCompression(float64(Compression))
	for _, v := range sorted {
		if math.IsNaN(v) {
			continue
		}
		digest.Add(v, 1)
	}
	s.adopt(digest)
	return s
}

// Add inserts one observation. Prefer FromSorted for determinism: a sketch built
// by repeated Add depends on the order the values arrived in.
func (s *Sketch) Add(v float64) {
	if math.IsNaN(v) {
		return
	}
	digest := s.digest()
	digest.Add(v, 1)
	s.adopt(digest)
}

// Quantile returns the value at q, where 0 <= q <= 1.
func (s *Sketch) Quantile(q float64) (float64, error) {
	if q < 0 || q > 1 || math.IsNaN(q) {
		return 0, fmt.Errorf("%w, got %v", ErrQuantileRange, q)
	}
	if s.count == 0 {
		return 0, ErrEmptySketch
	}
	return s.digest().Quantile(q), nil
}

// Count returns the number of observations.
func (s *Sketch) Count() int64 { return s.count }

// Compression returns the compression parameter this sketch was built with.
func (s *Sketch) Compression() uint32 {
	if s.compression == 0 {
		return Compression
	}
	return s.compression
}

// Merge folds other into s. Merge is associative and commutative over the same
// Compression: merging any permutation of the same sketches, in any grouping,
// yields the same serialised result. That holds because a merge is an exact
// multiset union and compression happens once, at serialisation.
func (s *Sketch) Merge(other *Sketch) error {
	if other == nil || other.count == 0 {
		return nil
	}
	if s.Compression() != other.Compression() {
		return fmt.Errorf("%w: %d vs %d", ErrCompressionMismatch, s.Compression(), other.Compression())
	}
	if s.count == 0 {
		s.compression = other.Compression()
		s.centroids = append([]centroid(nil), other.centroids...)
		s.count = other.count
		s.cacheValid = false
		return nil
	}

	// Accumulate the union of the centroid multisets and leave it uncompressed.
	//
	// Compressing at each merge step would make the result depend on how the
	// merges were grouped: (a+b)+c and a+(b+c) would compress different
	// intermediates and land in different places. Deferring compression to
	// serialisation makes a merge an exact multiset union, which is associative
	// and commutative by construction. The accumulated form is transient and only
	// as large as the sum of its parts; what reaches storage is compressed.
	// Both sides are already canonical, so the union is a linear merge rather than
	// a re-sort.
	s.centroids = mergeCanonical(s.centroids, other.centroids)
	s.count += other.count
	s.cacheValid = false
	return nil
}

// mergeCanonical merges two canonically ordered centroid slices into one, in
// linear time.
func mergeCanonical(a, b []centroid) []centroid {
	out := make([]centroid, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if lessCentroid(a[i], b[j]) {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// compressed returns the bounded representation of this sketch: at most a
// digest's worth of centroids, derived from the canonical union. It is what
// serialisation writes and what a query reads, so a merged sketch costs no more
// on disk than a built one.
func (s *Sketch) compressed() []centroid {
	if s.cacheValid {
		return s.compressedCache
	}
	if len(s.centroids) == 0 {
		s.compressedCache, s.cacheValid = nil, true
		return nil
	}
	digest := tdigest.NewWithCompression(float64(s.Compression()))
	for _, c := range s.centroids {
		digest.AddCentroid(tdigest.Centroid{Mean: c.mean, Weight: float64(c.weight)})
	}
	out := fromDigest(digest)
	canonicalise(out)

	s.compressedCache, s.cacheValid = out, true
	return out
}

// MergeAll merges every sketch in the slice into a new sketch.
func MergeAll(sketches []*Sketch) (*Sketch, error) {
	out := New()

	// Gather every centroid first and order them once. Folding pairwise would
	// re-copy the growing accumulator on each step, which is quadratic in the
	// number of sketches — and a day is 1,440 of them.
	total := 0
	for _, s := range sketches {
		if s == nil || s.count == 0 {
			continue
		}
		if s.Compression() != out.Compression() {
			return nil, fmt.Errorf("%w: %d vs %d", ErrCompressionMismatch, out.Compression(), s.Compression())
		}
		total += len(s.centroids)
	}

	union := make([]centroid, 0, total)
	var count int64
	for _, s := range sketches {
		if s == nil || s.count == 0 {
			continue
		}
		union = append(union, s.centroids...)
		count += s.count
	}
	canonicalise(union)

	out.centroids = union
	out.count = count
	return out, nil
}

// MarshalBinary serialises s. For the same multiset and Compression the output
// is byte-identical on every platform.
func (s *Sketch) MarshalBinary() ([]byte, error) {
	centroids := s.compressed()
	buf := make([]byte, headerSize+centroidSize*len(centroids))

	copy(buf[0:versionFieldSize], Version)
	binary.LittleEndian.PutUint32(buf[16:20], s.Compression())
	binary.LittleEndian.PutUint64(buf[20:28], uint64(s.count))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(len(centroids)))

	off := headerSize
	for _, c := range centroids {
		binary.LittleEndian.PutUint64(buf[off:off+8], math.Float64bits(c.mean))
		binary.LittleEndian.PutUint64(buf[off+8:off+16], uint64(c.weight))
		off += centroidSize
	}
	return buf, nil
}

// UnmarshalBinary restores a sketch. Returns ErrVersionMismatch when the payload
// was written by a different format version.
func (s *Sketch) UnmarshalBinary(b []byte) error {
	if len(b) < headerSize {
		return fmt.Errorf("%w: %d bytes, need at least %d", ErrMalformed, len(b), headerSize)
	}

	version := string(bytes.TrimRight(b[0:versionFieldSize], "\x00"))
	if version != Version {
		return fmt.Errorf("%w: file %s, binary %s", ErrVersionMismatch, version, Version)
	}

	compression := binary.LittleEndian.Uint32(b[16:20])
	count := int64(binary.LittleEndian.Uint64(b[20:28]))
	n := int(binary.LittleEndian.Uint32(b[28:32]))

	want := headerSize + centroidSize*n
	if len(b) != want {
		return fmt.Errorf("%w: %d bytes for %d centroids, want %d", ErrMalformed, len(b), n, want)
	}

	centroids := make([]centroid, n)
	off := headerSize
	var weightSum int64
	for i := 0; i < n; i++ {
		centroids[i] = centroid{
			mean:   math.Float64frombits(binary.LittleEndian.Uint64(b[off : off+8])),
			weight: int64(binary.LittleEndian.Uint64(b[off+8 : off+16])),
		}
		weightSum += centroids[i].weight
		off += centroidSize
	}
	if weightSum != count {
		return fmt.Errorf("%w: centroid weights total %d, header says %d", ErrMalformed, weightSum, count)
	}

	s.compression = compression
	s.centroids = centroids
	s.count = count
	s.cacheValid = false
	return nil
}

// adopt takes the digest's centroids into canonical form.
func (s *Sketch) adopt(digest *tdigest.TDigest) {
	centroids := fromDigest(digest)
	canonicalise(centroids)

	var count int64
	for _, c := range centroids {
		count += c.weight
	}

	s.centroids = centroids
	s.count = count
	s.cacheValid = false
	if s.compression == 0 {
		s.compression = Compression
	}
}

// fromDigest extracts a digest's centroids. Weights are whole numbers because
// every observation enters with weight 1 and merging only adds them, so rounding
// restores an exact integer rather than discarding information.
func fromDigest(digest *tdigest.TDigest) []centroid {
	raw := digest.Centroids()
	out := make([]centroid, 0, len(raw))
	for _, c := range raw {
		if c.Weight <= 0 {
			continue
		}
		out = append(out, centroid{mean: c.Mean, weight: int64(math.Round(c.Weight))})
	}
	return out
}

// digest rebuilds a t-digest from the canonical centroids, so every query and
// merge starts from the same state the bytes describe.
func (s *Sketch) digest() *tdigest.TDigest {
	d := tdigest.NewWithCompression(float64(s.Compression()))
	for _, c := range s.centroids {
		d.AddCentroid(tdigest.Centroid{Mean: c.mean, Weight: float64(c.weight)})
	}
	return d
}

// canonicalise sorts centroids into the one order the format allows: ascending
// mean, ties broken by ascending weight. Without the tiebreak, two centroids with
// equal means could be written in either order and the bytes would differ between
// runs.
func canonicalise(centroids []centroid) {
	sort.Slice(centroids, func(i, j int) bool { return lessCentroid(centroids[i], centroids[j]) })
}

// lessCentroid is the canonical order: ascending mean, ties broken by ascending
// weight.
func lessCentroid(a, b centroid) bool {
	if a.mean != b.mean {
		return a.mean < b.mean
	}
	return a.weight < b.weight
}
