// Chart.js helpers for consistent chart creation across dashboard pages.
// Loaded before app.js — provides ChartHelpers global.

var ChartHelpers = (function() {
    'use strict';

    /**
     * Apply theme-aware defaults to Chart.js global config.
     * Call on init and on theme toggle.
     */
    function applyThemeDefaults() {
        var isDark = document.documentElement.getAttribute('data-theme') === 'dark' ||
            (!document.documentElement.getAttribute('data-theme') &&
             window.matchMedia('(prefers-color-scheme: dark)').matches);

        var textColor = isDark ? '#cbd5e1' : '#475569';
        var gridColor = isDark ? 'rgba(148,163,184,0.12)' : 'rgba(0,0,0,0.06)';

        if (typeof Chart !== 'undefined') {
            Chart.defaults.color = textColor;
            Chart.defaults.font.family = 'Inter, system-ui, sans-serif';
            Chart.defaults.font.size = 12;
            Chart.defaults.plugins.tooltip.backgroundColor = isDark ? '#334155' : '#1e293b';
            Chart.defaults.plugins.tooltip.titleFont = { weight: '600' };
            Chart.defaults.plugins.tooltip.padding = 10;
            Chart.defaults.plugins.tooltip.cornerRadius = 6;
            Chart.defaults.scale = Chart.defaults.scale || {};
            Chart.defaults.scale.grid = Chart.defaults.scale.grid || {};
            Chart.defaults.scale.grid.color = gridColor;
        }
    }

    /**
     * Create a time-series line chart with gradient fill and optional comparison data.
     * @param {object} params
     * @param {string} params.canvasId - ID of the <canvas> element
     * @param {string} params.label - Dataset label
     * @param {string[]} params.labels - X-axis labels
     * @param {number[]|{current: number[], previous: number[]}} params.data - Data points
     * @param {string} params.borderColor - Line color
     * @param {string} params.backgroundColor - Gradient start color (alpha recommended)
     * @param {Chart|null} params.existing - Existing chart instance to destroy
     * @returns {Chart} The new Chart instance
     */
    function createTimeSeriesChart(params) {
        var ctx = document.getElementById(params.canvasId).getContext('2d');

        if (params.existing) {
            params.existing.destroy();
        }

        var gradient = ctx.createLinearGradient(0, 0, 0, 300);
        gradient.addColorStop(0, params.backgroundColor);
        gradient.addColorStop(1, 'rgba(255, 255, 255, 0)');

        var datasets = [{
            label: params.label + ' (Current)',
            data: params.data.current || params.data,
            borderColor: params.borderColor,
            backgroundColor: gradient,
            fill: true,
            tension: 0.4,
            pointRadius: 0,
            pointHoverRadius: 6,
            borderWidth: 2
        }];

        if (params.data.previous) {
            datasets.push({
                label: params.label + ' (Previous)',
                data: params.data.previous,
                borderColor: params.borderColor + '66',
                backgroundColor: 'transparent',
                fill: false,
                tension: 0.4,
                pointRadius: 0,
                borderWidth: 1,
                borderDash: [5, 5]
            });
        }

        return new Chart(ctx, {
            type: 'line',
            data: { labels: params.labels, datasets: datasets },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                interaction: { intersect: false, mode: 'index' },
                plugins: {
                    legend: { display: datasets.length > 1 }
                },
                scales: {
                    x: { display: true, ticks: { maxTicksAuto: true, maxRotation: 0 } },
                    y: { display: true, beginAtZero: true }
                }
            }
        });
    }

    /**
     * Standard color palette for dashboard charts.
     */
    var COLORS = {
        error:      { border: 'rgba(239, 68, 68, 0.8)',  bg: 'rgba(239, 68, 68, 0.15)' },
        latency:    { border: 'rgba(99, 102, 241, 0.8)', bg: 'rgba(99, 102, 241, 0.15)' },
        throughput: { border: 'rgba(34, 197, 94, 0.8)',   bg: 'rgba(34, 197, 94, 0.15)' },
        warning:    { border: 'rgba(245, 158, 11, 0.8)', bg: 'rgba(245, 158, 11, 0.15)' },
        info:       { border: 'rgba(59, 130, 246, 0.8)', bg: 'rgba(59, 130, 246, 0.15)' }
    };

    return {
        applyThemeDefaults: applyThemeDefaults,
        createTimeSeriesChart: createTimeSeriesChart,
        COLORS: COLORS
    };
})();
