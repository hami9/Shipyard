import { useEffect, useState } from "react";
import { ApiError, type Client, type RequestOf, type StreamEvent, type StreamId } from "./api/client.ts";
import { message } from "./useApi.ts";

export type StreamState = "connecting" | "open" | "ended" | "stopped" | "failed";

export interface Streamed<K extends StreamId> {
  events: StreamEvent<K>[];
  state: StreamState;
  error: string | undefined;
  /** The HTTP status that failed the stream, when the API answered one. */
  status?: number | undefined;
}

/** maxEvents bounds what a page keeps: a long log is cut at the top. */
export const maxEvents = 2000;

/** appendCapped adds items, keeping at most max of the newest. */
export function appendCapped<T>(list: T[], items: T[], max: number = maxEvents): T[] {
  const out = list.concat(items);
  return out.length > max ? out.slice(out.length - max) : out;
}

/**
 * useStream follows one event stream while the page shows it, and stops it
 * when the page leaves or the inputs change (key). Events arriving in one
 * burst are batched into one render.
 */
export function useStream<K extends StreamId>(client: Client, op: K, request: RequestOf<K>, key: string, enabled = true): Streamed<K> {
  const [s, setS] = useState<Streamed<K>>({ events: [], state: "connecting", error: undefined });
  useEffect(() => {
    if (!enabled) {
      setS((p) => ({ ...p, state: p.state === "open" || p.state === "connecting" ? "stopped" : p.state }));
      return;
    }
    const abort = new AbortController();
    let pending: StreamEvent<K>[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;
    const flush = () => {
      timer = undefined;
      const batch = pending;
      pending = [];
      setS((p) => ({ ...p, state: "open", events: appendCapped(p.events, batch) }));
    };
    setS({ events: [], state: "connecting", error: undefined });
    (async () => {
      try {
        const onOpen = () => setS((p) => ({ ...p, state: "open" }));
        for await (const e of client.stream(op, request, { signal: abort.signal, onOpen })) {
          pending.push(e);
          timer ??= setTimeout(flush, 50);
        }
        if (timer) {
          clearTimeout(timer);
          flush();
        }
        setS((p) => ({ ...p, state: "ended" }));
      } catch (err) {
        if (!abort.signal.aborted) {
          setS((p) => ({ ...p, state: "failed", error: message(err), status: err instanceof ApiError ? err.status : undefined }));
        }
      }
    })();
    return () => {
      abort.abort();
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [client, op, key, enabled]); // request is described by key
  return s;
}
