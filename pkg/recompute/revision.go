// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package recompute

import (
	"context"
	"errors"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// now is the clock used to stamp a revision. It is a variable so a test can make
// RevisedAt predictable; nothing else in this package reads a clock.
var now = time.Now

// revisionOutcome is what comparing a rebuild against what is stored concluded.
type revisionOutcome struct {
	// Manifest is the manifest to write.
	Manifest *manifest.Manifest
	// HadManifest reports whether a manifest was already stored for this partition.
	// A partition with correct data but no manifest predates GRVX-802 and has no
	// revision baseline; it starts at 0, which is recorded rather than assumed.
	HadManifest bool
	// Revised reports whether this rebuild changed the partition's published rows.
	Revised bool
}

// decideRevision builds the manifest for a partition and works out whether the
// rebuild revises a previously published value.
//
// The three cases are exhaustive and each means something different to a
// consumer: no manifest is a first publication, an identical digest is the same
// number as before, and a differing digest is a number that has changed since
// someone may have read it.
func decideRevision(ctx context.Context, store storage.ObjectStore, dataKey string, next manifest.Manifest) (*revisionOutcome, error) {
	previous, err := manifest.Read(ctx, store, dataKey)
	switch {
	case err == nil:
	case errors.Is(err, manifest.ErrNoManifest):
		previous = nil
	default:
		return nil, err
	}

	revised := previous != nil && previous.ContentDigest != next.ContentDigest

	return &revisionOutcome{
		Manifest:    manifest.Revise(next, previous, now()),
		HadManifest: previous != nil,
		Revised:     revised,
	}, nil
}
