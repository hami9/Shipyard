// A server-sent events parser over fetch, which unlike EventSource can send
// an Authorization header. It follows the HTML standard's interpretation
// rules [WHATWG-SSE]: lines end in CRLF, LF, or CR; ":" starts a comment;
// one space after the colon is dropped; data lines join with "\n"; an
// event is dispatched at a blank line, and an incomplete one at the end of
// the stream is discarded.

export interface ServerEvent {
  /** The event name; "message" when the server gave none. */
  event: string;
  data: string;
  /** The last event ID the stream set, if any. */
  id: string | undefined;
}

/** parseEvents yields body's events. onRetry receives each retry field, in milliseconds. */
export async function* parseEvents(
  body: ReadableStream<Uint8Array>,
  onRetry?: (ms: number) => void,
): AsyncGenerator<ServerEvent> {
  const reader = body.getReader();
  const decoder = new TextDecoder(); // UTF-8, dropping a leading BOM, as the standard says
  let buf = "";
  let data: string[] = [];
  let event = "";
  let id: string | undefined;
  let done = false;
  try {
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) {
        done = true;
        return;
      }
      buf += decoder.decode(chunk.value, { stream: true }); // a character may span chunks
      for (;;) {
        const i = buf.search(/[\r\n]/);
        // A CR at the very end may be the first half of a CRLF.
        if (i < 0 || (buf[i] === "\r" && i === buf.length - 1)) {
          break;
        }
        const line = buf.slice(0, i);
        buf = buf.slice(buf.startsWith("\r\n", i) ? i + 2 : i + 1);
        if (line === "") {
          if (data.length > 0) {
            yield { event: event || "message", data: data.join("\n"), id };
          }
          data = [];
          event = "";
          continue;
        }
        if (line.startsWith(":")) {
          continue;
        }
        const colon = line.indexOf(":");
        const field = colon < 0 ? line : line.slice(0, colon);
        let value = colon < 0 ? "" : line.slice(colon + 1);
        if (value.startsWith(" ")) {
          value = value.slice(1);
        }
        switch (field) {
          case "event":
            event = value;
            break;
          case "data":
            data.push(value);
            break;
          case "id":
            if (!value.includes("\0")) {
              id = value;
            }
            break;
          case "retry":
            if (/^[0-9]+$/.test(value)) {
              onRetry?.(Number(value));
            }
            break;
        }
      }
    }
  } finally {
    if (!done) {
      await reader.cancel().catch(() => {});
    }
  }
}
