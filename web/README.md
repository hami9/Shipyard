# web

Shipyard's web UI, Phase 6 of the [roadmap](../docs/ROADMAP.md). It is never required for a deploy: the CLI does everything.

React, built by esbuild, served on the API's own hostname, with the API token in the tab's `sessionStorage` ([ADR-0016](../docs/adr/0016-web-ui-build-and-session.md)). Today it has the token login, the app list, each app's settings and releases, and rollback; live events, logs, environment and domains come with P6.4 and P6.5.

| Path | What |
| --- | --- |
| `src/main.tsx`, `src/App.tsx` | The entry point, and the switch between login and the signed-in shell |
| `src/Login.tsx`, `src/Shell.tsx` | The token form; the header with the token's name, scopes and expiry, sign-out, and the page switch |
| `src/AppList.tsx`, `src/AppDetail.tsx` | The app list; an app's settings and its releases, a page at a time |
| `src/Rollback.tsx`, `src/rollbackRules.ts` | Confirming a rollback (one `Idempotency-Key` per confirmation), and the configuration choice after a 409 |
| `src/routes.ts`, `src/nav.tsx` | Paths to pages and back; navigation over the History API (`navigate`, `useRoute`, `Link`) |
| `src/useApi.ts`, `src/format.ts` | Loading data into a page; how times, sizes, commits and statuses read |
| `src/session.ts` | The token in `sessionStorage`, `signIn` (checks it with `whoami`), and the client for a session |
| `src/api/schema.ts` | **Generated** from [api/openapi.json](../api/openapi.json) by `internal/openapits`. Do not edit: run `make web-types` ([ADR-0015](../docs/adr/0015-openapi-and-typescript-client.md)) |
| `src/api/client.ts` | `Client`: `call` for JSON operations, `stream` for event streams (resumed with `Last-Event-ID`), `ApiError` for problem details, `onUnauthorized` for a token that stopped working |
| `src/api/sse.ts` | A server-sent events parser over `fetch`, which, unlike `EventSource`, can send the bearer token |
| `scripts/build.mjs` | The esbuild build (`dist/`), and the dev server |

```ts
const api = new Client({ token });
const { apps } = await api.call("listApps");
for await (const e of api.stream("streamEvents", { path: { id } })) {
  if (e.event === "end") console.log(e.data.status);
}
```

## Development

Node.js 24 or newer. Run the API (`make run-api`, or set `SHIPYARD_API_URL` to another one), then:

```bash
make web-dev
```

Open `http://127.0.0.1:5173` and sign in with a token from `shipyard-api token create`. The dev server proxies `/v1` to the API, so the page is same-origin as in production. After a change, reload the page.

## Checks

```bash
make web-check
```

It runs `npm ci`, `tsc` (the components, and the `@ts-expect-error` lines in the tests), `node --test` (Node runs the tests' TypeScript directly), and a production build into `dist/`. That the generated `schema.ts` is current is a Go test, run by `make test`.
