// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/export"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// DD-041. Where an export goes is the server's choice, never the caller's.
//
// GRVX-1107 §5.4's body carried a destination, and any role may export, so a
// viewer could have had the gateway write a file:// path on its own host or an
// s3:// bucket with the gateway's credentials. Scheduled exports stored a
// customer's s3:// URL and were never run (F-054); running them as stored would
// have written to whatever bucket an admin named, with Gravix's credentials.
//
// So an on-demand export comes back in the response, and a scheduled one is
// written under the tenant's own exports/ prefix in Gravix's store, where the
// tenant lists and downloads it. A tenant whose storage is its own bucket
// (ee/tenancy/byob) gets its exports there by the same rule.

// scheduledExportPrefix is where every run of one schedule is written.
func scheduledExportPrefix(tenantID, scheduleID string) string {
	return path.Join("exports", tenantID, "scheduled", scheduleID)
}

// runLayout names a run by the minute it was due, so two gateways that run
// the same schedule write the same files to the same place.
const runLayout = "20060102T1504Z"

var (
	runNameRe  = regexp.MustCompile(`^[0-9]{8}T[0-9]{4}Z$`)
	runFileRe  = regexp.MustCompile(`^(?:(?:facts|metrics|events)_[0-9]{8}\.(?:parquet|csv|jsonl)(?:\.gz)?|manifest\.json)$`)
	scheduleTZ = time.UTC
)

// ─── on demand ───────────────────────────────────────────────────────────────

// handleOnDemandExport handles POST /api/gateway/exports (GRVX-1107 §5.4).
// Any authenticated role may export, on any plan. The export is the response
// body, streamed one day's partition at a time.
func (gw *gateway) handleOnDemandExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if gw.metricStore == nil {
		writeError(w, http.StatusServiceUnavailable, "export storage not configured")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())

	// The body is read as a set of named fields first, so a field that is
	// present is refused even when its value is null, and an unknown one is
	// refused by name: a filter or a query is told it is not accepted rather
	// than silently ignored into a whole-range export (AC-11).
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if _, ok := fields["destination"]; ok {
		writeError(w, http.StatusBadRequest, `invalid field "destination": the server chooses where an export goes, and this endpoint returns it in the response`)
		return
	}
	for name := range fields {
		switch name {
		case "dataset", "format", "from", "to", "compress":
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid field %q: not accepted; an export takes dataset, format, from, to and compress", name))
			return
		}
	}
	var body struct {
		Dataset  string `json:"dataset"`
		Format   string `json:"format"`
		From     string `json:"from"`
		To       string `json:"to"`
		Compress bool   `json:"compress"`
	}
	raw, _ := json.Marshal(fields)
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Format == "" {
		body.Format = string(export.FormatParquet)
	}
	from, err := parseExportTime(body.From)
	if err != nil {
		writeError(w, http.StatusBadRequest, `invalid field "from": must be RFC 3339 or YYYY-MM-DD`)
		return
	}
	to, err := parseExportTime(body.To)
	if err != nil {
		writeError(w, http.StatusBadRequest, `invalid field "to": must be RFC 3339 or YYYY-MM-DD`)
		return
	}

	gw.activeExportsMu.Lock()
	if gw.activeExports == nil {
		gw.activeExports = map[string]bool{}
	}
	if gw.activeExports[claims.TenantID] {
		gw.activeExportsMu.Unlock()
		writeError(w, http.StatusTooManyRequests, "an export is already running for this tenant")
		return
	}
	gw.activeExports[claims.TenantID] = true
	gw.activeExportsMu.Unlock()
	defer func() {
		gw.activeExportsMu.Lock()
		delete(gw.activeExports, claims.TenantID)
		gw.activeExportsMu.Unlock()
	}()

	format := export.Format(body.Format)
	out := &lazyResponse{w: w, start: func(w http.ResponseWriter) {
		name := fmt.Sprintf("%s_%s_%s%s", body.Dataset, from.UTC().Format("20060102"), to.UTC().Format("20060102"),
			export.Extension(format, body.Compress))
		w.Header().Set("Content-Type", exportContentType(format, body.Compress))
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
	}}
	res, err := export.Run(r.Context(), gw.metricStore, export.Request{
		Dataset:     export.Dataset(body.Dataset),
		Format:      format,
		From:        from,
		To:          to,
		TenantID:    claims.TenantID,
		Destination: "-",
		Compress:    body.Compress,
		Out:         out,
	})
	if err != nil {
		if out.started {
			// The status line has gone. Ending the connection makes the
			// client see a failed transfer, not a short file that looks whole.
			slog.Error("export failed mid-stream", "tenant_id", claims.TenantID, "error", err)
			panic(http.ErrAbortHandler)
		}
		writeExportError(w, err, from, to)
		return
	}
	if !out.started {
		writeExportError(w, export.ErrNoData, from, to)
		return
	}
	slog.Info("export streamed", "tenant_id", claims.TenantID, "dataset", body.Dataset, "rows", res.Rows)
}

// lazyResponse sends the status line on the first byte, so an export that
// fails before writing anything can still answer with an error status.
type lazyResponse struct {
	w       http.ResponseWriter
	start   func(http.ResponseWriter)
	started bool
}

func (l *lazyResponse) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !l.started {
		l.started = true
		l.start(l.w)
	}
	return l.w.Write(p)
}

func parseExportTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

func exportContentType(format export.Format, compress bool) string {
	switch {
	case format == export.FormatParquet:
		return "application/vnd.apache.parquet"
	case compress:
		return "application/gzip"
	case format == export.FormatCSV:
		return "text/csv; charset=utf-8"
	default:
		return "application/x-ndjson"
	}
}

func writeExportError(w http.ResponseWriter, err error, from, to time.Time) {
	switch {
	case errors.Is(err, export.ErrUnknownDataset):
		writeError(w, http.StatusBadRequest, `invalid field "dataset": must be facts, metrics, or events`)
	case errors.Is(err, export.ErrUnknownFormat):
		writeError(w, http.StatusBadRequest, `invalid field "format": must be parquet, csv, or jsonl`)
	case errors.Is(err, export.ErrEmptyRange):
		writeError(w, http.StatusBadRequest, `invalid field "to": must be after from`)
	case errors.Is(err, export.ErrNoData):
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("no data in range %s .. %s", from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)))
	default:
		slog.Error("export failed", "error", err)
		writeError(w, http.StatusInternalServerError, "export failed")
	}
}

// ─── scheduled ───────────────────────────────────────────────────────────────

// scheduledDataset maps a schedule's data_type onto the export engine's
// datasets, so scheduled and on-demand exports are one code path (§6 step 6).
func scheduledDataset(dataType string) export.Dataset {
	if dataType == "service_events" {
		return export.DatasetEvents
	}
	return export.DatasetFacts
}

// runScheduledExportsLoop runs due schedules once a minute until ctx ends.
func (gw *gateway) runScheduledExportsLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		gw.runScheduledExports(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// runScheduledExports runs every active schedule that came due since its last
// run, once, for the most recent minute it was due. A schedule that missed
// several runs while the gateway was down runs once, not once per miss.
func (gw *gateway) runScheduledExports(ctx context.Context, now time.Time) {
	if gw.metricStore == nil {
		return
	}
	schedules, err := gw.db.ScheduledExports().ListActive(ctx)
	if err != nil {
		slog.Error("scheduled exports: list", "error", err)
		return
	}
	for _, s := range schedules {
		if ctx.Err() != nil {
			return
		}
		spec, err := parseCron(s.Schedule)
		if err != nil {
			_ = gw.db.ScheduledExports().UpdateLastRun(ctx, s.ID, now.UTC().Truncate(time.Minute), "invalid schedule: "+err.Error())
			continue
		}
		since := s.CreatedAt
		if s.LastRunAt != nil {
			since = *s.LastRunAt
		}
		due, ok := spec.lastMatch(since, now)
		if !ok {
			continue
		}
		msg := ""
		if _, err := gw.runScheduledExport(ctx, s, due); err != nil {
			msg = err.Error()
			if !errors.Is(err, export.ErrNoData) {
				slog.Error("scheduled export failed", "schedule_id", s.ID, "tenant_id", s.TenantID, "error", err)
			}
		}
		if err := gw.db.ScheduledExports().UpdateLastRun(ctx, s.ID, due, msg); err != nil {
			slog.Error("scheduled exports: record run", "schedule_id", s.ID, "error", err)
		}
	}
}

// runScheduledExport writes one run: the LookbackDays whole UTC days before
// the day it was due, under the schedule's own prefix.
func (gw *gateway) runScheduledExport(ctx context.Context, s *tenantdb.ScheduledExport, due time.Time) (*export.Result, error) {
	to := due.UTC().Truncate(24 * time.Hour)
	lookback := s.LookbackDays
	if lookback <= 0 {
		lookback = 7
	}
	prefix := path.Join(scheduledExportPrefix(s.TenantID, s.ID), due.UTC().Format(runLayout))
	return export.Run(ctx, gw.metricStore, export.Request{
		Dataset:     scheduledDataset(s.DataType),
		Format:      export.Format(s.Format),
		From:        to.AddDate(0, 0, -lookback),
		To:          to,
		TenantID:    s.TenantID,
		Destination: "s3://" + prefix,
	})
}

// handleScheduledExportRuns serves GET .../scheduled/<id>/runs, which lists
// the runs, and GET .../scheduled/<id>/runs/<run>/<file>, which downloads one
// file of one. Any role may read: reading an export is leaving with data the
// caller can already see.
func (gw *gateway) handleScheduledExportRuns(w http.ResponseWriter, r *http.Request, e *tenantdb.ScheduledExport, rest []string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if gw.metricStore == nil {
		writeError(w, http.StatusServiceUnavailable, "export storage not configured")
		return
	}
	prefix := scheduledExportPrefix(e.TenantID, e.ID)

	switch len(rest) {
	case 0:
		keys, err := gw.metricStore.List(r.Context(), prefix)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list runs")
			return
		}
		files := map[string][]string{}
		for _, k := range keys {
			run, file, ok := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(k, prefix), "/"), "/")
			if ok && runNameRe.MatchString(run) && runFileRe.MatchString(file) {
				files[run] = append(files[run], file)
			}
		}
		type runEntry struct {
			Run   string   `json:"run"`
			Files []string `json:"files"`
		}
		runs := make([]runEntry, 0, len(files))
		for run, fs := range files {
			sort.Strings(fs)
			runs = append(runs, runEntry{Run: run, Files: fs})
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].Run > runs[j].Run })
		writeJSON(w, http.StatusOK, map[string]interface{}{"runs": runs})

	case 2:
		run, file := rest[0], rest[1]
		if !runNameRe.MatchString(run) || !runFileRe.MatchString(file) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		rc, err := gw.metricStore.Get(r.Context(), path.Join(prefix, run, file))
		if err != nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file))
		w.Header().Set("Content-Type", "application/octet-stream")
		if file == "manifest.json" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)

	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// ─── cron ────────────────────────────────────────────────────────────────────

// cronSpec is a five-field cron expression in the forms the schedule API
// accepts: "*", a number, or "*/n" in each field (minute, hour, day of month,
// month, day of week, Sunday being 0 or 7). As in cron, when both day fields
// are restricted a day matches if either does.
type cronSpec struct {
	fields [5]cronField
}

type cronField struct {
	any  bool
	step int // "*/n"; zero otherwise
	val  int // a single value
}

var cronBounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

func parseCron(expr string) (cronSpec, error) {
	var c cronSpec
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return c, fmt.Errorf("want 5 fields, got %d", len(parts))
	}
	for i, p := range parts {
		lo, hi := cronBounds[i][0], cronBounds[i][1]
		switch {
		case p == "*":
			c.fields[i] = cronField{any: true}
		case strings.HasPrefix(p, "*/"):
			n, err := strconv.Atoi(p[2:])
			if err != nil || n < 1 || n > hi {
				return c, fmt.Errorf("field %d: step %q out of range", i+1, p)
			}
			c.fields[i] = cronField{step: n}
		default:
			n, err := strconv.Atoi(p)
			if err != nil || n < lo || n > hi {
				return c, fmt.Errorf("field %d: %q is not between %d and %d", i+1, p, lo, hi)
			}
			c.fields[i] = cronField{val: n}
		}
	}
	return c, nil
}

func (f cronField) matches(v, lo int) bool {
	switch {
	case f.any:
		return true
	case f.step > 0:
		return (v-lo)%f.step == 0
	default:
		return v == f.val
	}
}

func (c cronSpec) matches(t time.Time) bool {
	t = t.In(scheduleTZ)
	if !c.fields[0].matches(t.Minute(), 0) || !c.fields[1].matches(t.Hour(), 0) || !c.fields[3].matches(int(t.Month()), 1) {
		return false
	}
	dom := c.fields[2].matches(t.Day(), 1)
	wd := int(t.Weekday())
	dow := c.fields[4].matches(wd, 0) || (wd == 0 && !c.fields[4].any && c.fields[4].step == 0 && c.fields[4].val == 7)
	if !c.fields[2].any && !c.fields[4].any {
		return dom || dow
	}
	return dom && dow
}

// maxCronLookback bounds the search for a missed run. A schedule that has
// not matched in that long is not due; one that has runs for its latest match.
const maxCronLookback = 32 * 24 * time.Hour

// lastMatch returns the latest minute in (after, now] the expression matches.
func (c cronSpec) lastMatch(after, now time.Time) (time.Time, bool) {
	t := now.UTC().Truncate(time.Minute)
	floor := now.Add(-maxCronLookback)
	if after.After(floor) {
		floor = after
	}
	for ; t.After(floor); t = t.Add(-time.Minute) {
		if c.matches(t) {
			return t, true
		}
	}
	return time.Time{}, false
}
