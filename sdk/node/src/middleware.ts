/**
 * Framework middleware for automatic request fact recording.
 * Supports Express, Koa, and Fastify.
 */

import type { GravixClient } from "./client.js";

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
export function expressMiddleware(client: GravixClient) {
  return (req: any, res: any, next: any) => {
    const start = process.hrtime.bigint();

    const originalEnd = res.end;
    res.end = function (...args: any[]) {
      const elapsed = Number(process.hrtime.bigint() - start) / 1e6;
      const ua: string = req.headers["user-agent"] || "";
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
export function koaMiddleware(client: GravixClient) {
  return async (ctx: any, next: () => Promise<void>) => {
    const start = process.hrtime.bigint();

    await next();

    const elapsed = Number(process.hrtime.bigint() - start) / 1e6;
    const ua: string = ctx.request.headers["user-agent"] || "";
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
export function fastifyPlugin(client: GravixClient) {
  return async (fastify: any) => {
    fastify.addHook("onRequest", (request: any, _reply: any, done: () => void) => {
      request._gravixStart = process.hrtime.bigint();
      done();
    });

    fastify.addHook("onResponse", (request: any, reply: any, done: () => void) => {
      const start = request._gravixStart as bigint | undefined;
      const elapsed = start
        ? Number(process.hrtime.bigint() - start) / 1e6
        : 0;

      const ua: string = request.headers["user-agent"] || "";
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
