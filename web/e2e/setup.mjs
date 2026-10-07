// Playwright's global setup for the UI tests (P6.6): everything the pages
// talk to, real except the worker, which the UI never reaches directly.
//
//   1. test/uiseed: a fresh PostgreSQL database, migrated and seeded (apps,
//      25 releases, admin/deploy/read tokens), and an HPKE public key.
//   2. shipyard-api, built from this checkout, on a free loopback port.
//   3. A stand-in worker log socket (worker-logs.mjs).
//   4. The production build, served as Caddy serves it (server.mjs).
//
// Tests read the result from the environment (SHIPYARD_UI_*). The returned
// function stops and removes everything, the database included.

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { serve } from "./server.mjs";
import { workerLogs } from "./worker-logs.mjs";

const web = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repo = resolve(web, "..");
const exe = process.platform === "win32" ? ".exe" : "";

function freePort() {
  return new Promise((ok) => {
    const s = createServer().listen(0, "127.0.0.1", () => {
      const { port } = s.address();
      s.close(() => ok(port));
    });
  });
}

async function waitFor(url, what, proc) {
  for (let i = 0; i < 120; i++) {
    if (proc.exitCode !== null) {
      throw new Error(`${what} exited with ${proc.exitCode}`);
    }
    try {
      if ((await fetch(url)).ok) {
        return;
      }
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`${what} did not answer at ${url}`);
}

// started exposes what the tests need, from a seed (test/uiseed's JSON).
function started(seed, ui, worker) {
  process.env.SHIPYARD_UI_URL = ui.url;
  process.env.SHIPYARD_UI_WORKER = worker;
  for (const [scope, token] of Object.entries(seed.tokens)) {
    process.env[`SHIPYARD_UI_TOKEN_${scope.toUpperCase()}`] = token;
  }
}

export default async function setup() {
  // An API another host runs (e2e/serve-api.sh): only the UI is served here.
  if (process.env.SHIPYARD_UI_EXTERNAL) {
    const seed = JSON.parse(readFileSync(process.env.SHIPYARD_UI_EXTERNAL, "utf8"));
    execFileSync("node", ["scripts/build.mjs"], { cwd: web, stdio: "inherit" });
    const api = new URL(seed.api_url);
    const ui = await serve({ dist: join(web, "dist"), api: { hostname: api.hostname, port: Number(api.port) }, csp: seed.csp });
    started(seed, ui, "none");
    return async () => ui.server.close();
  }

  process.env.SHIPYARD_TEST_DATABASE_URL ??= "postgres://shipyard:shipyard@127.0.0.1:54320/postgres?sslmode=disable";
  const tmp = mkdtempSync(join(tmpdir(), "shipyard-ui-"));
  const kekDir = join(tmp, "kek");

  const seed = JSON.parse(execFileSync("go", ["run", "./test/uiseed", "create", kekDir], { cwd: repo, encoding: "utf8" }));
  const drop = () => execFileSync("go", ["run", "./test/uiseed", "drop", seed.database], { cwd: repo, stdio: "inherit" });
  try {
    const bin = join(tmp, "shipyard-api" + exe);
    execFileSync("go", ["build", "-o", bin, "./cmd/shipyard-api"], { cwd: repo, stdio: "inherit" });
    execFileSync("node", ["scripts/build.mjs"], { cwd: web, stdio: "inherit" });

    // Node on Windows listens on named pipes, not Unix sockets, which the
    // API dials: there the logs page meets "the worker is not running".
    const socket = join(tmp, "logs.sock");
    const logs = process.platform === "win32" ? undefined : await workerLogs(socket);
    const apiPort = await freePort();
    const api = spawn(bin, ["serve"], {
      env: {
        ...process.env,
        SHIPYARD_DATABASE_URL: seed.database_url,
        SHIPYARD_API_LISTEN: `127.0.0.1:${apiPort}`,
        SHIPYARD_KEK_DIR: kekDir,
        SHIPYARD_KEK_ACTIVE: seed.kek_id,
        SHIPYARD_WORKER_SOCKET: socket,
        SHIPYARD_DNS_PREFLIGHT: "false",
        SHIPYARD_API_RATE: "0", // one client makes every request
        SHIPYARD_AUTH_FAILURES: "0",
        SHIPYARD_LOG_FORMAT: "text",
      },
      stdio: ["ignore", "inherit", "inherit"],
    });
    await waitFor(`http://127.0.0.1:${apiPort}/healthz`, "shipyard-api", api);
    const ui = await serve({ dist: join(web, "dist"), api: { hostname: "127.0.0.1", port: apiPort }, csp: seed.csp });

    started(seed, ui, logs ? "stand-in" : "none");
    return async () => {
      ui.server.close();
      logs?.close();
      api.kill();
      await new Promise((r) => api.once("exit", r));
      drop();
      rmSync(tmp, { recursive: true, force: true });
    };
  } catch (err) {
    drop();
    rmSync(tmp, { recursive: true, force: true });
    throw err;
  }
}
