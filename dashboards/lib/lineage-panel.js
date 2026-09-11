// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// The lineage panel: click any number, see the facts behind it.
//
// Plain ES module, no dependency, no build step. The rendering is pure string
// functions so the whole of it is testable under `node --test` without a DOM
// library — which is also why there is no framework here. See GRVX-809 §3.
//
// Nothing in this file may render an individual fact record. The endpoint does
// not return one (non-goal §5); this file must not invent one either.

/** Escape text for interpolation into HTML. Every dynamic value goes through it. */
export function esc(value) {
    return String(value === undefined || value === null ? '' : value)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

/**
 * Format a bucket for a human, in their locale, with the UTC offset shown.
 *
 * The offset is not decoration. A provenance panel that says "14:23" without
 * saying 14:23 where is not evidence of anything, and every timestamp Gravix
 * stores is UTC.
 */
export function formatBucket(iso, locale, timeZone) {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return esc(iso);
    // Explicit components, not dateStyle/timeStyle: those cannot be combined with
    // timeZoneName, and Intl throws rather than ignoring the conflict — which
    // silently dropped the offset and left the panel showing a bare ISO string.
    const opts = {
        year: 'numeric', month: 'short', day: 'numeric',
        hour: '2-digit', minute: '2-digit',
        timeZoneName: 'shortOffset'
    };
    if (timeZone) opts.timeZone = timeZone;
    try {
        return esc(new Intl.DateTimeFormat(locale || undefined, opts).format(d));
    } catch (e) {
        // shortOffset needs a recent ICU. Falling back to the long name keeps the
        // zone visible, which is the part that matters.
        try {
            return esc(new Intl.DateTimeFormat(locale || undefined,
                Object.assign({}, opts, { timeZoneName: 'short' })).format(d));
        } catch (e2) {
            return esc(d.toISOString());
        }
    }
}

/**
 * Turn a semantic-layer time value into the RFC3339 the endpoint takes.
 *
 * Cube returns "2026-09-09T14:00:00.000" with no zone, and the percentile
 * endpoint returns "2026-09-09T14:00:00Z". Every timestamp Gravix stores is UTC,
 * so a missing zone means UTC — guessing the viewer's zone here would silently
 * ask about a different bucket.
 */
export function toBucketISO(raw) {
    if (!raw) return '';
    let v = String(raw).trim().replace(' ', 'T');
    v = v.replace(/\.\d+(?=(Z|[+-]\d{2}:?\d{2})?$)/, '');
    if (!/([Zz]|[+-]\d{2}:?\d{2})$/.test(v)) v += 'Z';
    return v;
}

const MAX_KEYS_SHOWN = 10;

function section(title, body) {
    return '<section class="lineage-panel__section"><h3>' + esc(title) + '</h3>' + body + '</section>';
}

function definitionList(pairs) {
    const rows = pairs
        .filter(([, v]) => v !== undefined && v !== null && v !== '')
        .map(([k, v]) => '<dt>' + esc(k) + '</dt><dd>' + esc(v) + '</dd>')
        .join('');
    return '<dl>' + rows + '</dl>';
}

/** §5.2.6 — the fact files, with the tail behind a <details> when there are many. */
export function renderFactKeys(keys) {
    const list = Array.isArray(keys) ? keys : [];
    if (list.length === 0) {
        return '<p>No source files recorded for this partition.</p>';
    }
    const items = k => '<li>' + esc(k) + '</li>';
    const head = list.slice(0, MAX_KEYS_SHOWN).map(items).join('');
    if (list.length <= MAX_KEYS_SHOWN) {
        return '<ul class="lineage-panel__keys">' + head + '</ul>';
    }
    const rest = list.slice(MAX_KEYS_SHOWN).map(items).join('');
    return '<ul class="lineage-panel__keys">' + head + '</ul>'
        + '<details><summary>and ' + (list.length - MAX_KEYS_SHOWN) + ' more</summary>'
        + '<ul class="lineage-panel__keys">' + rest + '</ul></details>';
}

/** §5.2.8 — the command, and a button that copies it. */
export function renderRecompute(cmd) {
    return '<code class="lineage-panel__code" data-lineage-cmd>' + esc(cmd) + '</code>'
        + '<button type="button" class="lineage-panel__copy" data-lineage-copy>Copy command</button>';
}

/**
 * §5.2.4 — how exact the number is.
 *
 * An approximate number must not look like an exact one. The difference is
 * carried by a bordered region, an icon and the word "Approximate", never by
 * colour alone, so it survives greyscale and colour blindness.
 */
export function renderExactness(l) {
    const rows = [['Exactness', l.exactness], ['Error bound', l.error_bound]];
    if (l.exactness !== 'approximate') {
        return definitionList(rows);
    }
    return '<div class="lineage-panel__warning" role="status">'
        + '<span class="lineage-panel__warning-icon" aria-hidden="true">&#9888;</span>'
        + '<div><strong>Approximate.</strong> '
        + esc(l.known_defect || 'This metric has a known defect and its value should not be relied on.')
        + '</div></div>'
        + definitionList(rows);
}

/** §5.2.7 — integrity, including the revision line when the partition was revised. */
export function renderIntegrity(l) {
    let out = definitionList([
        ['Idempotency key', l.idempotency_key],
        ['Content digest', l.content_digest],
        ['Data file', l.data_file]
    ]);
    const revision = Number(l.current_revision || 0);
    if (revision > 0) {
        const history = Array.isArray(l.revision_history) ? l.revision_history : [];
        const last = history.length ? history[history.length - 1] : {};
        out += '<p>Revised ' + revision + ' time' + (revision === 1 ? '' : 's')
            + '; last revised ' + esc(last.revised_at || 'unknown')
            + '; previously ' + esc(last.digest || 'unknown') + '</p>';
    }
    return out;
}

/**
 * Say so when the clicked point is wider than the grain being explained.
 *
 * Lineage is per minute bucket; the dashboard's charts are per hour. Clicking an
 * hourly point and being shown a minute's provenance without being told is the
 * kind of quiet mismatch this whole feature exists to eliminate, so the panel
 * states it. See SD-010.
 */
export function renderGrainNote(lineage, clickedGranularity) {
    const grain = (lineage && lineage.grain) || '';
    if (!clickedGranularity || !grain || clickedGranularity === grain) return '';
    return '<p class="lineage-panel__warning" role="status">'
        + '<span class="lineage-panel__warning-icon" aria-hidden="true">&#9888;</span>'
        + '<span>You clicked a value covering one ' + esc(clickedGranularity)
        + '. Provenance is recorded per ' + esc(grain)
        + ', so what follows explains the ' + esc(grain)
        + ' at the start of it — its numbers will be smaller than the one you clicked.</span></p>';
}

/**
 * Render the whole panel body for a Lineage response.
 *
 * Sections appear in the order GRVX-809 §5.2 gives, because the order is the
 * argument: what the number is, how it is computed, how exact it is, how it may
 * be combined, where it came from, and how to reproduce it.
 */
export function renderLineage(lineage, opts) {
    const l = lineage || {};
    const o = opts || {};

    if (!l.metric || !l.bucket) {
        return renderFailure('malformed');
    }

    const values = l.values && typeof l.values === 'object' ? l.values : {};
    const valueRows = Object.keys(values).sort().map(k => [k, values[k]]);

    return '<div class="lineage-panel__head">'
        + '<h2 class="lineage-panel__title">' + esc(l.metric) + '@' + esc(l.metric_version) + '</h2>'
        + '<span class="lineage-panel__bucket">' + formatBucket(l.bucket, o.locale, o.timeZone) + '</span>'
        + '<button type="button" class="lineage-panel__close" data-lineage-close aria-label="Close lineage">&times;</button>'
        + '</div>'
        + renderGrainNote(l, o.clickedGranularity)
        + section('Values', valueRows.length ? definitionList(valueRows) : '<p>No values in this row.</p>')
        + section('How this is computed', definitionList([
            ['Contract', l.contract_ref],
            ['Formula', l.formula],
            ['Grain', l.grain]
        ]))
        + section('How exact this is', renderExactness(l))
        + section('How it may be combined', definitionList([
            ['Mergeability', l.mergeability],
            ['Note', l.merge_note]
        ]))
        + section('Where it came from', definitionList([['Facts read', l.fact_count]]) + renderFactKeys(l.source_fact_keys))
        + section('Integrity', renderIntegrity(l))
        + section('Reproduce this number', renderRecompute(l.recompute_cmd));
}

/**
 * Failure copy, verbatim from GRVX-809 §5.4.
 *
 * Every one of these names the fix rather than the failure. That is the whole
 * standard: a panel that says "Error 409" has told the reader nothing they can
 * act on.
 */
export const FAILURE_COPY = {
    no_manifest: "Lineage isn't available for this bucket — it was computed before Gravix recorded provenance. Run this to make it available:",
    no_row: 'No data in this bucket for the current filters.',
    network: "Couldn't reach the Gravix gateway. Check that it's running: docker compose ps gateway",
    unauthorized: 'Your session expired. Reload the page to sign in again.',
    rate_limited: 'Too many requests. This panel will retry in ',
    malformed: "This lineage response wasn't understood. This is a Gravix bug — please report it with the bucket time."
};

/** Render one failure state. `detail` is the recompute command or retry seconds. */
export function renderFailure(kind, detail) {
    const close = '<div class="lineage-panel__head">'
        + '<h2 class="lineage-panel__title">Lineage</h2>'
        + '<button type="button" class="lineage-panel__close" data-lineage-close aria-label="Close lineage">&times;</button>'
        + '</div>';

    if (kind === 'no_manifest') {
        return close + '<p>' + esc(FAILURE_COPY.no_manifest) + '</p>' + renderRecompute(detail);
    }
    if (kind === 'rate_limited') {
        const seconds = Number(detail) > 0 ? Number(detail) : 1;
        return close + '<p role="status">' + esc(FAILURE_COPY.rate_limited)
            + '<span data-lineage-countdown>' + seconds + '</span>s.</p>';
    }
    const copy = FAILURE_COPY[kind] || FAILURE_COPY.malformed;
    return close + '<p role="status">' + esc(copy) + '</p>';
}

/** The loading state. Never an empty panel. */
export function renderLoading() {
    return '<p aria-busy="true">Loading lineage…</p>';
}

/** Map an HTTP status onto the failure kind the panel renders. */
export function failureKindForStatus(status) {
    if (status === 409) return 'no_manifest';
    if (status === 404) return 'no_row';
    if (status === 401) return 'unauthorized';
    if (status === 429) return 'rate_limited';
    return 'malformed';
}

/**
 * Build the request URL for a value.
 *
 * Only the three declared dimensions are ever sent. A high-cardinality filter is
 * a non-goal, and the place to stop one is before it is asked for.
 */
export function lineageUrl(base, params) {
    const p = params || {};
    const query = [
        ['metric', p.metric || 'request_metrics_minute'],
        ['bucket', p.bucket]
    ];
    ['service', 'method', 'path_template'].forEach(dim => {
        if (p[dim]) query.push([dim, p[dim]]);
    });
    return base + '?' + query
        .filter(([, v]) => v !== undefined && v !== null && v !== '')
        .map(([k, v]) => encodeURIComponent(k) + '=' + encodeURIComponent(v))
        .join('&');
}

/**
 * The DOM controller.
 *
 * Non-modal by design: it does not trap focus and does not cover the dashboard,
 * because provenance is something you read alongside the number, not instead of
 * it. Escape closes it and returns focus to whatever opened it.
 */
export class LineagePanel {
    constructor(container, options) {
        this.container = container;
        this.options = options || {};
        this.opener = null;
        this.container.setAttribute('role', 'region');
        this.container.setAttribute('aria-label', 'Lineage');
        this.container.classList.add('lineage-panel');
        this.container.hidden = true;

        this._onKeydown = event => {
            if (event.key === 'Escape' && !this.container.hidden) {
                this.close();
            }
        };
        this._onClick = event => {
            const target = event.target;
            if (target && target.closest && target.closest('[data-lineage-close]')) {
                this.close();
                return;
            }
            if (target && target.closest && target.closest('[data-lineage-copy]')) {
                this.copyCommand();
            }
        };
        this.container.addEventListener('click', this._onClick);
        (this.options.document || globalThis.document).addEventListener('keydown', this._onKeydown);
    }

    /** Show the loading state immediately, then fill it in. */
    open(opener, html) {
        this.opener = opener || null;
        this.container.hidden = false;
        this.container.innerHTML = html;
    }

    set(html) {
        this.container.innerHTML = html;
    }

    close() {
        this.container.hidden = true;
        this.container.innerHTML = '';
        if (this.opener && typeof this.opener.focus === 'function') {
            this.opener.focus();
        }
        this.opener = null;
    }

    copyCommand() {
        const el = this.container.querySelector('[data-lineage-cmd]');
        if (!el) return false;
        const text = el.textContent || '';
        const clipboard = this.options.clipboard
            || (globalThis.navigator && globalThis.navigator.clipboard);
        if (clipboard && typeof clipboard.writeText === 'function') {
            clipboard.writeText(text);
            return true;
        }
        return false;
    }

    /** Fetch and render. Every failure lands on a §5.4 state, never an exception. */
    async load(params) {
        this.set(renderLoading());
        const fetchImpl = this.options.fetch || globalThis.fetch;
        const url = lineageUrl(this.options.endpoint || '/api/v1/lineage', params);

        let resp;
        try {
            resp = await fetchImpl(url, { headers: this.options.headers ? this.options.headers() : {} });
        } catch (e) {
            this.set(renderFailure('network'));
            return;
        }

        let body = null;
        try {
            body = await resp.json();
        } catch (e) {
            body = null;
        }

        if (!resp.ok) {
            const kind = failureKindForStatus(resp.status);
            const detail = kind === 'no_manifest'
                ? (body && body.recompute_cmd)
                : (kind === 'rate_limited' ? retryAfterSeconds(resp) : undefined);
            this.set(renderFailure(kind, detail));
            return;
        }
        this.set(renderLineage(body, this.options));
    }
}

/** Seconds to wait, from the Retry-After header, defaulting to 5. */
export function retryAfterSeconds(resp) {
    const raw = resp && resp.headers && typeof resp.headers.get === 'function'
        ? resp.headers.get('Retry-After')
        : null;
    const n = Number(raw);
    return Number.isFinite(n) && n > 0 ? n : 5;
}
