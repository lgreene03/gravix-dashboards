import { randomUUID } from "node:crypto";
import {
  APIError,
  type BatchResult,
  type GravixClientOptions,
  type RequestFact,
  type ServiceEvent,
} from "./types.js";
import { sanitizePath } from "./sanitize.js";

/** Status codes that are safe to retry. */
const RETRYABLE = new Set([429, 500, 502, 503, 504]);

/**
 * Client for the Gravix ingestion API.
 *
 * Facts are buffered and flushed in batches. Events are sent synchronously.
 */
export class GravixClient {
  private baseUrl: string;
  private apiKey: string;
  private service: string;
  private batchSize: number;
  private flushIntervalMs: number;
  private autoSanitize: boolean;
  private maxRetries: number;
  private timeoutMs: number;
  private onError?: (err: Error) => void;

  private buffer: Record<string, unknown>[] = [];
  private timer: ReturnType<typeof setInterval> | null = null;
  private closed = false;

  constructor(options: GravixClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.apiKey = options.apiKey;
    this.service = options.service ?? "";
    this.batchSize = options.batchSize ?? 100;
    this.flushIntervalMs = options.flushIntervalMs ?? 5000;
    this.autoSanitize = options.autoSanitize ?? true;
    this.maxRetries = options.maxRetries ?? 3;
    this.timeoutMs = options.timeoutMs ?? 10000;
    this.onError = options.onError;

    this.timer = setInterval(() => {
      this.flush().catch(() => {});
    }, this.flushIntervalMs);

    // Allow the process to exit even if the timer is still running.
    if (this.timer && typeof this.timer === "object" && "unref" in this.timer) {
      (this.timer as NodeJS.Timeout).unref();
    }
  }

  // --- Public API ---

  /**
   * Queue a request fact for batched submission.
   * Auto-generates `eventId` and `eventTime` if not set.
   */
  async recordFact(fact: RequestFact): Promise<void> {
    const enriched = this.enrichFact(fact);
    this.buffer.push(enriched);

    if (this.buffer.length >= this.batchSize) {
      await this.flush();
    }
  }

  /**
   * Send a service event synchronously.
   * @throws {APIError} on failure.
   */
  async recordEvent(event: ServiceEvent): Promise<void> {
    const enriched = this.enrichEvent(event);
    const body = JSON.stringify(enriched);
    await this.doWithRetry("POST", "/api/v1/events", body);
  }

  /**
   * Send a single fact synchronously (bypasses the batcher).
   * @throws {APIError} on failure.
   */
  async sendFact(fact: RequestFact): Promise<void> {
    const enriched = this.enrichFact(fact);
    const body = JSON.stringify(enriched);
    await this.doWithRetry("POST", "/api/v1/facts", body);
  }

  /** Force an immediate flush of all buffered facts. */
  async flush(): Promise<void> {
    if (this.buffer.length === 0) return;

    const batch = this.buffer;
    this.buffer = [];

    const lines = batch.map((f) => JSON.stringify(f)).join("\n") + "\n";

    try {
      await this.doWithRetry("POST", "/api/v1/facts/batch", lines);
    } catch (err) {
      if (this.onError && err instanceof Error) {
        this.onError(err);
      } else {
        throw err;
      }
    }
  }

  /** Flush remaining facts and shut down the background timer. */
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;

    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }

    try {
      await this.flush();
    } catch {
      // Best-effort on close.
    }
  }

  // --- Internals ---

  private enrichFact(fact: RequestFact): Record<string, unknown> {
    const out: Record<string, unknown> = {
      event_id: fact.eventId || randomUUID(),
      event_time: fact.eventTime || new Date().toISOString(),
      service: fact.service || this.service,
      method: fact.method,
      path_template:
        this.autoSanitize && fact.pathTemplate
          ? sanitizePath(fact.pathTemplate)
          : fact.pathTemplate,
      status_code: fact.statusCode,
      latency_ms: fact.latencyMs,
    };
    if (fact.userAgentFamily) {
      out.user_agent_family = fact.userAgentFamily;
    }
    return out;
  }

  private enrichEvent(event: ServiceEvent): Record<string, unknown> {
    const out: Record<string, unknown> = {
      event_id: event.eventId || randomUUID(),
      event_time: event.eventTime || new Date().toISOString(),
      service: event.service || this.service,
      event_type: event.eventType,
    };
    if (event.entityId) out.entity_id = event.entityId;
    if (event.message) out.message = event.message;
    if (event.properties && Object.keys(event.properties).length > 0) {
      out.properties = event.properties;
    }
    return out;
  }

  private async doWithRetry(
    method: string,
    path: string,
    body: string
  ): Promise<Response> {
    const url = this.baseUrl + path;
    let lastErr: Error | null = null;

    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      if (attempt > 0 && lastErr) {
        const delay = this.retryDelay(attempt - 1);
        await this.sleep(delay);
      }

      let resp: Response;
      try {
        resp = await fetch(url, {
          method,
          headers: {
            "Content-Type": "application/json",
            "X-API-Key": this.apiKey,
          },
          body,
          signal: AbortSignal.timeout(this.timeoutMs),
        });
      } catch (err) {
        lastErr = err instanceof Error ? err : new Error(String(err));
        continue;
      }

      if (resp.status === 200 || resp.status === 201) {
        return resp;
      }

      // Parse error response.
      let msg = `HTTP ${resp.status}`;
      try {
        const errData = (await resp.json()) as { error?: string };
        if (errData.error) msg = errData.error;
      } catch {
        // Ignore parse errors.
      }

      const apiErr = new APIError(msg, resp.status);

      if (RETRYABLE.has(resp.status) && attempt < this.maxRetries) {
        lastErr = apiErr;
        // Respect Retry-After header.
        const retryAfter = resp.headers.get("Retry-After");
        if (retryAfter) {
          const seconds = parseInt(retryAfter, 10);
          if (!isNaN(seconds)) {
            await this.sleep(seconds * 1000);
            continue;
          }
        }
        continue;
      }

      throw apiErr;
    }

    throw lastErr ?? new APIError("max retries exceeded", 0);
  }

  private retryDelay(attempt: number): number {
    const base = 500 * Math.pow(2, attempt);
    const cap = Math.min(base, 10000);
    return Math.random() * cap;
  }

  private sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }
}
