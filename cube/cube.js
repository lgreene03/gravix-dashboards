module.exports = {
    checkSqlAuth: (req, auth) => {
        const apiKey = process.env.CUBEJS_API_SECRET;
        if (apiKey && auth.password !== apiKey) {
            throw new Error("Invalid API key");
        }
        return { password: auth.password };
    },

    queryRewrite: (query, { securityContext }) => {
        if (query.dimensions && query.dimensions.length > 5) {
            throw new Error("Too many dimensions requested. Keep it simple.");
        }
        return query;
    }
};
