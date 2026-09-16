// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// GRVX-903: one SLO card per discovered service, with no configuration.
//
// Nothing here is persisted. The page is rebuilt from live data on every load,
// which is what "no YAML" means literally: there is no dashboard definition to
// save, share, or drift from what is actually running.
//
// The rendering functions are pure and return strings, so they can be tested
// without a DOM. Only loadSLOPage touches elements, and it takes them as
// arguments rather than reaching for document.

// The fixed target every card is measured against. Deliberately not
// configurable in this spec: a target the user must choose is a config step,
// and this phase exists to remove those.
export const SLO_TARGET_AVAILABILITY = 0.999;

// The share of requests that may fail before the budget is gone: 0.1% at
// three nines.
const ERROR_BUDGET = 1 - SLO_TARGET_AVAILABILITY;

export function esc(v) {
    return String(v ?? '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function clampPercent(n) {
    if (!Number.isFinite(n)) return 0;
    return Math.min(100, Math.max(0, n));
}

// formatPercent renders to three decimal places, because the difference
// between 99.9% and 99.95% is the difference between meeting the target and
// missing it, and one decimal place hides it.
export function formatPercent(n) {
    return clampPercent(n).toFixed(3) + '%';
}

// availabilityPercent is the share of requests that did not fail.
export function availabilityPercent(errorRate) {
    return clampPercent((1 - toNumber(errorRate)) * 100);
}

// errorBudgetRemainingPercent is how much of the allowed failure rate is left.
//
// It is a fraction of the budget, not of all requests: at a 99.9% target an
// error rate of 0.05% has spent half the budget, not 0.05% of it. Reporting it
// against all requests is the mistake that makes an error budget look healthy
// right up until it is gone.
export function errorBudgetRemainingPercent(errorRate) {
    return clampPercent((1 - toNumber(errorRate) / ERROR_BUDGET) * 100);
}

function toNumber(v) {
    const n = typeof v === 'number' ? v : parseFloat(v);
    return Number.isFinite(n) ? n : 0;
}

// formatCount renders a request volume compactly, because a card is scanned,
// not read.
export function formatCount(v) {
    const n = toNumber(v);
    if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
    if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k';
    return String(Math.round(n));
}

export function formatLatency(v) {
    const n = toNumber(v);
    return (n >= 100 ? Math.round(n) : Math.round(n * 10) / 10) + ' ms';
}

// renderSLOCard returns the markup for one service.
//
// A service with no rolled-up rows yet still gets a card showing zeros. That is
// truthful — it was seen, it has not been aggregated — and it is the state a
// new user is in for the first five minutes, which is exactly when an error
// message would be read as "this is broken".
export function renderSLOCard(service, metrics = {}) {
    const availability = availabilityPercent(metrics.errorRate);
    const budget = errorBudgetRemainingPercent(metrics.errorRate);
    const breached = availability < SLO_TARGET_AVAILABILITY * 100;
    const title = metrics.failed ? ' title="query failed"' : '';

    return `<div class="card slo-card${breached ? ' slo-card-breached' : ''}"` +
        ` data-service="${esc(service)}"${title}>` +
        `<h3 class="slo-card-service">${esc(service)}</h3>` +
        `<dl class="slo-card-metrics">` +
        row('Availability', formatPercent(availability), 'slo-availability') +
        row('P95 latency', formatLatency(metrics.p95LatencyMs), 'slo-p95') +
        row('Requests (24h)', formatCount(metrics.requestCount), 'slo-requests') +
        row('Error budget left', formatPercent(budget), 'slo-budget') +
        `</dl>` +
        `<p class="slo-card-target">Target ${formatPercent(SLO_TARGET_AVAILABILITY * 100)} availability</p>` +
        `</div>`;
}

function row(label, value, cls) {
    return `<dt>${esc(label)}</dt><dd class="${cls}">${esc(value)}</dd>`;
}

// zeroMetrics is what a service shows before its first rollup, and what a
// failed query falls back to.
export function zeroMetrics(failed = false) {
    return { errorRate: 0, p95LatencyMs: 0, requestCount: 0, failed };
}

// loadSLOPage fetches the discovered services, then one metric set per service,
// and renders a card for each.
//
// Every dependency is injected so the whole flow is testable without a browser:
// fetchServices returns a Response-like, fetchMetrics resolves per service, and
// grid/empty are the two elements to swap between.
export async function loadSLOPage({ fetchServices, fetchMetrics, grid, empty, onEmpty }) {
    setHTML(grid, '');

    let services;
    try {
        const resp = await fetchServices();
        if (!resp || !resp.ok) {
            return showEmpty(grid, empty, onEmpty);
        }
        const body = await resp.json();
        services = Array.isArray(body?.services) ? body.services : [];
    } catch {
        // A discovery failure shows the empty state rather than an error. The
        // user cannot act on "the request failed", and the remedy — send some
        // traffic — is the same either way.
        return showEmpty(grid, empty, onEmpty);
    }

    if (services.length === 0) {
        return showEmpty(grid, empty, onEmpty);
    }

    // Fetched in parallel, rendered in the order the endpoint returned (already
    // sorted by name). Rendering in resolution order would reshuffle the page
    // on every refresh for no reason a reader could understand.
    const metrics = await Promise.all(services.map(s =>
        Promise.resolve()
            .then(() => fetchMetrics(s.name))
            .then(m => m || zeroMetrics())
            .catch(() => zeroMetrics(true))));

    showGrid(grid, empty);
    setHTML(grid, services.map((s, i) => renderSLOCard(s.name, metrics[i])).join(''));
    return services.length;
}

// .empty-state is display:none in styles.css and is revealed by a .visible
// class, which is the convention app.js already uses. Clearing style.display
// instead would fall straight back to the CSS rule and the empty state would
// never appear — and a test that only inspected style.display would not notice.
function showEmpty(grid, empty, onEmpty) {
    setHTML(grid, '');
    if (empty) empty.classList.toggle('visible', true);
    if (grid) grid.style.display = 'none';
    // The caller fills in the command that would populate this page. Injected
    // rather than imported so this module stays testable on its own.
    if (typeof onEmpty === 'function') onEmpty();
    return 0;
}

function showGrid(grid, empty) {
    if (empty) empty.classList.toggle('visible', false);
    if (grid) grid.style.display = 'grid';
}

function setHTML(el, html) {
    if (el) el.innerHTML = html;
}
