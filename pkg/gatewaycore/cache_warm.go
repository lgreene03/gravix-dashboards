// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CacheWarmer periodically issues the queries the dashboard's default view
// makes, so a user's first request finds a warm Cube result cache instead of
// paying for the cold path (GRVX-1006 §5.2).
//
// A warm query is a cache hit only if Cube generates the same SQL for it as for
// the dashboard's own request. Cube keys its result cache by SQL, and the SQL
// depends on the measures and their order, the dimensions, the time dimension,
// the order, the filters, the limit and the tenant filter cube/cube.js adds from
// the token. So DefaultWarmQueries copies the dashboard's queries exactly, and
// each tenant is warmed with a token for that tenant. A query that differs in
// any of those warms nothing, quietly; TestWarmQueriesMatchTheDashboard is the
// guard.
//
// Warming must never delay a user query. Cube runs two queries at once on both
// engines Gravix ships (its default queue concurrency), and the warmer keeps at
// most one in flight, so a user query always has a slot. A cycle that reaches
// MaxDuration is abandoned rather than continued or queued behind the next one.
//
// Abandoning means issuing no further query. The query already in flight is
// allowed to finish: Cube keeps running a query whose HTTP request has been
// cancelled, so cancelling it would not stop it, only hide it, and the next
// cycle would then put a second warm query in Cube beside the first.
type CacheWarmer struct {
	cfg     WarmerConfig
	cubeURL string
	client  *http.Client
	tenants func(ctx context.Context) ([]string, error)
	token   func(tenantID string) (string, error)
	now     func() time.Time

	mu    sync.Mutex
	stats WarmStats
	// next is where the next cycle starts in the sorted tenant list. An
	// abandoned cycle leaves it at the tenant it stopped on, so with more
	// tenants than one cycle can reach, every tenant is still reached in turn
	// instead of the first few every time.
	next int
}

// WarmQuery is one Cube REST query, sent as {"query": Query(now)}. It is built
// at the moment it is sent because the dashboard's default view is relative to
// today: its date range moves at midnight UTC, and with it the SQL and the
// cache entry.
type WarmQuery struct {
	Name  string
	Query func(now time.Time) map[string]any
}

// WarmerConfig bounds the warming.
type WarmerConfig struct {
	// Interval between cycle starts. Cube already answers a query it has seen
	// from its cache, refreshing in the background when a rollup writes, so a
	// query is cold only if nobody has asked it since Cube started or since the
	// default view's date range moved at midnight UTC. The interval bounds how
	// long either lasts. A cycle of cache hits costs four cached answers per
	// tenant (SD-058).
	Interval time.Duration
	// Queries are the default dashboard view's queries.
	Queries []WarmQuery
	// MaxDuration abandons a cycle that runs longer than this.
	MaxDuration time.Duration
	// Enabled turns warming off entirely when false.
	Enabled bool
}

// WarmStats reports the last completed or abandoned cycle.
type WarmStats struct {
	Cycles        int           // cycles started
	Abandoned     int           // cycles abandoned at MaxDuration
	LastStarted   time.Time     // start of the last cycle
	LastDuration  time.Duration // how long the last cycle ran
	LastQueries   int           // queries the last cycle completed
	LastErrors    int           // queries the last cycle saw fail
	LastAbandoned bool          // whether the last cycle was abandoned
}

const (
	defaultWarmInterval    = 30 * time.Second
	defaultWarmMaxDuration = 60 * time.Second
	// Cube answers "Continue wait" when a query outlives its long-poll window.
	// The query keeps running in Cube; asking again waits for the same result.
	cubeContinueWait = "Continue wait"
	// warmQueryCeiling bounds one query, in-flight polls included. It is Cube's
	// own default query timeout, so a query is only given up on once Cube
	// itself would have given up on it.
	warmQueryCeiling = 10 * time.Minute
)

// errCubeNoData is Cube's 400, which on a stack that has not rolled anything
// up yet means the warehouse has no table to read. The dashboard shows that as
// "no data yet", and the warmer does not log it as a failure every interval.
var errCubeNoData = errors.New("cube answered 400")

// DefaultWarmQueries returns the Cube queries dashboards/app.js sends when the
// overview first opens: the service list, the two hourly series Cube answers
// (the latency series goes to the gateway's percentile endpoint instead,
// GRVX-808), and the endpoints table.
//
// The overview opens with the last seven days selected. The page sets its "from"
// input to seven days ago and its "to" input to today, and a date input reports
// the UTC date. The hourly series send that range as Cube's dateRange, and the
// endpoints table sends it as two bucketStart filters, ending at T23:59:59.
func DefaultWarmQueries() []WarmQuery {
	days := func(now time.Time) (string, string) {
		now = now.UTC()
		return now.AddDate(0, 0, -7).Format("2006-01-02"), now.Format("2006-01-02")
	}
	hourly := func(measure string) func(time.Time) map[string]any {
		return func(now time.Time) map[string]any {
			from, to := days(now)
			return map[string]any{
				"measures": []string{measure},
				"timeDimensions": []map[string]any{{
					"dimension":   "RequestMetricsMinute.bucketStart",
					"granularity": "hour",
					"dateRange":   []string{from, to},
				}},
				"order":   map[string]string{"RequestMetricsMinute.bucketStart": "asc"},
				"filters": []any{},
			}
		}
	}
	return []WarmQuery{
		{Name: "services", Query: func(time.Time) map[string]any {
			return map[string]any{
				"dimensions": []string{"RequestMetricsMinute.service"},
				"order":      map[string]string{"RequestMetricsMinute.service": "asc"},
			}
		}},
		{Name: "error_rate_hourly", Query: hourly("RequestMetricsMinute.errorRate")},
		{Name: "request_count_hourly", Query: hourly("RequestMetricsMinute.requestCount")},
		{Name: "endpoints", Query: func(now time.Time) map[string]any {
			from, to := days(now)
			return map[string]any{
				"measures": []string{
					"RequestMetricsMinute.requestCount",
					"RequestMetricsMinute.errorCount",
					"RequestMetricsMinute.errorRate",
				},
				"dimensions": []string{"RequestMetricsMinute.pathTemplate", "RequestMetricsMinute.method"},
				"order":      map[string]string{"RequestMetricsMinute.errorCount": "desc"},
				"filters": []map[string]any{
					{"member": "RequestMetricsMinute.bucketStart", "operator": "gte", "values": []string{from + "T00:00:00"}},
					{"member": "RequestMetricsMinute.bucketStart", "operator": "lte", "values": []string{to + "T23:59:59"}},
				},
				"limit": 10,
			}
		}},
	}
}

// warmerConfigFromEnv reads CACHE_WARM_ENABLED, CACHE_WARM_INTERVAL and
// CACHE_WARM_MAX_DURATION. A value that does not parse is refused with the
// variable named, because a warmer silently running on a default the operator
// tried to change is worse than one that does not start.
func warmerConfigFromEnv(getenv func(string) string) (WarmerConfig, error) {
	cfg := WarmerConfig{
		Interval:    defaultWarmInterval,
		MaxDuration: defaultWarmMaxDuration,
		Queries:     DefaultWarmQueries(),
		Enabled:     true,
	}
	if v := strings.TrimSpace(getenv("CACHE_WARM_ENABLED")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return cfg, fmt.Errorf("CACHE_WARM_ENABLED=%q: want true or false", v)
		}
		cfg.Enabled = b
	}
	for _, d := range []struct {
		name string
		dst  *time.Duration
	}{
		{"CACHE_WARM_INTERVAL", &cfg.Interval},
		{"CACHE_WARM_MAX_DURATION", &cfg.MaxDuration},
	} {
		v := strings.TrimSpace(getenv(d.name))
		if v == "" {
			continue
		}
		parsed, err := time.ParseDuration(v)
		if err != nil || parsed <= 0 {
			return cfg, fmt.Errorf("%s=%q: want a positive duration such as 30s", d.name, v)
		}
		*d.dst = parsed
	}
	return cfg, nil
}

// NewCacheWarmer builds a warmer that posts to cubeURL. tenants lists the
// tenants to warm, and token mints a Cube token for one of them.
func NewCacheWarmer(cfg WarmerConfig, cubeURL string, tenants func(context.Context) ([]string, error), token func(string) (string, error)) *CacheWarmer {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultWarmInterval
	}
	if cfg.MaxDuration <= 0 {
		cfg.MaxDuration = defaultWarmMaxDuration
	}
	return &CacheWarmer{
		cfg:     cfg,
		cubeURL: cubeURL,
		client:  &http.Client{},
		tenants: tenants,
		token:   token,
		now:     time.Now,
	}
}

// Start begins warming until ctx ends, then returns ctx's error. A disabled
// warmer returns nil at once.
func (w *CacheWarmer) Start(ctx context.Context) error {
	if !w.cfg.Enabled || len(w.cfg.Queries) == 0 {
		return nil
	}
	// A ticker drops ticks its reader misses, so a cycle that overruns the
	// interval is followed by one cycle, not by a backlog of them.
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		w.cycle(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Stats reports the last cycle.
func (w *CacheWarmer) Stats() WarmStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

// cycle warms every query for every tenant, one query at a time, and stops at
// MaxDuration. It starts at the tenant the last abandoned cycle stopped on.
func (w *CacheWarmer) cycle(ctx context.Context) {
	start := time.Now()
	deadline := start.Add(w.cfg.MaxDuration)

	w.mu.Lock()
	w.stats.Cycles++
	w.stats.LastStarted = start
	w.mu.Unlock()

	done, failed := 0, 0
	var firstErr error
	onlyNoData := true
	abandoned := false

	tenants, err := w.tenants(ctx)
	if err != nil {
		failed++
		onlyNoData = false
		firstErr = fmt.Errorf("list tenants: %w", err)
	}

	w.mu.Lock()
	first := 0
	if len(tenants) > 0 {
		first = w.next % len(tenants)
	}
	w.mu.Unlock()
	stoppedAt := first

warm:
	for i := range tenants {
		pos := (first + i) % len(tenants)
		tenantID := tenants[pos]
		stoppedAt = pos
		tok, err := w.token(tenantID)
		if err != nil {
			failed++
			onlyNoData = false
			if firstErr == nil {
				firstErr = fmt.Errorf("token for tenant %s: %w", tenantID, err)
			}
			continue
		}
		for _, q := range w.cfg.Queries {
			// Checked before a query, never during one: see CacheWarmer.
			if time.Now().After(deadline) {
				abandoned = true
				break warm
			}
			qctx, cancel := context.WithTimeout(ctx, warmQueryCeiling)
			err := w.warmOne(qctx, tok, q)
			cancel()
			if ctx.Err() != nil {
				break warm // shutting down
			}
			if err != nil {
				failed++
				if !errors.Is(err, errCubeNoData) {
					onlyNoData = false
				}
				if firstErr == nil || (!errors.Is(err, errCubeNoData) && errors.Is(firstErr, errCubeNoData)) {
					firstErr = fmt.Errorf("tenant %s, query %s: %w", tenantID, q.Name, err)
				}
				continue
			}
			done++
		}
	}

	elapsed := time.Since(start)
	w.mu.Lock()
	w.stats.LastDuration = elapsed
	w.stats.LastQueries = done
	w.stats.LastErrors = failed
	w.stats.LastAbandoned = abandoned
	if abandoned {
		w.stats.Abandoned++
		w.next = stoppedAt
	}
	w.mu.Unlock()

	if abandoned {
		slog.Warn(fmt.Sprintf("cache warm cycle abandoned after %s", w.cfg.MaxDuration),
			"queries_completed", done)
	}
	switch {
	case firstErr == nil:
	case onlyNoData:
		slog.Debug("cache warm cycle found no data to warm", "errors", failed, "first", firstErr.Error())
	default:
		// Once per cycle, not once per query: an unreachable Cube would
		// otherwise log a line per tenant per query every interval.
		slog.Warn("cache warm cycle saw errors", "errors", failed, "first", firstErr.Error())
	}
}

// warmOne sends one query and waits for its result, asking again while Cube
// answers "Continue wait", so the warmer never has two queries in Cube at once.
func (w *CacheWarmer) warmOne(ctx context.Context, token string, q WarmQuery) error {
	body, err := json.Marshal(map[string]any{"query": q.Query(w.now())})
	if err != nil {
		return err
	}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cubeURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := w.client.Do(req)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		var res struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &res)
		if res.Error == cubeContinueWait {
			continue
		}
		if resp.StatusCode == http.StatusBadRequest {
			return fmt.Errorf("%w: %s", errCubeNoData, res.Error)
		}
		if resp.StatusCode != http.StatusOK {
			if res.Error != "" {
				return fmt.Errorf("cube answered %d: %s", resp.StatusCode, res.Error)
			}
			return fmt.Errorf("cube answered %d", resp.StatusCode)
		}
		if res.Error != "" {
			return errors.New(res.Error)
		}
		return nil
	}
}

// startCacheWarmer wires the warmer into the gateway: every active tenant in
// the tenant database is warmed with a token the gateway signs, the same token
// shape the alert evaluator already sends Cube.
func (gw *gateway) startCacheWarmer(ctx context.Context) {
	cfg, err := warmerConfigFromEnv(os.Getenv)
	if err != nil {
		slog.Error("cache warmer not started", "error", err)
		return
	}
	if !cfg.Enabled {
		slog.Info("cache warmer disabled by CACHE_WARM_ENABLED")
		return
	}
	tenants := func(ctx context.Context) ([]string, error) {
		all, err := gw.db.Tenants().List(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(all))
		for _, t := range all {
			if t.Status == "" || t.Status == "active" {
				ids = append(ids, t.ID)
			}
		}
		sort.Strings(ids)
		return ids, nil
	}
	token := func(tenantID string) (string, error) {
		return gw.tokens.Generate(tenantID, "cache-warmer", "cache-warmer@system", "viewer")
	}
	w := NewCacheWarmer(cfg, gw.cubeAPIURL, tenants, token)
	slog.Info("cache warmer started", "interval", cfg.Interval, "max_duration", cfg.MaxDuration,
		"queries", len(cfg.Queries))
	go func() { _ = w.Start(ctx) }()
}
