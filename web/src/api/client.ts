// The typed client for Shipyard's API (ADR-0015). Its types come from
// schema.ts, which is generated from api/openapi.json; this file is the
// small runtime around them: the bearer token, problem+json errors, and
// event streams over fetch.

import { operations, type Operations, type Problem } from "./schema.ts";
import { parseEvents } from "./sse.ts";

export type { Operations };
export type OperationId = keyof Operations;

/** The operations that answer with an event stream. */
export type StreamId = {
  [K in OperationId]: (typeof operations)[K]["stream"] extends true ? K : never;
}[OperationId];

/** The operations that answer with JSON, or nothing. */
export type CallId = Exclude<OperationId, StreamId>;

export type RequestOf<K extends OperationId> = Operations[K]["request"];
export type ResponseOf<K extends OperationId> = Operations[K]["response"];

// A request with nothing required may be left out.
type Args<K extends OperationId> = {} extends RequestOf<K> ? [request?: RequestOf<K>] : [request: RequestOf<K>];

type Events<K extends StreamId> = Operations[K]["events"];

/** One event of K's stream, its data typed by its name. */
export type StreamEvent<K extends StreamId> = {
  [E in keyof Events<K>]: { event: E; data: Events<K>[E]; id: string | undefined };
}[keyof Events<K>];

/** An error response. problem is its RFC 9457 body, when it had one. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: Problem | undefined;
  /** Seconds to wait, from Retry-After (429). */
  readonly retryAfter: number | undefined;

  constructor(status: number, problem: Problem | undefined, retryAfter: number | undefined, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.retryAfter = retryAfter;
  }

  static async from(res: Response): Promise<ApiError> {
    let problem: Problem | undefined;
    if ((res.headers.get("Content-Type") ?? "").startsWith("application/problem+json")) {
      problem = (await res.json().catch(() => undefined)) as Problem | undefined;
    }
    const after = Number.parseInt(res.headers.get("Retry-After") ?? "", 10);
    return new ApiError(res.status, problem, Number.isNaN(after) ? undefined : after,
      problem?.detail ?? `${res.status} ${res.statusText}`.trim());
  }
}

export interface ClientOptions {
  /** The API's origin, such as https://shipyard.example.com. Default: the page's own. */
  baseUrl?: string;
  /** An API token (shp_…). */
  token: string;
  fetch?: typeof fetch;
  /** Called on any 401, before the ApiError is thrown: the token no longer works. */
  onUnauthorized?: () => void;
}

export interface StreamOptions {
  signal?: AbortSignal;
  /** Resume after this event ID (Last-Event-ID). */
  lastEventId?: string;
}

interface SendOptions {
  accept: string;
  signal?: AbortSignal | undefined;
  lastEventId?: string | undefined;
}

// Reconnections in a row without a new event before a stream gives up.
const maxReconnects = 5;

export class Client {
  readonly #baseUrl: string;
  readonly #token: string;
  readonly #fetch: typeof fetch;
  readonly #onUnauthorized: (() => void) | undefined;

  constructor(options: ClientOptions) {
    this.#baseUrl = (options.baseUrl ?? "").replace(/\/+$/, "");
    this.#token = options.token;
    this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.#onUnauthorized = options.onUnauthorized;
  }

  /** call runs an operation and returns its success body (undefined for 204). Errors throw ApiError. */
  async call<K extends CallId>(op: K, ...args: Args<K>): Promise<ResponseOf<K>> {
    const res = await this.#send(op, args[0], { accept: "application/json" });
    if (res.status === 204) {
      return undefined as ResponseOf<K>;
    }
    return (await res.json()) as ResponseOf<K>;
  }

  /**
   * stream yields an event stream's events with parsed data, and returns
   * after an "end" event. When the server asked for reconnection (a retry
   * field, as operation events do), a dropped connection is resumed with
   * Last-Event-ID; a stream without one, such as logs, just ends.
   */
  async *stream<K extends StreamId>(op: K, request: RequestOf<K>, options: StreamOptions = {}): AsyncGenerator<StreamEvent<K>> {
    let lastEventId = options.lastEventId;
    let retry: number | undefined;
    let failures = 0;
    for (;;) {
      const res = await this.#send(op, request, { accept: "text/event-stream", signal: options.signal, lastEventId });
      if (!res.body) {
        return;
      }
      try {
        for await (const e of parseEvents(res.body, (ms) => (retry = ms))) {
          failures = 0;
          lastEventId = e.id ?? lastEventId;
          yield { event: e.event, data: JSON.parse(e.data), id: e.id } as StreamEvent<K>;
          if (e.event === "end") {
            return;
          }
        }
      } catch (err) {
        if (options.signal?.aborted || retry === undefined) {
          throw err;
        }
      }
      if (retry === undefined || options.signal?.aborted) {
        return;
      }
      if (++failures > maxReconnects) {
        throw new Error(`${op}: the stream dropped ${maxReconnects} times in a row`);
      }
      await sleep(retry, options.signal);
    }
  }

  async #send(op: OperationId, request: unknown, options: SendOptions): Promise<Response> {
    const { method, path } = operations[op];
    const req = (request ?? {}) as {
      path?: Record<string, unknown>;
      query?: Record<string, unknown>;
      header?: Record<string, unknown>;
      body?: unknown;
    };
    const url = this.#baseUrl + expand(path, req.path ?? {}) + query(req.query ?? {});
    const headers = new Headers({ Accept: options.accept, Authorization: `Bearer ${this.#token}` });
    for (const [name, value] of Object.entries(req.header ?? {})) {
      if (value !== undefined) {
        headers.set(name, String(value));
      }
    }
    if (options.lastEventId !== undefined) {
      headers.set("Last-Event-ID", options.lastEventId);
    }
    let body: string | undefined;
    if (req.body !== undefined) {
      body = JSON.stringify(req.body);
      headers.set("Content-Type", "application/json");
    }
    const init: RequestInit = { method, headers };
    if (body !== undefined) {
      init.body = body;
    }
    if (options.signal) {
      init.signal = options.signal;
    }
    const res = await this.#fetch(url, init);
    if (!res.ok) {
      if (res.status === 401) {
        this.#onUnauthorized?.();
      }
      throw await ApiError.from(res);
    }
    return res;
  }
}

/** expand fills a path template's {names}, escaping each value. */
export function expand(template: string, values: Record<string, unknown>): string {
  return template.replace(/\{([^}]+)\}/g, (_, name: string) => {
    const v = values[name];
    if (v === undefined || v === null || v === "") {
      throw new TypeError(`missing path parameter ${name} for ${template}`);
    }
    return encodeURIComponent(String(v));
  });
}

function query(values: Record<string, unknown>): string {
  const q = new URLSearchParams();
  for (const [name, value] of Object.entries(values)) {
    if (value !== undefined) {
      q.set(name, String(value));
    }
  }
  const s = q.toString();
  return s ? `?${s}` : "";
}

function sleep(ms: number, signal: AbortSignal | undefined): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason);
      return;
    }
    const t = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => {
      clearTimeout(t);
      reject(signal.reason);
    }, { once: true });
  });
}
