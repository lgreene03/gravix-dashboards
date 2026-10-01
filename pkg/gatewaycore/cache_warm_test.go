// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCube is a Cube REST endpoint with a fixed number of query slots, the way
// Cube's queue runs two queries at once on both engines Gravix ships. A request
// that finds every slot taken waits, and that wait is what a user would feel.
type fakeCube struct {
	slots   chan struct{}
	service time.Duration

	mu          sync.Mutex
	warmBusy    int
	maxWarmBusy int
	userWaits   []time.Duration
	auths       []string
	queries     []string
	// continueWait answers "Continue wait" this many times before a result.
	continueWait int32
	requests     int32
}

func newFakeCube(slots int, service time.Duration) *fakeCube {
	return &fakeCube{slots: make(chan struct{}, slots), service: service}
}

func (f *fakeCube) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&f.requests, 1)
	body, _ := io.ReadAll(r.Body)
	auth := r.Header.Get("Authorization")
	warm := strings.HasPrefix(auth, "Bearer warm-")

	f.mu.Lock()
	f.auths = append(f.auths, auth)
	f.queries = append(f.queries, string(body))
	if warm {
		f.warmBusy++
		if f.warmBusy > f.maxWarmBusy {
			f.maxWarmBusy = f.warmBusy
		}
	}
	f.mu.Unlock()
	// The query is over before its answer is written, as in Cube; counting it
	// as busy until after the write would see the next request first.
	finished := func() {
		if warm {
			f.mu.Lock()
			f.warmBusy--
			f.mu.Unlock()
		}
	}

	if atomic.AddInt32(&f.continueWait, -1) >= 0 {
		finished()
		_, _ = w.Write([]byte(`{"error":"Continue wait"}`))
		return
	}

	// A free slot is taken at once and counts as no wait at all, so the
	// measurement is of blocking, not of scheduler noise.
	var waited time.Duration
	select {
	case f.slots <- struct{}{}:
	default:
		start := time.Now()
		select {
		case f.slots <- struct{}{}:
		case <-r.Context().Done():
			finished()
			return
		}
		waited = time.Since(start)
	}
	if !warm {
		f.mu.Lock()
		f.userWaits = append(f.userWaits, waited)
		f.mu.Unlock()
	}
	// Like Cube, a query runs to the end even if its caller has gone.
	time.Sleep(f.service)
	<-f.slots
	finished()
	_, _ = w.Write([]byte(`{"data":[]}`))
}

func staticTenants(ids ...string) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) { return ids, nil }
}

func warmToken(id string) (string, error) { return "warm-" + id, nil }

func p95(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[(len(s)*95+99)/100-1]
}

// captureLogs routes slog to a buffer for the duration of a test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	var mu sync.Mutex
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// -------------------------------------------------------------------- AC-6 --

// TestWarmingDoesNotStarveUsers — a user query never waits for a slot because
// of the warmer. Cube runs two queries at once; the warmer holds at most one,
// so one is always free for the user. The wait is measured inside the fake
// Cube, at the slot, which is where a starved query would lose its time.
func TestWarmingDoesNotStarveUsers(t *testing.T) {
	cube := newFakeCube(2, 5*time.Millisecond)
	srv := httptest.NewServer(cube)
	defer srv.Close()

	userQuery := func() {
		req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"query":{}}`))
		req.Header.Set("Authorization", "Bearer user")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("user query: %v", err)
		}
		resp.Body.Close()
	}

	// Baseline: users alone.
	for i := 0; i < 40; i++ {
		userQuery()
	}
	cube.mu.Lock()
	before := p95(cube.userWaits)
	cube.userWaits = nil
	cube.mu.Unlock()

	// The warmer running flat out: a one-millisecond interval and eight tenants,
	// so it always has a query in Cube.
	w := NewCacheWarmer(WarmerConfig{
		Interval: time.Millisecond, MaxDuration: time.Minute,
		Queries: DefaultWarmQueries(), Enabled: true,
	}, srv.URL, staticTenants("a", "b", "c", "d", "e", "f", "g", "h"), warmToken)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = w.Start(ctx); close(done) }()

	// Let the warmer get going, then measure users beside it.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&cube.requests) < 45 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 40; i++ {
		userQuery()
	}
	cancel()
	<-done

	cube.mu.Lock()
	after := p95(cube.userWaits)
	maxWarm := cube.maxWarmBusy
	cube.mu.Unlock()

	if maxWarm > 1 {
		t.Errorf("the warmer had %d queries in Cube at once; it must never hold more than one slot", maxWarm)
	}
	if increase := after - before; increase > 0 {
		t.Errorf("warming increased user query p95 by %dms (slot wait p95 %s alone, %s beside the warmer)",
			increase.Milliseconds(), before, after)
	}
}

// -------------------------------------------------------------------- AC-7 --

// TestWarmCycleAbandonedOnTimeout — a cycle that reaches MaxDuration issues no
// further query, logs §6.1's message once, and leaves nothing running. The
// query in flight at the limit finishes, because Cube would run it anyway.
func TestWarmCycleAbandonedOnTimeout(t *testing.T) {
	logs := captureLogs(t)
	cube := newFakeCube(2, 40*time.Millisecond)
	srv := httptest.NewServer(cube)
	defer srv.Close()

	max := 100 * time.Millisecond
	w := NewCacheWarmer(WarmerConfig{
		Interval: time.Hour, MaxDuration: max,
		Queries: DefaultWarmQueries(), Enabled: true,
	}, srv.URL, staticTenants("a", "b", "c"), warmToken)

	start := time.Now()
	w.cycle(context.Background())
	elapsed := time.Since(start)

	st := w.Stats()
	if !st.LastAbandoned || st.Abandoned != 1 {
		t.Fatalf("stats = %+v, want the cycle recorded as abandoned", st)
	}
	// Twelve queries at 40ms is 480ms of work. Abandoning means stopping at
	// the first query boundary after the limit, not finishing late.
	if elapsed > max+40*time.Millisecond+60*time.Millisecond {
		t.Errorf("the cycle ran %s against a %s limit and 40ms queries; an abandoned cycle must stop, not finish", elapsed, max)
	}
	if st.LastQueries >= 12 {
		t.Errorf("the cycle completed %d of 12 queries; it was not abandoned", st.LastQueries)
	}

	want := fmt.Sprintf("cache warm cycle abandoned after %s", max)
	if got := strings.Count(logs.String(), want); got != 1 {
		t.Errorf("log has %d copies of %q, want exactly 1:\n%s", got, want, logs.String())
	}

	// Nothing of the abandoned cycle is left running.
	time.Sleep(60 * time.Millisecond)
	n := atomic.LoadInt32(&cube.requests)
	time.Sleep(100 * time.Millisecond)
	if after := atomic.LoadInt32(&cube.requests); after != n {
		t.Errorf("Cube received %d more requests after the cycle was abandoned", after-n)
	}
}

// TestAbandonedCycleResumesWhereItStopped — with more tenants than one cycle
// can reach, the next cycle starts at the tenant the last one stopped on, so
// the tenants late in the list are warmed too.
func TestAbandonedCycleResumesWhereItStopped(t *testing.T) {
	cube := newFakeCube(2, 30*time.Millisecond)
	srv := httptest.NewServer(cube)
	defer srv.Close()

	// Four queries at 30ms is 120ms per tenant; a 150ms limit reaches about one.
	w := NewCacheWarmer(WarmerConfig{Interval: time.Hour, MaxDuration: 150 * time.Millisecond,
		Queries: DefaultWarmQueries(), Enabled: true}, srv.URL, staticTenants("a", "b", "c", "d"), warmToken)

	for i := 0; i < 6; i++ {
		w.cycle(context.Background())
	}

	seen := map[string]bool{}
	cube.mu.Lock()
	for _, a := range cube.auths {
		seen[strings.TrimPrefix(a, "Bearer warm-")] = true
	}
	cube.mu.Unlock()
	for _, id := range []string{"a", "b", "c", "d"} {
		if !seen[id] {
			t.Errorf("tenant %s was never warmed in six abandoned cycles; each cycle restarts at the top of the list", id)
		}
	}
}

// TestOverrunningCyclesDoNotQueue — when every cycle overruns the interval, the
// warmer runs one after another, never two at once and never a backlog.
func TestOverrunningCyclesDoNotQueue(t *testing.T) {
	cube := newFakeCube(2, 20*time.Millisecond)
	srv := httptest.NewServer(cube)
	defer srv.Close()

	w := NewCacheWarmer(WarmerConfig{
		Interval: 5 * time.Millisecond, MaxDuration: 50 * time.Millisecond,
		Queries: DefaultWarmQueries(), Enabled: true,
	}, srv.URL, staticTenants("a", "b"), warmToken)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = w.Start(ctx)

	st := w.Stats()
	cube.mu.Lock()
	maxWarm := cube.maxWarmBusy
	cube.mu.Unlock()
	if maxWarm > 1 {
		t.Errorf("%d warm queries in Cube at once; overrunning cycles overlapped", maxWarm)
	}
	// 500ms of 50ms cycles is at most about ten. A queue of the ticks missed
	// would not raise this, but overlapping cycles would.
	if st.Cycles > 12 {
		t.Errorf("%d cycles in 500ms with a 50ms limit; cycles are piling up", st.Cycles)
	}
	if st.Abandoned == 0 {
		t.Errorf("no cycle was abandoned, so this test did not exercise overrun")
	}
}

// ------------------------------------------------------------ behaviour --

// TestWarmerSendsEachTenantItsOwnToken — Cube adds the tenant filter from the
// token, so a tenant's dashboard is warm only if it was warmed with that
// tenant's token.
func TestWarmerSendsEachTenantItsOwnToken(t *testing.T) {
	cube := newFakeCube(2, 0)
	srv := httptest.NewServer(cube)
	defer srv.Close()

	w := NewCacheWarmer(WarmerConfig{Interval: time.Hour, MaxDuration: time.Minute,
		Queries: DefaultWarmQueries(), Enabled: true}, srv.URL, staticTenants("t1", "t2"), warmToken)
	w.cycle(context.Background())

	counts := map[string]int{}
	cube.mu.Lock()
	for _, a := range cube.auths {
		counts[a]++
	}
	cube.mu.Unlock()
	n := len(DefaultWarmQueries())
	for _, id := range []string{"t1", "t2"} {
		if got := counts["Bearer warm-"+id]; got != n {
			t.Errorf("tenant %s: %d queries with its token, want %d", id, got, n)
		}
	}
	if st := w.Stats(); st.LastQueries != 2*n || st.LastErrors != 0 {
		t.Errorf("stats = %+v, want %d queries and no errors", st, 2*n)
	}
}

// TestWarmerWaitsThroughContinueWait — Cube answers "Continue wait" when a
// query outlives its long poll, and the query is still running. Moving on
// would put a second warm query in Cube beside it.
func TestWarmerWaitsThroughContinueWait(t *testing.T) {
	cube := newFakeCube(2, 0)
	cube.continueWait = 2
	srv := httptest.NewServer(cube)
	defer srv.Close()

	one := DefaultWarmQueries()[:1]
	w := NewCacheWarmer(WarmerConfig{Interval: time.Hour, MaxDuration: time.Minute,
		Queries: one, Enabled: true}, srv.URL, staticTenants("t1"), warmToken)
	w.cycle(context.Background())

	if got := atomic.LoadInt32(&cube.requests); got != 3 {
		t.Errorf("Cube saw %d requests for one query answered \"Continue wait\" twice, want 3", got)
	}
	if st := w.Stats(); st.LastQueries != 1 || st.LastErrors != 0 {
		t.Errorf("stats = %+v, want one completed query", st)
	}
}

// TestWarmerCountsCubeErrors — a Cube that refuses is counted and logged once
// per cycle, and the cycle carries on with the next query.
func TestWarmerCountsCubeErrors(t *testing.T) {
	logs := captureLogs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Invalid or expired token"}`))
	}))
	defer srv.Close()

	w := NewCacheWarmer(WarmerConfig{Interval: time.Hour, MaxDuration: time.Minute,
		Queries: DefaultWarmQueries(), Enabled: true}, srv.URL, staticTenants("t1"), warmToken)
	w.cycle(context.Background())

	st := w.Stats()
	if st.LastErrors != len(DefaultWarmQueries()) || st.LastQueries != 0 || st.LastAbandoned {
		t.Errorf("stats = %+v, want every query counted as an error and the cycle not abandoned", st)
	}
	if got := strings.Count(logs.String(), "cache warm cycle saw errors"); got != 1 {
		t.Errorf("logged the errors %d times, want once per cycle", got)
	}
	if !strings.Contains(logs.String(), "Invalid or expired token") {
		t.Errorf("the log does not carry Cube's reason:\n%s", logs.String())
	}
}

// TestWarmerIsQuietBeforeTheFirstRollup — before any rollup, Cube answers 400
// because there is no table to read. That is "no data yet", not a fault, and a
// warning every 30 seconds on every fresh install would teach people to ignore
// the warmer's warnings.
func TestWarmerIsQuietBeforeTheFirstRollup(t *testing.T) {
	logs := captureLogs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"IO Error: No files found that match the pattern"}`))
	}))
	defer srv.Close()

	w := NewCacheWarmer(WarmerConfig{Interval: time.Hour, MaxDuration: time.Minute,
		Queries: DefaultWarmQueries(), Enabled: true}, srv.URL, staticTenants("t1"), warmToken)
	w.cycle(context.Background())

	if st := w.Stats(); st.LastErrors != len(DefaultWarmQueries()) {
		t.Errorf("stats = %+v, want each 400 still counted", st)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("a stack with no data yet logged a warning:\n%s", logs.String())
	}
}

// TestDisabledWarmerReturnsAtOnce — CACHE_WARM_ENABLED=false sends nothing.
func TestDisabledWarmerReturnsAtOnce(t *testing.T) {
	cube := newFakeCube(2, 0)
	srv := httptest.NewServer(cube)
	defer srv.Close()

	w := NewCacheWarmer(WarmerConfig{Interval: time.Millisecond, MaxDuration: time.Minute,
		Queries: DefaultWarmQueries(), Enabled: false}, srv.URL, staticTenants("t1"), warmToken)
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start on a disabled warmer: %v", err)
	}
	if got := atomic.LoadInt32(&cube.requests); got != 0 {
		t.Errorf("a disabled warmer sent %d requests", got)
	}
}

func TestWarmerConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	cfg, err := warmerConfigFromEnv(env(nil))
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if !cfg.Enabled || cfg.Interval != 30*time.Second || cfg.MaxDuration != time.Minute || len(cfg.Queries) != 4 {
		t.Errorf("defaults = %+v", cfg)
	}

	cfg, err = warmerConfigFromEnv(env(map[string]string{
		"CACHE_WARM_ENABLED": "false", "CACHE_WARM_INTERVAL": "90s", "CACHE_WARM_MAX_DURATION": "2m",
	}))
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if cfg.Enabled || cfg.Interval != 90*time.Second || cfg.MaxDuration != 2*time.Minute {
		t.Errorf("overrides = %+v", cfg)
	}

	for k, v := range map[string]string{
		"CACHE_WARM_ENABLED":      "sometimes",
		"CACHE_WARM_INTERVAL":     "4",
		"CACHE_WARM_MAX_DURATION": "-1s",
	} {
		_, err := warmerConfigFromEnv(env(map[string]string{k: v}))
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%q: err = %v, want a refusal naming the variable", k, v, err)
		}
	}
}

// ------------------------------------------------------------- the guard --

// TestWarmQueriesMatchTheDashboard — a warm query that differs from the
// dashboard's in any way Cube puts into SQL warms nothing, and nothing else
// would notice. This reads dashboards/app.js and requires, whitespace aside,
// every line that decides what the overview sends when it first opens, and
// pins the warm queries for one fixed day.
func TestWarmQueriesMatchTheDashboard(t *testing.T) {
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join("..", "..", "dashboards", name))
		if err != nil {
			t.Fatalf("reading the dashboard: %v", err)
		}
		return regexp.MustCompile(`\s+`).ReplaceAllString(string(raw), "")
	}
	app, html := read("app.js"), read("index.html")

	// The default view: the last seven days, no service, no comparison.
	for _, s := range []string{
		`initSevenDaysAgo.setDate(initSevenDaysAgo.getDate()-7);document.getElementById('dateFrom').valueAsDate=initSevenDaysAgo;document.getElementById('dateTo').valueAsDate=newDate();`,
		`constfilters=[];if(service){`,
		`if(dateFrom&&dateTo&&dateFrom!==dateTo){filters.push({member:"RequestMetricsMinute.bucketStart",operator:"gte",values:[dateFrom+"T00:00:00"]});filters.push({member:"RequestMetricsMinute.bucketStart",operator:"lte",values:[dateTo+"T23:59:59"]});`,
		`fetchCubeData(["RequestMetricsMinute.errorRate"],filters,compare||null)`,
		`fetchCubeData(["RequestMetricsMinute.requestCount"],filters,compare||null)`,
		`fetchEndPointsData(filters)`,
		// fetchCubeData moves the two date filters into the time dimension.
		`if(f.member==="RequestMetricsMinute.bucketStart"){if(f.operator==="gte")dateFrom=f.values[0];elseif(f.operator==="lte")dateTo=f.values[0];}else{otherFilters.push(f);}`,
		`consttimeDim={dimension:"RequestMetricsMinute.bucketStart",granularity:"hour"};`,
		`timeDim.dateRange=[dateFrom.slice(0,10),(dateTo||dateFrom).slice(0,10)];`,
		`measures:measures,timeDimensions:[timeDim],order:{"RequestMetricsMinute.bucketStart":"asc"},filters:otherFilters`,
		// fetchEndPointsData sends the filters as they are.
		`measures:["RequestMetricsMinute.requestCount","RequestMetricsMinute.errorCount","RequestMetricsMinute.errorRate"],dimensions:["RequestMetricsMinute.pathTemplate","RequestMetricsMinute.method"],order:{"RequestMetricsMinute.errorCount":"desc"},filters:filters,limit:10`,
		`query:{dimensions:["RequestMetricsMinute.service"],order:{"RequestMetricsMinute.service":"asc"}}`,
	} {
		if !strings.Contains(app, s) {
			t.Errorf("dashboards/app.js no longer contains\n  %s\nso the warm queries may no longer be what the overview sends. "+
				"Update DefaultWarmQueries to match the dashboard, then this test.", s)
		}
	}
	if !strings.Contains(html, `<optionvalue="">NoComparison</option>`) {
		t.Errorf("the comparison selector no longer defaults to none")
	}

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	want := map[string]string{
		"services":             `{"dimensions":["RequestMetricsMinute.service"],"order":{"RequestMetricsMinute.service":"asc"}}`,
		"error_rate_hourly":    `{"filters":[],"measures":["RequestMetricsMinute.errorRate"],"order":{"RequestMetricsMinute.bucketStart":"asc"},"timeDimensions":[{"dateRange":["2026-09-24","2026-10-01"],"dimension":"RequestMetricsMinute.bucketStart","granularity":"hour"}]}`,
		"request_count_hourly": `{"filters":[],"measures":["RequestMetricsMinute.requestCount"],"order":{"RequestMetricsMinute.bucketStart":"asc"},"timeDimensions":[{"dateRange":["2026-09-24","2026-10-01"],"dimension":"RequestMetricsMinute.bucketStart","granularity":"hour"}]}`,
		"endpoints":            `{"dimensions":["RequestMetricsMinute.pathTemplate","RequestMetricsMinute.method"],"filters":[{"member":"RequestMetricsMinute.bucketStart","operator":"gte","values":["2026-09-24T00:00:00"]},{"member":"RequestMetricsMinute.bucketStart","operator":"lte","values":["2026-10-01T23:59:59"]}],"limit":10,"measures":["RequestMetricsMinute.requestCount","RequestMetricsMinute.errorCount","RequestMetricsMinute.errorRate"],"order":{"RequestMetricsMinute.errorCount":"desc"}}`,
	}
	qs := DefaultWarmQueries()
	if len(qs) != len(want) {
		t.Errorf("%d warm queries, %d pinned here; pin every one", len(qs), len(want))
	}
	for _, q := range qs {
		b, err := json.Marshal(q.Query(now))
		if err != nil {
			t.Fatalf("%s: %v", q.Name, err)
		}
		if string(b) != want[q.Name] {
			t.Errorf("%s on %s:\n got  %s\n want %s", q.Name, now.Format("2006-01-02"), b, want[q.Name])
		}
	}
}

// TestWarmQueriesFollowTheUTCDay — the range is the browser's date input, which
// reports UTC, so a warmer in another time zone must not shift it.
func TestWarmQueriesFollowTheUTCDay(t *testing.T) {
	east := time.FixedZone("UTC+10", 10*3600)
	// 01:00 on 2 October in UTC+10 is still 1 October in UTC.
	now := time.Date(2026, 10, 2, 1, 0, 0, 0, east)
	for _, q := range DefaultWarmQueries() {
		b, _ := json.Marshal(q.Query(now))
		if strings.Contains(string(b), "2026-10-02") {
			t.Errorf("%s used the local date: %s", q.Name, b)
		}
	}
}
