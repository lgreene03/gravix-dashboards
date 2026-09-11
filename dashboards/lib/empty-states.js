// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// GRVX-907: every empty state carries the command that fills it.
//
// The old onboarding block printed a curl using a literal localhost and the
// shell variable $API_KEY, which nothing sets. A reader had to already know
// their key, already have uuidgen installed, and already know that neither was
// explained. That is not an actionable empty state, it is a screenshot of one.
//
// These commands are built from the dashboard's own live config, so on the
// bootstrap stack they paste and run. Where the config is absent the command is
// still rendered, with an honest note about the two fields to set — degrading
// to a broken command would be worse than degrading to an explained one.

// FALLBACK_CURL_NOTE is rendered when the API key is unknown. It names the two
// fields and where they live, rather than saying "configuration missing".
export const FALLBACK_CURL_NOTE =
    'Set window.GRAVIX_CONFIG.apiKey and window.GRAVIX_CONFIG.ingestionApiUrl ' +
    '(see dashboards/dashboard_config.js) to render a working command here. ' +
    'Until then, replace $API_KEY below with your own key:';

// The host used when the dashboard has no configured ingestion URL. Matches
// app.js's own default, so the two cannot disagree.
const DEFAULT_INGESTION_URL = 'http://localhost:8090';

// Gravix requires event_id to be a UUID **version 7**
// (schemas/request_fact.go, schemas/service_event.go: "event_id must be UUIDv7").
//
// crypto.randomUUID() returns a v4, so a command built with it is rejected with
// a 400 by every endpoint it targets. That was found by actually POSTing the
// rendered command rather than by reading it — see SD-017. An empty state whose
// command fails is worse than one with no command, because it tells the reader
// the product is broken.
//
// Last-resort placeholder, also v7: the version nibble is the `7` in the third
// group, and the variant bits are the `8` in the fourth.
const PLACEHOLDER_UUID = '00000000-0000-7000-8000-000000000000';

// uuidv7 builds an RFC 9562 version-7 UUID: 48 bits of Unix milliseconds, then
// the version, then random bits.
function uuidv7(deps = {}) {
    const now = deps.nowMs ? deps.nowMs() : Date.now();
    const bytes = new Uint8Array(16);

    const c = deps.crypto || (typeof globalThis !== 'undefined' ? globalThis.crypto : undefined);
    if (c && typeof c.getRandomValues === 'function') {
        try {
            c.getRandomValues(bytes);
        } catch {
            return PLACEHOLDER_UUID;
        }
    } else {
        // No crypto: this is an example command a human pastes once, not a key,
        // so a weaker source is acceptable where the alternative is no command.
        for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
    }

    // Big-endian 48-bit timestamp across bytes 0-5.
    let ts = Math.floor(now);
    for (let i = 5; i >= 0; i--) {
        bytes[i] = ts % 256;
        ts = Math.floor(ts / 256);
    }
    bytes[6] = (bytes[6] & 0x0f) | 0x70; // version 7
    bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 10

    const hex = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-` +
        `${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function newUUID(cryptoImpl) {
    return uuidv7({ crypto: cryptoImpl });
}

export { uuidv7 };

// examplePayload returns a schema-valid body for each kind.
//
// Every field is chosen to pass ValidateRequestFact / ValidateServiceEvent as
// written: status_code inside 100-599, latency_ms >= 0, and a path_template
// with no raw id and no query string. A command that returns 400 teaches the
// reader that Gravix is broken.
function examplePayload(kind, uuid, now) {
    if (kind === 'event') {
        return {
            event_id: uuid,
            event_time: now,
            service: 'my-service',
            event_type: 'deploy_completed',
            message: 'manual test event'
        };
    }
    return {
        event_id: uuid,
        event_time: now,
        service: 'my-service',
        method: 'GET',
        path_template: '/api/v1/health',
        status_code: 200,
        latency_ms: 42,
        user_agent_family: 'curl'
    };
}

// buildCurlCommand returns the exact, copy-pasteable command for one example
// payload.
//
// Config is read at call time rather than captured, so a dashboard_config.js
// that changes between loads is always reflected.
export function buildCurlCommand(kind, config = {}, deps = {}) {
    const host = (config.ingestionApiUrl || DEFAULT_INGESTION_URL).replace(/\/+$/, '');
    const apiKey = config.apiKey || '';
    const path = kind === 'event' ? '/api/v1/events' : '/api/v1/facts';

    const uuid = deps.uuid ? deps.uuid() : newUUID(deps.crypto);
    const now = deps.now ? deps.now() : new Date().toISOString();
    const body = JSON.stringify(examplePayload(kind, uuid, now));

    const command =
        `curl -X POST ${host}${path} \\\n` +
        `  -H "Content-Type: application/json" \\\n` +
        `  -H "X-API-Key: ${apiKey || '$API_KEY'}" \\\n` +
        `  -d '${body}'`;

    if (apiKey) {
        return command;
    }
    return `${FALLBACK_CURL_NOTE}\n${command}`;
}

// renderEmptyStateCommand writes the command into the `${prefix}-curl` element.
//
// textContent, never innerHTML: the command carries an API key and a service
// name, and a dashboard that renders either as markup would be an injection
// point in the one place a user is told to trust.
export function renderEmptyStateCommand(prefix, kind, config = {}, doc = null) {
    const d = doc || (typeof document !== 'undefined' ? document : null);
    if (!d) return null;

    const el = d.getElementById(`${prefix}-curl`);
    // Absent is a no-op: a page that does not have this empty state should not
    // throw on the way to rendering the ones it does.
    if (!el) return null;

    const command = buildCurlCommand(kind, config);
    el.textContent = command;
    return command;
}
