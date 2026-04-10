// Package chaos provides fault injection helpers for testing error paths
// in Gravix services. These are test utilities, not a chaos engineering framework.
package chaos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/referral"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// ErrFaultInjected is the default error returned by fault injection helpers.
var ErrFaultInjected = errors.New("chaos: fault injected")

// ------------------- FaultyStore -------------------

// FaultyStore wraps a storage.ObjectStore and injects configurable failures.
type FaultyStore struct {
	inner    storage.ObjectStore
	mu       sync.RWMutex
	errRate  float64            // probability [0,1] of random failure
	latency  time.Duration      // added delay before each operation
	keyErrs  map[string]error   // deterministic errors for specific keys
	opErrs   map[string]error   // deterministic errors for specific operations (Put, Get, Delete, List, Exists)
	disabled bool               // kill switch: all ops fail when true
}

// NewFaultyStore wraps an existing ObjectStore with fault injection.
// Pass nil for inner to get a store that only produces errors.
func NewFaultyStore(inner storage.ObjectStore) *FaultyStore {
	return &FaultyStore{
		inner:   inner,
		keyErrs: make(map[string]error),
		opErrs:  make(map[string]error),
	}
}

// SetErrorRate sets the probability [0,1] that any operation returns an error.
func (f *FaultyStore) SetErrorRate(rate float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errRate = rate
}

// SetLatency sets a delay injected before each operation.
func (f *FaultyStore) SetLatency(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latency = d
}

// SetKeyError makes operations on a specific key always return err.
func (f *FaultyStore) SetKeyError(key string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keyErrs[key] = err
}

// ClearKeyError removes a specific key error.
func (f *FaultyStore) ClearKeyError(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.keyErrs, key)
}

// SetOpError makes a specific operation always fail (op: "Put", "Get", "Delete", "List", "Exists").
func (f *FaultyStore) SetOpError(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opErrs[op] = err
}

// SetDisabled sets the kill switch. When true, all operations fail.
func (f *FaultyStore) SetDisabled(disabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disabled = disabled
}

func (f *FaultyStore) maybeFail(op, key string) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.disabled {
		return fmt.Errorf("chaos: store disabled")
	}
	if f.latency > 0 {
		time.Sleep(f.latency)
	}
	if err, ok := f.opErrs[op]; ok {
		return err
	}
	if key != "" {
		if err, ok := f.keyErrs[key]; ok {
			return err
		}
	}
	if f.errRate > 0 && rand.Float64() < f.errRate {
		return ErrFaultInjected
	}
	return nil
}

func (f *FaultyStore) Put(ctx context.Context, key string, reader io.Reader) error {
	if err := f.maybeFail("Put", key); err != nil {
		return err
	}
	if f.inner == nil {
		return nil
	}
	return f.inner.Put(ctx, key, reader)
}

func (f *FaultyStore) PutWithStorageClass(ctx context.Context, key string, reader io.Reader, class storage.StorageClass) error {
	if err := f.maybeFail("Put", key); err != nil {
		return err
	}
	if f.inner == nil {
		return nil
	}
	return f.inner.PutWithStorageClass(ctx, key, reader, class)
}

func (f *FaultyStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := f.maybeFail("Get", key); err != nil {
		return nil, err
	}
	if f.inner == nil {
		return nil, fmt.Errorf("chaos: no inner store and no fault configured")
	}
	return f.inner.Get(ctx, key)
}

func (f *FaultyStore) Delete(ctx context.Context, key string) error {
	if err := f.maybeFail("Delete", key); err != nil {
		return err
	}
	if f.inner == nil {
		return nil
	}
	return f.inner.Delete(ctx, key)
}

func (f *FaultyStore) List(ctx context.Context, prefix string) ([]string, error) {
	if err := f.maybeFail("List", prefix); err != nil {
		return nil, err
	}
	if f.inner == nil {
		return nil, nil
	}
	return f.inner.List(ctx, prefix)
}

func (f *FaultyStore) Exists(ctx context.Context, key string) (bool, error) {
	if err := f.maybeFail("Exists", key); err != nil {
		return false, err
	}
	if f.inner == nil {
		return false, nil
	}
	return f.inner.Exists(ctx, key)
}

// ------------------- FaultyDB -------------------

// FaultyDB wraps a tenantdb.DB and allows injecting errors for specific repo methods.
// By default it delegates everything to the inner DB. Set repo-level overrides
// via the SetXxxError methods to make specific repo accessor methods fail.
type FaultyDB struct {
	inner          tenantdb.DB
	mu             sync.RWMutex
	repoErrs       map[string]error // keyed by repo name: "Tenants", "Users", etc.
}

// NewFaultyDB wraps an existing tenantdb.DB with fault injection.
func NewFaultyDB(inner tenantdb.DB) *FaultyDB {
	return &FaultyDB{
		inner:    inner,
		repoErrs: make(map[string]error),
	}
}

// SetRepoError configures a repo to return a failing proxy.
// repo should be one of: "Tenants", "APIKeys", "Users", "AlertRules", etc.
func (f *FaultyDB) SetRepoError(repo string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repoErrs[repo] = err
}

// ClearRepoError removes a repo-level error.
func (f *FaultyDB) ClearRepoError(repo string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.repoErrs, repo)
}

func (f *FaultyDB) repoErr(name string) error {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.repoErrs[name]
}

// faultyTenantRepo wraps TenantRepo and fails all calls with a configured error.
type faultyTenantRepo struct {
	err error
}

func (r *faultyTenantRepo) Create(_ context.Context, _ *tenantdb.Tenant) error            { return r.err }
func (r *faultyTenantRepo) GetByID(_ context.Context, _ string) (*tenantdb.Tenant, error)  { return nil, r.err }
func (r *faultyTenantRepo) GetByEmail(_ context.Context, _ string) (*tenantdb.Tenant, error) { return nil, r.err }
func (r *faultyTenantRepo) UpdatePlan(_ context.Context, _, _ string) error                 { return r.err }
func (r *faultyTenantRepo) UpdateStatus(_ context.Context, _, _ string) error               { return r.err }
func (r *faultyTenantRepo) UpdateStripe(_ context.Context, _, _, _ string) error            { return r.err }
func (r *faultyTenantRepo) UpdateTrial(_ context.Context, _ string, _, _ *time.Time) error  { return r.err }
func (r *faultyTenantRepo) UpdateParentTenant(_ context.Context, _, _ string) error         { return r.err }
func (r *faultyTenantRepo) ListChildren(_ context.Context, _ string) ([]*tenantdb.Tenant, error) { return nil, r.err }
func (r *faultyTenantRepo) List(_ context.Context) ([]*tenantdb.Tenant, error)              { return nil, r.err }

// faultyUserRepo wraps UserRepo and fails all calls with a configured error.
type faultyUserRepo struct {
	err error
}

func (r *faultyUserRepo) Create(_ context.Context, _ *tenantdb.User) error                 { return r.err }
func (r *faultyUserRepo) GetByEmail(_ context.Context, _ string) (*tenantdb.User, error)   { return nil, r.err }
func (r *faultyUserRepo) GetByID(_ context.Context, _ string) (*tenantdb.User, error)      { return nil, r.err }
func (r *faultyUserRepo) ListByTenant(_ context.Context, _ string) ([]*tenantdb.User, error) { return nil, r.err }
func (r *faultyUserRepo) UpdatePassword(_ context.Context, _, _ string) error               { return r.err }
func (r *faultyUserRepo) UpdateEmailVerified(_ context.Context, _ string, _ bool) error     { return r.err }
func (r *faultyUserRepo) UpdateLastLogin(_ context.Context, _ string, _ time.Time) error    { return r.err }
func (r *faultyUserRepo) UpdateRole(_ context.Context, _, _ string) error                   { return r.err }
func (r *faultyUserRepo) UpdateStatus(_ context.Context, _, _ string) error                 { return r.err }
func (r *faultyUserRepo) UpdateTwoFactor(_ context.Context, _ string, _ bool, _ string) error { return r.err }
func (r *faultyUserRepo) CountByTenant(_ context.Context, _ string) (int, error)            { return 0, r.err }

func (f *FaultyDB) Tenants() tenantdb.TenantRepo {
	if err := f.repoErr("Tenants"); err != nil {
		return &faultyTenantRepo{err: err}
	}
	return f.inner.Tenants()
}

func (f *FaultyDB) Users() tenantdb.UserRepo {
	if err := f.repoErr("Users"); err != nil {
		return &faultyUserRepo{err: err}
	}
	return f.inner.Users()
}

// For the remaining repos, we delegate directly. The pattern above can be
// extended to any repo that needs fault injection. For brevity, the rest
// just pass through.

func (f *FaultyDB) APIKeys() tenantdb.APIKeyRepo                       { return f.inner.APIKeys() }
func (f *FaultyDB) EventCounters() tenantdb.EventCounterRepo           { return f.inner.EventCounters() }
func (f *FaultyDB) MonthlyUsage() tenantdb.MonthlyUsageRepo            { return f.inner.MonthlyUsage() }
func (f *FaultyDB) NotificationChannels() tenantdb.NotificationChannelRepo { return f.inner.NotificationChannels() }
func (f *FaultyDB) AlertRules() tenantdb.AlertRuleRepo                 { return f.inner.AlertRules() }
func (f *FaultyDB) AlertHistory() tenantdb.AlertHistoryRepo            { return f.inner.AlertHistory() }
func (f *FaultyDB) AuditLog() tenantdb.AuditRepo                      { return f.inner.AuditLog() }
func (f *FaultyDB) RetentionPolicies() tenantdb.RetentionPolicyRepo    { return f.inner.RetentionPolicies() }
func (f *FaultyDB) PasswordResets() tenantdb.PasswordResetRepo         { return f.inner.PasswordResets() }
func (f *FaultyDB) EmailVerifications() tenantdb.EmailVerificationRepo { return f.inner.EmailVerifications() }
func (f *FaultyDB) Invitations() tenantdb.InvitationRepo               { return f.inner.Invitations() }
func (f *FaultyDB) ConsentRecords() tenantdb.ConsentRecordRepo         { return f.inner.ConsentRecords() }
func (f *FaultyDB) DeletionRequests() tenantdb.DeletionRequestRepo     { return f.inner.DeletionRequests() }
func (f *FaultyDB) SSOConfigs() tenantdb.SSOConfigRepo                 { return f.inner.SSOConfigs() }
func (f *FaultyDB) Sessions() tenantdb.SessionRepo                     { return f.inner.Sessions() }
func (f *FaultyDB) RecoveryCodes() tenantdb.RecoveryCodeRepo           { return f.inner.RecoveryCodes() }
func (f *FaultyDB) RevokedTokens() tenantdb.RevokedTokenRepo           { return f.inner.RevokedTokens() }
func (f *FaultyDB) SSOStates() tenantdb.SSOStateRepo                   { return f.inner.SSOStates() }
func (f *FaultyDB) Referrals() referral.ReferralRepo                   { return f.inner.Referrals() }
func (f *FaultyDB) Close() error                                       { return f.inner.Close() }

// ------------------- SlowReader -------------------

// SlowReader wraps an io.Reader and injects a delay before each Read call.
type SlowReader struct {
	inner   io.Reader
	delay   time.Duration
	once    bool // if true, delay only on the first read
	mu      sync.Mutex
	delayed bool
}

// NewSlowReader creates a reader that adds delay before each Read call.
func NewSlowReader(r io.Reader, delay time.Duration) *SlowReader {
	return &SlowReader{inner: r, delay: delay}
}

// NewSlowReaderOnce creates a reader that adds delay only on the first Read.
func NewSlowReaderOnce(r io.Reader, delay time.Duration) *SlowReader {
	return &SlowReader{inner: r, delay: delay, once: true}
}

func (s *SlowReader) Read(p []byte) (int, error) {
	if s.once {
		s.mu.Lock()
		alreadyDelayed := s.delayed
		s.delayed = true
		s.mu.Unlock()
		if !alreadyDelayed {
			time.Sleep(s.delay)
		}
	} else {
		time.Sleep(s.delay)
	}
	return s.inner.Read(p)
}

// ------------------- NetworkPartition -------------------

// NetworkPartition wraps an http.RoundTripper to simulate network failures.
type NetworkPartition struct {
	inner      http.RoundTripper
	mu         sync.RWMutex
	blocked    bool          // all requests fail when true
	errRate    float64       // probability [0,1] of random failure
	latency    time.Duration // added latency to every request
	hostErrors map[string]error // per-host errors
}

// NewNetworkPartition wraps an http.RoundTripper with fault injection.
// Pass nil to wrap http.DefaultTransport.
func NewNetworkPartition(inner http.RoundTripper) *NetworkPartition {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &NetworkPartition{
		inner:      inner,
		hostErrors: make(map[string]error),
	}
}

// SetBlocked enables or disables the full partition (all requests fail).
func (n *NetworkPartition) SetBlocked(blocked bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = blocked
}

// SetErrorRate sets the probability [0,1] of a random network error.
func (n *NetworkPartition) SetErrorRate(rate float64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.errRate = rate
}

// SetLatency sets a delay added before every request.
func (n *NetworkPartition) SetLatency(d time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.latency = d
}

// SetHostError makes requests to a specific host always fail with err.
func (n *NetworkPartition) SetHostError(host string, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hostErrors[host] = err
}

// ClearHostError removes a host-specific error.
func (n *NetworkPartition) ClearHostError(host string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.hostErrors, host)
}

// RoundTrip implements http.RoundTripper.
func (n *NetworkPartition) RoundTrip(req *http.Request) (*http.Response, error) {
	n.mu.RLock()
	blocked := n.blocked
	errRate := n.errRate
	latency := n.latency
	hostErr := n.hostErrors[req.URL.Host]
	n.mu.RUnlock()

	if blocked {
		return nil, fmt.Errorf("chaos: network partition — all traffic blocked")
	}
	if hostErr != nil {
		return nil, hostErr
	}
	if latency > 0 {
		time.Sleep(latency)
	}
	if errRate > 0 && rand.Float64() < errRate {
		return nil, fmt.Errorf("chaos: random network failure")
	}
	return n.inner.RoundTrip(req)
}
