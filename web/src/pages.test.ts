import assert from "node:assert/strict";
import { test } from "node:test";
import { ago, bytes, shortSHA, statusLabel, tone } from "./format.ts";
import { href, parseRoute } from "./routes.ts";

test("routes: paths to pages and back", () => {
  assert.deepEqual(parseRoute("/"), { page: "apps" });
  assert.deepEqual(parseRoute("/apps"), { page: "apps" });
  assert.deepEqual(parseRoute("/apps/"), { page: "apps" });
  assert.deepEqual(parseRoute("/apps/web-1"), { page: "app", slug: "web-1" });
  assert.deepEqual(parseRoute("/apps/web-1/"), { page: "app", slug: "web-1" });
  for (const p of ["/apps/Web", "/apps/-x", "/apps/a/b", "/tokens", "/apps/%E0%A4%A", "/apps/x%2F..%2Fy"]) {
    assert.equal(parseRoute(p).page, "missing", p);
  }
  assert.equal(href({ page: "apps" }), "/");
  assert.equal(href({ page: "app", slug: "web-1" }), "/apps/web-1");
  assert.equal(href(parseRoute("/apps/api")), "/apps/api");
});

test("ago", () => {
  const now = new Date("2026-10-06T12:00:00Z");
  assert.equal(ago("2026-10-06T11:59:30Z", now), "just now");
  assert.equal(ago("2026-10-06T11:57:00Z", now), "3 min ago");
  assert.equal(ago("2026-10-06T09:00:00Z", now), "3 h ago");
  assert.equal(ago("2026-10-01T12:00:00Z", now), "5 d ago");
  assert.equal(ago("2026-01-01T12:00:00Z", now), "2026-01-01");
  assert.equal(ago("soon", now), "soon");
});

test("bytes, SHAs, statuses", () => {
  assert.equal(bytes(536870912), "512 MiB");
  assert.equal(bytes(2 * 1024 ** 3), "2 GiB");
  assert.equal(bytes(6291456), "6 MiB");
  assert.equal(bytes(7000000), "6.7 MiB");
  assert.equal(shortSHA("2a639c871743d926863a3cb2c336f8ba42bbf368"), "2a639c871743");
  assert.equal(tone("active"), "ok");
  assert.equal(tone("failed"), "bad");
  assert.equal(tone("superseded"), "idle");
  assert.equal(tone("health_checking"), "busy");
  assert.equal(statusLabel("health_checking"), "health checking");
});
