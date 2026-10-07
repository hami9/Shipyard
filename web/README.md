# web

Shipyard's web UI, Phase 6 of the [roadmap](../docs/ROADMAP.md). It is never required for a deploy: the CLI does everything.

React, built by esbuild, served on the API's own hostname, with the API token in the tab's `sessionStorage` ([ADR-0016](../docs/adr/0016-web-ui-build-and-session.md)). It does everything the CLI does but install: the token login and token management; creating, changing and deleting apps; their releases, environment and domains; deploy and rollback; and live operation events and logs.

| Path | What |
| --- | --- |
| `src/main.tsx`, `src/App.tsx` | The entry point, and the switch between login and the signed-in shell |
| `src/Login.tsx`, `src/Shell.tsx` | The token form; the header with the token's name, scopes and expiry, sign-out, and the page switch |
| `src/AppList.tsx`, `src/AppDetail.tsx` | The app list; an app's settings and its releases, a page at a time |
| `src/OperationView.tsx`, `src/Logs.tsx` | An operation's events, live until its end event; an app's logs with tail, follow and stop. `EventLog` keeps to the bottom unless the reader scrolled up |
| `src/AppEditor.tsx`, `src/appForm.ts` | New app, an app's settings, and deleting it behind its typed name; form values to requests (only changed fields in a PATCH; Go durations compared by value) |
| `src/TokensPage.tsx`, `src/tokens.ts` | Rotating the signed-in token (the page switches to the new one and shows it once); listing and revoking tokens (admin) |
| `src/Deploy.tsx` | Deploying the branch head or a commit, then following the operation |
| `src/EnvPage.tsx`, `src/DomainsPage.tsx`, `src/forms.ts` | An app's variables (keys only; values write-only) and hostnames; the checks a form makes first, and the API's field errors (`explain`) |
| `src/useStream.ts` | Following one event stream while a page shows it: batched renders, at most 2000 kept, stopped when the page leaves |
| `src/Rollback.tsx`, `src/rollbackRules.ts` | Confirming a rollback (one `Idempotency-Key` per confirmation), and the configuration choice after a 409 |
| `src/routes.ts`, `src/nav.tsx` | Paths to pages and back; navigation over the History API (`navigate`, `useRoute`, `Link`) |
| `src/a11y.tsx` | Keyboard and focus behavior: `ConfirmButton` (asks again in place, Cancel focused, Escape), `useDialogFocus` (panels take focus, Escape closes, focus returns), `focusHeading` |
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

## End-to-end tests

```bash
make test-ui
```

Playwright (Chromium) against the real API and PostgreSQL (`make dev-up` first). `e2e/setup.mjs`:
- seeds a throwaway database (`test/uiseed`: two apps, 25 releases, admin, deploy and read tokens);
- builds and starts `shipyard-api`, and a stand-in worker log socket (`e2e/worker-logs.mjs`);
- serves the production build as Caddy does, CSP included (`e2e/server.mjs`).

Every test signs in through the login form. `e2e/helpers.ts` has `accessible(page)`, axe's WCAG 2.2 AA rules, and `cspViolations(page)`. Windows with Docker in WSL: see `make test-ui` in [docs/DEVELOPMENT.md](../docs/DEVELOPMENT.md).
