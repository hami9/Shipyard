import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError, Client, expand, type StreamEvent } from "./client.ts";

interface Seen {
  url: string;
  method: string;
  headers: Headers;
  body: string | undefined;
}

// fake answers each request with the next response and records it.
function fake(...responses: Response[]): { client: Client; seen: Seen[] } {
  const seen: Seen[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    seen.push({
      url: String(input),
      method: init?.method ?? "GET",
      headers: new Headers(init?.headers),
      body: init?.body === undefined || init.body === null ? undefined : String(init.body),
    });
    const r = responses.shift();
    if (!r) {
      throw new Error("no more responses");
    }
    return r;
  };
  return { client: new Client({ baseUrl: "https://api.test/", token: "shp_secret", fetch }), seen };
}

const json = (status: number, body: unknown, type = "application/json") =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": type } });

const sse = (text: string) => new Response(text, { status: 200, headers: { "Content-Type": "text/event-stream" } });

test("a call: URL, method, headers, and JSON both ways", async () => {
  const { client, seen } = fake(json(202, { operation: { id: "op" }, created: true, superseded: [] }));
  const res = await client.call("deploy", {
    path: { app: "my app" },
    header: { "Idempotency-Key": "k1" },
    body: { ref: "a".repeat(40) },
  });
  assert.equal(res.created, true);
  const s = seen[0]!;
  assert.equal(s.url, "https://api.test/v1/apps/my%20app/deployments");
  assert.equal(s.method, "POST");
  assert.equal(s.headers.get("Authorization"), "Bearer shp_secret");
  assert.equal(s.headers.get("Idempotency-Key"), "k1");
  assert.equal(s.headers.get("Content-Type"), "application/json");
  assert.deepEqual(JSON.parse(s.body ?? ""), { ref: "a".repeat(40) });
});

test("query parameters, an optional request, and 204", async () => {
  const { client, seen } = fake(json(200, { deployments: [] }), json(200, { apps: [] }), new Response(null, { status: 204 }));
  await client.call("listReleases", { path: { app: "web" }, query: { limit: 5 } });
  await client.call("listApps");
  assert.equal(await client.call("removeDomain", { path: { app: "web", hostname: "a.example.com" } }), undefined);
  assert.equal(seen[0]!.url, "https://api.test/v1/apps/web/deployments?limit=5");
  assert.equal(seen[1]!.url, "https://api.test/v1/apps");
  assert.equal(seen[1]!.body, undefined);
  assert.equal(seen[1]!.headers.get("Content-Type"), null);
});

test("errors: problem details and Retry-After", async () => {
  const problem = { type: "about:blank", title: "Too Many Requests", status: 429, detail: "slow down" };
  const { client } = fake(
    new Response(JSON.stringify(problem), { status: 429, headers: { "Content-Type": "application/problem+json", "Retry-After": "7" } }),
    new Response("bad gateway", { status: 502, statusText: "Bad Gateway" }),
  );
  await assert.rejects(client.call("listApps"), (err: unknown) => {
    assert.ok(err instanceof ApiError);
    assert.equal(err.status, 429);
    assert.equal(err.message, "slow down");
    assert.equal(err.retryAfter, 7);
    assert.deepEqual(err.problem, problem);
    return true;
  });
  await assert.rejects(client.call("listApps"), (err: unknown) => {
    assert.ok(err instanceof ApiError);
    assert.equal(err.problem, undefined);
    assert.equal(err.message, "502 Bad Gateway");
    return true;
  });
});

test("a path parameter is required", () => {
  assert.throws(() => expand("/v1/apps/{app}", {}), TypeError);
  assert.equal(expand("/v1/apps/{app}/env/{key}", { app: "a/b", key: "K" }), "/v1/apps/a%2Fb/env/K");
});

test("an operation's events, resumed with Last-Event-ID, until end", async () => {
  const op = { id: "o", app_id: "a", kind: "deploy", status: "succeeded", payload: {}, idempotency_key: "k", attempt: 1, max_attempts: 3, created_at: "2026-10-06T10:00:00Z" };
  const event = (seq: number) => `id: ${seq}\ndata: ${JSON.stringify({ seq, ts: "2026-10-06T10:00:00Z", level: "info", message: `m${seq}` })}\n\n`;
  const { client, seen } = fake(
    sse(`retry: 1\n\n${event(1)}${event(2)}`), // dropped before the end
    sse(`retry: 1\n\n${event(3)}event: end\ndata: ${JSON.stringify(op)}\n\n`),
  );
  const got: StreamEvent<"streamEvents">[] = [];
  let opened = 0;
  for await (const e of client.stream("streamEvents", { path: { id: "o" } }, { onOpen: () => opened++ })) {
    got.push(e);
  }
  assert.equal(opened, 2); // the first connection and the resumed one
  assert.deepEqual(got.map((e) => (e.event === "message" ? e.data.seq : e.data.status)), [1, 2, 3, "succeeded"]);
  assert.equal(seen[0]!.headers.get("Accept"), "text/event-stream");
  assert.equal(seen[0]!.headers.get("Last-Event-ID"), null);
  assert.equal(seen[1]!.headers.get("Last-Event-ID"), "2");
});

test("a reconnection rides out a restarting server, but not a 4xx", async () => {
  const event = (seq: number) => `id: ${seq}\ndata: ${JSON.stringify({ seq, ts: "2026-10-06T10:00:00Z", level: "info", message: "m" })}\n\n`;
  const op = { id: "o", app_id: "a", kind: "deploy", status: "succeeded", payload: {}, idempotency_key: "k", attempt: 1, max_attempts: 3, created_at: "2026-10-06T10:00:00Z" };
  const down = () => new Response("bad gateway", { status: 502 });
  const { client, seen } = fake(
    sse(`retry: 1\n\n${event(1)}`), // dropped
    down(), // the API restarting
    down(),
    sse(`retry: 1\n\n${event(2)}event: end\ndata: ${JSON.stringify(op)}\n\n`),
  );
  const seqs: number[] = [];
  for await (const e of client.stream("streamEvents", { path: { id: "o" } })) {
    if (e.event === "message") {
      seqs.push(e.data.seq);
    }
  }
  assert.deepEqual(seqs, [1, 2]);
  assert.equal(seen.length, 4);
  assert.equal(seen[3]!.headers.get("Last-Event-ID"), "1");

  // The operation is gone (a finished delete): no retrying a 404.
  const gone = fake(sse(`retry: 1\n\n${event(1)}`), new Response("{}", { status: 404, headers: { "Content-Type": "application/problem+json" } }));
  await assert.rejects(async () => {
    for await (const _ of gone.client.stream("streamEvents", { path: { id: "o" } })) {
      // drain
    }
  }, ApiError);
  assert.equal(gone.seen.length, 2);
});

test("a stream without retry, such as logs, ends with its connection", async () => {
  const { client, seen } = fake(sse(`: logs\n\ndata: ${JSON.stringify({ ts: "2026-10-06T10:00:00Z", stream: "stdout", line: "hi" })}\n\n`));
  const lines: string[] = [];
  for await (const e of client.stream("streamLogs", { path: { app: "web" }, query: { tail: 10 } })) {
    if (e.event === "message") {
      lines.push(e.data.line);
    }
  }
  assert.deepEqual(lines, ["hi"]);
  assert.equal(seen.length, 1);
  assert.equal(seen[0]!.url, "https://api.test/v1/apps/web/logs?tail=10");
});

// Compile-time checks: tsc must reject each marked line. Never called.
export function typeChecks(c: Client): void {
  // @ts-expect-error getApp needs its path parameter
  void c.call("getApp");
  // @ts-expect-error an unknown field in the body
  void c.call("createApp", { body: { slug: "a", repo: "o/r", branch: "main", port: 80, colour: "red" } });
  // @ts-expect-error a stream is not a call
  void c.call("streamLogs", { path: { app: "a" } });
  // @ts-expect-error tail is a number
  void c.stream("streamLogs", { path: { app: "a" }, query: { tail: "10" } });
}
