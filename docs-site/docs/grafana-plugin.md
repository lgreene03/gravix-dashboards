---
title: Grafana datasource plugin
sidebar_position: 5
---

# Keep Grafana, get Gravix's numbers

If your team already runs Grafana, you should not have to adopt a second dashboard to use Gravix.
This plugin puts Gravix's request metrics into the Grafana panels you already have.

Charter §7.3 Q1: whether you can keep the tool you already use is not a product tier. The plugin is
Apache-2.0, like the rest of the core.

## What it does, and what it deliberately does not

It queries `gravix.raw.request_metrics_minute` through Trino and renders a time series. One query
type, four inputs.

It is **not a SQL box.** [`docs/04-non-goals.md`](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/04-non-goals.md)
§6 refuses a query language, and a text field that accepted arbitrary SQL would be one — with the
added property of being an injection surface pointed at your warehouse. The query editor is four
structured filters, and the field list is closed on both sides: the dropdown offers six columns and
the backend accepts exactly the same six.

Alerting, annotations and template variables are not implemented. That is scope, not a tease.

## Installing it

The plugin is not in the Grafana catalog yet — catalog submission needs an organisation account and
a signing key this repository does not have, and the procedure is written down in
[Grafana plugin publishing](grafana-plugin-publishing.md) for whoever does it.

Until then, install it unsigned:

```bash
cd grafana-plugin/gravix-datasource
go build -o dist/gpx_gravix_datasource ./cmd
npm install && npm run build
```

`dist/` now holds the backend binary, `module.js` and `plugin.json`. Mount it into Grafana and allow
the unsigned plugin:

```yaml
grafana:
  image: grafana/grafana:11.1.0
  environment:
    - GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=gravix-datasource
  volumes:
    - ./grafana-plugin/gravix-datasource/dist:/var/lib/grafana/plugins/gravix-datasource
```

Then add a **Gravix** datasource, set the Trino host and port (defaults: `localhost` and `8081`),
and press **Save & test**. A healthy connection reports `gravix: trino reachable`.

## Building a panel

| Field | Required | Meaning |
|---|---|---|
| Service | yes | The service name as it appears in your facts |
| Method | no | Blank matches every method |
| Path template | no | Blank matches every path. Use the template (`/orders/{id}`), not a real path |
| Field | yes | One of `request_count`, `error_count`, `error_rate`, `p50_latency_ms`, `p95_latency_ms`, `p99_latency_ms` |

The panel's time range becomes a `bucket_start` filter, so zooming works the way it does for every
other datasource.

### A note on the percentiles

`p95_latency_ms` here is the **per-minute** percentile as stored. Grafana will happily average those
across a wider interval if the panel asks it to, and an averaged percentile is not a percentile —
that is the error [mergeable sketches](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/02-derived-metrics.md)
exist to prevent, and it can be 62% off on a long-tailed distribution.

This plugin does not expose the sketch column, so for a correct percentile over a range wider than a
minute, query the warehouse directly rather than letting a panel aggregate these. That is a real
limitation of this plugin, stated here rather than discovered later.

## Why it talks to Trino and not to Cube

Fewer moving parts, and one fewer place for the numbers to differ. A Grafana panel reads the same
warehouse tables [DuckDB reads](bare-parquet-access.md) and [Spark reads](iceberg-tables.md), with
no Gravix-shaped layer in between that could disagree.

## What is proven, and what is not

| | |
|---|---|
| The field allowlist, and that nothing else reaches the SQL | **Tested** |
| An empty service is refused before any query runs | **Tested** |
| An unreachable Trino reports a health error, not a crash | **Tested** |
| The backend builds with `CGO_ENABLED=0` | **Tested** — it has to be cross-buildable for whatever runs Grafana |
| `npm install && npm run build` produce a loadable `dist/` | **Run** — `module.js` and `plugin.json` emitted, typecheck clean |
| A healthy Trino reports `gravix: trino reachable` | Needs a running Trino — `TestCheckHealthOK` |
| Grafana actually loads the plugin | Needs Docker — `TestGrafanaLoadsPlugin` |

The last two have not been run in an environment that had a stack. Treat "Grafana loads it" as
**built and not yet demonstrated** until that test has passed somewhere you can see.
`docs/oss/spec-defects.md` SD-051 records why.
