// The UI as Caddy serves it (ADR-0016), for the Playwright tests: the
// production build from dist/, index.html for any path that is no file, the
// same Content-Security-Policy, and /v1 proxied to the API. A page that
// needed an inline script or a foreign origin would break here as in
// production.

import { readFile } from "node:fs/promises";
import http from "node:http";
import { extname, join, normalize } from "node:path";

const types = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".map": "application/json", ".txt": "text/plain" };

/** serve listens on a free loopback port and resolves to the server and its URL. */
export function serve({ dist, api, csp }) {
  const server = http.createServer(async (req, res) => {
    const path = decodeURIComponent(new URL(req.url ?? "/", "http://x").pathname);
    if (path.startsWith("/v1/") || path === "/healthz") {
      const fwd = http.request({ host: api.hostname, port: api.port, path: req.url, method: req.method, headers: req.headers }, (up) => {
        res.writeHead(up.statusCode ?? 502, up.headers);
        up.pipe(res);
      });
      fwd.on("error", () => res.writeHead(502).end());
      req.pipe(fwd);
      return;
    }
    const headers = {
      "Content-Security-Policy": csp,
      "X-Content-Type-Options": "nosniff",
      "X-Frame-Options": "DENY",
      "Referrer-Policy": "no-referrer",
    };
    let file = join(dist, normalize(path));
    if (!file.startsWith(dist)) {
      res.writeHead(400).end();
      return;
    }
    let body;
    try {
      body = await readFile(file);
    } catch {
      file = join(dist, "index.html");
      body = await readFile(file);
    }
    res.writeHead(200, { ...headers, "Content-Type": types[extname(file)] ?? "application/octet-stream" });
    res.end(body);
  });
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => resolve({ server, url: `http://127.0.0.1:${server.address().port}` }));
  });
}
