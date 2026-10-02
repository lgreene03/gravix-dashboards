#!/usr/bin/env python3
# Copyright 2026 The Gravix Authors
# SPDX-License-Identifier: Apache-2.0
"""Asks one Cube a fixed set of questions, or compares two sets of answers.

Every number on the dashboard passes through Cube, so changing the Cube
version changes the semantic layer under all of them. F-060 found that the
pinned Cube stalls on concurrent first queries and that a later one does not;
this is what has to hold before a later one ships: the same questions, asked
of the same warehouse, get the same answers.

  cube_equivalence.py capture OUT.json [--range FROM TO]
      Waits until the warehouse answers with data, then asks every query in
      QUERIES under up to three tokens: one with no tenant; one scoped to
      TENANT_ID, which is the path the dashboard takes through cube.js's
      tenant filter; and one for a tenant that has no data, which must see
      none (F-076). --range reuses a date range from an earlier capture so
      both answer the same question.

  cube_equivalence.py trino-fixture DAY
      Prints the INSERT statements that seed the full stack's three Cube
      tables with rows for DAY (YYYY-MM-DD), in the formats the rollups write.
      Nothing in the full stack writes where its Trino tables read (F-070), so
      without them every query there would compare two empty answers.

  cube_equivalence.py compare A.json B.json
      Prints every difference and exits 1 if there is one. Numbers are
      compared as numbers, because Cube versions differ in whether a measure
      arrives as "12" or 12, and timestamps with their trailing ".000" and
      "Z" removed. Nothing else is normalised.

Environment: SECRET (Cube's JWT secret), TENANT_ID (may be empty),
CUBE_URL (default http://localhost:4000), DATA_WAIT_SECONDS (default 600).

A query that errors on both versions because its cube has no files yet, such
as the event cubes before their hourly rollup, is listed as not compared. Any
other error on either side counts as a difference.

Exit codes: 0 captured, or no differences; 1 different, or nothing compared;
2 the warehouse never answered with data; 4 a tenant with no data read data.
"""
import base64
import datetime
import hashlib
import hmac
import json
import math
import os
import sys
import time
import urllib.error
import urllib.request

R = "RequestMetricsMinute"
QUERIES = {
    "hourly_counts": {"measures": [f"{R}.requestCount", f"{R}.errorCount", f"{R}.errorRate"],
                      "timeDimensions": [{"dimension": f"{R}.bucketStart", "granularity": "hour"}],
                      "order": {f"{R}.bucketStart": "asc"}},
    "by_service": {"measures": [f"{R}.requestCount", f"{R}.errorCount", f"{R}.errorRate"],
                   "dimensions": [f"{R}.service"], "order": {f"{R}.service": "asc"}},
    "by_endpoint": {"measures": [f"{R}.requestCount", f"{R}.errorCount", f"{R}.errorRate"],
                    "dimensions": [f"{R}.pathTemplate", f"{R}.method"],
                    "order": {f"{R}.pathTemplate": "asc", f"{R}.method": "asc"}},
    # Not RequestMetricsMinute.count: it counts metric rows, is hidden (SD-006),
    # and Cube refuses a hidden member.
    "total_requests": {"measures": [f"{R}.requestCount"]},
    "minute_percentiles": {"measures": [f"{R}.bucketP50LatencyMs", f"{R}.bucketP95LatencyMs",
                                        f"{R}.bucketP99LatencyMs"],
                           "dimensions": [f"{R}.service"],
                           "timeDimensions": [{"dimension": f"{R}.bucketStart", "granularity": "minute"}],
                           "order": {f"{R}.bucketStart": "asc", f"{R}.service": "asc"}, "limit": 500},
    "service_list": {"dimensions": [f"{R}.service"], "order": {f"{R}.service": "asc"}},
    "events_hourly": {"measures": ["ServiceEvents.count"], "dimensions": ["ServiceEvents.eventType"],
                      "timeDimensions": [{"dimension": "ServiceEvents.eventTime", "granularity": "hour"}],
                      "order": {"ServiceEvents.eventTime": "asc", "ServiceEvents.eventType": "asc"}},
    "events_daily": {"measures": ["ServiceEventsDaily.eventCount"],
                     "dimensions": ["ServiceEventsDaily.eventDay", "ServiceEventsDaily.service",
                                    "ServiceEventsDaily.eventType"],
                     "order": {"ServiceEventsDaily.eventDay": "asc", "ServiceEventsDaily.service": "asc",
                               "ServiceEventsDaily.eventType": "asc"}},
}
# A tenant no stack creates.
OTHER_TENANT = "00000000-0000-4000-8000-00000000f076"
# Queries whose time dimension takes the date range.
RANGED = {"hourly_counts", "minute_percentiles", "events_hourly"}


def token(secret, tenant):
    b64 = lambda b: base64.urlsafe_b64encode(b).rstrip(b"=").decode()
    now = int(time.time())
    claims = {"iat": now, "exp": now + 3600}
    if tenant:
        claims["tenant_id"] = tenant
    head = b64(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
    body = b64(json.dumps(claims).encode())
    sig = b64(hmac.new(secret.encode(), f"{head}.{body}".encode(), hashlib.sha256).digest())
    return f"{head}.{body}.{sig}"


def load(url, tok, query, limit=120):
    """Returns ("data", rows) or ("error", message), waiting out Continue wait."""
    start = time.time()
    while time.time() - start < limit:
        req = urllib.request.Request(url + "/cubejs-api/v1/load", data=json.dumps({"query": query}).encode(),
                                     headers={"Authorization": "Bearer " + tok,
                                              "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=limit) as r:
                answer = json.loads(r.read())
        except urllib.error.HTTPError as e:
            return "error", f"HTTP {e.code}: {e.read().decode(errors='replace')[:300]}"
        except (urllib.error.URLError, TimeoutError, OSError) as e:
            return "error", str(e)[:300]
        if answer.get("error") == "Continue wait":
            time.sleep(1)
            continue
        if "error" in answer:
            return "error", str(answer["error"])[:300]
        return "data", answer.get("data", [])
    return "error", f"no answer within {limit}s"


def capture(out, date_range):
    url = os.environ.get("CUBE_URL", "http://localhost:4000")
    secret = os.environ["SECRET"]
    tenant = os.environ.get("TENANT_ID", "")
    tokens = {"no_tenant": token(secret, "")}
    if tenant:
        tokens["tenant"] = token(secret, tenant)
        # A tenant with no data: what it reads is what one tenant can read of
        # another's. One warehouse tenant cannot show a missing filter, because
        # its filtered and unfiltered answers are the same.
        tokens["other_tenant"] = token(secret, OTHER_TENANT)

    deadline = time.time() + float(os.environ.get("DATA_WAIT_SECONDS", "600"))
    while True:
        kind, rows = load(url, tokens["no_tenant"], QUERIES["total_requests"])
        if kind == "data" and rows and float(list(rows[0].values())[0] or 0) > 0:
            break
        if time.time() > deadline:
            print(f"no data within the wait; last answer: {kind} {rows}", file=sys.stderr)
            return 2
        time.sleep(10)

    if not date_range:
        today = datetime.datetime.now(datetime.timezone.utc).date()
        date_range = [str(today - datetime.timedelta(days=1)), str(today)]
    results = {}
    for who, tok in tokens.items():
        for name, q in QUERIES.items():
            q = json.loads(json.dumps(q))
            if name in RANGED:
                q["timeDimensions"][0]["dateRange"] = date_range
            kind, rows = load(url, tok, q)
            results[f"{who}/{name}"] = {"kind": kind, "rows": rows}
            label = f"{len(rows)} row(s)" if kind == "data" else rows
            print(f"{who}/{name}: {label}")
    with open(out, "w") as f:
        json.dump({"range": date_range, "tenant": tenant, "results": results}, f, indent=1, sort_keys=True)
    leaked = [name for name in QUERIES if leaks(results.get(f"other_tenant/{name}"))]
    if leaked:
        print(f"ISOLATION BROKEN: a token for tenant {OTHER_TENANT}, which has no data, read rows from "
              f"{', '.join(leaked)} (F-076)")
        return 4
    return 0


def leaks(result):
    """True if an answer holds a row with any non-empty, non-zero value."""
    if not result or result["kind"] != "data":
        return False
    for row in result["rows"]:
        for v in row.values():
            if v in (None, "", 0, "0"):
                continue
            try:
                if float(v) == 0:
                    continue
            except (TypeError, ValueError):
                pass
            return True
    return False


def trino_fixture(day):
    """Deterministic rows for one day: three services, two endpoints, 150
    minutes across three hours, and a few events, so every query in QUERIES has
    more than one row to compare."""
    services = ["checkout", "payments", "search"]
    endpoints = [("GET", "/orders/{id}"), ("POST", "/orders")]
    metrics = []
    for m in range(150):
        for si, svc in enumerate(services):
            for ei, (method, path) in enumerate(endpoints):
                req = (m * 7 + si * 3 + ei) % 50 + 1
                err = req // 10 if m % 5 == 0 else 0
                p50 = 5.0 + (m + si) % 13
                metrics.append(f"('{day} {m // 60:02d}:{m % 60:02d}:00', '{svc}', '{method}', '{path}', "
                               f"{req}, {err}, {err / req!r}, {p50!r}, {p50 * 2!r}, {p50 * 3!r}, '{day}')")
    daily, detail = [], []
    for si, svc in enumerate(services):
        for ti, kind in enumerate(["deploy", "config_change"]):
            daily.append(f"('{day}', '{svc}', '{kind}', {si * 2 + ti + 1})")
            for k in range(si * 2 + ti + 1):
                minute = (si * 37 + ti * 11 + k * 23) % 180
                # transforms/service_events_detail writes event_time as RFC 3339.
                detail.append(f"('', '{day}T{minute // 60:02d}:{minute % 60:02d}:00Z', '{svc}', '{kind}', "
                              f"'{svc}-{k}', '{kind} {k}', '{{}}')")
    return ";\n".join([
        "INSERT INTO gravix.raw.request_metrics_minute (bucket_start, service, method, path_template, "
        "request_count, error_count, error_rate, p50_latency_ms, p95_latency_ms, p99_latency_ms, event_day) "
        "VALUES " + ",\n".join(metrics),
        "INSERT INTO gravix.raw.service_events_daily (event_day, service, event_type, event_count) "
        "VALUES " + ",\n".join(daily),
        "INSERT INTO gravix.raw.service_events_detail (tenant_id, event_time, service, event_type, "
        "entity_id, message, properties) VALUES " + ",\n".join(detail),
    ]) + ";"


def norm(v):
    if isinstance(v, (int, float)) and not isinstance(v, bool):
        return float(v)
    if isinstance(v, str):
        try:
            return float(v)
        except ValueError:
            s = v[:-1] if v.endswith("Z") else v
            return s[:-4] if s.endswith(".000") else s
    return v


def same(a, b):
    if isinstance(a, float) and isinstance(b, float):
        return a == b or math.isclose(a, b, rel_tol=1e-9, abs_tol=1e-12)
    return a == b


def compare(path_a, path_b):
    a, b = json.load(open(path_a)), json.load(open(path_b))
    if a["range"] != b["range"]:
        print(f"the captures asked different date ranges: {a['range']} and {b['range']}")
        return 1
    diffs, compared, uncompared = 0, 0, []
    for key in sorted(set(a["results"]) | set(b["results"])):
        ra, rb = a["results"].get(key), b["results"].get(key)
        if ra is None or rb is None:
            print(f"{key}: only in {'A' if rb is None else 'B'}")
            diffs += 1
            continue
        if ra["kind"] != "data" and rb["kind"] != "data" and \
                "No files found" in ra["rows"] and "No files found" in rb["rows"]:
            print(f"{key}: not compared, no files for this cube on either version")
            uncompared.append(key)
            continue
        if ra["kind"] != "data" or rb["kind"] != "data":
            print(f"{key}: A {ra['kind']}, B {rb['kind']}")
            for side, r in (("A", ra), ("B", rb)):
                if r["kind"] != "data":
                    print(f"  {side}: {r['rows']}")
            diffs += 1
            continue
        compared += 1
        rows_a, rows_b = ra["rows"], rb["rows"]
        if len(rows_a) != len(rows_b):
            print(f"{key}: {len(rows_a)} row(s) against {len(rows_b)}")
            print(f"  A: {rows_a[:3]}")
            print(f"  B: {rows_b[:3]}")
            diffs += 1
            continue
        bad = 0
        for i, (x, y) in enumerate(zip(rows_a, rows_b)):
            # A newer Cube may add a member, such as a time dimension without
            # its granularity suffix. Only members both returned are compared,
            # and an extra one is reported, not counted as a different number.
            for m in sorted(set(x) & set(y)):
                if not same(norm(x[m]), norm(y[m])):
                    if bad < 5:
                        print(f"{key} row {i} {m}: {x[m]!r} against {y[m]!r}")
                    bad += 1
            if i == 0 and set(x) != set(y):
                print(f"{key}: members only in A {sorted(set(x) - set(y))}, only in B {sorted(set(y) - set(x))}")
        if bad:
            diffs += 1
        print(f"{key}: {len(rows_a)} row(s), " + ("identical" if not bad else f"{bad} value(s) differ"))
    if diffs:
        print(f"DIFFERENT: {diffs} query result(s) differ")
        return 1
    if compared == 0:
        print("NOTHING COMPARED: no query returned data on both versions")
        return 1
    print(f"EQUIVALENT: {compared} query result(s) identical"
          + (f"; {len(uncompared)} not compared, no data on either version: {', '.join(uncompared)}" if uncompared else ""))
    return 0


def main(argv):
    if len(argv) >= 2 and argv[0] == "capture":
        rng = argv[3:5] if len(argv) == 5 and argv[2] == "--range" else None
        return capture(argv[1], rng)
    if len(argv) == 2 and argv[0] == "trino-fixture":
        print(trino_fixture(argv[1]))
        return 0
    if len(argv) == 3 and argv[0] == "compare":
        return compare(argv[1], argv[2])
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
