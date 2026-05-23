const jwt = require('jsonwebtoken');

module.exports = {
    scheduledRefreshTimer: 300,

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
        const jwtSecret = process.env.JWT_SECRET;
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
