import assert from "node:assert/strict";
import { test } from "node:test";
import { ago, bytes, clock, opTone, shortSHA, statusLabel, tone } from "./format.ts";
import { appendCapped } from "./useStream.ts";
import { canChange, explain, keyProblem, valueProblem } from "./forms.ts";
import { href, parseRoute } from "./routes.ts";
import { ApiError } from "./api/client.ts";
import type { App, Release } from "./api/schema.ts";
import { createBody, durationMs, updateBody, valuesOf } from "./appForm.ts";
import { graces, tokenTone, whoamiOf } from "./tokens.ts";
import { canRollBack, needsConfigChoice, rollbackBody } from "./rollbackRules.ts";

test("rollback: which releases, which tokens", () => {
  const r = (status: Release["status"]): Release => ({
    id: "d", operation_id: "o", kind: "build", status, commit: "c", env_revision: 0, created_at: "2026-10-06T12:00:00Z",
  });
  assert.equal(canRollBack(r("superseded"), ["deploy"]), true);
  assert.equal(canRollBack(r("superseded"), ["admin"]), true);
  assert.equal(canRollBack(r("superseded"), ["read"]), false);
  for (const s of ["active", "failed", "cancelled", "building", "queued"] as const) {
    assert.equal(canRollBack(r(s), ["admin"]), false, s);
  }
});

test("rollback: the secrets-changed 409 and the choice it asks for", () => {
  const problem = (status: number, detail: string) =>
    new ApiError(status, { type: "about:blank", title: "", status, detail }, undefined, detail);
  // The API's message (internal/api/rollbacks.go) names both fields.
  const changed = "secrets changed since this deployment: DB_PASSWORD. Retry with with_current_config (today's values) or with_old_config (the values it ran with)";
  assert.equal(needsConfigChoice(problem(409, changed)), true);
  assert.equal(needsConfigChoice(problem(409, "deployment x is already active")), false);
  assert.equal(needsConfigChoice(problem(422, changed)), false);
  assert.equal(needsConfigChoice(new Error(changed)), false);
  assert.deepEqual(rollbackBody("d1"), { to: "d1" });
  assert.deepEqual(rollbackBody("d1", "current"), { to: "d1", with_current_config: true });
  assert.deepEqual(rollbackBody("d1", "old"), { to: "d1", with_old_config: true });
});

test("routes: paths to pages and back", () => {
  assert.deepEqual(parseRoute("/"), { page: "apps" });
  assert.deepEqual(parseRoute("/apps"), { page: "apps" });
  assert.deepEqual(parseRoute("/apps/"), { page: "apps" });
  assert.deepEqual(parseRoute("/apps/web-1"), { page: "app", slug: "web-1" });
  assert.deepEqual(parseRoute("/apps/web-1/"), { page: "app", slug: "web-1" });
  for (const p of ["/apps/Web", "/apps/-x", "/apps/a/b", "/settings", "/apps/%E0%A4%A", "/apps/x%2F..%2Fy"]) {
    assert.equal(parseRoute(p).page, "missing", p);
  }
  assert.equal(href({ page: "apps" }), "/");
  assert.equal(href({ page: "app", slug: "web-1" }), "/apps/web-1");
  assert.equal(href(parseRoute("/apps/api")), "/apps/api");
});

test("routes: operations and logs", () => {
  const id = "5e21d7f1-0000-4000-8000-000000000001";
  assert.deepEqual(parseRoute(`/operations/${id}`), { page: "operation", id });
  assert.deepEqual(parseRoute("/apps/web/logs"), { page: "logs", slug: "web" });
  for (const p of ["/operations/42", "/operations/", `/operations/${id.toUpperCase()}`, "/apps/web/log", "/apps/Web/logs", "/apps/web/logs/x"]) {
    assert.equal(parseRoute(p).page, "missing", p);
  }
  assert.equal(href({ page: "operation", id }), `/operations/${id}`);
  assert.equal(href({ page: "logs", slug: "web" }), "/apps/web/logs");
});

test("routes: environment and domains", () => {
  assert.deepEqual(parseRoute("/apps/web/env"), { page: "env", slug: "web" });
  assert.deepEqual(parseRoute("/apps/web/domains"), { page: "domains", slug: "web" });
  assert.equal(href({ page: "env", slug: "web" }), "/apps/web/env");
  assert.equal(href({ page: "domains", slug: "web" }), "/apps/web/domains");
  assert.equal(parseRoute("/apps/web/secrets").page, "missing");
});

test("forms: who may change, and what the API accepts", () => {
  assert.equal(canChange(["admin"]), true);
  assert.equal(canChange(["deploy"]), false);
  assert.equal(canChange(["read"]), false);
  for (const k of ["DATABASE_URL", "_X", "a1", "K".repeat(255)]) {
    assert.equal(keyProblem(k), undefined, k);
  }
  for (const k of ["", "1X", "A-B", "A B", "K".repeat(256), "Ä"]) {
    assert.ok(keyProblem(k), k);
  }
  assert.equal(valueProblem("postgres://u:p@h/db"), undefined);
  assert.equal(valueProblem(""), undefined);
  assert.ok(valueProblem("a\0b"));
  assert.equal(valueProblem("x".repeat(64 << 10)), undefined);
  assert.ok(valueProblem("x".repeat((64 << 10) + 1)));
  assert.ok(valueProblem("é".repeat(40000))); // 80 000 bytes in UTF-8
});

test("forms: the API's field errors by field, other errors as one message", () => {
  const p = (status: number, errors?: { field: string; detail: string }[]) =>
    new ApiError(status, { type: "about:blank", title: "", status, detail: "some fields are invalid", ...(errors ? { errors } : {}) }, undefined, "some fields are invalid");
  assert.deepEqual(explain(p(422, [{ field: "hostname", detail: "resolves to 198.51.100.7, which is not this server" }])), {
    message: "",
    fields: { hostname: "resolves to 198.51.100.7, which is not this server" },
  });
  assert.deepEqual(explain(p(422, [{ field: "key", detail: "a" }, { field: "key", detail: "b" }])).fields, { key: "a; b" });
  assert.deepEqual(explain(p(409)), { message: "some fields are invalid", fields: {} });
  assert.deepEqual(explain(new TypeError("fetch failed")), { message: "The API is not reachable.", fields: {} });
});

test("routes: new app and settings", () => {
  assert.deepEqual(parseRoute("/new"), { page: "new" });
  assert.deepEqual(parseRoute("/apps/new"), { page: "app", slug: "new" }); // an app may be called new
  assert.deepEqual(parseRoute("/apps/web/settings"), { page: "settings", slug: "web" });
  assert.equal(href({ page: "new" }), "/new");
  assert.equal(href({ page: "settings", slug: "web" }), "/apps/web/settings");
});

test("durations as Go writes and reads them", () => {
  assert.equal(durationMs("90s"), 90_000);
  assert.equal(durationMs("1m30s"), 90_000);
  assert.equal(durationMs("1m0s"), 60_000);
  assert.equal(durationMs("500ms"), 500);
  assert.equal(durationMs("1.5h"), 5_400_000);
  assert.equal(durationMs("0s"), 0);
  assert.equal(durationMs("0"), 0);
  for (const s of ["", "10", "5 min", "s", "1d", "-1s", "1m30"]) {
    assert.equal(durationMs(s), undefined, s);
  }
});

const app: App = {
  id: "0b6e1c1e-2a0c-4bde-9c43-0d3f1c2b9a10", slug: "web", repo: "acme/web", branch: "main", dockerfile_path: "Dockerfile",
  build_context: ".", port: 8080, health_path: "/healthz", health_timeout: "1m0s", cpu_limit: 1, memory_limit: 512 << 20,
  stop_timeout: "10s", auto_deploy: false, github_installation_id: null, created_at: "2026-10-07T10:00:00Z", updated_at: "2026-10-07T10:00:00Z",
};

test("a new app: defaults, required fields, and the request", () => {
  const v = valuesOf();
  assert.deepEqual(createBody(v).fields, { slug: "is required", repo: "is required", port: "a number from 1 to 65535" });
  const { body, fields } = createBody({ ...v, slug: " web ", repo: "acme/web", port: "8080", memory_mib: "256" });
  assert.deepEqual(fields, {});
  assert.deepEqual(body, {
    slug: "web", repo: "acme/web", branch: "main", port: 8080, dockerfile_path: "Dockerfile", build_context: ".",
    health_path: "/", health_timeout: "60s", stop_timeout: "10s", cpu_limit: 1, memory_limit: 256 << 20, auto_deploy: false,
  });
  const bad = createBody({ ...v, slug: "web", repo: "a/b", port: "80", cpu_limit: "0", memory_mib: "5", health_timeout: "1 min", github_installation_id: "x" });
  assert.deepEqual(Object.keys(bad.fields).sort(), ["cpu_limit", "github_installation_id", "health_timeout", "memory_limit"]);
  assert.equal(bad.body, undefined);
});

test("a change: only what differs, durations compared by value", () => {
  const v = valuesOf(app);
  assert.equal(v.memory_mib, "512");
  assert.deepEqual(updateBody(v, app), { body: {}, fields: {} }); // nothing changed, even "1m0s" vs itself
  assert.deepEqual(updateBody({ ...v, health_timeout: "60s" }, app).body, {}); // the same duration, written another way
  assert.deepEqual(updateBody({ ...v, port: "3000", auto_deploy: true, memory_mib: "1024", github_installation_id: "42" }, app).body, {
    port: 3000, auto_deploy: true, memory_limit: 1 << 30, github_installation_id: 42,
  });
  assert.deepEqual(updateBody({ ...v, port: "99999" }, app), { fields: { port: "a number from 1 to 65535" } });
});

test("tokens: the route, the session after a rotation, graces, tones", () => {
  assert.deepEqual(parseRoute("/tokens"), { page: "tokens" });
  assert.equal(href({ page: "tokens" }), "/tokens");
  const t = { prefix: "shp_abcdefgh", name: "ci", scopes: ["deploy" as const], status: "active" as const, expires_at: "2027-01-01T00:00:00Z",
    last_used_at: null, revoked_at: null, created_at: "2026-10-07T00:00:00Z" };
  assert.deepEqual(whoamiOf(t), { token: "shp_abcdefgh", name: "ci", scopes: ["deploy"], expires_at: "2027-01-01T00:00:00Z" });
  // The API refuses more than 7 days (api/openapi.json RotateRequest).
  assert.ok(graces.every((g) => g.seconds >= 0 && g.seconds <= 604800));
  assert.equal(graces[0]?.seconds, 0);
  assert.equal(tokenTone("active"), "ok");
  assert.equal(tokenTone("revoked"), "bad");
  assert.equal(tokenTone("expired"), "idle");
});

test("appendCapped keeps the newest", () => {
  assert.deepEqual(appendCapped([1, 2], [3], 5), [1, 2, 3]);
  assert.deepEqual(appendCapped([1, 2, 3, 4], [5, 6, 7], 5), [3, 4, 5, 6, 7]);
  assert.deepEqual(appendCapped([], [1, 2, 3], 2), [2, 3]);
});

test("clock and operation tones", () => {
  // Local time: offsets are not always whole hours (Iran is +03:30).
  const t = "2026-10-06T12:03:04Z";
  const d = new Date(t);
  const two = (n: number) => String(n).padStart(2, "0");
  assert.equal(clock(t), `${two(d.getHours())}:${two(d.getMinutes())}:04`);
  assert.equal(clock("never"), "never");
  assert.equal(opTone("succeeded"), "ok");
  assert.equal(opTone("failed"), "bad");
  assert.equal(opTone("cancelled"), "idle");
  assert.equal(opTone("running"), "busy");
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
