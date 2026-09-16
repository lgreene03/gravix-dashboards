// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package spendcap enforces a customer-set hard monthly spend cap in front of
// the unmodified OSS ingestion service, for Gravix Cloud tenants on
// usage-based billing.
//
// docs/oss/01-competitive-thesis.md Axis 1 criticises exactly the failure this
// package prevents: an engineer adds a high-cardinality tag "temporarily",
// and nobody finds out until the invoice — with Grafana Cloud overage
// singled out because it is "not a cutoff, it is continuous metering at the
// same rate". Having published that, shipping unpredictable metered billing
// ourselves would collapse the thesis. So the cap here is HARD. At the limit
// Gravix stops accepting new data and says so with a 402; it does not keep
// accepting and keep charging.
//
// A self-hoster never runs this. There is no metered bill on a machine you own,
// so there is nothing to cap — which is why it is the one kind of thing that
// can live in ee/ without taking anything away from the free product.
//
// Two invariants hold everywhere in this package:
//
//   - A fact upstream ingestion has already accepted with a 2xx is never
//     deleted, never re-priced and never re-billed. The cap governs what is
//     accepted next, never what was accepted already.
//   - A rejection is never silence. Every refused request increments a counter
//     the customer can read, because "your data stopped arriving" discovered
//     from a dashboard gap is the same bad afternoon as a surprise invoice.
package spendcap

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Decision is the outcome of one Allow call.
type Decision struct {
	Allowed        bool
	CapCents       int64  // 0 when the tenant has no configured cap
	SpentCents     int64  // spend recorded for the current billing period before this request
	ProjectedCents int64  // SpentCents plus this request's projected cost
	Reason         string // non-empty when Allowed is false
}

// ReasonCapReached is the Reason on every refusal, and the "error" field of
// the gate's 402 body. It is one fixed string so that a customer's alerting
// can match on it.
const ReasonCapReached = "monthly spend cap reached"

// Enforcer decides whether a tenant's ingestion request may proceed, and
// records spend for requests the upstream ingestion service accepts.
type Enforcer struct {
	store         Store
	rateCentsPerM int64 // cost in cents per 1,000,000 events
	cacheTTL      time.Duration

	mu    sync.Mutex
	cache map[string]*cachedTenant
	now   func() time.Time // overridden in tests; nothing else reassigns it
}

// cachedTenant is one tenant's cap and period spend, plus what has been
// recorded since the last refresh.
//
// pendingCents exists so the cache does not lie between refreshes. Without it
// a tenant one request below their cap could send a thousand more inside one
// TTL and every one would be allowed against the same stale total — which is
// precisely the "the bill ran away before anybody noticed" failure this
// package is here to prevent.
type cachedTenant struct {
	capCents     int64
	spentCents   int64
	pendingCents int64
	refreshedAt  time.Time
}

// NewEnforcer constructs an Enforcer. rateCentsPerM must be > 0.
func NewEnforcer(store Store, rateCentsPerM int64, cacheTTL time.Duration) *Enforcer {
	return &Enforcer{
		store:         store,
		rateCentsPerM: rateCentsPerM,
		cacheTTL:      cacheTTL,
		cache:         map[string]*cachedTenant{},
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// ErrInvalidRate is returned by Allow and RecordAccepted when the enforcer was
// built with a non-positive rate. A zero rate would make every request free
// and the cap unreachable, which is a silently disabled cap — worse than a
// loud failure.
var ErrInvalidRate = errors.New("spendcap: rate must be greater than zero cents per million events")

// Allow reports whether tenantID may send eventCount more events.
//
// It performs no writes and never mutates recorded spend. A tenant with no
// configured cap (CapCents == 0) is always allowed: a cap is something a
// customer opts into, and defaulting to "refuse" would take a product that
// works and stop it.
func (e *Enforcer) Allow(ctx context.Context, tenantID string, eventCount int64) (Decision, error) {
	if e.rateCentsPerM <= 0 {
		return Decision{}, ErrInvalidRate
	}

	t, err := e.tenant(ctx, tenantID)
	if err != nil {
		return Decision{}, err
	}

	e.mu.Lock()
	capCents := t.capCents
	spent := t.spentCents + t.pendingCents
	e.mu.Unlock()

	projected := spent + PriceCents(eventCount, e.rateCentsPerM)

	d := Decision{
		Allowed:        true,
		CapCents:       capCents,
		SpentCents:     spent,
		ProjectedCents: projected,
	}
	if capCents > 0 && projected > capCents {
		d.Allowed = false
		d.Reason = ReasonCapReached
	}
	return d, nil
}

// RecordAccepted adds eventCount events, priced at rateCentsPerM, to
// tenantID's current-period spend.
//
// Call it only after upstream ingestion has returned a 2xx. A request the
// upstream refused produced no stored fact, so billing for it would be
// charging for nothing — and counting it against the cap would shorten the
// customer's month for data they never got.
func (e *Enforcer) RecordAccepted(ctx context.Context, tenantID string, eventCount int64) error {
	if e.rateCentsPerM <= 0 {
		return ErrInvalidRate
	}
	if eventCount <= 0 {
		return nil
	}

	delta := PriceCents(eventCount, e.rateCentsPerM)
	if err := e.store.AddSpend(ctx, tenantID, e.period(), delta); err != nil {
		return err
	}

	// Reflected in the cache immediately, so the next Allow within this TTL
	// decides against what has actually been sent rather than against the
	// figure at the last refresh.
	e.mu.Lock()
	if t, ok := e.cache[tenantID]; ok {
		t.pendingCents += delta
	}
	e.mu.Unlock()
	return nil
}

// RecordRejected notes that eventCount events were refused. It is the visible
// signal a cap leaves behind: a customer can ask "how much did I lose, and
// when did it start" and get a number rather than an absence.
func (e *Enforcer) RecordRejected(ctx context.Context, tenantID string, eventCount int64) error {
	if eventCount <= 0 {
		return nil
	}
	return e.store.AddRejection(ctx, tenantID, e.period(), eventCount)
}

// Period returns the billing period Allow and RecordAccepted are currently
// working in, as "2006-01".
func (e *Enforcer) Period() string { return e.period() }

// Status returns tenantID's cap and current-period totals, read through from
// the store rather than from the cache, for the admin API.
func (e *Enforcer) Status(ctx context.Context, tenantID string) (capCents, spentCents, rejectedEvents int64, err error) {
	capCents, err = e.store.GetCap(ctx, tenantID)
	if err != nil {
		return 0, 0, 0, err
	}
	spentCents, rejectedEvents, err = e.store.GetPeriod(ctx, tenantID, e.period())
	if err != nil {
		return 0, 0, 0, err
	}
	return capCents, spentCents, rejectedEvents, nil
}

// SetCap stores a new cap and drops the tenant's cache entry so the change is
// visible to the next Allow rather than up to a TTL later.
//
// §6 step 4 puts a bound of cacheTTL on how long a raise may take. Making it
// immediate satisfies that bound; the customer raising their cap is usually
// doing it because data is being refused right now.
func (e *Enforcer) SetCap(ctx context.Context, tenantID string, capCents int64) error {
	if capCents <= 0 {
		return fmt.Errorf("spendcap: cap must be positive, got %d", capCents)
	}
	if err := e.store.SetCap(ctx, tenantID, capCents); err != nil {
		return err
	}
	e.mu.Lock()
	delete(e.cache, tenantID)
	e.mu.Unlock()
	return nil
}

// tenant returns tenantID's cached cap and spend, refreshing from the store
// when the entry is absent or older than cacheTTL.
func (e *Enforcer) tenant(ctx context.Context, tenantID string) (*cachedTenant, error) {
	now := e.now()

	e.mu.Lock()
	t, ok := e.cache[tenantID]
	fresh := ok && now.Sub(t.refreshedAt) < e.cacheTTL
	e.mu.Unlock()

	if fresh {
		return t, nil
	}

	capCents, err := e.store.GetCap(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	spentCents, _, err := e.store.GetPeriod(ctx, tenantID, e.period())
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	// pendingCents resets here rather than carrying over: the freshly-read
	// spentCents already includes everything RecordAccepted wrote.
	e.cache[tenantID] = &cachedTenant{
		capCents:    capCents,
		spentCents:  spentCents,
		refreshedAt: now,
	}
	return e.cache[tenantID], nil
}

func (e *Enforcer) period() string { return e.now().Format("2006-01") }

// centsPerMillionDivisor is the event count one rateCentsPerM covers.
const centsPerMillionDivisor = 1_000_000

// PriceCents returns the cost in cents of eventCount events at rateCentsPerM,
// rounded UP to the nearest cent.
//
// Rounding up, not to nearest: a fraction of a cent rounded down means a
// tenant sending single facts is billed zero forever and the cap is never
// reached. The overcharge is at most one cent per request and the alternative
// is a cap that does not work.
func PriceCents(eventCount, rateCentsPerM int64) int64 {
	if eventCount <= 0 || rateCentsPerM <= 0 {
		return 0
	}
	product := eventCount * rateCentsPerM
	return (product + centsPerMillionDivisor - 1) / centsPerMillionDivisor
}
