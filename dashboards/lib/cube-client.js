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

    return {
        CACHE_MAX_AGE_MS: CACHE_MAX_AGE_MS,
        makeCacheKey: makeCacheKey,
        cacheGet: cacheGet,
        cacheSet: cacheSet,
        isStale: isStale,
        buildHeaders: buildHeaders,
        shiftDate: shiftDate
    };
})();
