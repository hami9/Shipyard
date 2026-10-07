// Builds the web UI with esbuild (ADR-0016).
//
//   node scripts/build.mjs        dist/: index.html and hashed, minified assets
//   node scripts/build.mjs --dev  rebuild on change, serve on 127.0.0.1:5173,
//                                 and proxy /v1 to the API (SHIPYARD_API_URL,
//                                 default http://127.0.0.1:8080, `make run-api`)
//
// Plain JavaScript, so it needs no @types/node; tsc checks src/ only.

import * as esbuild from "esbuild";
import { mkdir, rm, writeFile } from "node:fs/promises";
import http from "node:http";

const dev = process.argv.includes("--dev");
const out = dev ? ".dev-web" : "dist";

/** @type {import("esbuild").BuildOptions} */
const options = {
  entryPoints: { app: "src/main.tsx" },
  bundle: true,
  format: "esm",
  target: "es2022",
  jsx: "automatic",
  outdir: `${out}/assets`,
  entryNames: dev ? "[name]" : "[name]-[hash]",
  assetNames: dev ? "[name]" : "[name]-[hash]",
  minify: !dev,
  sourcemap: dev ? "inline" : "linked",
  legalComments: dev ? "none" : "linked",
  metafile: true,
  define: { "process.env.NODE_ENV": JSON.stringify(dev ? "development" : "production") },
  logLevel: "info",
};

// The page has no inline script or style, so the CSP that serves it can
// forbid both (ADR-0016).
function html(js, css) {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Shipyard</title>
<link rel="stylesheet" href="/${css}">
</head>
<body>
<div id="root"></div>
<script type="module" src="/${js}"></script>
</body>
</html>
`;
}

// assets names the entry's JS and CSS files from the metafile, relative to out.
function assets(metafile) {
  let js, css;
  for (const [file, o] of Object.entries(metafile.outputs)) {
    const rel = file.slice(out.length + 1);
    if (o.entryPoint === "src/main.tsx") {
      js = rel;
      css = o.cssBundle?.slice(out.length + 1);
    }
  }
  if (!js || !css) {
    throw new Error("esbuild produced no app.js or app.css");
  }
  return { js, css };
}

await rm(out, { recursive: true, force: true });
await mkdir(`${out}/assets`, { recursive: true });

if (!dev) {
  const result = await esbuild.build(options);
  const { js, css } = assets(result.metafile);
  await writeFile(`${out}/index.html`, html(js, css));
  console.log(`dist/index.html → ${js}, ${css}`);
} else {
  const ctx = await esbuild.context(options);
  await writeFile(`${out}/index.html`, html("assets/app.js", "assets/app.css"));
  await ctx.watch();
  const { port } = await ctx.serve({ host: "127.0.0.1", servedir: out, fallback: `${out}/index.html` });
  const api = new URL(process.env.SHIPYARD_API_URL ?? "http://127.0.0.1:8080");

  // Same origin, as in production: /v1 goes to the API, the rest to esbuild.
  http
    .createServer((req, res) => {
      const toAPI = req.url?.startsWith("/v1/") || req.url === "/healthz" || req.url === "/readyz";
      const target = toAPI ? { host: api.hostname, port: api.port || 80 } : { host: "127.0.0.1", port };
      const fwd = http.request(
        { ...target, path: req.url, method: req.method, headers: { ...req.headers, host: `${target.host}:${target.port}` } },
        (up) => {
          res.writeHead(up.statusCode ?? 502, up.headers);
          up.pipe(res); // streams too: events and logs
        },
      );
      fwd.on("error", (err) => {
        res.writeHead(502, { "Content-Type": "text/plain" });
        res.end(`${toAPI ? "the API" : "esbuild"} is not reachable at ${target.host}:${target.port}: ${err.message}\n`);
      });
      req.pipe(fwd);
    })
    .listen(5173, "127.0.0.1", () => {
      console.log(`Shipyard UI on http://127.0.0.1:5173 (API: ${api.origin}). Reload the page after a change.`);
    });
}
