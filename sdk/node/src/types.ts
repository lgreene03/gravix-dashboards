/**
 * A single HTTP request observation.
 * `eventId` and `eventTime` are auto-generated if not provided.
 */
export interface RequestFact {
  /** Service name (e.g. "auth-api"). */
  service?: string;
  /** HTTP method (GET, POST, etc.). */
  method: string;
  /** URL path with {id} placeholders. */
  pathTemplate: string;
  /** HTTP status code (100-599). */
  statusCode: number;
  /** Response time in milliseconds (>= 0). */
  latencyMs: number;
  /** UUIDv7 event identifier (auto-generated if omitted). */
  eventId?: string;
  /** ISO 8601 timestamp (auto-generated if omitted). */
  eventTime?: string;
  /** User agent family (optional). */
  userAgentFamily?: string;
}

/**
 * A service lifecycle event (deploy, restart, scale, etc.).
 * `eventId` and `eventTime` are auto-generated if not provided.
 */
export interface ServiceEvent {
  /** Service name. */
  service?: string;
  /** Snake_case event type (e.g. "deploy_completed"). */
  eventType: string;
  /** UUIDv7 event identifier (auto-generated if omitted). */
  eventId?: string;
  /** ISO 8601 timestamp (auto-generated if omitted). */
  eventTime?: string;
  /** Related entity identifier (optional). */
  entityId?: string;
  /** Human-readable message (optional). */
  message?: string;
  /** Flat key-value metadata (optional). */
  properties?: Record<string, string>;
}

/** Response from the batch facts endpoint. */
export interface BatchResult {
  accepted: number;
  rejected: number;
  errors?: string[];
}

/** Configuration options for the Gravix client. */
export interface GravixClientOptions {
  /** Ingestion service URL (e.g. "http://localhost:8090"). */
  baseUrl: string;
  /** X-API-Key value. */
  apiKey: string;
  /** Default service name for facts/events. */
  service?: string;
  /** Max facts buffered before auto-flush (default: 100). */
  batchSize?: number;
  /** Milliseconds between auto-flushes (default: 5000). */
  flushIntervalMs?: number;
  /** Auto-replace UUIDs/IDs in paths (default: true). */
  autoSanitize?: boolean;
  /** Max retry attempts on failure (default: 3). */
  maxRetries?: number;
  /** HTTP request timeout in milliseconds (default: 10000). */
  timeoutMs?: number;
  /** Callback for background batch flush errors. */
  onError?: (err: Error) => void;
}

/** Error returned by the Gravix API. */
export class APIError extends Error {
  statusCode: number;

  constructor(message: string, statusCode: number) {
    super(message);
    this.name = "APIError";
    this.statusCode = statusCode;
  }
}
