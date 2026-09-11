// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// GRVX-908: make the wait between first traffic and first chart legible.
//
// A dashboard that looks broken for four minutes is indistinguishable, to
// someone seeing it for the first time, from a dashboard that is broken. The
// wait is not reducible — it is two batch cadences — so the fix is to say what
// is happening rather than to show the same "send your first event" spinner to
// someone who has already sent one.

// FIRST_ROLLUP_ESTIMATE_SECONDS is a fixed, documented estimate of the time
// between an event being accepted and it appearing in a chart:
//
//   half the DurableSink rotation interval   30s   (services/ingestion/main.go:516,
//                                                   ticker := time.NewTicker(60 * time.Second))
//   half the rollup cron interval           150s   (docker-compose.bootstrap.yml,
//                                                   request-metrics-rollup: sleep 300)
//   fixed first-boot overhead                60s   (image build and cold start)
//                                           ----
//                                           240s
//
// Not computed live. Both source cadences were re-read against the code when
// this was implemented, and both match. Deliberately conservative in the
// direction of finishing before the estimate elapses: a countdown that reaches
// zero while the user is still waiting costs less trust than one that promises
// less time than it takes.
export const FIRST_ROLLUP_ESTIMATE_SECONDS = 240;

// FIRST_SERVICE_SEEN_KEY holds the ISO8601 moment this tab first saw a service.
export const FIRST_SERVICE_SEEN_KEY = 'gravix_first_service_seen_at';

// ALMOST_THERE_TEXT is shown once the estimate has fully elapsed. It does not
// claim a new deadline: an estimate that keeps extending itself is worse than
// one that admits it has run out.
export const ALMOST_THERE_TEXT = 'Almost there — checking for data...';

// formatCountdown renders seconds as "M:SS". Negative input clamps to zero, so
// a clock skew that puts the start time in the future shows "0:00" rather than
// a negative duration.
export function formatCountdown(totalSeconds) {
    const s = Number.isFinite(totalSeconds) ? Math.max(0, Math.floor(totalSeconds)) : 0;
    const minutes = Math.floor(s / 60);
    const seconds = s % 60;
    return `${minutes}:${String(seconds).padStart(2, '0')}`;
}

// computeCountdownText is the whole display decision as a pure function, so the
// text can be tested at any elapsed time without waiting for one.
export function computeCountdownText(firstSeenAt, now) {
    // Clamped at zero *before* the subtraction, not after. A start time in the
    // future (clock skew between the browser and whatever wrote it) would
    // otherwise make elapsed negative and the remaining time larger than the
    // estimate itself — showing "4:30" on a four-minute countdown, which reads
    // as a bug rather than as a clock problem.
    const elapsed = Math.max(0, Math.floor((now.getTime() - firstSeenAt.getTime()) / 1000));
    if (Number.isNaN(elapsed)) {
        // An unparseable stored timestamp: show the full estimate rather than
        // "NaN:NaN", and let the next poll correct it.
        return `First rollup completes in ~${formatCountdown(FIRST_ROLLUP_ESTIMATE_SECONDS)}`;
    }
    if (elapsed >= FIRST_ROLLUP_ESTIMATE_SECONDS) {
        return ALMOST_THERE_TEXT;
    }
    return `First rollup completes in ~${formatCountdown(FIRST_ROLLUP_ESTIMATE_SECONDS - elapsed)}`;
}

// At most one interval is ever active, module-wide.
let countdownIntervalId = null;

// startCountdown paints immediately and then once a second, and stops itself
// when the estimate runs out — a timer that keeps firing forever on an
// unattended tab is a battery cost with nothing to show for it.
export function startCountdown(firstSeenAt, deps = {}) {
    const doc = deps.document || (typeof document !== 'undefined' ? document : null);
    const setIv = deps.setInterval || ((fn, ms) => setInterval(fn, ms));
    const clearIv = deps.clearInterval || (id => clearInterval(id));
    const now = deps.now || (() => new Date());

    if (countdownIntervalId !== null) {
        clearIv(countdownIntervalId);
        countdownIntervalId = null;
    }

    const paint = () => {
        const text = computeCountdownText(firstSeenAt, now());
        const el = doc && doc.getElementById('onboard-countdown-text');
        if (el) el.textContent = text;
        return text;
    };

    // Painted before the first tick, so the element never shows the markup's
    // placeholder for a second before the real value replaces it.
    if (paint() === ALMOST_THERE_TEXT) {
        return null;
    }

    countdownIntervalId = setIv(() => {
        if (paint() === ALMOST_THERE_TEXT) {
            clearIv(countdownIntervalId);
            countdownIntervalId = null;
        }
    }, 1000);
    return countdownIntervalId;
}

// activeCountdownId exposes the interval id for tests. Not part of the display.
export function activeCountdownId() {
    return countdownIntervalId;
}

// stopCountdown is used by tests to leave no timer behind.
export function stopCountdown(deps = {}) {
    const clearIv = deps.clearInterval || (id => clearInterval(id));
    if (countdownIntervalId !== null) {
        clearIv(countdownIntervalId);
        countdownIntervalId = null;
    }
}

// checkFirstRunState decides which of the two onboarding sub-states to show.
//
// "We do not know" resolves to the waiting state, not the countdown: showing a
// countdown to someone who has sent nothing would be a promise about data that
// does not exist, and the curl command is the useful thing in that case.
export async function checkFirstRunState(deps = {}) {
    const doc = deps.document || (typeof document !== 'undefined' ? document : null);
    const storage = deps.storage
        || (typeof sessionStorage !== 'undefined' ? sessionStorage : null);
    const now = deps.now || (() => new Date());

    const waiting = doc && doc.getElementById('onboard-waiting');
    const countdown = doc && doc.getElementById('onboard-countdown');

    const showWaiting = () => {
        if (waiting) waiting.style.display = 'block';
        if (countdown) countdown.style.display = 'none';
        return 'waiting';
    };

    let services = [];
    try {
        const resp = await deps.fetchServices();
        if (!resp || !resp.ok) return showWaiting();
        const body = await resp.json();
        services = Array.isArray(body?.services) ? body.services : [];
    } catch {
        return showWaiting();
    }

    if (services.length === 0) return showWaiting();

    // Never overwritten: the countdown measures from when traffic was first
    // seen, and restarting it on every poll would leave it permanently at 4:00.
    let firstSeen = null;
    try {
        firstSeen = storage && storage.getItem(FIRST_SERVICE_SEEN_KEY);
    } catch {
        // Storage can throw in a sandboxed context rather than returning null.
        firstSeen = null;
    }
    if (!firstSeen) {
        firstSeen = now().toISOString();
        try {
            if (storage) storage.setItem(FIRST_SERVICE_SEEN_KEY, firstSeen);
        } catch {
            // Without storage the countdown restarts on each poll. Degraded,
            // but still better than showing "send your first event" to someone
            // who already has.
        }
    }

    if (waiting) waiting.style.display = 'none';
    if (countdown) countdown.style.display = 'block';

    startCountdown(new Date(firstSeen), deps);
    return 'countdown';
}
