// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package plugin is the Grafana backend datasource for Gravix.
//
// It exists so that a team standardised on Grafana does not have to adopt
// Gravix's dashboard to use Gravix's numbers. Charter §7.3 Q1: whether you can
// keep the tool you already have is not a product tier.
//
// It queries Trino directly and bypasses Cube. That keeps this module's
// dependency tree small and, more usefully, means a Grafana panel reads the
// same warehouse tables any other engine reads — there is no Gravix-shaped
// layer in between that could disagree.
package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	_ "github.com/trinodb/trino-go-client/trino"
)

// bucketStartLayout is how transforms/request_metrics_minute writes
// bucket_start. Parsing with any other layout silently yields a zero time and
// a panel that renders every point in 1970.
const bucketStartLayout = "2006-01-02 15:04:05"

// ErrUnsupportedField is returned for a field name outside the fixed set.
var ErrUnsupportedField = errors.New("gravix datasource: unsupported field")

// QueryModel is the JSON shape of one Grafana panel query targeting Gravix.
//
// A structured filter, not free-text SQL. docs/04-non-goals.md §6 refuses a
// query language, and an editor that accepted arbitrary SQL would be one —
// with the added property that it would also be an injection surface pointed
// at the warehouse.
type QueryModel struct {
	Service      string `json:"service"`
	Method       string `json:"method,omitempty"`
	PathTemplate string `json:"pathTemplate,omitempty"`
	Field        string `json:"field"`
}

// fieldColumns is the complete set of selectable columns.
//
// A map lookup rather than validation-then-interpolation: the only strings
// that can reach the SELECT clause are the values in this map, so there is no
// path by which user input becomes a column name. Everything else in the
// statement is a bound parameter.
var fieldColumns = map[string]string{
	"request_count":  "request_count",
	"error_count":    "error_count",
	"error_rate":     "error_rate",
	"p50_latency_ms": "p50_latency_ms",
	"p95_latency_ms": "p95_latency_ms",
	"p99_latency_ms": "p99_latency_ms",
}

// fieldColumn maps a QueryModel.Field to the literal SQL column it selects.
func fieldColumn(field string) (string, error) {
	col, ok := fieldColumns[field]
	if !ok {
		return "", ErrUnsupportedField
	}
	return col, nil
}

// Datasource implements the Grafana backend contract for Gravix.
type Datasource struct {
	trinoHost string
	trinoPort int
	db        *sql.DB
}

// instanceSettings is the shape Grafana stores from ConfigEditor.tsx.
type instanceSettings struct {
	TrinoHost string `json:"trinoHost"`
	TrinoPort int    `json:"trinoPort"`
}

// NewDatasource constructs a Datasource from Grafana's instance settings.
func NewDatasource(_ context.Context, settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	cfg := instanceSettings{TrinoHost: "localhost", TrinoPort: 8081}
	if len(settings.JSONData) > 0 {
		// A malformed settings blob leaves the defaults in place rather than
		// failing to construct: an unusable datasource that reports "trino
		// unreachable" on its health check is easier to diagnose than one
		// Grafana refuses to instantiate at all.
		var parsed instanceSettings
		if err := json.Unmarshal(settings.JSONData, &parsed); err == nil {
			if parsed.TrinoHost != "" {
				cfg.TrinoHost = parsed.TrinoHost
			}
			if parsed.TrinoPort != 0 {
				cfg.TrinoPort = parsed.TrinoPort
			}
		}
	}

	db, err := sql.Open("trino", dsn(cfg.TrinoHost, cfg.TrinoPort))
	if err != nil {
		return nil, fmt.Errorf("gravix datasource: opening trino connection: %w", err)
	}

	return &Datasource{trinoHost: cfg.TrinoHost, trinoPort: cfg.TrinoPort, db: db}, nil
}

func dsn(host string, port int) string {
	return fmt.Sprintf("http://gravix@%s:%d?catalog=gravix&schema=raw", host, port)
}

// Dispose closes the connection pool when Grafana discards the instance.
func (d *Datasource) Dispose() {
	if d.db != nil {
		d.db.Close()
	}
}

// querySQL is the only statement this plugin runs against the warehouse.
//
// Everything variable is a bound parameter except <column>, which comes from
// fieldColumns and can therefore only ever be one of six literals.
const querySQL = `SELECT bucket_start, %s
FROM gravix.raw.request_metrics_minute
WHERE service = ?
  AND (? = '' OR method = ?)
  AND (? = '' OR path_template = ?)
  AND bucket_start >= ? AND bucket_start < ?
ORDER BY bucket_start ASC`

// QueryData answers one or more panel queries in a single Grafana request.
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	resp := backend.NewQueryDataResponse()

	for _, q := range req.Queries {
		resp.Responses[q.RefID] = d.query(ctx, q)
	}
	return resp, nil
}

func (d *Datasource) query(ctx context.Context, q backend.DataQuery) backend.DataResponse {
	var model QueryModel
	if err := json.Unmarshal(q.JSON, &model); err != nil {
		return errorFrame(fmt.Sprintf("gravix: could not read the query: %v", err))
	}

	if model.Service == "" {
		return errorFrame("service is required")
	}
	col, err := fieldColumn(model.Field)
	if err != nil {
		return errorFrame(err.Error())
	}

	from := q.TimeRange.From.UTC().Format(bucketStartLayout)
	to := q.TimeRange.To.UTC().Format(bucketStartLayout)

	rows, err := d.db.QueryContext(ctx, fmt.Sprintf(querySQL, col),
		model.Service,
		model.Method, model.Method,
		model.PathTemplate, model.PathTemplate,
		from, to)
	if err != nil {
		return errorFrame(fmt.Sprintf("gravix: query failed: %v", err))
	}
	defer rows.Close()

	times := []time.Time{}
	values := []float64{}
	for rows.Next() {
		var bucket string
		var value float64
		if err := rows.Scan(&bucket, &value); err != nil {
			return errorFrame(fmt.Sprintf("gravix: query failed: %v", err))
		}
		ts, err := time.Parse(bucketStartLayout, bucket)
		if err != nil {
			return errorFrame(fmt.Sprintf("gravix: query failed: unreadable bucket_start %q: %v", bucket, err))
		}
		times = append(times, ts)
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return errorFrame(fmt.Sprintf("gravix: query failed: %v", err))
	}

	frame := data.NewFrame("response",
		data.NewField("time", nil, times),
		data.NewField(model.Field, nil, values),
	)
	return backend.DataResponse{Frames: data.Frames{frame}}
}

func errorFrame(msg string) backend.DataResponse {
	return backend.DataResponse{Error: errors.New(msg)}
}

// CheckHealth runs SELECT 1 and reports the result as Grafana's connection test.
func (d *Datasource) CheckHealth(ctx context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	var one int
	if err := d.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: fmt.Sprintf("gravix: trino unreachable: %v", err),
		}, nil
	}
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "gravix: trino reachable",
	}, nil
}
