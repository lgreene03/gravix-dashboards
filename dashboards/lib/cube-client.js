// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Cube.js data-fetching layer with localStorage caching for graceful degradation.
// Loaded before app.js — provides CubeClient global.

var CubeClient = (function() {
    'use strict';

    var CACHE_PREFIX = 'gravix_cache_';
    var CACHE_MAX_AGE_MS = 10 * 60 * 1000; // 10 minutes

    function makeCacheKey(measures, filters, compareType) {
        return CACHE_PREFIX + JSON.stringify({ m: measures, f: filters, c: compareType });
    }

    function cacheGet(key) {
        try {
            var raw = localStorage.getItem(key);
            if (!raw) return null;
            return JSON.parse(raw);
        } catch (e) { return null; }
    }

    function cacheSet(key, data) {
        try {
            localStorage.setItem(key, JSON.stringify({ data: data, timestamp: Date.now() }));
        } catch (e) { /* localStorage full */ }
    }

    /**
     * Check whether a cached entry is stale.
     * @param {{ data: any, timestamp: number }} cached
     * @returns {boolean}
     */
    function isStale(cached) {
        return !cached || (Date.now() - cached.timestamp > CACHE_MAX_AGE_MS);
    }

    /**
     * Build Cube.js-compatible auth headers.
     * @param {string} token - API token (JWT or password)
     * @param {boolean} isMultiTenant
     */
    function buildHeaders(token, isMultiTenant) {
        var h = { 'Content-Type': 'application/json' };
        if (token) {
            h['Authorization'] = isMultiTenant ? 'Bearer ' + token : token;
        }
        return h;
    }

    /**
     * Shift a date string by N days.
     * @param {string} dateStr - ISO date (YYYY-MM-DD or full ISO)
     * @param {number} days
     * @returns {string} YYYY-MM-DD
     */
    function shiftDate(dateStr, days) {
        var parts = dateStr.slice(0, 10).split('-');
        var d = new Date(+parts[0], +parts[1] - 1, +parts[2]);
        d.setDate(d.getDate() - days);
        var p = function(n) { return String(n).padStart(2, '0'); };
        return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate());
    }

    // Percentile measures in the Cube model are correct only at minute
    // granularity — Cube cannot merge t-digests, so it cannot compute a wider
    // window's percentile at all. Anything coarser must go to the gateway, which
    // merges the sketches in Go.
    var PERCENTILE_MEASURES = [
        'RequestMetricsMinute.bucketP50LatencyMs',
        'RequestMetricsMinute.bucketP95LatencyMs',
        'RequestMetricsMinute.bucketP99LatencyMs'
    ];

    var MEASURE_QUANTILES = {
        'RequestMetricsMinute.bucketP50LatencyMs': 0.5,
        'RequestMetricsMinute.bucketP95LatencyMs': 0.95,
        'RequestMetricsMinute.bucketP99LatencyMs': 0.99
    };

    /**
     * Whether a measure is a per-bucket percentile that must not be aggregated.
     * @param {string} measure
     * @returns {boolean}
     */
    function isPercentileMeasure(measure) {
        return PERCENTILE_MEASURES.indexOf(measure) !== -1;
    }

    /**
     * Decide where a percentile query has to be answered.
     *
     * Returning 'gateway' is not an optimisation — it is the only place the
     * question can be answered correctly. Asking Cube for an hour's p95 would
     * return min (or, before this change, max) of the per-minute values, which is
     * not a percentile and has no error bound.
     *
     * @param {string[]} measures
     * @param {string} granularity - 'minute', 'hour', 'day', ...
     * @returns {'cube'|'gateway'}
     */
    function routeFor(measures, granularity) {
        var hasPercentile = (measures || []).some(isPercentileMeasure);
        if (!hasPercentile) return 'cube';
        return granularity === 'minute' ? 'cube' : 'gateway';
    }

    /**
     * Build the gateway percentile request for a measure and window.
     * @param {string} measure
     * @param {{from: string, to: string, granularity: string, filters: Object}} opts
     * @returns {{path: string, params: Object}}
     */
    function percentileRequest(measure, opts) {
        var params = {
            metric: 'request_metrics_minute',
            quantile: MEASURE_QUANTILES[measure],
            from: opts.from,
            to: opts.to,
            granularity: opts.granularity || 'all'
        };
        var filters = opts.filters || {};
        ['service', 'method', 'path_template'].forEach(function(dim) {
            if (filters[dim]) params[dim] = filters[dim];
        });
        return { path: '/api/v1/percentile', params: params };
    }

    return {
        CACHE_MAX_AGE_MS: CACHE_MAX_AGE_MS,
        PERCENTILE_MEASURES: PERCENTILE_MEASURES,
        isPercentileMeasure: isPercentileMeasure,
        routeFor: routeFor,
        percentileRequest: percentileRequest,
        makeCacheKey: makeCacheKey,
        cacheGet: cacheGet,
        cacheSet: cacheSet,
        isStale: isStale,
        buildHeaders: buildHeaders,
        shiftDate: shiftDate
    };
})();
