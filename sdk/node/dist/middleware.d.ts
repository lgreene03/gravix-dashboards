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
export declare function expressMiddleware(client: GravixClient): (req: any, res: any, next: any) => void;
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
export declare function koaMiddleware(client: GravixClient): (ctx: any, next: () => Promise<void>) => Promise<void>;
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
export declare function fastifyPlugin(client: GravixClient): (fastify: any) => Promise<void>;
