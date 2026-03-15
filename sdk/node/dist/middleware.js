/**
 * Framework middleware for automatic request fact recording.
 * Supports Express, Koa, and Fastify.
 */
/**
 * Express middleware that records request facts.
 *
 * @example
 * ```ts
 * import express from "express";
 * import { GravixClient } from "@gravix/sdk";
 * import { expressMiddleware } from "@gravix/sdk/middleware";
 *
 * const app = express();
 * const client = new GravixClient({ baseUrl: "...", apiKey: "...", service: "my-app" });
 * app.use(expressMiddleware(client));
 * ```
 */
export function expressMiddleware(client) {
    return (req, res, next) => {
        const start = process.hrtime.bigint();
        const originalEnd = res.end;
        res.end = function (...args) {
            const elapsed = Number(process.hrtime.bigint() - start) / 1e6;
            const ua = req.headers["user-agent"] || "";
            const family = ua.includes("/")
                ? ua.split("/")[0]
                : ua.split(" ")[0] || "";
            client.recordFact({
                method: req.method,
                pathTemplate: req.path || req.url,
                statusCode: res.statusCode,
                latencyMs: Math.round(elapsed),
                userAgentFamily: family.slice(0, 64),
            });
            return originalEnd.apply(res, args);
        };
        next();
    };
}
/**
 * Koa middleware that records request facts.
 *
 * @example
 * ```ts
 * import Koa from "koa";
 * import { GravixClient } from "@gravix/sdk";
 * import { koaMiddleware } from "@gravix/sdk/middleware";
 *
 * const app = new Koa();
 * const client = new GravixClient({ baseUrl: "...", apiKey: "...", service: "my-app" });
 * app.use(koaMiddleware(client));
 * ```
 */
export function koaMiddleware(client) {
    return async (ctx, next) => {
        const start = process.hrtime.bigint();
        await next();
        const elapsed = Number(process.hrtime.bigint() - start) / 1e6;
        const ua = ctx.request.headers["user-agent"] || "";
        const family = ua.includes("/")
            ? ua.split("/")[0]
            : ua.split(" ")[0] || "";
        client.recordFact({
            method: ctx.method,
            pathTemplate: ctx.path,
            statusCode: ctx.status,
            latencyMs: Math.round(elapsed),
            userAgentFamily: family.slice(0, 64),
        });
    };
}
/**
 * Fastify plugin that records request facts via the `onResponse` hook.
 *
 * @example
 * ```ts
 * import Fastify from "fastify";
 * import { GravixClient } from "@gravix/sdk";
 * import { fastifyPlugin } from "@gravix/sdk/middleware";
 *
 * const app = Fastify();
 * const client = new GravixClient({ baseUrl: "...", apiKey: "...", service: "my-app" });
 * app.register(fastifyPlugin(client));
 * ```
 */
export function fastifyPlugin(client) {
    return async (fastify) => {
        fastify.addHook("onRequest", (request, _reply, done) => {
            request._gravixStart = process.hrtime.bigint();
            done();
        });
        fastify.addHook("onResponse", (request, reply, done) => {
            const start = request._gravixStart;
            const elapsed = start
                ? Number(process.hrtime.bigint() - start) / 1e6
                : 0;
            const ua = request.headers["user-agent"] || "";
            const family = ua.includes("/")
                ? ua.split("/")[0]
                : ua.split(" ")[0] || "";
            client.recordFact({
                method: request.method,
                pathTemplate: request.url,
                statusCode: reply.statusCode,
                latencyMs: Math.round(elapsed),
                userAgentFamily: family.slice(0, 64),
            });
            done();
        });
    };
}
