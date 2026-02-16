module.exports = {
    // Database connection is configured via ENV vars (CUBEJS_DB_TYPE=trino, etc)
    checkSqlAuth: (req, auth) => {
        // No auth for MVP
        return {
            password: auth.password,
        };
    },

    // Enforcing the MVP scope "No high cardinality"
    queryRewrite: (query, { securityContext }) => {
        if (query.dimensions && query.dimensions.length > 5) {
            throw new Error("Too many dimensions requested. Keep it simple.");
        }
        return query;
    }
};
