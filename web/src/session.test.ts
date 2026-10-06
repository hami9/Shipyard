import assert from "node:assert/strict";
import { test } from "node:test";
import { Client } from "./api/client.ts";
import { expiresIn, forgetToken, saveToken, savedToken, signIn, type TokenStore } from "./session.ts";

function memory(): TokenStore & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  };
}

test("the token round-trips through storage", () => {
  const s = memory();
  assert.equal(savedToken(s), undefined);
  saveToken("shp_abc", s);
  assert.equal(savedToken(s), "shp_abc");
  forgetToken(s);
  assert.equal(savedToken(s), undefined);
});

test("storage that throws or is missing loses nothing but persistence", () => {
  const broken: TokenStore = {
    getItem: () => { throw new Error("blocked"); },
    setItem: () => { throw new Error("blocked"); },
    removeItem: () => { throw new Error("blocked"); },
  };
  saveToken("shp_abc", broken);
  assert.equal(savedToken(broken), undefined);
  forgetToken(broken);
  assert.equal(savedToken(undefined), undefined);
});

const whoami = { token: "shp_abcdefgh", name: "admin", scopes: ["admin"], expires_at: null };

function client(status: number, body: unknown, headers: Record<string, string> = {}): (token: string) => Client {
  return (token) =>
    new Client({
      token,
      baseUrl: "https://api.test",
      fetch: async (_url, init) => {
        assert.equal(new Headers(init?.headers).get("Authorization"), `Bearer ${token}`);
        return new Response(JSON.stringify(body), {
          status,
          headers: { "Content-Type": status < 400 ? "application/json" : "application/problem+json", ...headers },
        });
      },
    });
}

test("signIn: a good token, trimmed", async () => {
  assert.deepEqual(await signIn("  shp_good_token\n", client(200, whoami)), { token: "shp_good_token", whoami });
});

test("signIn: the messages the form shows", async () => {
  const problem = (status: number) => ({ type: "about:blank", title: "", status, detail: "no" });
  assert.match(String(await signIn("not-a-token", client(200, whoami))), /starts with shp_/);
  assert.match(String(await signIn("shp_x", client(401, problem(401)))), /invalid, expired, or revoked/);
  assert.match(String(await signIn("shp_x", client(429, problem(429), { "Retry-After": "30" }))), /30 seconds/);
  assert.match(String(await signIn("shp_x", client(503, problem(503)))), /answered 503: no/);
  const down = () => new Client({ token: "shp_x", fetch: async () => { throw new TypeError("fetch failed"); } });
  assert.match(String(await signIn("shp_x", down)), /not reachable/);
});

test("a 401 anywhere tells the app before the error", async () => {
  let told = 0;
  const c = new Client({
    token: "shp_x",
    onUnauthorized: () => told++,
    fetch: async () => new Response("{}", { status: 401, headers: { "Content-Type": "application/problem+json" } }),
  });
  await assert.rejects(c.call("listApps"));
  assert.equal(told, 1);
});

test("expiresIn", () => {
  const now = new Date("2026-10-06T12:00:00Z");
  assert.equal(expiresIn(null, now), "never expires");
  assert.equal(expiresIn("2026-10-05T12:00:00Z", now), "expired");
  assert.equal(expiresIn("2026-10-06T18:00:00Z", now), "expires today");
  assert.equal(expiresIn("2026-10-07T13:00:00Z", now), "expires tomorrow");
  assert.equal(expiresIn("2027-01-04T12:00:00Z", now), "expires in 90 days");
});
