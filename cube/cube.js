// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

const jwt = require('jsonwebtoken');
const fs = require('fs');

// The signing secret, read from a file when one is named. bootstrap_seed
// generates it at first boot and the gateway reads the same file, which is what
// keeps the signer and the verifier in agreement without a literal secret in a
// compose file that anyone can read (F-029). An unreadable file is not silently
// tolerated: falling back would leave Cube verifying with a different value than
// the gateway signs with, which presents as an empty dashboard rather than as an
// error.
function signingSecret() {
    const file = process.env.JWT_SECRET_FILE;
    if (file) {
        return fs.readFileSync(file, 'utf8').trim();
    }
    return process.env.JWT_SECRET;
}

module.exports = {
    // A literal here overrides the environment, so the bootstrap stack could not
    // turn the scheduler off. It has to: without Cube Store there are no
    // pre-aggregations to refresh, and the scheduler's only output was the same
    // Cube Store error every 300s (F-035).
    scheduledRefreshTimer:
        process.env.CUBEJS_SCHEDULED_REFRESH_TIMER === 'false' ? false : 300,

    // Isolate each tenant into its own Cube app context so pre-aggregation
    // namespaces and connection pool partitions don't bleed across tenants.
    contextToAppId: ({ securityContext }) => {
        if (securityContext && securityContext.tenant_id) {
            return `GRAVIX_${securityContext.tenant_id}`;
        }
        return 'GRAVIX_GLOBAL';
    },

    // Pre-aggregation storage is also partitioned per tenant.
    contextToOrchestratorId: ({ securityContext }) => {
        if (securityContext && securityContext.tenant_id) {
            return `GRAVIX_${securityContext.tenant_id}`;
        }
        return 'GRAVIX_GLOBAL';
    },

    checkAuth: (req, auth) => {
        const jwtSecret = signingSecret();
        const apiSecret = process.env.CUBEJS_API_SECRET;

        // Multi-tenant mode: validate JWT and extract tenant context
        if (jwtSecret) {
            const token = auth && auth.replace('Bearer ', '');
            if (!token) {
                throw new Error('No authorization token provided');
            }
            try {
                const decoded = jwt.verify(token, jwtSecret);
                return { securityContext: { tenant_id: decoded.tenant_id } };
            } catch (e) {
                throw new Error('Invalid or expired token');
            }
        }

        // Legacy mode: simple API secret check
        if (apiSecret && auth !== apiSecret) {
            throw new Error('Invalid API key');
        }
        return {};
    },

    queryRewrite: (query, { securityContext }) => {
        if (query.dimensions && query.dimensions.length > 5) {
            throw new Error("Too many dimensions requested. Keep it simple.");
        }

        // Multi-tenant isolation: force tenant_id filter when security context is present
        if (securityContext && securityContext.tenant_id) {
            const tenantFilter = {
                member: query.measures && query.measures[0]
                    ? query.measures[0].split('.')[0] + '.tenantId'
                    : 'RequestMetricsMinute.tenantId',
                operator: 'equals',
                values: [securityContext.tenant_id]
            };

            if (!query.filters) {
                query.filters = [];
            }
            query.filters.push(tenantFilter);
        }

        return query;
    }
};
