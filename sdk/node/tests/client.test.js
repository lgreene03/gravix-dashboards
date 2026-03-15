import { describe, it, afterEach } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { GravixClient, APIError } from "../dist/index.js";

/**
 * Start a mock HTTP server and return { url, server, requests }.
 * Each incoming request is captured in the `requests` array.
 * The optional `handler` is called per-request to customise the response.
 */
function createMockServer(handler) {
  const requests = [];

  const server = http.createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const body = Buffer.concat(chunks).toString();

    requests.push({
      method: req.method,
      url: req.url,
      headers: req.headers,
      body,
    });

    if (handler) {
      handler(req, res, requests.length);
    } else {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ accepted: 1, rejected: 0 }));
    }
  });

  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      resolve({
        url: `http://127.0.0.1:${port}`,
        server,
        requests,
      });
    });
  });
}

describe("GravixClient", () => {
  let servers = [];

  afterEach(async () => {
    for (const s of servers) {
      await new Promise((resolve) => s.close(resolve));
    }
    servers = [];
  });

  async function setup(handler) {
    const mock = await createMockServer(handler);
    servers.push(mock.server);
    return mock;
  }

  // --- sendFact ---

  it("sendFact sends a single fact synchronously", async () => {
    const { url, requests } = await setup();
    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      flushIntervalMs: 60000,
    });

    await client.sendFact({
      method: "GET",
      pathTemplate: "/api/v1/users/{id}",
      statusCode: 200,
      latencyMs: 42,
    });
    await client.close();

    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, "/api/v1/facts");
    assert.equal(requests[0].headers["x-api-key"], "test-key");

    const body = JSON.parse(requests[0].body);
    assert.equal(body.method, "GET");
    assert.equal(body.path_template, "/api/v1/users/{id}");
    assert.equal(body.status_code, 200);
    assert.equal(body.latency_ms, 42);
    assert.ok(body.event_id, "event_id should be auto-generated");
    assert.ok(body.event_time, "event_time should be auto-generated");
  });

  // --- recordEvent ---

  it("recordEvent sends an event synchronously", async () => {
    const { url, requests } = await setup();
    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      flushIntervalMs: 60000,
    });

    await client.recordEvent({
      service: "auth",
      eventType: "deploy_completed",
      message: "Deployed v1.2.3",
      properties: { version: "1.2.3" },
    });
    await client.close();

    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, "/api/v1/events");

    const body = JSON.parse(requests[0].body);
    assert.equal(body.service, "auth");
    assert.equal(body.event_type, "deploy_completed");
    assert.equal(body.message, "Deployed v1.2.3");
    assert.deepEqual(body.properties, { version: "1.2.3" });
  });

  // --- auto-sanitize ---

  it("auto-sanitizes UUIDs in path_template", async () => {
    const { url, requests } = await setup();
    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      flushIntervalMs: 60000,
    });

    await client.sendFact({
      method: "GET",
      pathTemplate: "/users/550e8400-e29b-41d4-a716-446655440000/orders",
      statusCode: 200,
      latencyMs: 10,
    });
    await client.close();

    const body = JSON.parse(requests[0].body);
    assert.equal(body.path_template, "/users/{id}/orders");
  });

  it("skips sanitization when autoSanitize is false", async () => {
    const { url, requests } = await setup();
    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      autoSanitize: false,
      flushIntervalMs: 60000,
    });

    const rawPath = "/users/550e8400-e29b-41d4-a716-446655440000/orders";
    await client.sendFact({
      method: "GET",
      pathTemplate: rawPath,
      statusCode: 200,
      latencyMs: 10,
    });
    await client.close();

    const body = JSON.parse(requests[0].body);
    assert.equal(body.path_template, rawPath);
  });

  // --- default service ---

  it("uses default service name from constructor", async () => {
    const { url, requests } = await setup();
    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      service: "default-svc",
      flushIntervalMs: 60000,
    });

    await client.sendFact({
      method: "POST",
      pathTemplate: "/api/items",
      statusCode: 201,
      latencyMs: 5,
    });
    await client.close();

    const body = JSON.parse(requests[0].body);
    assert.equal(body.service, "default-svc");
  });

  it("fact-level service overrides default", async () => {
    const { url, requests } = await setup();
    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      service: "default-svc",
      flushIntervalMs: 60000,
    });

    await client.sendFact({
      service: "override-svc",
      method: "POST",
      pathTemplate: "/api/items",
      statusCode: 201,
      latencyMs: 5,
    });
    await client.close();

    const body = JSON.parse(requests[0].body);
    assert.equal(body.service, "override-svc");
  });

  // --- batching ---

  it("flushes batch when buffer reaches batchSize", async () => {
    const { url, requests } = await setup();
    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      batchSize: 3,
      flushIntervalMs: 60000,
    });

    // Send 3 facts — should trigger an auto-flush.
    for (let i = 0; i < 3; i++) {
      await client.recordFact({
        method: "GET",
        pathTemplate: `/path/${i}`,
        statusCode: 200,
        latencyMs: 1,
      });
    }

    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, "/api/v1/facts/batch");

    // JSONL: each line is a JSON object.
    const lines = requests[0].body.trim().split("\n");
    assert.equal(lines.length, 3);

    await client.close();
  });

  it("flushes remaining facts on close", async () => {
    const { url, requests } = await setup();
    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      batchSize: 100,
      flushIntervalMs: 60000,
    });

    await client.recordFact({
      method: "GET",
      pathTemplate: "/api/v1/health",
      statusCode: 200,
      latencyMs: 1,
    });

    // Nothing flushed yet (batch size not reached).
    assert.equal(requests.length, 0);

    await client.close();

    // Now flushed.
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, "/api/v1/facts/batch");
  });

  // --- retry ---

  it("retries on 500 and eventually succeeds", async () => {
    let callCount = 0;
    const { url, requests } = await setup((_req, res, n) => {
      callCount = n;
      if (n <= 2) {
        res.writeHead(500, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: "internal error" }));
      } else {
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ accepted: 1 }));
      }
    });

    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      maxRetries: 3,
      flushIntervalMs: 60000,
    });

    await client.sendFact({
      method: "GET",
      pathTemplate: "/health",
      statusCode: 200,
      latencyMs: 1,
    });

    assert.equal(callCount, 3);
    await client.close();
  });

  it("does not retry on 400", async () => {
    const { url, requests } = await setup((_req, res) => {
      res.writeHead(400, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "bad request" }));
    });

    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      maxRetries: 3,
      flushIntervalMs: 60000,
    });

    await assert.rejects(
      () =>
        client.sendFact({
          method: "GET",
          pathTemplate: "/health",
          statusCode: 200,
          latencyMs: 1,
        }),
      (err) => {
        assert.ok(err instanceof APIError);
        assert.equal(err.statusCode, 400);
        return true;
      }
    );

    assert.equal(requests.length, 1);
    await client.close();
  });

  it("does not retry on 401", async () => {
    const { url, requests } = await setup((_req, res) => {
      res.writeHead(401, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "unauthorized" }));
    });

    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      maxRetries: 3,
      flushIntervalMs: 60000,
    });

    await assert.rejects(
      () =>
        client.sendFact({
          method: "GET",
          pathTemplate: "/health",
          statusCode: 200,
          latencyMs: 1,
        }),
      (err) => {
        assert.ok(err instanceof APIError);
        assert.equal(err.statusCode, 401);
        return true;
      }
    );

    assert.equal(requests.length, 1);
    await client.close();
  });

  // --- onError callback ---

  it("calls onError for batch flush failures", async () => {
    const { url } = await setup((_req, res) => {
      res.writeHead(500, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "server error" }));
    });

    let capturedErr = null;
    const client = new GravixClient({
      baseUrl: url,
      apiKey: "test-key",
      maxRetries: 0,
      flushIntervalMs: 60000,
      onError: (err) => {
        capturedErr = err;
      },
    });

    await client.recordFact({
      method: "GET",
      pathTemplate: "/health",
      statusCode: 200,
      latencyMs: 1,
    });

    // Manual flush to trigger the onError.
    await client.flush();

    assert.ok(capturedErr, "onError should have been called");
    assert.ok(capturedErr instanceof APIError);
    await client.close();
  });

  // --- trailing slash on baseUrl ---

  it("trims trailing slash from baseUrl", async () => {
    const { url, requests } = await setup();
    const client = new GravixClient({
      baseUrl: url + "/",
      apiKey: "test-key",
      flushIntervalMs: 60000,
    });

    await client.sendFact({
      method: "GET",
      pathTemplate: "/health",
      statusCode: 200,
      latencyMs: 1,
    });

    assert.equal(requests[0].url, "/api/v1/facts");
    await client.close();
  });
});
