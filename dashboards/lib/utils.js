// Shared UI utility functions for the Gravix dashboard.
// Loaded before app.js — provides globals used across all pages.

/**
 * Escape HTML entities to prevent XSS in dynamic content.
 */
function escapeHtml(str) {
    if (str == null) return '';
    const div = document.createElement('div');
    div.textContent = String(str);
    return div.innerHTML;
}

/**
 * Format large numbers with K/M suffixes.
 */
function formatNumber(n) {
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
    if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
    return n.toString();
}

/**
 * Show a toast notification.
 * @param {string} message - Text to display
 * @param {'info'|'success'|'warning'|'error'} type - Toast style
 * @param {number} [duration=3000] - Auto-dismiss delay in ms
 */
function showToast(message, type, duration) {
    type = type || 'info';
    duration = duration || 3000;
    const container = document.getElementById('toast-container');
    if (!container) return;
    const toast = document.createElement('div');
    toast.className = 'toast ' + type;
    toast.textContent = message;
    toast.style.animationDuration = '0.3s, 0.3s';
    toast.style.animationDelay = '0s, ' + (duration - 300) + 'ms';
    container.appendChild(toast);
    setTimeout(function() { toast.remove(); }, duration);
}

/**
 * Copy text to clipboard with toast feedback.
 * @param {string} text - Text to copy
 * @param {string} [label='Text'] - What was copied (for the toast)
 */
function copyToClipboard(text, label) {
    navigator.clipboard.writeText(text).then(function() {
        showToast((label || 'Text') + ' copied to clipboard', 'success');
    }).catch(function() {
        showToast('Failed to copy', 'error');
    });
}

/**
 * Authenticated fetch helper for the gateway API.
 * Reads the JWT from sessionStorage and adds Authorization + Content-Type headers.
 * Throws on non-OK responses with the server error message.
 * @param {string} gatewayUrl - Base URL of the gateway
 * @param {string} path - API path (e.g., '/api/gateway/channels')
 * @param {RequestInit} [options] - Additional fetch options
 * @returns {Promise<any>} Parsed JSON response
 */
async function gatewayApiFetch(gatewayUrl, path, options) {
    options = options || {};
    const token = sessionStorage.getItem('gravix_token');
    const headers = Object.assign({
        'Content-Type': 'application/json'
    }, options.headers || {});
    if (token) {
        headers['Authorization'] = 'Bearer ' + token;
    }
    const resp = await fetch(gatewayUrl + path, Object.assign({}, options, { headers: headers }));
    if (!resp.ok) {
        const body = await resp.json().catch(function() { return {}; });
        throw new Error(body.error || 'Request failed: ' + resp.status);
    }
    return resp.json();
}
