import assert from "node:assert/strict";
import { test } from "node:test";
import { ago, bytes, clock, opTone, shortSHA, statusLabel, tone } from "./format.ts";
import { appendCapped } from "./useStream.ts";
import { canChange, explain, keyProblem, valueProblem } from "./forms.ts";
import { href, parseRoute } from "./routes.ts";
import { ApiError } from "./api/client.ts";
import type { Release } from "./api/schema.ts";
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
  for (const p of ["/apps/Web", "/apps/-x", "/apps/a/b", "/tokens", "/apps/%E0%A4%A", "/apps/x%2F..%2Fy"]) {
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
