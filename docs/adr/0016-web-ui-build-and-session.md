# ADR-0016: The web UI is React built by esbuild, served on the API's hostname, with the token in sessionStorage

- **Status:** Accepted
- **Date:** 2026-10-06
- **Deciders:** Project owner (bundler, token storage and hostname chosen 2026-10-06), Claude Code
- **Sources:** `NODE-TS`, `TS-7`, `ESBUILD`, `NPM-SCRIPTS`

## Context

- **Roadmap P6.2:** a React and TypeScript app in `web/`, served as static files by Caddy, with a token login. CLAUDE.md fixes React and says the UI must never be required for a deploy.
- **The API authenticates with bearer tokens** (ADR-0007, ADR-0011), with no cookies or sessions. The UI must hold a token somewhere in the browser.
- **The client is typed already** (ADR-0015). What is left: a bundler, where the token lives, and where the page is served from.

## Decision

- **esbuild** (the owner's choice) bundles `src/main.tsx` into one hashed JS file and one CSS file, minified, with linked source maps and license comments.
  - `web/scripts/build.mjs` writes `dist/index.html`, which references only those two files: there is no inline script or style.
  - In `--dev` mode it rebuilds on change and serves on `127.0.0.1:5173`, proxying `/v1` to the API, so the page is same-origin as in production. There is no hot reload; reload the page.
- **Dependencies** (exact versions): `react` and `react-dom` 19.3.0 at runtime; `esbuild` 0.28.2 and `@types/react`/`@types/react-dom` 19.3.0 for development, besides `typescript`. npm's install scripts stay blocked (npm 11's default): esbuild runs from its platform package without its postinstall `[NPM-SCRIPTS]`.
- **The token lives in `sessionStorage`** (the owner's choice): it survives a reload and is gone when the tab closes.
  - It is only ever sent as an `Authorization` header, never as a cookie, so the API needs no CSRF defense.
  - A saved token is checked again (`whoami`) before it is used.
  - Any 401 signs the tab out, with a message. Sign-out clears the token.
  - If storage is unavailable, the session lasts as long as the page.
- **Served on `SHIPYARD_API_HOSTNAME`** (the owner's choice): the UI at `/`, the API at `/v1`. Same origin means no CORS, and the token is only sent where the page came from.
  - **P6.2b** adds the serving: Caddy mounts the built directory read-only and serves it with a Content-Security-Policy that allows only same-origin scripts, styles and connections, and no framing.

## Consequences

- **Positive:**
  - One small build tool. The page has no inline code, so a strict CSP fits it.
  - No CORS and no cookies, so no CSRF.
  - Pure logic (the session, the client, the SSE parser) is tested on Node's own runner; `tsc` checks the components.
- **Negative / risks:**
  - **A script injected into the page could read the token** from `sessionStorage`. The CSP (P6.2b) and React's escaping are the defenses. Memory-only storage was offered and declined, for convenience.
  - **No component tests yet.** A DOM test library would be one more dependency; P6.6 brings Playwright for the flows.
  - **Without hot reload,** development reloads the page by hand.
  - The JavaScript is about 224 KB minified, mostly React.

## Alternatives considered

- **Vite 8 with `@vitejs/plugin-react`:** hot reload and the most common React setup, but many more packages (Rolldown, Lightning CSS, PostCSS…).
- **`localStorage`:** the token outlives the tab, so a stolen copy stays useful longer.
- **Memory only:** the safest, but every reload asks for the token again.
- **A separate UI hostname:** the API would need CORS, plus a second certificate and DNS record.
