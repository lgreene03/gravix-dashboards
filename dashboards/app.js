        // --- CONFIGURATION ---
        // Override via window.GRAVIX_CONFIG before this script, or edit config.js
        const GRAVIX_CONFIG = Object.assign({
            cubeApiUrl: "http://localhost:4000/cubejs-api/v1/load",
            gatewayUrl: "",  // e.g. "http://localhost:8091" for multi-tenant mode
            refreshIntervalMs: 60000,
            staleThresholdMs: 10 * 60 * 1000 // 10 minutes
        }, window.GRAVIX_CONFIG || {});

        const CUBE_API_URL = GRAVIX_CONFIG.cubeApiUrl;
        const GATEWAY_URL = GRAVIX_CONFIG.gatewayUrl;
        const REFRESH_INTERVAL_MS = GRAVIX_CONFIG.refreshIntervalMs;
        const IS_MULTI_TENANT = !!GATEWAY_URL;

        // --- THEME ---
        function initTheme() {
            const saved = localStorage.getItem('gravix_theme');
            if (saved === 'dark' || saved === 'light') {
                document.documentElement.setAttribute('data-theme', saved);
            }
            // If no saved preference, rely on CSS @media (prefers-color-scheme)
            updateThemeUI();
        }

        function toggleTheme() {
            const current = getEffectiveTheme();
            const next = current === 'dark' ? 'light' : 'dark';
            document.documentElement.setAttribute('data-theme', next);
            localStorage.setItem('gravix_theme', next);
            updateThemeUI();
            updateChartTheme();
        }

        function getEffectiveTheme() {
            const attr = document.documentElement.getAttribute('data-theme');
            if (attr) return attr;
            return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
        }

        function updateThemeUI() {
            const isDark = getEffectiveTheme() === 'dark';
            const icon = document.getElementById('themeIcon');
            const label = document.getElementById('themeLabel');
            if (icon) icon.textContent = isDark ? '☀️' : '🌙';
            if (label) label.textContent = isDark ? 'Light Mode' : 'Dark Mode';
        }

        function updateChartTheme() {
            const isDark = getEffectiveTheme() === 'dark';
            const textColor = isDark ? '#94a3b8' : '#64748b';
            const gridColor = isDark ? 'rgba(148, 163, 184, 0.1)' : 'rgba(0, 0, 0, 0.05)';
            Chart.defaults.color = textColor;
            Chart.defaults.borderColor = gridColor;
            Chart.defaults.plugins.tooltip.backgroundColor = isDark ? '#334155' : '#1e293b';
            Chart.defaults.plugins.tooltip.titleColor = '#f1f5f9';
            Chart.defaults.plugins.tooltip.bodyColor = '#e2e8f0';
            // Refresh all existing charts (charts may not be declared yet at init time)
            if (typeof charts !== 'undefined') {
                Object.values(charts).forEach(c => { if (c && c.update) c.update(); });
            }
        }

        initTheme();

        // Listen for system theme changes
        window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
            if (!localStorage.getItem('gravix_theme')) {
                updateThemeUI();
                updateChartTheme();
            }
        });

        // --- AUTH GATE ---
        let dashboardApiToken = sessionStorage.getItem('gravix_token') || '';

        async function checkAuthRequired() {
            // Multi-tenant mode: always require login
            if (IS_MULTI_TENANT) return true;

            // Legacy mode: probe Cube.js
            try {
                const resp = await fetch(CUBE_API_URL, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ query: { measures: [], dimensions: [], limit: 1 } })
                });
                return resp.status === 403 || resp.status === 401;
            } catch (e) {
                return false;
            }
        }

        async function tryAuth(tokenOrPassword) {
            if (IS_MULTI_TENANT) {
                // Validate JWT by calling /api/gateway/me
                try {
                    const resp = await fetch(GATEWAY_URL + '/api/gateway/me', {
                        headers: { 'Authorization': 'Bearer ' + tokenOrPassword }
                    });
                    return resp.ok;
                } catch (e) {
                    return false;
                }
            }
            // Legacy: probe Cube.js with password
            try {
                const resp = await fetch(CUBE_API_URL, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json', 'Authorization': tokenOrPassword },
                    body: JSON.stringify({ query: { measures: [], dimensions: [], limit: 1 } })
                });
                return resp.status !== 403 && resp.status !== 401;
            } catch (e) {
                return false;
            }
        }

        let currentAuthTab = 'login';
        let onboardApiKeyValue = '';

        function showAuthGate() {
            document.getElementById('authOverlay').classList.remove('hidden');
            if (IS_MULTI_TENANT) {
                document.getElementById('authEmail').style.display = 'block';
                document.getElementById('authTabs').style.display = 'block';
                document.getElementById('authPrompt').textContent = 'Enter your email and password to continue.';
                document.getElementById('authEmail').focus();
            } else {
                document.getElementById('authPrompt').textContent = 'Enter the dashboard password to continue.';
                document.getElementById('authPassword').focus();
            }
        }

        function hideAuthGate() {
            document.getElementById('authOverlay').classList.add('hidden');
        }

        // Tab switching
        document.querySelectorAll('.auth-tab').forEach(tab => {
            tab.addEventListener('click', () => {
                const target = tab.dataset.tab;
                currentAuthTab = target;
                document.querySelectorAll('.auth-tab').forEach(t => {
                    t.classList.remove('active');
                    t.style.color = '#64748b';
                    t.style.fontWeight = '500';
                    t.style.borderBottom = '2px solid transparent';
                });
                tab.classList.add('active');
                tab.style.color = '#3b82f6';
                tab.style.fontWeight = '600';
                tab.style.borderBottom = '2px solid #3b82f6';
                document.getElementById('authError').style.display = 'none';

                if (target === 'login') {
                    document.getElementById('authForm').style.display = 'block';
                    document.getElementById('signupForm').style.display = 'none';
                    document.getElementById('authTitle').textContent = 'Sign In';
                    document.getElementById('authPrompt').textContent = 'Enter your email and password to continue.';
                } else {
                    document.getElementById('authForm').style.display = 'none';
                    document.getElementById('signupForm').style.display = 'block';
                    document.getElementById('authTitle').textContent = 'Create Account';
                    document.getElementById('authPrompt').textContent = 'Start monitoring your services for free.';
                }
            });
        });

        // Login form handler
        document.getElementById('authForm').addEventListener('submit', async () => {
            const errorEl = document.getElementById('authError');
            errorEl.style.display = 'none';
            document.getElementById('authSubmit').textContent = 'Verifying...';

            if (IS_MULTI_TENANT) {
                const email = document.getElementById('authEmail').value;
                const password = document.getElementById('authPassword').value;
                try {
                    const resp = await fetch(GATEWAY_URL + '/api/gateway/login', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ email, password })
                    });
                    if (resp.ok) {
                        const data = await resp.json();
                        dashboardApiToken = data.token;
                        sessionStorage.setItem('gravix_token', data.token);
                        hideAuthGate();
                        initDashboard();
                    } else {
                        errorEl.textContent = 'Invalid email or password.';
                        errorEl.style.display = 'block';
                        document.getElementById('authSubmit').textContent = 'Sign In';
                    }
                } catch (e) {
                    errorEl.textContent = 'Cannot reach gateway service.';
                    errorEl.style.display = 'block';
                    document.getElementById('authSubmit').textContent = 'Sign In';
                }
            } else {
                const password = document.getElementById('authPassword').value;
                const ok = await tryAuth(password);
                if (ok) {
                    dashboardApiToken = password;
                    sessionStorage.setItem('gravix_token', password);
                    hideAuthGate();
                    initDashboard();
                } else {
                    errorEl.style.display = 'block';
                    document.getElementById('authSubmit').textContent = 'Sign In';
                }
            }
        });

        // Signup form handler
        document.getElementById('signupForm').addEventListener('submit', async () => {
            const errorEl = document.getElementById('authError');
            errorEl.style.display = 'none';

            const name = document.getElementById('signupName').value.trim();
            const email = document.getElementById('signupEmail').value.trim();
            const password = document.getElementById('signupPassword').value;
            const confirm = document.getElementById('signupPasswordConfirm').value;

            if (!name || !email || !password) {
                errorEl.textContent = 'All fields are required.';
                errorEl.style.display = 'block';
                return;
            }
            if (password.length < 8) {
                errorEl.textContent = 'Password must be at least 8 characters.';
                errorEl.style.display = 'block';
                return;
            }
            if (password !== confirm) {
                errorEl.textContent = 'Passwords do not match.';
                errorEl.style.display = 'block';
                return;
            }

            document.getElementById('signupSubmit').textContent = 'Creating...';
            try {
                const resp = await fetch(GATEWAY_URL + '/api/gateway/register', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ name, email, password })
                });
                const data = await resp.json();
                if (resp.ok) {
                    dashboardApiToken = data.token;
                    sessionStorage.setItem('gravix_token', data.token);
                    hideAuthGate();
                    // Show onboarding with API key
                    showOnboarding(data.api_key);
                } else {
                    errorEl.textContent = data.error || 'Registration failed.';
                    errorEl.style.display = 'block';
                    document.getElementById('signupSubmit').textContent = 'Create Account';
                }
            } catch (e) {
                errorEl.textContent = 'Cannot reach gateway service.';
                errorEl.style.display = 'block';
                document.getElementById('signupSubmit').textContent = 'Create Account';
            }
        });

        // --- ONBOARDING ---
        function showOnboarding(apiKey) {
            onboardApiKeyValue = apiKey;
            document.getElementById('onboardApiKey').textContent = apiKey;

            // Build curl command
            const ingestUrl = window.location.origin.replace(':8000', ':8090');
            const curlCmd = `curl -X POST ${ingestUrl}/api/v1/facts \\
  -H "Content-Type: application/json" \\
  -H "X-API-Key: ${apiKey}" \\
  -d '{
    "event_id": "'"$(uuidgen | tr A-Z a-z)"'",
    "event_time": "'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'",
    "service": "my-api",
    "method": "GET",
    "path_template": "/api/v1/users/{id}",
    "status_code": 200,
    "latency_ms": 42,
    "user_agent_family": "curl"
  }'`;
            document.getElementById('onboardCurl').textContent = curlCmd;

            document.getElementById('onboardingOverlay').classList.remove('hidden');
        }

        // Copy helpers
        document.getElementById('copyKeyBtn').addEventListener('click', () => {
            navigator.clipboard.writeText(onboardApiKeyValue).then(() => {
                document.getElementById('copyKeyBtn').textContent = 'Copied!';
                setTimeout(() => { document.getElementById('copyKeyBtn').textContent = 'Copy'; }, 2000);
            });
        });

        document.getElementById('copyCurlBtn').addEventListener('click', () => {
            navigator.clipboard.writeText(document.getElementById('onboardCurl').textContent).then(() => {
                document.getElementById('copyCurlBtn').textContent = 'Copied!';
                setTimeout(() => { document.getElementById('copyCurlBtn').textContent = 'Copy'; }, 2000);
            });
        });

        document.getElementById('onboardDone').addEventListener('click', () => {
            document.getElementById('onboardingOverlay').classList.add('hidden');
            initDashboard();
        });

        // --- CHART DEFAULTS ---
        Chart.defaults.font.family = "'Inter', sans-serif";
        Chart.defaults.plugins.tooltip.padding = 10;
        Chart.defaults.plugins.tooltip.cornerRadius = 8;

        let charts = {}; // Store chart instances
        updateChartTheme(); // Apply theme-aware chart colors
        let availableServices = new Set();
        let lastSuccessfulUpdate = null;
        let hasConnectionError = false;
        let retryAttempts = 0;
        const MAX_RETRY_INTERVAL = 120000; // 2 minutes max

        // --- PAGE SWITCHING ---
        let currentPage = 'overview';

        function switchPage(pageName) {
            document.querySelectorAll('.page-content').forEach(p => p.style.display = 'none');
            const target = document.getElementById('page-' + pageName);
            if (target) target.style.display = 'block';

            document.querySelectorAll('.nav-link').forEach(l => {
                l.classList.remove('active');
                l.removeAttribute('aria-current');
            });
            const activeLink = document.querySelector('.nav-link[data-page="' + pageName + '"]');
            if (activeLink) {
                activeLink.classList.add('active');
                activeLink.setAttribute('aria-current', 'page');
            }

            currentPage = pageName;
            saveFiltersToHash();

            // Show/hide dashboard filters depending on page
            const headerEl = document.querySelector('.header');
            if (headerEl) {
                headerEl.style.display = (pageName === 'alerts' || pageName === 'ingestion') ? 'none' : '';
            }

            if (pageName === 'endpoints') {
                updateEndpointsPage();
            } else if (pageName === 'alerts') {
                loadAlertsPage();
            } else if (pageName === 'ingestion') {
                loadIngestionPage();
            }

            window.scrollTo({ top: 0, behavior: 'smooth' });
            document.getElementById('sidebar').classList.remove('open');
            document.getElementById('sidebarBackdrop').classList.remove('open');
        }

        // --- SHARED FILTER BUILDER ---
        function buildCurrentFilters() {
            const service = document.getElementById('serviceFilter').value;
            const dateFrom = document.getElementById('dateFrom').value;
            const dateTo = document.getElementById('dateTo').value;
            const filters = [];
            if (service) {
                filters.push({ member: "RequestMetricsMinute.service", operator: "equals", values: [service] });
            }
            if (dateFrom && dateTo && dateFrom !== dateTo) {
                filters.push({ member: "RequestMetricsMinute.bucketStart", operator: "gte", values: [dateFrom + "T00:00:00"] });
                filters.push({ member: "RequestMetricsMinute.bucketStart", operator: "lte", values: [dateTo + "T23:59:59"] });
            } else if (dateFrom) {
                filters.push({ member: "RequestMetricsMinute.bucketStart", operator: "gte", values: [dateFrom + "T00:00:00"] });
                filters.push({ member: "RequestMetricsMinute.bucketStart", operator: "lte", values: [dateFrom + "T23:59:59"] });
            }
            return filters;
        }

        // --- ENDPOINTS PAGE ---
        let allEndpointsData = [];
        let endpointsSortColumn = 'requests';
        let endpointsSortDir = 'desc';
        let currentEndpointPath = null;
        let currentEndpointMethod = null;

        function getErrorRateSeverityClass(rate) {
            if (rate <= 0.01) return 'severity-healthy';
            if (rate <= 0.05) return 'severity-warning';
            return 'severity-critical';
        }

        async function fetchAllEndpointsData(filters) {
            const query = {
                measures: [
                    "RequestMetricsMinute.requestCount", "RequestMetricsMinute.errorCount",
                    "RequestMetricsMinute.errorRate", "RequestMetricsMinute.p50Latency",
                    "RequestMetricsMinute.p95Latency", "RequestMetricsMinute.p99Latency"
                ],
                dimensions: ["RequestMetricsMinute.pathTemplate", "RequestMetricsMinute.method"],
                order: { "RequestMetricsMinute.requestCount": "desc" },
                filters: filters,
                limit: 500
            };
            const response = await fetch(CUBE_API_URL, {
                method: "POST", headers: cubeHeaders(),
                body: JSON.stringify({ query: query })
            });
            if (!response.ok) {
                if (response.status === 400) return [];
                throw new Error("Cube API returned " + response.status);
            }
            const result = await response.json();
            return (result.results && result.results[0]) ? result.results[0].data : (result.data || []);
        }

        function renderAllEndpointsTable(data) {
            allEndpointsData = data;
            const body = document.getElementById('all-endpoints-body');
            body.innerHTML = '';
            const countLabel = document.getElementById('endpoints-count-label');
            countLabel.textContent = data.length + ' endpoint' + (data.length !== 1 ? 's' : '') + ' found';

            const searchTerm = (document.getElementById('endpointSearch').value || '').toLowerCase();
            let filtered = data;
            if (searchTerm) {
                filtered = data.filter(row => {
                    const path = (row["RequestMetricsMinute.pathTemplate"] || '').toLowerCase();
                    const method = (row["RequestMetricsMinute.method"] || '').toLowerCase();
                    return path.includes(searchTerm) || method.includes(searchTerm);
                });
                countLabel.textContent = filtered.length + ' of ' + data.length + ' endpoints';
            }

            const sortKeyMap = {
                path: "RequestMetricsMinute.pathTemplate", method: "RequestMetricsMinute.method",
                requests: "RequestMetricsMinute.requestCount", errors: "RequestMetricsMinute.errorCount",
                errorRate: "RequestMetricsMinute.errorRate", p50: "RequestMetricsMinute.p50Latency",
                p95: "RequestMetricsMinute.p95Latency", p99: "RequestMetricsMinute.p99Latency"
            };
            const sortKey = sortKeyMap[endpointsSortColumn] || "RequestMetricsMinute.requestCount";

            filtered.sort((a, b) => {
                let va = a[sortKey], vb = b[sortKey];
                if (typeof va === 'string' && typeof vb === 'string') {
                    return endpointsSortDir === 'asc' ? va.localeCompare(vb) : vb.localeCompare(va);
                }
                va = parseFloat(va) || 0; vb = parseFloat(vb) || 0;
                return endpointsSortDir === 'asc' ? va - vb : vb - va;
            });

            document.querySelectorAll('#all-endpoints-table .sortable-header').forEach(th => {
                th.classList.remove('sort-asc', 'sort-desc');
                if (th.dataset.sort === endpointsSortColumn) {
                    th.classList.add(endpointsSortDir === 'asc' ? 'sort-asc' : 'sort-desc');
                }
            });

            filtered.forEach(row => {
                const tr = document.createElement('tr');
                const errorRate = parseFloat(row["RequestMetricsMinute.errorRate"]) || 0;
                const sevClass = getErrorRateSeverityClass(errorRate);
                tr.innerHTML = '<td><a href="#" class="ep-path-link">' + escapeHtml(row["RequestMetricsMinute.pathTemplate"]) + '</a></td>'
                    + '<td><code>' + escapeHtml(row["RequestMetricsMinute.method"]) + '</code></td>'
                    + '<td>' + Number(row["RequestMetricsMinute.requestCount"] || 0).toLocaleString() + '</td>'
                    + '<td>' + Number(row["RequestMetricsMinute.errorCount"] || 0).toLocaleString() + '</td>'
                    + '<td><span class="' + sevClass + ' severity-badge">' + (errorRate * 100).toFixed(2) + '%</span></td>'
                    + '<td>' + Number(row["RequestMetricsMinute.p50Latency"] || 0).toFixed(0) + '</td>'
                    + '<td>' + Number(row["RequestMetricsMinute.p95Latency"] || 0).toFixed(0) + '</td>'
                    + '<td>' + Number(row["RequestMetricsMinute.p99Latency"] || 0).toFixed(0) + '</td>';
                tr.querySelector('.ep-path-link').dataset.path = row["RequestMetricsMinute.pathTemplate"];
                tr.querySelector('.ep-path-link').dataset.method = row["RequestMetricsMinute.method"] || '';
                body.appendChild(tr);
            });

            document.querySelectorAll('.ep-path-link').forEach(link => {
                link.onclick = (e) => {
                    e.preventDefault();
                    showEndpointDetail(e.target.dataset.path, e.target.dataset.method);
                };
            });
        }

        function renderEndpointSummary(data) {
            const container = document.getElementById('endpoint-summary');
            const errorRate = parseFloat(data["RequestMetricsMinute.errorRate"]) || 0;
            const sevClass = getErrorRateSeverityClass(errorRate);
            container.innerHTML = '<div class="metric-card"><div class="metric-value">'
                + Number(data["RequestMetricsMinute.requestCount"] || 0).toLocaleString()
                + '</div><div class="metric-label">Total Requests</div></div>'
                + '<div class="metric-card"><div class="metric-value"><span class="' + sevClass + ' severity-badge">'
                + (errorRate * 100).toFixed(2) + '%</span></div><div class="metric-label">Error Rate</div></div>'
                + '<div class="metric-card"><div class="metric-value">'
                + Number(data["RequestMetricsMinute.p50Latency"] || 0).toFixed(0)
                + ' ms</div><div class="metric-label">P50 Latency</div></div>'
                + '<div class="metric-card"><div class="metric-value">'
                + Number(data["RequestMetricsMinute.p95Latency"] || 0).toFixed(0)
                + ' ms</div><div class="metric-label">P95 Latency</div></div>'
                + '<div class="metric-card"><div class="metric-value">'
                + Number(data["RequestMetricsMinute.p99Latency"] || 0).toFixed(0)
                + ' ms</div><div class="metric-label">P99 Latency</div></div>';
        }

        function renderLatencyMultiChart(p50Data, p95Data, p99Data) {
            const ctx = document.getElementById('epLatencyChart').getContext('2d');
            const refData = p95Data.length ? p95Data : p50Data;
            const timeKey = refData.length > 0 && ("RequestMetricsMinute.bucketStart.hour" in refData[0])
                ? "RequestMetricsMinute.bucketStart.hour" : "RequestMetricsMinute.bucketStart";
            const labels = refData.map(d => {
                const v = d[timeKey] || "";
                return v.length >= 16 ? v.substring(11, 16) : v;
            });
            const datasets = [
                { label: 'P50', data: p50Data.map(d => d["RequestMetricsMinute.p50Latency"]),
                  borderColor: '#22c55e', backgroundColor: 'transparent', fill: false, tension: 0.4,
                  pointRadius: 0, pointHoverRadius: 4, borderWidth: 1.5 },
                { label: 'P95', data: p95Data.map(d => d["RequestMetricsMinute.p95Latency"]),
                  borderColor: '#3b82f6', backgroundColor: 'rgba(59,130,246,0.08)', fill: true, tension: 0.4,
                  pointRadius: 0, pointHoverRadius: 4, borderWidth: 2 },
                { label: 'P99', data: p99Data.map(d => d["RequestMetricsMinute.p99Latency"]),
                  borderColor: '#ef4444', backgroundColor: 'transparent', fill: false, tension: 0.4,
                  pointRadius: 0, pointHoverRadius: 4, borderWidth: 1.5, borderDash: [4, 4] }
            ];
            if (charts['epLatency']) {
                charts['epLatency'].data.labels = labels;
                charts['epLatency'].data.datasets = datasets;
                charts['epLatency'].update();
            } else {
                charts['epLatency'] = new Chart(ctx, {
                    type: 'line', data: { labels, datasets },
                    options: {
                        responsive: true, maintainAspectRatio: false,
                        interaction: { mode: 'index', intersect: false },
                        scales: {
                            x: { display: true, grid: { display: false }, ticks: { maxTicksLimit: 8 } },
                            y: { beginAtZero: true, border: { display: false }, title: { display: true, text: 'ms' } }
                        },
                        plugins: { legend: { display: true, position: 'top', align: 'end' } }
                    }
                });
            }
        }

        async function showEndpointDetail(path, method) {
            currentEndpointPath = path;
            currentEndpointMethod = method;

            document.getElementById('endpoints-list').style.display = 'none';
            document.getElementById('endpoint-detail').style.display = 'block';
            document.getElementById('endpointDetailTitle').textContent = (method ? method + ' ' : '') + path;

            const filters = buildCurrentFilters();
            filters.push({ member: "RequestMetricsMinute.pathTemplate", operator: "equals", values: [path] });
            if (method) {
                filters.push({ member: "RequestMetricsMinute.method", operator: "equals", values: [method] });
            }

            const compare = document.getElementById('compareFilter').value;

            ['loading-ep-error', 'loading-ep-latency', 'loading-ep-throughput'].forEach(id => {
                document.getElementById(id).style.display = 'flex';
            });

            try {
                // Summary aggregate
                const summaryQuery = {
                    measures: ["RequestMetricsMinute.requestCount", "RequestMetricsMinute.errorCount",
                        "RequestMetricsMinute.errorRate", "RequestMetricsMinute.p50Latency",
                        "RequestMetricsMinute.p95Latency", "RequestMetricsMinute.p99Latency"],
                    filters: filters
                };
                const summaryResp = await fetch(CUBE_API_URL, {
                    method: "POST", headers: cubeHeaders(),
                    body: JSON.stringify({ query: summaryQuery })
                });
                const summaryResult = await summaryResp.json();
                const sd = (summaryResult.results && summaryResult.results[0])
                    ? summaryResult.results[0].data[0] : (summaryResult.data && summaryResult.data[0]) || {};
                renderEndpointSummary(sd);

                // Time-series in parallel
                const [errorData, p50Data, p95Data, p99Data, tpData] = await Promise.all([
                    fetchCubeData(["RequestMetricsMinute.errorRate"], filters, compare || null),
                    fetchCubeData(["RequestMetricsMinute.p50Latency"], filters, null),
                    fetchCubeData(["RequestMetricsMinute.p95Latency"], filters, null),
                    fetchCubeData(["RequestMetricsMinute.p99Latency"], filters, null),
                    fetchCubeData(["RequestMetricsMinute.requestCount"], filters, compare || null)
                ]);

                const processSeries = (data, measure) => {
                    const timeKey = data.length > 0 && ("RequestMetricsMinute.bucketStart.hour" in data[0])
                        ? "RequestMetricsMinute.bucketStart.hour" : "RequestMetricsMinute.bucketStart";
                    if (compare) {
                        const current = data.filter(d => d._comparison === "Current");
                        const previous = data.filter(d => d._comparison === "Previous");
                        return {
                            labels: current.map(d => {
                                const v = d[timeKey] || "";
                                return v.length >= 16 ? v.substring(11, 16) : v;
                            }),
                            values: { current: current.map(d => d[measure]), previous: previous.map(d => d[measure]) }
                        };
                    }
                    return {
                        labels: data.map(d => {
                            const v = d[timeKey] || "";
                            return v.length >= 16 ? v.substring(11, 16) : v;
                        }),
                        values: data.map(d => d[measure])
                    };
                };

                const errRes = processSeries(errorData, "RequestMetricsMinute.errorRate");
                updateChart('epErrorRate', 'epErrorRateChart', 'Error Rate', errRes.labels, errRes.values, {
                    borderColor: '#ef4444', backgroundColor: 'rgba(239,68,68,0.1)', yMax: 1.0, yTitle: 'Rate'
                });

                renderLatencyMultiChart(p50Data, p95Data, p99Data);

                const tpRes = processSeries(tpData, "RequestMetricsMinute.requestCount");
                updateChart('epThroughput', 'epThroughputChart', 'Requests', tpRes.labels, tpRes.values, {
                    borderColor: '#22c55e', backgroundColor: 'rgba(34,197,94,0.1)', yTitle: 'count'
                });
            } catch (err) {
                console.error("Endpoint detail fetch failed:", err);
            } finally {
                ['loading-ep-error', 'loading-ep-latency', 'loading-ep-throughput'].forEach(id => {
                    document.getElementById(id).style.display = 'none';
                });
            }

            saveFiltersToHash();
        }

        async function updateEndpointsPage() {
            if (currentPage !== 'endpoints') return;
            document.getElementById('loading-all-endpoints').style.display = 'flex';
            try {
                const filters = buildCurrentFilters();
                const data = await fetchAllEndpointsData(filters);
                renderAllEndpointsTable(data);
            } catch (err) {
                console.error("Endpoints page update failed:", err);
            } finally {
                document.getElementById('loading-all-endpoints').style.display = 'none';
            }
        }

        // --- SECURITY: HTML escape helper to prevent XSS ---
        function escapeHtml(str) {
            if (str == null) return '';
            const div = document.createElement('div');
            div.textContent = String(str);
            return div.innerHTML;
        }

        // --- DATA FETCHING ---
        function cubeHeaders() {
            const h = { "Content-Type": "application/json" };
            if (dashboardApiToken) {
                h["Authorization"] = IS_MULTI_TENANT
                    ? "Bearer " + dashboardApiToken
                    : dashboardApiToken;
            }
            return h;
        }

        async function fetchCubeData(measures, filters = [], compareType = null) {
            // Separate bucketStart date filters from other filters
            const otherFilters = [];
            let dateFrom = null, dateTo = null;
            filters.forEach(f => {
                if (f.member === "RequestMetricsMinute.bucketStart") {
                    if (f.operator === "gte") dateFrom = f.values[0];
                    else if (f.operator === "lte") dateTo = f.values[0];
                } else {
                    otherFilters.push(f);
                }
            });

            // Build timeDimensions entry
            const timeDim = {
                dimension: "RequestMetricsMinute.bucketStart",
                granularity: "hour"
            };

            const shiftDate = (dateStr, days) => {
                const parts = dateStr.slice(0, 10).split('-');
                const d = new Date(+parts[0], +parts[1] - 1, +parts[2]);
                d.setDate(d.getDate() - days);
                const p = n => String(n).padStart(2, '0');
                return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate());
            };

            if (compareType) {
                const shiftDays = compareType === "day" ? 1 : 7;
                let curFrom, curTo;

                if (dateFrom) {
                    curFrom = dateFrom.slice(0, 10);
                    curTo = (dateTo || dateFrom).slice(0, 10);
                } else {
                    // Defaults when no date range selected
                    const now = new Date();
                    const p = n => String(n).padStart(2, '0');
                    const fmtD = d => d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate());
                    curTo = fmtD(now);
                    const from = new Date(now);
                    from.setDate(from.getDate() - (compareType === "day" ? 0 : 6));
                    curFrom = fmtD(from);
                }

                timeDim.compareDateRange = [
                    [curFrom, curTo],
                    [shiftDate(curFrom, shiftDays), shiftDate(curTo, shiftDays)]
                ];
            } else if (dateFrom) {
                timeDim.dateRange = [dateFrom.slice(0, 10), (dateTo || dateFrom).slice(0, 10)];
            }
            // No dates + no comparison → omit dateRange so Cube returns all data

            const query = {
                measures: measures,
                timeDimensions: [timeDim],
                order: { "RequestMetricsMinute.bucketStart": "asc" },
                filters: otherFilters
            };

            const response = await fetch(CUBE_API_URL, {
                method: "POST",
                headers: cubeHeaders(),
                body: JSON.stringify({ query: query })
            });

            if (!response.ok) {
                // 400 from Cube typically means Trino tables don't exist yet (no data).
                // Treat as empty result rather than a connection error.
                if (response.status === 400) {
                    console.warn("Cube returned 400 — table may not exist yet (no rollup data)");
                    return [];
                }
                throw new Error("Cube API returned " + response.status);
            }

            const result = await response.json();

            // compareDateRange returns multiple result sets
            if (compareType && result.results && result.results.length > 1) {
                const currentData = (result.results[0].data || []).map(
                    d => Object.assign({}, d, { _comparison: "Current" })
                );
                const previousData = (result.results[1].data || []).map(
                    d => Object.assign({}, d, { _comparison: "Previous" })
                );
                return currentData.concat(previousData);
            }

            let data = [];
            if (result.results && result.results[0]) data = result.results[0].data;
            else if (result.data) data = result.data;

            return data;
        }

        async function fetchServices() {
            let response;
            try {
                response = await fetch(CUBE_API_URL, {
                    method: "POST",
                    headers: cubeHeaders(),
                    body: JSON.stringify({
                        query: {
                            dimensions: ["RequestMetricsMinute.service"],
                            order: { "RequestMetricsMinute.service": "asc" }
                        }
                    })
                });
            } catch (e) {
                console.warn("fetchServices network error:", e);
                return; // Don't populate services, but don't throw
            }

            // If Cube returns 400 (table not found), treat as no services yet
            if (!response.ok) {
                if (response.status === 400) {
                    console.warn("No metrics table yet — services list will be empty until first rollup");
                } else {
                    console.warn("fetchServices: Cube returned", response.status);
                }
                return;
            }
            const result = await response.json();
            let data = [];
            if (result.data) data = result.data;
            else if (result.results && result.results[0]) data = result.results[0].data;

            const select = document.getElementById('serviceFilter');
            while (select.options.length > 1) select.remove(1);

            data.forEach(row => {
                const service = row["RequestMetricsMinute.service"];
                if (service && !availableServices.has(service)) {
                    availableServices.add(service);
                    const option = document.createElement("option");
                    option.value = service;
                    option.text = service;
                    select.appendChild(option);
                }
            });
        }

        // --- UI STATE HELPERS ---
        function showError(show) {
            const banner = document.getElementById('errorBanner');
            banner.classList.toggle('visible', show);
            hasConnectionError = show;
        }

        function hasActiveFilters() {
            const service = document.getElementById('serviceFilter').value;
            const dateFrom = document.getElementById('dateFrom').value;
            const dateTo = document.getElementById('dateTo').value;
            const today = new Date().toISOString().split('T')[0];
            // Filters are "active" if a service is selected or dates differ from today
            return !!service || (dateFrom && dateFrom !== today) || (dateTo && dateTo !== today);
        }

        function showEmpty(show) {
            document.getElementById('emptyState').classList.toggle('visible', show);
            document.getElementById('dashboardGrid').style.display = show ? 'none' : 'grid';
            if (show) {
                const filtered = hasActiveFilters();
                document.getElementById('emptyFiltered').style.display = filtered ? 'block' : 'none';
                document.getElementById('emptyOnboard').style.display = filtered ? 'none' : 'block';
            }
        }

        function showSectionError(sectionId, message) {
            let el = document.getElementById(sectionId + '-error');
            if (!el) {
                el = document.createElement('div');
                el.id = sectionId + '-error';
                el.className = 'section-error';
                const container = document.getElementById(sectionId);
                if (container) container.prepend(el);
            }
            el.innerHTML = '<span>' + escapeHtml(message) + '</span>'
                + ' <button class="section-error-retry" onclick="clearSectionError(\'' + sectionId + '\')">Dismiss</button>';
            el.style.display = 'flex';
            // Hide skeleton loaders in this section
            const card = el.closest('.card');
            if (card) card.classList.add('loaded');
        }

        function clearSectionError(sectionId) {
            const el = document.getElementById(sectionId + '-error');
            if (el) el.style.display = 'none';
        }

        function resetFilters() {
            document.getElementById('serviceFilter').value = '';
            document.getElementById('dateFrom').valueAsDate = new Date();
            document.getElementById('dateTo').valueAsDate = new Date();
            currentDrilldownPath = null;
            document.getElementById('default-header').style.display = 'block';
            document.getElementById('drilldown-nav').style.display = 'none';
            saveFiltersToHash();
            onFilterChange();
        }

        function updateFreshnessDisplay() {
            const el = document.getElementById('lastUpdated');
            if (!lastSuccessfulUpdate) {
                el.textContent = 'No data yet';
                el.className = 'last-updated';
                return;
            }
            const ageMs = Date.now() - lastSuccessfulUpdate.getTime();
            const timeStr = lastSuccessfulUpdate.toLocaleTimeString();
            if (ageMs > GRAVIX_CONFIG.staleThresholdMs) {
                el.innerHTML = 'Last updated: ' + timeStr + ' <span class="stale-warning">(stale)</span>';
            } else {
                el.textContent = 'Last updated: ' + timeStr;
                el.className = 'last-updated';
            }
        }

        // --- RENDERING ---
        async function updateDashboard() {
            const service = document.getElementById('serviceFilter').value;
            const dateFrom = document.getElementById('dateFrom').value;
            const dateTo = document.getElementById('dateTo').value;
            const compare = document.getElementById('compareFilter').value;

            const filters = [];
            if (service) {
                filters.push({
                    member: "RequestMetricsMinute.service",
                    operator: "equals",
                    values: [service]
                });
            }
            if (dateFrom && dateTo && dateFrom !== dateTo) {
                // Date range: from start of dateFrom to end of dateTo
                filters.push({
                    member: "RequestMetricsMinute.bucketStart",
                    operator: "gte",
                    values: [dateFrom + "T00:00:00"]
                });
                filters.push({
                    member: "RequestMetricsMinute.bucketStart",
                    operator: "lte",
                    values: [dateTo + "T23:59:59"]
                });
            } else if (dateFrom) {
                // Single day (from only, or from == to)
                filters.push({
                    member: "RequestMetricsMinute.bucketStart",
                    operator: "gte",
                    values: [dateFrom + "T00:00:00"]
                });
                filters.push({
                    member: "RequestMetricsMinute.bucketStart",
                    operator: "lte",
                    values: [dateFrom + "T23:59:59"]
                });
            }
            if (currentDrilldownPath) {
                filters.push({
                    member: "RequestMetricsMinute.pathTemplate",
                    operator: "equals",
                    values: [currentDrilldownPath]
                });
            }

            // Save filter state to URL hash
            saveFiltersToHash();

            // Show skeleton states if first load
            if (!charts['errorRate']) document.getElementById('card-error').classList.remove('loaded');
            if (!charts['latency']) document.getElementById('card-latency').classList.remove('loaded');
            if (!charts['throughput']) document.getElementById('card-throughput').classList.remove('loaded');
            document.getElementById('card-endpoints').classList.remove('loaded');

            const lastUpdatedEl = document.getElementById('lastUpdated');
            lastUpdatedEl.textContent = 'Fetching data...';

            try {
                const [errorData, latencyData, throughputData, endpointData] = await Promise.all([
                    fetchCubeData(["RequestMetricsMinute.errorRate"], filters, compare || null),
                    fetchCubeData(["RequestMetricsMinute.p95Latency"], filters, compare || null),
                    fetchCubeData(["RequestMetricsMinute.requestCount"], filters, compare || null),
                    fetchEndPointsData(filters)
                ]);

                // Connection succeeded — clear any error banner
                showError(false);

                // Check if all data is empty
                const totalData = errorData.length + latencyData.length + throughputData.length + endpointData.length;
                if (totalData === 0) {
                    showEmpty(true);
                    lastSuccessfulUpdate = new Date();
                    updateFreshnessDisplay();
                    return;
                }
                showEmpty(false);

                renderEndpointsTable(endpointData);

                const processSeries = (data, measure) => {
                    const timeKey = data.length > 0 && ("RequestMetricsMinute.bucketStart.hour" in data[0])
                        ? "RequestMetricsMinute.bucketStart.hour" : "RequestMetricsMinute.bucketStart";
                    if (compare) {
                        const current = data.filter(d => d._comparison === "Current");
                        const previous = data.filter(d => d._comparison === "Previous");
                        return {
                            labels: current.map(d => {
                                const v = d[timeKey] || "";
                                return v.includes("T") ? v.substring(11, 16) : (v.split(' ')[1] || v);
                            }),
                            values: {
                                current: current.map(d => d[measure]),
                                previous: previous.map(d => d[measure])
                            }
                        };
                    }
                    return {
                        labels: data.map(d => d[timeKey] || ""),
                        values: data.map(d => d[measure])
                    };
                };

                const errorRes = processSeries(errorData, "RequestMetricsMinute.errorRate");
                const latencyRes = processSeries(latencyData, "RequestMetricsMinute.p95Latency");
                const throughputRes = processSeries(throughputData, "RequestMetricsMinute.requestCount");

                updateChart('errorRate', 'errorRateChart', 'Error Rate', errorRes.labels, errorRes.values, {
                    borderColor: '#ef4444',
                    backgroundColor: 'rgba(239, 68, 68, 0.1)',
                    yMax: 1.0,
                    yTitle: 'Rate'
                });

                updateChart('latency', 'latencyChart', 'Latency (ms)', latencyRes.labels, latencyRes.values, {
                    borderColor: '#3b82f6',
                    backgroundColor: 'rgba(59, 130, 246, 0.1)',
                    yTitle: 'ms'
                });

                updateChart('throughput', 'throughputChart', 'Requests', throughputRes.labels, throughputRes.values, {
                    borderColor: '#22c55e',
                    backgroundColor: 'rgba(34, 197, 94, 0.1)',
                    yTitle: 'count'
                });

                lastSuccessfulUpdate = new Date();
                retryAttempts = 0; // Reset on success
                updateFreshnessDisplay();

            } catch (err) {
                console.error("Dashboard update failed:", err);
                showError(true);
                retryAttempts++;
                const backoffMs = Math.min(REFRESH_INTERVAL_MS * Math.pow(2, retryAttempts - 1), MAX_RETRY_INTERVAL);
                console.log(`Retry #${retryAttempts} in ${backoffMs / 1000}s`);
                setTimeout(updateDashboard, backoffMs);
                updateFreshnessDisplay();
            } finally {
                document.getElementById('card-error').classList.add('loaded');
                document.getElementById('card-latency').classList.add('loaded');
                document.getElementById('card-throughput').classList.add('loaded');
                document.getElementById('card-endpoints').classList.add('loaded');
            }
        }

        function updateChart(key, canvasId, label, labels, data, options) {
            const ctx = document.getElementById(canvasId).getContext('2d');

            const datasets = [];

            // Current Data (Primary)
            const currentGradient = ctx.createLinearGradient(0, 0, 0, 300);
            currentGradient.addColorStop(0, options.backgroundColor);
            currentGradient.addColorStop(1, 'rgba(255, 255, 255, 0)');

            datasets.push({
                label: label + ' (Current)',
                data: data.current || data,
                borderColor: options.borderColor,
                backgroundColor: currentGradient,
                fill: true,
                tension: 0.4,
                pointRadius: 0,
                pointHoverRadius: 6,
                borderWidth: 2
            });

            // Previous Data (Historical - Dashed)
            if (data.previous) {
                datasets.push({
                    label: label + ' (Previous)',
                    data: data.previous,
                    borderColor: '#94a3b8', // Gray
                    backgroundColor: 'transparent',
                    fill: false,
                    tension: 0.4,
                    pointRadius: 0,
                    pointHoverRadius: 4,
                    borderWidth: 1,
                    borderDash: [5, 5]
                });
            }

            if (charts[key]) {
                charts[key].data.labels = labels;
                charts[key].data.datasets = datasets;
                charts[key].update();
            } else {
                charts[key] = new Chart(ctx, {
                    type: 'line',
                    data: {
                        labels: labels,
                        datasets: datasets
                    },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        interaction: {
                            mode: 'index',
                            intersect: false,
                        },
                        scales: {
                            x: {
                                display: true,
                                grid: { display: false },
                                ticks: { maxTicksLimit: 8 }
                            },
                            y: {
                                beginAtZero: true,
                                max: options.yMax,
                                border: { display: false },
                                title: { display: !!options.yTitle, text: options.yTitle }
                            }
                        },
                        plugins: {
                            legend: { display: data.previous ? true : false, position: 'top', align: 'end' }
                        }
                    }
                });
            }
        }

        // --- URL HASH STATE ---
        function saveFiltersToHash() {
            const params = new URLSearchParams();
            const service = document.getElementById('serviceFilter').value;
            const dateFrom = document.getElementById('dateFrom').value;
            const dateTo = document.getElementById('dateTo').value;
            const compare = document.getElementById('compareFilter').value;
            if (service) params.set('service', service);
            if (dateFrom) params.set('from', dateFrom);
            if (dateTo && dateTo !== dateFrom) params.set('to', dateTo);
            if (compare) params.set('compare', compare);
            if (currentDrilldownPath) params.set('path', currentDrilldownPath);
            if (currentPage && currentPage !== 'overview') params.set('page', currentPage);
            if (currentEndpointPath) params.set('endpoint', currentEndpointPath);
            if (currentEndpointMethod) params.set('epmethod', currentEndpointMethod);
            const hash = params.toString();
            if (hash) {
                history.replaceState(null, '', '#' + hash);
            } else {
                history.replaceState(null, '', location.pathname);
            }
        }

        function loadFiltersFromHash() {
            const hash = location.hash.slice(1);
            if (!hash) return;
            const params = new URLSearchParams(hash);
            if (params.has('service')) document.getElementById('serviceFilter').value = params.get('service');
            if (params.has('from')) document.getElementById('dateFrom').value = params.get('from');
            if (params.has('to')) document.getElementById('dateTo').value = params.get('to');
            // Legacy: support old 'date' param
            if (params.has('date') && !params.has('from')) document.getElementById('dateFrom').value = params.get('date');
            if (params.has('compare')) document.getElementById('compareFilter').value = params.get('compare');
            if (params.has('path')) {
                currentDrilldownPath = params.get('path');
                document.getElementById('default-header').style.display = 'none';
                document.getElementById('drilldown-nav').style.display = 'flex';
                document.getElementById('currentServiceTitle').textContent = currentDrilldownPath;
            }
            if (params.has('page')) {
                const page = params.get('page');
                if (page === 'endpoints') {
                    currentPage = 'endpoints';
                    if (params.has('endpoint')) {
                        currentEndpointPath = params.get('endpoint');
                        currentEndpointMethod = params.get('epmethod') || '';
                    }
                } else if (page === 'alerts') {
                    currentPage = 'alerts';
                } else if (page === 'ingestion') {
                    currentPage = 'ingestion';
                }
            }
        }

        // --- INGESTION / DLQ ---
        async function loadIngestionPage() {
            await refreshDLQ();
        }

        async function refreshDLQ() {
            const tbody = document.getElementById('dlq-table-body');
            const emptyEl = document.getElementById('dlq-empty');
            const tableEl = document.getElementById('dlq-table');
            const countLabel = document.getElementById('dlq-count-label');

            tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;padding:24px;">Loading...</td></tr>';
            emptyEl.style.display = 'none';
            tableEl.style.display = '';

            try {
                const data = await gatewayFetch('/api/gateway/dlq?limit=100');

                if (!data.entries || data.entries.length === 0) {
                    tbody.innerHTML = '';
                    tableEl.style.display = 'none';
                    emptyEl.style.display = '';
                    countLabel.textContent = 'No rejected facts';
                    return;
                }

                countLabel.textContent = data.entries.length + ' rejected fact' + (data.entries.length === 1 ? '' : 's');
                renderDLQTable(data.entries);
            } catch (err) {
                console.error('DLQ fetch failed:', err);
                tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;padding:24px;color:var(--text-secondary);">Failed to load DLQ: ' + err.message + '</td></tr>';
            }
        }

        function renderDLQTable(entries) {
            const tbody = document.getElementById('dlq-table-body');
            tbody.innerHTML = '';

            entries.forEach((entry, idx) => {
                const tr = document.createElement('tr');
                const ts = (entry.timestamp || '').substring(0, 19).replace('T', ' ');
                const rawStr = typeof entry.raw_json === 'string'
                    ? entry.raw_json : JSON.stringify(entry.raw_json);
                const truncRaw = rawStr.length > 100 ? rawStr.substring(0, 97) + '...' : rawStr;

                tr.innerHTML = '<td style="white-space:nowrap;font-size:0.85rem;">' + escapeHtml(ts) + '</td>'
                    + '<td style="max-width:300px;overflow:hidden;text-overflow:ellipsis;font-size:0.85rem;" title="' + escapeAttr(entry.error) + '">' + escapeHtml(entry.error) + '</td>'
                    + '<td style="max-width:250px;overflow:hidden;text-overflow:ellipsis;font-family:monospace;font-size:0.8rem;" title="' + escapeAttr(rawStr) + '">' + escapeHtml(truncRaw) + '</td>'
                    + '<td><button class="export-btn" onclick="replayDLQEntry(' + idx + ')" title="Replay this fact">&#x21ba; Replay</button></td>';
                tbody.appendChild(tr);
            });

            // Store for replay
            window._dlqEntries = entries;
        }

        async function replayDLQEntry(idx) {
            const entry = window._dlqEntries && window._dlqEntries[idx];
            if (!entry) return;

            try {
                const result = await gatewayFetch('/api/gateway/dlq/replay', {
                    method: 'POST',
                    body: JSON.stringify({ entries: [{ raw_json: entry.raw_json }] })
                });

                if (result.accepted > 0) {
                    alert('Replay successful: ' + result.accepted + ' fact(s) accepted.');
                    refreshDLQ();
                } else {
                    alert('Replay result: ' + (result.rejected || 0) + ' rejected. ' + (result.errors ? result.errors.join('; ') : ''));
                }
            } catch (err) {
                alert('Replay failed: ' + err.message);
            }
        }

        function escapeHtml(s) {
            const div = document.createElement('div');
            div.appendChild(document.createTextNode(s || ''));
            return div.innerHTML;
        }

        function escapeAttr(s) {
            return (s || '').replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
        }

        // --- INIT ---
        document.getElementById('dateFrom').valueAsDate = new Date();
        document.getElementById('dateTo').valueAsDate = new Date();

        function initDashboard() {
            loadFiltersFromHash(); // Restore from URL before first fetch
            fetchServices()
                .then(() => {
                    loadFiltersFromHash(); // Re-apply after services list is populated

                    // Restore page from hash state
                    if (currentPage === 'endpoints') {
                        switchPage('endpoints');
                        if (currentEndpointPath) {
                            showEndpointDetail(currentEndpointPath, currentEndpointMethod);
                        }
                    } else if (currentPage === 'alerts') {
                        switchPage('alerts');
                    } else {
                        updateDashboard();
                    }

                    // Start auto-refresh only after dashboard is initialized
                    setInterval(() => {
                        if (currentPage === 'overview') updateDashboard();
                        else if (currentPage === 'endpoints' && !currentEndpointPath) updateEndpointsPage();
                    }, REFRESH_INTERVAL_MS);
                    setInterval(updateFreshnessDisplay, 30000);
                })
                .catch(() => {
                    showError(true);
                    document.getElementById('lastUpdated').textContent = 'Connection failed';
                });
        }

        // Check if auth is required, show gate or proceed
        (async function() {
            const needsAuth = await checkAuthRequired();
            if (needsAuth && !dashboardApiToken) {
                showAuthGate();
            } else if (needsAuth && dashboardApiToken) {
                // Validate stored token
                const valid = await tryAuth(dashboardApiToken);
                if (valid) {
                    initDashboard();
                } else {
                    sessionStorage.removeItem('gravix_token');
                    dashboardApiToken = '';
                    showAuthGate();
                }
            } else {
                initDashboard();
            }
        })();

        // Listeners — dispatch to active page
        function onFilterChange() {
            if (currentPage === 'endpoints') {
                if (currentEndpointPath) {
                    showEndpointDetail(currentEndpointPath, currentEndpointMethod);
                } else {
                    updateEndpointsPage();
                }
            } else {
                updateDashboard();
            }
        }
        document.getElementById('serviceFilter').addEventListener('change', onFilterChange);
        document.getElementById('dateFrom').addEventListener('change', onFilterChange);
        document.getElementById('dateTo').addEventListener('change', onFilterChange);
        document.getElementById('compareFilter').addEventListener('change', onFilterChange);

        async function fetchEndPointsData(filters) {
            const query = {
                measures: ["RequestMetricsMinute.requestCount", "RequestMetricsMinute.errorCount", "RequestMetricsMinute.errorRate"],
                dimensions: ["RequestMetricsMinute.pathTemplate", "RequestMetricsMinute.method"],
                order: { "RequestMetricsMinute.errorCount": "desc" },
                filters: filters,
                limit: 10
            };

            const response = await fetch(CUBE_API_URL, {
                method: "POST",
                headers: cubeHeaders(),
                body: JSON.stringify({ query: query })
            });
            const result = await response.json();
            return (result.results && result.results[0]) ? result.results[0].data : (result.data || []);
        }

        function renderEndpointsTable(data) {
            const body = document.getElementById('endpoints-body');
            body.innerHTML = '';

            data.forEach(row => {
                const tr = document.createElement('tr');
                const errorRate = (row["RequestMetricsMinute.errorRate"] * 100).toFixed(2);
                const rateBadge = row["RequestMetricsMinute.errorRate"] > 0.01 ? 'badge-error' : 'badge-success';

                const pathVal = escapeHtml(row["RequestMetricsMinute.pathTemplate"]);
                const methodVal = escapeHtml(row["RequestMetricsMinute.method"]);
                const reqCount = escapeHtml(row["RequestMetricsMinute.requestCount"]);
                const errCount = escapeHtml(row["RequestMetricsMinute.errorCount"]);
                tr.innerHTML = `
                    <td><a href="#" class="path-link">${pathVal}</a></td>
                    <td><code>${methodVal}</code></td>
                    <td>${reqCount}</td>
                    <td>${errCount}</td>
                    <td><span class="${rateBadge}">${errorRate}%</span></td>
                `;
                // Set data-path safely via DOM API to prevent attribute injection
                tr.querySelector('.path-link').dataset.path = row["RequestMetricsMinute.pathTemplate"];
                body.appendChild(tr);
            });

            // Add drilldown listeners
            document.querySelectorAll('.path-link').forEach(link => {
                link.onclick = (e) => {
                    e.preventDefault();
                    drillDownToPath(e.target.dataset.path);
                };
            });
        }

        function drillDownToPath(path) {
            const serviceFilter = document.getElementById('serviceFilter');
            const currentService = serviceFilter.value || "All Services";

            document.getElementById('default-header').style.display = 'none';
            document.getElementById('drilldown-nav').style.display = 'flex';
            document.getElementById('currentServiceTitle').textContent = `${currentService} > ${path}`;

            currentDrilldownPath = path;
            updateDashboard();
        }

        let currentDrilldownPath = null;

        document.getElementById('backToServices').onclick = () => {
            document.getElementById('default-header').style.display = 'block';
            document.getElementById('drilldown-nav').style.display = 'none';
            currentDrilldownPath = null;
            updateDashboard();
        };

        // --- SERVICE EVENTS TIMELINE ---
        const EVENT_TYPE_COLORS = {
            deploy_started: '#22c55e',
            deploy_completed: '#16a34a',
            restart: '#f97316',
            scale_up: '#3b82f6',
            scale_down: '#6366f1',
            health_check_failed: '#ef4444'
        };
        const DEFAULT_EVENT_COLOR = '#94a3b8';

        function getEventColor(eventType) {
            return EVENT_TYPE_COLORS[eventType] || DEFAULT_EVENT_COLOR;
        }

        function getEventBadgeClass(eventType) {
            if (eventType.includes('deploy')) return 'badge-success';
            if (eventType.includes('fail') || eventType === 'restart') return 'badge-error';
            return 'badge-info';
        }

        let eventsLogData = [];
        let eventsLogOffset = 0;
        const EVENTS_PAGE_SIZE = 25;
        let deployAnnotations = []; // timestamps for chart annotations

        // Build event-specific filters (uses ServiceEvents model with time dimension)
        function buildEventFilters() {
            const filters = [];
            const service = document.getElementById('serviceFilter').value;
            const dateFrom = document.getElementById('dateFrom').value;
            const dateTo = document.getElementById('dateTo').value;
            if (service) {
                filters.push({ member: "ServiceEvents.service", operator: "equals", values: [service] });
            }
            if (dateFrom && dateTo && dateFrom !== dateTo) {
                filters.push({ member: "ServiceEvents.eventTime", operator: "gte", values: [dateFrom + "T00:00:00"] });
                filters.push({ member: "ServiceEvents.eventTime", operator: "lte", values: [dateTo + "T23:59:59"] });
            } else if (dateFrom) {
                filters.push({ member: "ServiceEvents.eventTime", operator: "gte", values: [dateFrom + "T00:00:00"] });
                filters.push({ member: "ServiceEvents.eventTime", operator: "lte", values: [dateFrom + "T23:59:59"] });
            }
            return filters;
        }

        // Fetch timeline data: event counts grouped by hour and event type
        async function fetchEventsTimeline() {
            const filters = buildEventFilters();
            const query = {
                measures: ["ServiceEvents.count"],
                timeDimensions: [{
                    dimension: "ServiceEvents.eventTime",
                    granularity: "hour"
                }],
                dimensions: ["ServiceEvents.eventType"],
                order: { "ServiceEvents.eventTime": "asc" },
                filters: filters,
                limit: 5000
            };
            const response = await fetch(CUBE_API_URL, {
                method: "POST",
                headers: cubeHeaders(),
                body: JSON.stringify({ query: query })
            });
            if (!response.ok) return [];
            const result = await response.json();
            return (result.results && result.results[0]) ? result.results[0].data : (result.data || []);
        }

        // Fetch individual events for the event log
        async function fetchEventLog(offset, limit, eventTypeFilter) {
            const filters = buildEventFilters();
            if (eventTypeFilter) {
                filters.push({ member: "ServiceEvents.eventType", operator: "equals", values: [eventTypeFilter] });
            }
            const query = {
                dimensions: [
                    "ServiceEvents.eventTime", "ServiceEvents.service",
                    "ServiceEvents.eventType", "ServiceEvents.entityId",
                    "ServiceEvents.message"
                ],
                order: { "ServiceEvents.eventTime": "desc" },
                filters: filters,
                offset: offset,
                limit: limit
            };
            const response = await fetch(CUBE_API_URL, {
                method: "POST",
                headers: cubeHeaders(),
                body: JSON.stringify({ query: query })
            });
            if (!response.ok) return [];
            const result = await response.json();
            return (result.results && result.results[0]) ? result.results[0].data : (result.data || []);
        }

        // Fetch deploy events for chart annotations
        async function fetchDeployAnnotations() {
            const filters = buildEventFilters();
            filters.push({ member: "ServiceEvents.eventType", operator: "contains", values: ["deploy"] });
            const query = {
                dimensions: ["ServiceEvents.eventTime", "ServiceEvents.service", "ServiceEvents.eventType"],
                order: { "ServiceEvents.eventTime": "asc" },
                filters: filters,
                limit: 100
            };
            const response = await fetch(CUBE_API_URL, {
                method: "POST",
                headers: cubeHeaders(),
                body: JSON.stringify({ query: query })
            });
            if (!response.ok) return [];
            const result = await response.json();
            return (result.results && result.results[0]) ? result.results[0].data : (result.data || []);
        }

        // Render stacked bar chart for events timeline
        function renderEventsTimeline(data) {
            const wrapper = document.getElementById('events-timeline-wrapper');
            const empty = document.getElementById('events-empty');

            if (!data || data.length === 0) {
                wrapper.style.display = 'none';
                empty.style.display = 'block';
                return;
            }
            wrapper.style.display = 'block';
            empty.style.display = 'none';

            // Group data by time bucket, collect all event types
            const buckets = {};
            const eventTypes = new Set();
            data.forEach(row => {
                const time = row["ServiceEvents.eventTime.hour"] || row["ServiceEvents.eventTime"];
                const type = row["ServiceEvents.eventType"];
                const count = parseInt(row["ServiceEvents.count"], 10) || 0;
                eventTypes.add(type);
                if (!buckets[time]) buckets[time] = {};
                buckets[time][type] = (buckets[time][type] || 0) + count;
            });

            const labels = Object.keys(buckets).sort();
            const displayLabels = labels.map(l => {
                const d = new Date(l);
                return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
            });

            const datasets = Array.from(eventTypes).sort().map(type => ({
                label: type.replace(/_/g, ' '),
                data: labels.map(l => buckets[l][type] || 0),
                backgroundColor: getEventColor(type),
                borderRadius: 3
            }));

            const ctx = document.getElementById('eventsTimelineChart').getContext('2d');

            if (charts['eventsTimeline']) {
                charts['eventsTimeline'].data.labels = displayLabels;
                charts['eventsTimeline'].data.datasets = datasets;
                charts['eventsTimeline'].update();
            } else {
                charts['eventsTimeline'] = new Chart(ctx, {
                    type: 'bar',
                    data: { labels: displayLabels, datasets: datasets },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        interaction: { mode: 'index', intersect: false },
                        scales: {
                            x: { stacked: true, grid: { display: false }, ticks: { maxTicksLimit: 12 } },
                            y: { stacked: true, beginAtZero: true, border: { display: false }, title: { display: true, text: 'Events' } }
                        },
                        plugins: {
                            legend: { display: true, position: 'top', align: 'end', labels: { boxWidth: 12, padding: 8, font: { size: 11 } } }
                        }
                    }
                });
            }
        }

        // Render event log table
        function renderEventLog(data, append) {
            const table = document.getElementById('events-table');
            const body = document.getElementById('events-body');
            const logSection = document.getElementById('events-log-section');
            const countLabel = document.getElementById('events-log-count');
            const loadMore = document.getElementById('events-load-more');

            if (!append) body.innerHTML = '';

            if (!data || data.length === 0) {
                if (!append) {
                    logSection.style.display = 'none';
                }
                loadMore.style.display = 'none';
                return;
            }

            logSection.style.display = 'block';

            data.forEach(row => {
                const tr = document.createElement('tr');
                const eventType = row["ServiceEvents.eventType"] || "unknown";
                const badgeClass = getEventBadgeClass(eventType);
                const eventTime = new Date(row["ServiceEvents.eventTime"]);
                const timeStr = eventTime.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' });
                tr.innerHTML = `
                    <td style="white-space: nowrap; font-size: 0.8rem; color: var(--text-secondary);">${timeStr}</td>
                    <td><strong>${escapeHtml(row["ServiceEvents.service"] || '')}</strong></td>
                    <td><span class="${badgeClass}">${escapeHtml(eventType)}</span></td>
                    <td style="font-size: 0.85rem;">${escapeHtml(row["ServiceEvents.entityId"] || '-')}</td>
                    <td style="font-size: 0.85rem; max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;" title="${escapeHtml(row["ServiceEvents.message"] || '')}">${escapeHtml(row["ServiceEvents.message"] || '-')}</td>
                `;
                body.appendChild(tr);
            });

            const totalRows = body.children.length;
            countLabel.textContent = totalRows + ' event' + (totalRows !== 1 ? 's' : '') + ' shown';

            // Show load more if we got a full page
            loadMore.style.display = data.length >= EVENTS_PAGE_SIZE ? 'block' : 'none';
        }

        // Populate event type filter dropdown from observed data
        function populateEventTypeFilter(data) {
            const select = document.getElementById('eventTypeFilter');
            const current = select.value;
            const types = new Set();
            data.forEach(row => {
                const t = row["ServiceEvents.eventType"];
                if (t) types.add(t);
            });
            select.innerHTML = '<option value="">All Types</option>';
            Array.from(types).sort().forEach(t => {
                const opt = document.createElement('option');
                opt.value = t;
                opt.textContent = t.replace(/_/g, ' ');
                select.appendChild(opt);
            });
            if (current) select.value = current;
        }

        // Chart.js plugin for deploy annotations (vertical lines on metric charts)
        const deployAnnotationPlugin = {
            id: 'deployAnnotations',
            afterDraw(chart) {
                const events = chart.options.plugins.deployAnnotations?.events;
                if (!events || events.length === 0) return;

                const ctx = chart.ctx;
                const xScale = chart.scales.x;
                const yScale = chart.scales.y;
                const labels = chart.data.labels || [];

                events.forEach(evt => {
                    // Find closest label index for this event time
                    const evtTime = evt.time;
                    let closestIdx = -1;
                    let closestDist = Infinity;
                    labels.forEach((label, idx) => {
                        // Labels may be time strings; try to match by substring
                        if (label && evtTime.includes(label)) {
                            closestIdx = idx;
                            closestDist = 0;
                        } else if (closestIdx === -1) {
                            // Fallback: try date parsing
                            const labelTime = new Date(label).getTime();
                            const evtTimeMs = new Date(evtTime).getTime();
                            const dist = Math.abs(labelTime - evtTimeMs);
                            if (dist < closestDist) {
                                closestDist = dist;
                                closestIdx = idx;
                            }
                        }
                    });

                    if (closestIdx < 0) return;

                    const x = xScale.getPixelForValue(closestIdx);
                    const topY = yScale.top;
                    const bottomY = yScale.bottom;

                    // Draw vertical dashed line
                    ctx.save();
                    ctx.beginPath();
                    ctx.setLineDash([4, 4]);
                    ctx.strokeStyle = 'rgba(34, 197, 94, 0.6)';
                    ctx.lineWidth = 1.5;
                    ctx.moveTo(x, topY);
                    ctx.lineTo(x, bottomY);
                    ctx.stroke();

                    // Draw small triangle marker at top
                    ctx.setLineDash([]);
                    ctx.fillStyle = '#22c55e';
                    ctx.beginPath();
                    ctx.moveTo(x - 5, topY);
                    ctx.lineTo(x + 5, topY);
                    ctx.lineTo(x, topY + 8);
                    ctx.closePath();
                    ctx.fill();
                    ctx.restore();
                });
            }
        };
        Chart.register(deployAnnotationPlugin);

        // Full events refresh: timeline + log + annotations
        async function refreshServiceEvents() {
            document.getElementById('card-events').classList.remove('loaded');
            try {
                const [timelineData, logData, annotationData] = await Promise.all([
                    fetchEventsTimeline(),
                    fetchEventLog(0, EVENTS_PAGE_SIZE, document.getElementById('eventTypeFilter').value),
                    fetchDeployAnnotations()
                ]);

                renderEventsTimeline(timelineData);

                eventsLogData = logData;
                eventsLogOffset = logData.length;
                renderEventLog(logData, false);
                populateEventTypeFilter(timelineData);

                // Store deploy annotations for chart overlay
                deployAnnotations = annotationData.map(row => ({
                    time: row["ServiceEvents.eventTime"],
                    service: row["ServiceEvents.service"],
                    type: row["ServiceEvents.eventType"]
                }));

                // Update existing metric charts with annotation data
                updateChartAnnotations();

            } catch (err) {
                console.warn("Service events fetch failed:", err);
                renderEventsTimeline([]);
                renderEventLog([], false);
                deployAnnotations = [];
            } finally {
                document.getElementById('card-events').classList.add('loaded');
            }
        }

        // Apply deploy annotations to all metric charts
        function updateChartAnnotations() {
            ['errorRate', 'latency', 'throughput'].forEach(key => {
                if (charts[key]) {
                    charts[key].options.plugins.deployAnnotations = { events: deployAnnotations };
                    charts[key].update('none'); // Update without animation
                }
            });
        }

        // Extend updateDashboard to also fetch events
        const _originalUpdateDashboard = updateDashboard;
        updateDashboard = async function() {
            await _originalUpdateDashboard();
            await refreshServiceEvents();
        };

        // Event type filter change handler
        document.getElementById('eventTypeFilter').addEventListener('change', async () => {
            const typeFilter = document.getElementById('eventTypeFilter').value;
            const logData = await fetchEventLog(0, EVENTS_PAGE_SIZE, typeFilter);
            eventsLogData = logData;
            eventsLogOffset = logData.length;
            renderEventLog(logData, false);
        });

        // Load more button
        document.getElementById('loadMoreEventsBtn').addEventListener('click', async () => {
            const typeFilter = document.getElementById('eventTypeFilter').value;
            const moreData = await fetchEventLog(eventsLogOffset, EVENTS_PAGE_SIZE, typeFilter);
            eventsLogOffset += moreData.length;
            renderEventLog(moreData, true);
        });

        // Auto-refresh intervals are now started inside initDashboard() to avoid
        // firing during the auth gate.

        // --- RESET FILTERS BUTTON ---
        document.getElementById('resetFiltersBtn').addEventListener('click', resetFilters);

        // --- NAV LINKS ---
        document.querySelectorAll('.nav-link').forEach(link => {
            link.addEventListener('click', (e) => {
                e.preventDefault();
                if (link.classList.contains('disabled')) return;
                switchPage(link.dataset.page);
            });
        });

        // --- ENDPOINTS PAGE: sort headers, search, back button ---
        document.querySelectorAll('#all-endpoints-table .sortable-header').forEach(th => {
            th.addEventListener('click', () => {
                const col = th.dataset.sort;
                if (endpointsSortColumn === col) {
                    endpointsSortDir = endpointsSortDir === 'asc' ? 'desc' : 'asc';
                } else {
                    endpointsSortColumn = col;
                    endpointsSortDir = 'desc';
                }
                renderAllEndpointsTable(allEndpointsData);
            });
        });

        document.getElementById('endpointSearch').addEventListener('input', () => {
            renderAllEndpointsTable(allEndpointsData);
        });

        document.getElementById('backToEndpoints').addEventListener('click', () => {
            document.getElementById('endpoint-detail').style.display = 'none';
            document.getElementById('endpoints-list').style.display = 'block';
            currentEndpointPath = null;
            currentEndpointMethod = null;
            saveFiltersToHash();
        });

        // --- SIDEBAR TOGGLE (mobile) ---
        document.getElementById('sidebarToggle').addEventListener('click', () => {
            document.getElementById('sidebar').classList.toggle('open');
            document.getElementById('sidebarBackdrop').classList.toggle('open');
        });
        document.getElementById('sidebarBackdrop').addEventListener('click', () => {
            document.getElementById('sidebar').classList.remove('open');
            document.getElementById('sidebarBackdrop').classList.remove('open');
        });

        // --- THEME TOGGLE ---
        document.getElementById('themeToggle').addEventListener('click', toggleTheme);

        // --- DATA EXPORT ---
        function downloadBlob(filename, blob) {
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = filename;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
        }

        function exportTableCSV(tableId) {
            const table = document.getElementById(tableId);
            if (!table) return;
            const rows = [];
            table.querySelectorAll('tr').forEach(tr => {
                const cells = [];
                tr.querySelectorAll('th, td').forEach(td => {
                    let text = td.textContent.trim().replace(/"/g, '""');
                    if (text.includes(',') || text.includes('"') || text.includes('\n')) {
                        text = '"' + text + '"';
                    }
                    cells.push(text);
                });
                if (cells.length > 0) rows.push(cells.join(','));
            });
            const csv = rows.join('\n');
            downloadBlob(tableId + '-export.csv', new Blob([csv], { type: 'text/csv' }));
        }

        function exportChartPNG(chartKey, filename) {
            const chart = charts[chartKey];
            if (!chart) return;
            const url = chart.toBase64Image('image/png', 1);
            const a = document.createElement('a');
            a.href = url;
            a.download = (filename || chartKey) + '.png';
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
        }

        // === ALERTS PAGE ===
        // Gate alerts nav link — only available in multi-tenant mode
        if (!IS_MULTI_TENANT) {
            const navAlerts = document.getElementById('nav-alerts');
            if (navAlerts) {
                navAlerts.classList.add('disabled');
                navAlerts.title = 'Alerts require multi-tenant mode';
            }
        }

        // Alerts state
        let alertRulesData = [];
        let channelsData = [];
        let alertHistoryData = [];

        // --- Alerts sub-tab switching ---
        document.querySelectorAll('.alerts-tab').forEach(tab => {
            tab.addEventListener('click', () => {
                document.querySelectorAll('.alerts-tab').forEach(t => t.classList.remove('active'));
                tab.classList.add('active');
                const target = tab.dataset.alertsTab;
                document.getElementById('alerts-tab-rules').style.display = target === 'rules' ? 'block' : 'none';
                document.getElementById('alerts-tab-channels').style.display = target === 'channels' ? 'block' : 'none';
                document.getElementById('alerts-tab-history').style.display = target === 'history' ? 'block' : 'none';
            });
        });

        // --- Helper: gateway fetch with auth ---
        async function gatewayFetch(path, options = {}) {
            const headers = Object.assign({
                'Authorization': 'Bearer ' + dashboardApiToken,
                'Content-Type': 'application/json'
            }, options.headers || {});
            const resp = await fetch(GATEWAY_URL + path, Object.assign({}, options, { headers }));
            if (!resp.ok) {
                const body = await resp.json().catch(() => ({}));
                throw new Error(body.error || 'Request failed: ' + resp.status);
            }
            return resp.json();
        }

        // --- Load functions ---
        async function loadChannels() {
            try {
                const data = await gatewayFetch('/api/gateway/channels');
                channelsData = data.channels || [];
                renderChannelsTable();
            } catch (err) {
                console.warn('Failed to load channels:', err);
                channelsData = [];
                renderChannelsTable();
            }
        }

        async function loadAlertRules() {
            try {
                const data = await gatewayFetch('/api/gateway/alert-rules');
                alertRulesData = data.rules || [];
                renderAlertRulesTable();
            } catch (err) {
                console.warn('Failed to load alert rules:', err);
                alertRulesData = [];
                renderAlertRulesTable();
            }
        }

        async function loadAlertHistory() {
            try {
                const data = await gatewayFetch('/api/gateway/alert-history');
                alertHistoryData = data.history || [];
                renderAlertHistoryTable();
            } catch (err) {
                console.warn('Failed to load alert history:', err);
                alertHistoryData = [];
                renderAlertHistoryTable();
            }
        }

        function loadAlertsPage() {
            loadChannels();
            loadAlertRules();
            loadAlertHistory();
        }

        // --- Render functions ---
        function renderAlertRulesTable() {
            const body = document.getElementById('alert-rules-body');
            body.innerHTML = '';
            document.getElementById('rules-count-label').textContent = alertRulesData.length + ' rule' + (alertRulesData.length !== 1 ? 's' : '');

            if (alertRulesData.length === 0) {
                body.innerHTML = '<tr><td colspan="8" style="text-align:center;color:var(--text-secondary);padding:32px;">No alert rules configured</td></tr>';
                return;
            }

            // Build a channel name lookup
            const channelNames = {};
            channelsData.forEach(ch => { channelNames[ch.ID] = ch.Name; });

            alertRulesData.forEach(rule => {
                const tr = document.createElement('tr');
                let conditionText;
                if (rule.Operator === 'anomaly') {
                    conditionText = 'Anomaly (' + rule.Threshold + '\u03C3, ' + rule.WindowMinutes + 'd)';
                } else {
                    const opSymbol = rule.Operator === 'gt' ? '>' : '<';
                    conditionText = opSymbol + ' ' + rule.Threshold;
                }
                const channelName = channelNames[rule.ChannelID] || rule.ChannelID;
                const statusClass = rule.Status === 'active' ? 'status-active' : 'status-paused';
                const lastTriggered = rule.LastTriggeredAt
                    ? new Date(rule.LastTriggeredAt).toLocaleString()
                    : 'Never';
                const toggleLabel = rule.Status === 'active' ? 'Pause' : 'Resume';

                tr.innerHTML = `
                    <td>${escapeHtml(rule.Name)}</td>
                    <td>${escapeHtml(rule.Metric)}</td>
                    <td>${conditionText}</td>
                    <td>${escapeHtml(rule.Service || 'All')}</td>
                    <td>${escapeHtml(channelName)}</td>
                    <td><span class="status-badge ${statusClass}">${rule.Status}</span></td>
                    <td>${lastTriggered}</td>
                    <td>
                        <button class="ep-back-btn" onclick="toggleRuleStatus('${rule.ID}', '${rule.Status}')" style="padding:4px 10px;font-size:0.8rem;margin-right:4px;">${toggleLabel}</button>
                        <button class="ep-back-btn" onclick="deleteAlertRule('${rule.ID}')" style="padding:4px 10px;font-size:0.8rem;color:#ef4444;">Delete</button>
                    </td>
                `;
                body.appendChild(tr);
            });
        }

        function renderChannelsTable() {
            const body = document.getElementById('channels-body');
            body.innerHTML = '';
            document.getElementById('channels-count-label').textContent = channelsData.length + ' channel' + (channelsData.length !== 1 ? 's' : '');

            if (channelsData.length === 0) {
                body.innerHTML = '<tr><td colspan="5" style="text-align:center;color:var(--text-secondary);padding:32px;">No notification channels configured</td></tr>';
                return;
            }

            channelsData.forEach(ch => {
                const tr = document.createElement('tr');
                const typeLabel = ch.Type === 'slack' ? '🔔 Slack' : '🌐 Webhook';
                const createdAt = new Date(ch.CreatedAt).toLocaleDateString();
                tr.innerHTML = `
                    <td>${escapeHtml(ch.Name)}</td>
                    <td>${typeLabel}</td>
                    <td><span class="status-badge status-active">${ch.Status}</span></td>
                    <td>${createdAt}</td>
                    <td>
                        <button class="ep-back-btn" onclick="testChannel('${ch.ID}')" style="padding:4px 10px;font-size:0.8rem;margin-right:4px;">Test</button>
                        <button class="ep-back-btn" onclick="deleteChannel('${ch.ID}')" style="padding:4px 10px;font-size:0.8rem;color:#ef4444;">Delete</button>
                    </td>
                `;
                body.appendChild(tr);
            });
        }

        function renderAlertHistoryTable() {
            const body = document.getElementById('alert-history-body');
            body.innerHTML = '';
            document.getElementById('history-count-label').textContent = alertHistoryData.length + ' event' + (alertHistoryData.length !== 1 ? 's' : '');

            if (alertHistoryData.length === 0) {
                body.innerHTML = '<tr><td colspan="7" style="text-align:center;color:var(--text-secondary);padding:32px;">No alerts have fired yet</td></tr>';
                return;
            }

            // Build rule name lookup from loaded rules
            const ruleNames = {};
            alertRulesData.forEach(r => { ruleNames[r.ID] = r.Name; });

            alertHistoryData.forEach(entry => {
                const tr = document.createElement('tr');
                const time = new Date(entry.CreatedAt).toLocaleString();
                const statusClass = entry.Status === 'fired' ? 'status-fired' : (entry.Status === 'error' ? 'status-error' : 'status-active');
                const ruleName = ruleNames[entry.RuleID] || entry.RuleID;
                tr.innerHTML = `
                    <td>${time}</td>
                    <td>${escapeHtml(ruleName)}</td>
                    <td>${escapeHtml(entry.Metric)}</td>
                    <td>${entry.ActualValue}</td>
                    <td>${entry.Threshold}</td>
                    <td>${escapeHtml(entry.Service || 'All')}</td>
                    <td><span class="status-badge ${statusClass}">${entry.Status}</span></td>
                `;
                body.appendChild(tr);
            });
        }

        // --- CRUD functions ---
        async function createChannel(name, type, webhookUrl, authHeader) {
            const config = { webhook_url: webhookUrl };
            if (authHeader) config.auth_header = authHeader;
            await gatewayFetch('/api/gateway/channels', {
                method: 'POST',
                body: JSON.stringify({ name, type, config })
            });
            await loadChannels();
            // Refresh channel select in rule modal
            populateChannelSelect();
        }

        async function deleteChannel(id) {
            if (!confirm('Delete this notification channel? Any rules using it will stop working.')) return;
            try {
                await gatewayFetch('/api/gateway/channels/' + id, { method: 'DELETE' });
                await loadChannels();
                populateChannelSelect();
            } catch (err) {
                alert('Failed to delete channel: ' + err.message);
            }
        }

        async function testChannel(id) {
            try {
                await gatewayFetch('/api/gateway/channels/' + id + '/test', { method: 'POST' });
                alert('Test notification sent successfully!');
            } catch (err) {
                alert('Test failed: ' + err.message);
            }
        }

        async function createAlertRule(data) {
            await gatewayFetch('/api/gateway/alert-rules', {
                method: 'POST',
                body: JSON.stringify(data)
            });
            await loadAlertRules();
        }

        async function deleteAlertRule(id) {
            if (!confirm('Delete this alert rule and its history?')) return;
            try {
                await gatewayFetch('/api/gateway/alert-rules/' + id, { method: 'DELETE' });
                await loadAlertRules();
                await loadAlertHistory();
            } catch (err) {
                alert('Failed to delete rule: ' + err.message);
            }
        }

        async function toggleRuleStatus(id, currentStatus) {
            const rule = alertRulesData.find(r => r.ID === id);
            if (!rule) return;
            const newStatus = currentStatus === 'active' ? 'paused' : 'active';
            try {
                await gatewayFetch('/api/gateway/alert-rules/' + id, {
                    method: 'PUT',
                    body: JSON.stringify({
                        name: rule.Name,
                        metric: rule.Metric,
                        operator: rule.Operator,
                        threshold: rule.Threshold,
                        window_minutes: rule.WindowMinutes,
                        service: rule.Service,
                        path_template: rule.PathTemplate,
                        channel_id: rule.ChannelID,
                        cooldown_minutes: rule.CooldownMinutes,
                        status: newStatus
                    })
                });
                await loadAlertRules();
            } catch (err) {
                alert('Failed to update rule: ' + err.message);
            }
        }

        // --- Channel select helper ---
        function populateChannelSelect() {
            const select = document.getElementById('ruleChannelSelect');
            select.innerHTML = '';
            if (channelsData.length === 0) {
                select.innerHTML = '<option value="">No channels — create one first</option>';
                return;
            }
            channelsData.forEach(ch => {
                const opt = document.createElement('option');
                opt.value = ch.ID;
                opt.textContent = ch.Name + ' (' + ch.Type + ')';
                select.appendChild(opt);
            });
        }

        // --- Modal logic ---

        // Toggle anomaly vs threshold fields based on operator selection
        function toggleAnomalyFields() {
            const op = document.getElementById('ruleOperatorSelect').value;
            document.getElementById('thresholdFields').style.display = op === 'anomaly' ? 'none' : 'flex';
            document.getElementById('anomalyFields').style.display = op === 'anomaly' ? '' : 'none';
        }
        document.getElementById('ruleOperatorSelect').addEventListener('change', toggleAnomalyFields);

        // Rule modal
        document.getElementById('createRuleBtn').addEventListener('click', () => {
            document.getElementById('rule-modal-title').textContent = 'New Alert Rule';
            document.getElementById('ruleNameInput').value = '';
            document.getElementById('ruleMetricSelect').value = 'error_rate';
            document.getElementById('ruleOperatorSelect').value = 'gt';
            document.getElementById('ruleThresholdInput').value = '';
            document.getElementById('ruleWindowSelect').value = '5';
            document.getElementById('ruleCooldownSelect').value = '15';
            document.getElementById('ruleServiceInput').value = '';
            document.getElementById('rulePathInput').value = '';
            document.getElementById('anomalyStddevInput').value = '2.0';
            document.getElementById('anomalyLookbackSelect').value = '7';
            toggleAnomalyFields();
            populateChannelSelect();
            document.getElementById('rule-modal').style.display = 'flex';
        });

        document.getElementById('ruleModalCancel').addEventListener('click', () => {
            document.getElementById('rule-modal').style.display = 'none';
        });

        document.getElementById('ruleModalSave').addEventListener('click', async () => {
            const name = document.getElementById('ruleNameInput').value.trim();
            const metric = document.getElementById('ruleMetricSelect').value;
            const operator = document.getElementById('ruleOperatorSelect').value;
            const cooldownMinutes = parseInt(document.getElementById('ruleCooldownSelect').value, 10);
            const service = document.getElementById('ruleServiceInput').value.trim();
            const pathTemplate = document.getElementById('rulePathInput').value.trim();
            const channelId = document.getElementById('ruleChannelSelect').value;

            let threshold, windowMinutes;
            if (operator === 'anomaly') {
                threshold = parseFloat(document.getElementById('anomalyStddevInput').value);
                windowMinutes = parseInt(document.getElementById('anomalyLookbackSelect').value, 10);
                if (isNaN(threshold) || threshold <= 0) { alert('Stddev multiplier must be > 0'); return; }
            } else {
                threshold = parseFloat(document.getElementById('ruleThresholdInput').value);
                windowMinutes = parseInt(document.getElementById('ruleWindowSelect').value, 10);
                if (isNaN(threshold) || threshold < 0) { alert('Threshold must be a non-negative number'); return; }
            }

            if (!name) { alert('Name is required'); return; }
            if (!channelId) { alert('Please select a notification channel'); return; }

            try {
                await createAlertRule({
                    name, metric, operator, threshold,
                    window_minutes: windowMinutes,
                    cooldown_minutes: cooldownMinutes,
                    service, path_template: pathTemplate,
                    channel_id: channelId
                });
                document.getElementById('rule-modal').style.display = 'none';
            } catch (err) {
                alert('Failed to create rule: ' + err.message);
            }
        });

        // Close rule modal on overlay click
        document.getElementById('rule-modal').addEventListener('click', (e) => {
            if (e.target === e.currentTarget) {
                document.getElementById('rule-modal').style.display = 'none';
            }
        });

        // Channel modal
        document.getElementById('createChannelBtn').addEventListener('click', () => {
            document.getElementById('channelNameInput').value = '';
            document.getElementById('channelTypeSelect').value = 'slack';
            document.getElementById('channelWebhookInput').value = '';
            document.getElementById('channelAuthInput').value = '';
            document.getElementById('channelAuthLabel').style.display = 'none';
            document.getElementById('channel-modal').style.display = 'flex';
        });

        document.getElementById('channelModalCancel').addEventListener('click', () => {
            document.getElementById('channel-modal').style.display = 'none';
        });

        // Show/hide auth header field based on channel type
        document.getElementById('channelTypeSelect').addEventListener('change', () => {
            const isWebhook = document.getElementById('channelTypeSelect').value === 'webhook';
            document.getElementById('channelAuthLabel').style.display = isWebhook ? 'block' : 'none';
        });

        document.getElementById('channelModalSave').addEventListener('click', async () => {
            const name = document.getElementById('channelNameInput').value.trim();
            const type = document.getElementById('channelTypeSelect').value;
            const webhookUrl = document.getElementById('channelWebhookInput').value.trim();
            const authHeader = document.getElementById('channelAuthInput').value.trim();

            if (!name) { alert('Name is required'); return; }
            if (!webhookUrl) { alert('Webhook URL is required'); return; }

            try {
                await createChannel(name, type, webhookUrl, authHeader);
                document.getElementById('channel-modal').style.display = 'none';
            } catch (err) {
                alert('Failed to create channel: ' + err.message);
            }
        });

        // Close channel modal on overlay click
        document.getElementById('channel-modal').addEventListener('click', (e) => {
            if (e.target === e.currentTarget) {
                document.getElementById('channel-modal').style.display = 'none';
            }
        });

    // ─── Quick-Start Wizard Logic ───
    (function() {
        const overlay = document.getElementById('wizardOverlay');
        const progressSteps = document.querySelectorAll('#wizardProgress .wizard-progress-step');
        const steps = document.querySelectorAll('.wizard-step');
        const btnBack = document.getElementById('wizBack');
        const btnNext = document.getElementById('wizNext');
        const stepLabel = document.getElementById('wizStepLabel');
        let currentStep = 1;
        let selectedLang = 'go';
        const totalSteps = 4;

        // Open / close
        document.getElementById('quickStartBtn').addEventListener('click', () => {
            // Pre-fill endpoint from current page origin or known config
            const epInput = document.getElementById('wizEndpoint');
            if (!epInput.value) {
                epInput.value = (window.GRAVIX_ENDPOINT || 'http://localhost:8090');
            }
            // Pre-fill API key if stored
            const storedKey = localStorage.getItem('gravix_api_key');
            const keyInput = document.getElementById('wizApiKey');
            if (!keyInput.value && storedKey) {
                keyInput.value = storedKey;
            }
            currentStep = 1;
            renderStep();
            overlay.classList.add('open');
        });

        document.getElementById('wizardClose').addEventListener('click', () => {
            overlay.classList.remove('open');
        });
        overlay.addEventListener('click', (e) => {
            if (e.target === overlay) overlay.classList.remove('open');
        });
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape' && overlay.classList.contains('open')) {
                overlay.classList.remove('open');
            }
        });

        // Language selection
        document.querySelectorAll('.wizard-lang-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                document.querySelectorAll('.wizard-lang-btn').forEach(b => b.classList.remove('selected'));
                btn.classList.add('selected');
                selectedLang = btn.dataset.lang;
            });
        });

        // Navigation
        btnNext.addEventListener('click', () => {
            if (currentStep < totalSteps) {
                currentStep++;
                if (currentStep === 3) generateCode();
                renderStep();
            } else {
                overlay.classList.remove('open');
            }
        });
        btnBack.addEventListener('click', () => {
            if (currentStep > 1) {
                currentStep--;
                renderStep();
            }
        });

        // Copy buttons
        document.querySelectorAll('.wizard-copy-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                const target = document.getElementById(btn.dataset.copy);
                if (target) {
                    navigator.clipboard.writeText(target.textContent).then(() => {
                        const orig = btn.textContent;
                        btn.textContent = 'Copied!';
                        setTimeout(() => btn.textContent = orig, 1500);
                    });
                }
            });
        });

        function renderStep() {
            steps.forEach(s => s.classList.remove('active'));
            const active = document.querySelector(`.wizard-step[data-step="${currentStep}"]`);
            if (active) active.classList.add('active');

            progressSteps.forEach((s, i) => {
                s.classList.remove('active', 'done');
                if (i + 1 === currentStep) s.classList.add('active');
                else if (i + 1 < currentStep) s.classList.add('done');
            });

            stepLabel.textContent = `Step ${currentStep} of ${totalSteps}`;
            btnBack.style.display = currentStep > 1 ? 'inline-block' : 'none';
            btnNext.textContent = currentStep === totalSteps ? 'Done' : 'Next';
        }

        function esc(s) { return s.replace(/'/g, "\\'").replace(/"/g, '\\"'); }

        function generateCode() {
            const svc = document.getElementById('wizServiceName').value.trim() || 'my-service';
            const ep = document.getElementById('wizEndpoint').value.trim() || 'http://localhost:8090';
            const key = document.getElementById('wizApiKey').value.trim() || 'your-api-key';

            let install = '', code = '';

            if (selectedLang === 'go') {
                install = 'go get github.com/gravix-io/gravix-go@latest';
                code = `package main

import (
    "context"
    "log"
    "net/http"
    "time"

    gravix "github.com/gravix-io/gravix-go"
)

func main() {
    client := gravix.New(
        "${esc(ep)}",
        "${esc(key)}",
        gravix.WithService("${esc(svc)}"),
    )
    defer client.Close()

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()

        // Your handler logic here
        w.WriteHeader(200)
        w.Write([]byte("ok"))

        client.RecordFact(r.Context(), gravix.RequestFact{
            Method:       r.Method,
            PathTemplate: r.URL.Path,
            StatusCode:   200,
            LatencyMs:    int(time.Since(start).Milliseconds()),
        })
    })

    log.Println("Listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}`;
            } else if (selectedLang === 'python') {
                install = 'pip install gravix';
                code = `from flask import Flask
from gravix import GravixClient
from gravix.middleware import flask_middleware

app = Flask(__name__)

client = GravixClient(
    "${esc(ep)}",
    "${esc(key)}",
    service="${esc(svc)}",
)
flask_middleware(client)(app)

@app.route("/")
def hello():
    return "ok"

if __name__ == "__main__":
    app.run(port=8080)`;
            } else if (selectedLang === 'node') {
                install = 'npm install @gravix/sdk';
                code = `import express from "express";
import { GravixClient, expressMiddleware } from "@gravix/sdk";

const app = express();

const client = new GravixClient({
  baseUrl: "${esc(ep)}",
  apiKey: "${esc(key)}",
  service: "${esc(svc)}",
});

app.use(expressMiddleware(client));

app.get("/", (req, res) => {
  res.send("ok");
});

app.listen(8080, () => {
  console.log("Listening on :8080");
});`;
            } else {
                // curl
                install = '# No installation needed — curl is built in.';
                code = `curl -X POST ${ep}/api/v1/facts \\
  -H "X-API-Key: ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "service": "${svc}",
    "method": "GET",
    "pathTemplate": "/api/v1/users/{id}",
    "statusCode": 200,
    "latencyMs": 42,
    "userAgentFamily": "curl"
  }'`;
            }

            document.getElementById('wizInstallCode').textContent = install;
            document.getElementById('wizIntegrationCode').textContent = code;

            // Update titles for curl
            if (selectedLang === 'curl') {
                document.getElementById('wizInstallTitle').textContent = 'Prerequisites';
                document.getElementById('wizInstallDesc').textContent = 'cURL is available on most systems by default.';
            } else {
                document.getElementById('wizInstallTitle').textContent = 'Install the SDK';
                document.getElementById('wizInstallDesc').textContent = 'Run this command to add the SDK to your project.';
            }
        }
    })();
