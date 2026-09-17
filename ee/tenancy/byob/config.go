// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package byob lets a Gravix Cloud tenant register their own S3-compatible
// bucket, verifies Gravix can write, read and delete objects in it, and
// produces the exact environment variables that let the unmodified OSS
// ingestion and rollup binaries write into that bucket instead of the
// shared Cloud bucket.
//
// Nothing here is a capability a self-hoster lacks. A self-hoster's facts
// already sit in storage they own; they set S3_BUCKET and that is the end of
// it. This package exists only because Gravix Cloud runs installations on
// other people's behalf, which is the one situation where "whose bucket is
// this" is a question at all.
package byob

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// BucketConfig is one tenant's bring-your-own-bucket registration.
type BucketConfig struct {
	TenantID        string
	Endpoint        string // e.g. "https://s3.us-east-1.amazonaws.com"
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Status          string // "pending", "verified", "failed"
	FailureReason   string // non-empty only when Status == "failed"
	VerifiedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const (
	StatusPending  = "pending"
	StatusVerified = "verified"
	StatusFailed   = "failed"
)

var (
	ErrBucketNotWritable      = errors.New("byob: bucket rejected a write")
	ErrBucketNotReadable      = errors.New("byob: object written but could not be read back")
	ErrBucketContentMismatch  = errors.New("byob: object content did not round-trip")
	ErrBucketNotDeletable     = errors.New("byob: bucket rejected a delete of the marker object")
	ErrBucketDeleteNotVisible = errors.New("byob: deleted marker object is still visible")
)

// markerPrefix is where verification objects are written. It is a distinct
// prefix from anything Gravix reads, so a marker left behind by an interrupted
// verification can never be mistaken for data.
const markerPrefix = "gravix-byob-verify/"

// markerSize is the size of the random payload written during verification.
// Large enough that a backend returning a canned or truncated response fails
// the comparison, small enough to be free.
const markerSize = 32

// newStore builds the object store ValidateBucket verifies against. It is a
// variable so tests can substitute a fake backend: storage.NewS3Store needs a
// real endpoint, and a test that needs a real endpoint is a test that does not
// run. Production code never reassigns it.
var newStore = func(ctx context.Context, cfg BucketConfig) (storage.ObjectStore, error) {
	return storage.NewS3Store(ctx, cfg.Endpoint, cfg.Region, cfg.Bucket, cfg.AccessKeyID, cfg.SecretAccessKey)
}

// ValidateBucket writes a marker object, reads it back, confirms the bytes
// round-trip, then deletes it and confirms the delete took effect.
//
// All five steps are checked because all five are things a misconfigured
// bucket does wrong independently. A write-only IAM policy passes step one and
// fails step two. A bucket with object-lock enabled passes the first three and
// fails the fourth — and that bucket would accept every fact Gravix sent it
// and then refuse every retention purge, which is a compliance problem
// discovered months later rather than at registration.
//
// It constructs its own storage.ObjectStore from cfg — it never reuses a
// process-wide store, because the whole point is to exercise the credentials
// the tenant just handed over rather than the ones this process already has.
func ValidateBucket(ctx context.Context, cfg BucketConfig) error {
	store, err := newStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBucketNotWritable, err)
	}

	markerKey := markerPrefix + uuid.New().String()
	content := make([]byte, markerSize)
	if _, err := rand.Read(content); err != nil {
		return fmt.Errorf("byob: could not generate a marker payload: %w", err)
	}

	if err := store.Put(ctx, markerKey, bytes.NewReader(content)); err != nil {
		return fmt.Errorf("%w: %v", ErrBucketNotWritable, err)
	}

	rc, err := store.Get(ctx, markerKey)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBucketNotReadable, err)
	}
	got, readErr := io.ReadAll(rc)
	rc.Close()
	if readErr != nil {
		return fmt.Errorf("%w: %v", ErrBucketNotReadable, readErr)
	}
	if !bytes.Equal(got, content) {
		return fmt.Errorf("%w: wrote %d bytes, read back %d", ErrBucketContentMismatch, len(content), len(got))
	}

	if err := store.Delete(ctx, markerKey); err != nil {
		return fmt.Errorf("%w: %v", ErrBucketNotDeletable, err)
	}

	// A Delete that returns nil and leaves the object readable is the failure
	// mode of a versioned or object-locked bucket. Retention depends on delete
	// actually deleting, so this is checked rather than assumed.
	exists, err := store.Exists(ctx, markerKey)
	if err == nil && exists {
		return ErrBucketDeleteNotVisible
	}

	return nil
}

// ProvisionEnv returns the exact five environment variables that, injected
// into an ingestion or rollup deployment, make the unmodified OSS binaries
// read and write cfg's bucket.
//
// These keys are read verbatim by services/ingestion/main.go and by every
// binary under transforms/. That is the entire mechanism: bring-your-own-bucket
// is a deployment-time parameterisation of software that already supports it,
// not a feature bolted onto the ingest path. Nothing in core knows this
// package exists.
//
// It is pure. It performs no I/O and does not touch the Store.
func ProvisionEnv(cfg BucketConfig) map[string]string {
	return map[string]string{
		"S3_ENDPOINT":   cfg.Endpoint,
		"S3_REGION":     cfg.Region,
		"S3_BUCKET":     cfg.Bucket,
		"S3_ACCESS_KEY": cfg.AccessKeyID,
		"S3_SECRET_KEY": cfg.SecretAccessKey,
	}
}
