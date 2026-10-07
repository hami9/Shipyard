import assert from "node:assert/strict";
import { test } from "node:test";
import { parseEvents, type ServerEvent } from "./sse.ts";

// stream serves chunks as a body, split exactly where the test says.
function stream(...chunks: string[]): ReadableStream<Uint8Array> {
  const enc = new TextEncoder();
  return new ReadableStream({
    start(c) {
      for (const s of chunks) {
        c.enqueue(enc.encode(s));
      }
      c.close();
    },
  });
}

async function all(body: ReadableStream<Uint8Array>, retries: number[] = []): Promise<ServerEvent[]> {
  const out: ServerEvent[] = [];
  for await (const e of parseEvents(body, (ms) => retries.push(ms))) {
    out.push(e);
  }
  return out;
}

test("events, names, ids, comments, and retry", async () => {
  const retries: number[] = [];
  const got = await all(stream("retry: 2000\n\n: keepalive\n\nid: 1\ndata: {\"seq\":1}\n\n", "event: end\ndata: {}\n\n"), retries);
  assert.deepEqual(got, [
    { event: "message", data: "{\"seq\":1}", id: "1" },
    { event: "end", data: "{}", id: "1" }, // the last ID persists
  ]);
  assert.deepEqual(retries, [2000]);
});

test("lines split anywhere, with CRLF, CR, and LF", async () => {
  const got = await all(stream("da", "ta: a\r", "\ndata:b\rdata:  c\n", "\r", "\n"));
  assert.deepEqual(got, [{ event: "message", data: "a\nb\n c", id: undefined }]);
});

test("a character split across chunks", async () => {
  const bytes = new TextEncoder().encode("data: سلام\n\n");
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      c.enqueue(bytes.slice(0, 7)); // inside the first Persian letter
      c.enqueue(bytes.slice(7));
      c.close();
    },
  });
  const got = await all(body);
  assert.deepEqual(got.map((e) => e.data), ["سلام"]);
});

test("an incomplete event at the end is discarded", async () => {
  const got = await all(stream("data: kept\n\ndata: cut off"));
  assert.deepEqual(got.map((e) => e.data), ["kept"]);
});

test("no data, no event; an empty data line is an empty event", async () => {
  const got = await all(stream("event: x\n\ndata\n\nid: a\u0000b\nretry: soon\ndata: y\n\n"));
  assert.deepEqual(got, [
    { event: "message", data: "", id: undefined },
    { event: "message", data: "y", id: undefined }, // an id with NUL is ignored
  ]);
});

test("leaving early cancels the body", async () => {
  let cancelled = false;
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      c.enqueue(new TextEncoder().encode("data: 1\n\ndata: 2\n\n"));
    },
    cancel() {
      cancelled = true;
    },
  });
  for await (const e of parseEvents(body)) {
    assert.equal(e.data, "1");
    break;
  }
  assert.ok(cancelled);
});
