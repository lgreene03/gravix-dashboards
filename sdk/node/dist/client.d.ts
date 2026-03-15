import { type GravixClientOptions, type RequestFact, type ServiceEvent } from "./types.js";
/**
 * Client for the Gravix ingestion API.
 *
 * Facts are buffered and flushed in batches. Events are sent synchronously.
 */
export declare class GravixClient {
    private baseUrl;
    private apiKey;
    private service;
    private batchSize;
    private flushIntervalMs;
    private autoSanitize;
    private maxRetries;
    private timeoutMs;
    private onError?;
    private buffer;
    private timer;
    private closed;
    constructor(options: GravixClientOptions);
    /**
     * Queue a request fact for batched submission.
     * Auto-generates `eventId` and `eventTime` if not set.
     */
    recordFact(fact: RequestFact): Promise<void>;
    /**
     * Send a service event synchronously.
     * @throws {APIError} on failure.
     */
    recordEvent(event: ServiceEvent): Promise<void>;
    /**
     * Send a single fact synchronously (bypasses the batcher).
     * @throws {APIError} on failure.
     */
    sendFact(fact: RequestFact): Promise<void>;
    /** Force an immediate flush of all buffered facts. */
    flush(): Promise<void>;
    /** Flush remaining facts and shut down the background timer. */
    close(): Promise<void>;
    private enrichFact;
    private enrichEvent;
    private doWithRetry;
    private retryDelay;
    private sleep;
}
