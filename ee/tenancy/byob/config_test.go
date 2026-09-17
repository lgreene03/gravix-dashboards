// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package byob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// fakeBucket is an in-memory ObjectStore standing in for a customer's S3.
// Each hook, when set, replaces the corresponding healthy behaviour, so a test
// describes one broken bucket rather than a whole new implementation.
type fakeBucket struct {
	mu      sync.Mutex
	objects map[string][]byte

	putErr     error
	getErr     error
	deleteErr  error
	existsErr  error
	corruptGet func([]byte) []byte
	keepOnDel  bool // Delete returns nil but the object survives
}

func newFakeBucket() *fakeBucket {
	return &fakeBucket{objects: map[string][]byte{}}
}

func (f *fakeBucket) Put(_ context.Context, key string, r io.Reader) error {
	if f.putErr != nil {
		return f.putErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = b
	return nil
}

func (f *fakeBucket) PutWithStorageClass(ctx context.Context, key string, r io.Reader, _ storage.StorageClass) error {
	return f.Put(ctx, key, r)
}

func (f *fakeBucket) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	if f.corruptGet != nil {
		b = f.corruptGet(b)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeBucket) Delete(_ context.Context, key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if f.keepOnDel {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}

func (f *fakeBucket) List(_ context.Context, prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeBucket) Exists(_ context.Context, key string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok, nil
}

// withBucket points ValidateBucket at fake for the duration of one test.
func withBucket(t *testing.T, fake *fakeBucket) {
	t.Helper()
	prev := newStore
	newStore = func(context.Context, BucketConfig) (storage.ObjectStore, error) {
		return fake, nil
	}
	t.Cleanup(func() { newStore = prev })
}

func testConfig() BucketConfig {
	return BucketConfig{
		TenantID:        "ten_abc123",
		Endpoint:        "https://s3.us-east-1.amazonaws.com",
		Region:          "us-east-1",
		Bucket:          "acme-gravix-facts",
		AccessKeyID:     "AKIAEXAMPLEKEYID0000",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
}

// AC-1
func TestValidateBucketAcceptsHealthyBucket(t *testing.T) {
	fake := newFakeBucket()
	withBucket(t, fake)

	if err := ValidateBucket(context.Background(), testConfig()); err != nil {
		t.Fatalf("a healthy bucket was rejected: %v", err)
	}

	// The marker is cleaned up. A verification that leaves litter in a
	// customer's bucket is a support ticket every time they run it.
	if len(fake.objects) != 0 {
		t.Errorf("verification left %d object(s) behind: %v", len(fake.objects), fake.objects)
	}
}

// AC-2
func TestValidateBucketRejectsUnwritableBucket(t *testing.T) {
	fake := newFakeBucket()
	fake.putErr = errors.New("AccessDenied: s3:PutObject")
	withBucket(t, fake)

	err := ValidateBucket(context.Background(), testConfig())
	if !errors.Is(err, ErrBucketNotWritable) {
		t.Fatalf("got %v, want ErrBucketNotWritable", err)
	}
	// The underlying cause survives the wrap. "bucket rejected a write" alone
	// sends an operator looking at the bucket policy when the answer was in
	// the S3 error all along.
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("the S3 error was swallowed: %v", err)
	}
}

// AC-3
func TestValidateBucketRejectsContentMismatch(t *testing.T) {
	fake := newFakeBucket()
	fake.corruptGet = func(b []byte) []byte {
		altered := append([]byte(nil), b...)
		altered[0] ^= 0xFF
		return altered
	}
	withBucket(t, fake)

	if err := ValidateBucket(context.Background(), testConfig()); !errors.Is(err, ErrBucketContentMismatch) {
		t.Fatalf("got %v, want ErrBucketContentMismatch", err)
	}
}

// TestValidateBucketRejectsTruncatedReadBack covers the other shape of a
// mismatch: same prefix, fewer bytes. A proxy that truncates would otherwise
// pass a prefix comparison.
func TestValidateBucketRejectsTruncatedReadBack(t *testing.T) {
	fake := newFakeBucket()
	fake.corruptGet = func(b []byte) []byte { return b[:len(b)-1] }
	withBucket(t, fake)

	if err := ValidateBucket(context.Background(), testConfig()); !errors.Is(err, ErrBucketContentMismatch) {
		t.Fatalf("got %v, want ErrBucketContentMismatch", err)
	}
}

func TestValidateBucketRejectsUnreadableBucket(t *testing.T) {
	fake := newFakeBucket()
	fake.getErr = errors.New("AccessDenied: s3:GetObject")
	withBucket(t, fake)

	if err := ValidateBucket(context.Background(), testConfig()); !errors.Is(err, ErrBucketNotReadable) {
		t.Fatalf("got %v, want ErrBucketNotReadable", err)
	}
}

func TestValidateBucketRejectsUndeletableBucket(t *testing.T) {
	fake := newFakeBucket()
	fake.deleteErr = errors.New("AccessDenied: s3:DeleteObject")
	withBucket(t, fake)

	if err := ValidateBucket(context.Background(), testConfig()); !errors.Is(err, ErrBucketNotDeletable) {
		t.Fatalf("got %v, want ErrBucketNotDeletable", err)
	}
}

// TestValidateBucketRejectsDeleteThatDoesNotDelete is the object-lock case.
// The bucket says yes to everything and quietly keeps the object. Gravix would
// happily ingest into it for months and then fail every retention purge, so
// this is caught at registration rather than at the first compliance audit.
func TestValidateBucketRejectsDeleteThatDoesNotDelete(t *testing.T) {
	fake := newFakeBucket()
	fake.keepOnDel = true
	withBucket(t, fake)

	if err := ValidateBucket(context.Background(), testConfig()); !errors.Is(err, ErrBucketDeleteNotVisible) {
		t.Fatalf("got %v, want ErrBucketDeleteNotVisible", err)
	}
}

// TestValidateBucketToleratesExistsFailure: a bucket whose HeadObject is
// denied but whose delete succeeded is usable. Refusing it would turn a
// narrower-than-usual IAM policy into a registration failure with no
// actionable message.
func TestValidateBucketToleratesExistsFailure(t *testing.T) {
	fake := newFakeBucket()
	fake.existsErr = errors.New("AccessDenied: s3:ListBucket")
	withBucket(t, fake)

	if err := ValidateBucket(context.Background(), testConfig()); err != nil {
		t.Fatalf("a bucket with a denied HeadObject was rejected: %v", err)
	}
}

func TestValidateBucketReportsStoreConstructionFailure(t *testing.T) {
	prev := newStore
	newStore = func(context.Context, BucketConfig) (storage.ObjectStore, error) {
		return nil, errors.New("invalid region \"moon-north-1\"")
	}
	t.Cleanup(func() { newStore = prev })

	err := ValidateBucket(context.Background(), testConfig())
	if !errors.Is(err, ErrBucketNotWritable) {
		t.Fatalf("got %v, want ErrBucketNotWritable", err)
	}
	if !strings.Contains(err.Error(), "moon-north-1") {
		t.Errorf("the construction error was swallowed: %v", err)
	}
}

// TestValidateBucketUsesADistinctMarkerKeyEachTime: two concurrent
// verifications of the same bucket must not collide, and a marker must never
// look like a data key.
func TestValidateBucketUsesADistinctMarkerKeyEachTime(t *testing.T) {
	seen := map[string]bool{}
	fake := newFakeBucket()
	prev := newStore
	newStore = func(context.Context, BucketConfig) (storage.ObjectStore, error) {
		return &keyRecorder{ObjectStore: fake, seen: seen}, nil
	}
	t.Cleanup(func() { newStore = prev })

	for i := 0; i < 3; i++ {
		if err := ValidateBucket(context.Background(), testConfig()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if len(seen) != 3 {
		t.Errorf("three verifications used %d distinct marker keys, want 3: %v", len(seen), seen)
	}
	for k := range seen {
		if !strings.HasPrefix(k, markerPrefix) {
			t.Errorf("marker key %q is not under the verification prefix %q", k, markerPrefix)
		}
	}
}

type keyRecorder struct {
	storage.ObjectStore
	seen map[string]bool
}

func (k *keyRecorder) Put(ctx context.Context, key string, r io.Reader) error {
	k.seen[key] = true
	return k.ObjectStore.Put(ctx, key, r)
}

// AC-4
func TestProvisionEnvMatchesIngestionEnvVars(t *testing.T) {
	cfg := testConfig()
	env := ProvisionEnv(cfg)

	want := map[string]string{
		"S3_ENDPOINT":   cfg.Endpoint,
		"S3_REGION":     cfg.Region,
		"S3_BUCKET":     cfg.Bucket,
		"S3_ACCESS_KEY": cfg.AccessKeyID,
		"S3_SECRET_KEY": cfg.SecretAccessKey,
	}
	if len(env) != len(want) {
		t.Fatalf("ProvisionEnv returned %d keys, want %d: %v", len(env), len(want), env)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
}

// TestProvisionEnvKeysAreTheOnesTheBinariesRead is the criterion that matters
// and the one a unit test would normally miss: ProvisionEnv could agree with
// itself forever while the ingestion binary read different names.
//
// So this reads the core source. If somebody renames S3_BUCKET in
// services/ingestion/main.go, every BYOB tenant's facts silently start landing
// in the shared Cloud bucket — the exact outcome bring-your-own-bucket is sold
// to prevent — and nothing else in the repository would notice.
func TestProvisionEnvKeysAreTheOnesTheBinariesRead(t *testing.T) {
	// ee/tenancy/byob -> repository root
	root := filepath.Join("..", "..", "..")
	sources := []string{
		filepath.Join(root, "services", "ingestion", "main.go"),
		filepath.Join(root, "transforms", "request_metrics_minute", "main.go"),
		filepath.Join(root, "transforms", "service_events_daily", "main.go"),
		filepath.Join(root, "transforms", "service_events_detail", "main.go"),
	}

	for _, src := range sources {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("reading %s: %v", src, err)
		}
		body := string(b)
		for key := range ProvisionEnv(testConfig()) {
			// os.Getenv("S3_BUCKET") — the literal, as the binary reads it.
			pattern := regexp.MustCompile(`os\.Getenv\(\s*"` + regexp.QuoteMeta(key) + `"\s*\)`)
			if !pattern.MatchString(body) {
				t.Errorf("%s does not read %s; ProvisionEnv would set a variable nothing consumes, "+
					"and this tenant's facts would land in the shared bucket", src, key)
			}
		}
	}
}

// TestProvisionEnvIsPure pins that it performs no I/O and does not mutate its
// argument — it is called once per cell provisioning, from code that has
// already decided what the config is.
func TestProvisionEnvIsPure(t *testing.T) {
	cfg := testConfig()
	before := cfg
	first := ProvisionEnv(cfg)
	second := ProvisionEnv(cfg)

	if cfg != before {
		t.Error("ProvisionEnv mutated its argument")
	}
	for k, v := range first {
		if second[k] != v {
			t.Errorf("ProvisionEnv is not deterministic: %s was %q then %q", k, v, second[k])
		}
	}
	// A returned map that the caller mutates must not affect the next call.
	first["S3_BUCKET"] = "somebody-elses-bucket"
	if ProvisionEnv(cfg)["S3_BUCKET"] != cfg.Bucket {
		t.Error("ProvisionEnv returns shared state across calls")
	}
}
