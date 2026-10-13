# nginx Edge, Path Mounts, Volumes and Serial Deploys: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Shipyard run behind an existing host nginx (no Caddy), mount apps under a path of a host nginx already serves, give apps persistent volumes, and deploy single-copy apps serially, so it can run on the hami9 VPS without touching `hami9.ir`.

**Architecture:**
- **Routing:** the worker's routing layer gets an `Edge` interface. Today's Caddy code becomes `CaddyEdge`. A new `NginxEdge` renders nginx files from the routes table and hands them to a small root helper, which swaps them in, runs `nginx -t` (restoring on failure) and reloads. The route is then verified through a unix-socket `verify` server, as with Caddy.
- **Data model:**
  - routes gain a `path_prefix`, and are activated by route ID;
  - apps gain named volumes and a `deploy_strategy`.

**Tech Stack:**
- Go 1.26 (`toolchain go1.26.8`), pgx/v5, moby client;
- PostgreSQL 18;
- nginx 1.24 (Ubuntu 24.04), certbot 2.9 webroot;
- the web UI: React, built with esbuild, tested with Playwright.

**Spec:** `docs/superpowers/specs/2026-10-13-nginx-edge-design.md`. Read it and `CLAUDE.md` first.

This is plan 1 of 3. Plan 2 is the VPS install, plan 3 the broadcast bot and Mini App; both are written once this plan's release is cut.

## Global Constraints

- **Repo rules:** CLAUDE.md applies in full.
  - **Commits:** subjects of 1–2 words, plus the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - **Every slice** adds a WORKLOG entry and a CHANGELOG `[Unreleased]` line, ticks the roadmap, and stays at or under about 400 changed lines.
- **Defaults:** `SHIPYARD_EDGE` defaults to `caddy`. With the default, behaviour, rendered Caddy JSON and every existing test are unchanged.
- **Container safety:** containers never publish ports and never get host bind mounts (invariant 10). Volumes are Docker named volumes only.
- **API and edge:** the API never touches Docker, nginx, the helper or the edge files (invariant 1).
- **Pure render:** the edge config is a pure function of the routes table, pending switches and settings. Every render output has golden tests.
- **Edge input safety:**
  - Nothing from a repository, an env value or an HTTP header reaches nginx config.
  - Hostnames use `app.NormalizeHostname`.
  - Prefixes match `^/[a-z0-9][a-z0-9/_-]{0,62}/$` (leading and trailing slash, no `//`, no `..`).
- **Helper verbs:** exactly `apply <staging-dir>`, `cert <hostname>` and `status`. Any other argv exits with code 2 and touches nothing.
- **Fixed paths (nginx edge):**
  - config `/etc/nginx/shipyard/` (`sites.conf`, `verify.conf`, `mounts/<host>.conf`);
  - ACME webroot `/var/lib/shipyard/acme`;
  - verify socket `/run/shipyard/nginx/verify.sock` (directory `0750`, group `shipyard-caddy`, reused as the edge group);
  - certificates `/etc/letsencrypt/live/<host>/{fullchain,privkey}.pem`.
- **Panel defaults:**
  - prefix `/admin-shipyard/`;
  - API reached at `<prefix>v1/` and `<prefix>hooks/github`, with the prefix stripped to `/v1/…` and `/hooks/github`;
  - `X-Robots-Tag: noindex, nofollow, noarchive`;
  - `limit_req` of 5 r/s, burst 20;
  - `client_max_body_size` of 1m for the API and 25m for hooks;
  - `auth_basic` with `/etc/nginx/shipyard/htpasswd` on everything except hooks.
- **Volumes:** at most 4 per app. Names match `^[a-z][a-z0-9-]{0,30}$`. Docker volume name `shipyard-vol-<slug>-<name>`, label `io.shipyard.app=<slug>`. The mount path is absolute, clean, not `/` and not under `/proc`, `/sys` or `/dev`.
- **Deploy strategy:** `deploy_strategy` is `overlap` (the default) or `serial`.
- **Builder idle stop:** `SHIPYARD_BUILDER_IDLE_STOP` defaults to `0` (off). The VPS uses `10m`.

## Review Focus

1. **A broken nginx render or a failing `nginx -t`:**
   - the files that were live stay live, and nginx is never reloaded;
   - the deploy fails and the old route keeps serving.
   - Test: `TestHelperApplyRestoresOnFailedTest` (Task 4) and `TestNginxEdgeApplyFailureKeepsServing` (Task 5).
2. **Hostile hostnames or prefixes reaching the renderer** (`;`, `{`, `}`, newline, `$`, quote, space, `..`, `//`): the renderer refuses them with `ErrInvalid` and produces no file. Test: `TestRenderNginxRefusesHostileInput` (Task 3).
3. **A mount that would shadow the site:** a prefix equal to, or inside, a reserved path (`/api/`, `/blog/x/`), or `/`, is refused with 422 by the API. Test: `TestAddDomainRefusesReservedPrefix` (Task 7).
4. **A container restarted by Docker with a new IP:** a sync re-renders with the new IP within one reconcile, or within seconds through the start event. An unchanged render does not reload. Test: `TestNginxEdgeRerendersOnIPChange` (Task 5), and `TestStartEventsTriggerSync` (Task 6).
5. **A serial deploy whose candidate fails the health check:** the previous container is started again and its route kept, the deploy is marked failed, and no route points at the candidate. Test: `TestSerialDeployRestoresPreviousOnUnhealthy` (Task 11).

---

### Task 1: ADRs and roadmap

**Files:**
- Create: `docs/adr/0017-nginx-edge-and-path-mounts.md`, `docs/adr/0018-volumes-and-serial-deploys.md`
- Modify: `docs/adr/0016-*.md` (implementation note: the UI under a path through `<base href>`), `docs/adr/README.md`, `CLAUDE.md` §3, `docs/ROADMAP.md` (new phase `P8: nginx edge and stateful apps`, items P8.1–P8.13 = Tasks 2–14), `docs/ARCHITECTURE.md` §5 and §7 (pointer paragraphs only), `docs/WORKLOG.md`

- [ ] **Step 1: Write ADR-0017** from spec §4.1, §4.2, §4.4 and §4.5 using `0000-template.md`, with status `Proposed`.
  - **Alternatives considered:** Caddy behind nginx; host Caddy.
  - **Why nginx owns 80/443:** the existing site.
  - **The helper's verbs and sudoers line:**
    ```
    shipyard-worker ALL=(root) NOPASSWD: /usr/local/bin/shipyard-edge-helper
    ```
  - **Invariants 2, 11 and 12:** the new wording, copied verbatim from spec §4.1.
- [ ] **Step 2: Write ADR-0018** from spec §4.3, with status `Proposed`. Include the downtime trade-off, rollback-not-restoring-data, and invariant 5's serial wording.
- [ ] **Step 3: Update `CLAUDE.md` §3.** Invariants 2, 11 and 12 get the new text, each with "(ADR-0017)". Invariant 5 gets the serial clause with "(ADR-0018)".
- [ ] **Step 4: Add P8 to `ROADMAP.md`** with exit criteria. Add a WORKLOG entry and set `Current status` → Next task: P8.1.
- [ ] **Step 5: Commit** `ADR nginx`. The owner accepts both ADRs (status → `Accepted`) before Task 2 starts. Ask in chat; do not proceed without it.

### Task 2: `Edge` interface; Caddy behind it (no behaviour change)

**Files:**
- Create: `internal/routing/edge.go`, `internal/routing/caddy_edge.go`
- Modify: `internal/routing/router.go`, `cmd/shipyard-worker/main.go:577-600` (`syncRoutes`)
- Test: `internal/routing/router_test.go` (switch to a fake `Edge`), `internal/routing/caddy_edge_test.go` (moved check-loop tests)

**Interfaces:**
- Produces:
  ```go
  // edge.go
  type Route struct { Hostname, PathPrefix, Upstream string } // PathPrefix "" = whole host
  type Edge interface {
      Apply(ctx context.Context, routes []Route) (changed bool, err error)
      // Check requests path on host through the edge until 2xx/3xx (5 tries, 500ms apart, 5s each).
      Check(ctx context.Context, host, path string) error
  }
  func NewRouter(routes RouteLister, edge Edge) *Router
  func NewCaddyEdge(s Settings) (*CaddyEdge, error) // s.VerifySocket required, s.validate()
  ```
- `Router.Switch`, `Release` and `Sync` keep their signatures in this task.

- [ ] **Step 1:** Move `Route` to `edge.go` and add `PathPrefix`. `Render` (Caddy) returns `ErrInvalid` for any route with a non-empty `PathPrefix`. Test: `TestRenderRefusesPathPrefix`.
- [ ] **Step 2:** Move `admin`, `verify` and `check` into `CaddyEdge`. `Router` holds an `Edge`.
  - Rewrite `router_test.go` against `fakeEdge{applied [][]Route; checkErr map[string]error}`.
  - Existing assertions stay: pending switches survive `Sync`, `Release` drops the switch, and no routes means nothing is applied.
- [ ] **Step 3:** Run `go test -race ./internal/routing/... ./cmd/...` and `go vet ./...`. Expected: PASS, with the Caddy golden files unchanged (`git diff --exit-code internal/routing/testdata`).
- [ ] **Step 4:** Commit `Edge interface`.

### Task 3: nginx renderer (pure)

**Files:**
- Create: `internal/routing/nginx_render.go`, `internal/routing/testdata/nginx/*.golden`
- Test: `internal/routing/nginx_render_test.go`

**Interfaces:**
- Produces:
  ```go
  type NginxSettings struct {
      VerifySocket   string        // /run/shipyard/nginx/verify.sock
      ACMEWebroot    string        // /var/lib/shipyard/acme
      MountHosts     []string      // hosts nginx already serves; mounts only
      ReservedPaths  []string      // e.g. "/api/", "/blog/"
      Panel          *PanelSettings // nil: no panel (Task 8 fills it)
  }
  type NginxRoute struct { Route; Addr netip.AddrPort; HasCert bool } // Addr zero: no active deployment
  type NginxFiles struct { Sites, Verify string; Mounts map[string]string } // host -> file body
  func RenderNginx(s NginxSettings, routes []NginxRoute) (NginxFiles, error)
  ```

- **Render rules:**
  - **Output:** deterministic, sorted by host and then prefix, with a header comment `# rendered by shipyard-worker; do not edit`.
  - **Host routes:** each non-mount host gets a server on `listen 80` that serves `/.well-known/acme-challenge/` from the webroot and returns `301 https://$host$request_uri` for everything else.
    - With `HasCert`, it also gets `listen 443 ssl` (and `[::]`), `http2 on;`-free syntax valid on 1.24 (`listen 443 ssl http2`), the certificate paths, and `location / { proxy_pass http://<addr>; }` with `Host`, `X-Forwarded-For`, `X-Forwarded-Proto` and `X-Real-IP`.
    - A zero `Addr` renders `return 503 "no active deployment\n"`.
  - **Mount routes** go into `Mounts[host]`: `location <prefix> { proxy_pass http://<addr>/; proxy_set_header X-Forwarded-Prefix <prefix-without-trailing-slash>; … }`.
  - **The verify server** in `Verify` uses `listen unix:<VerifySocket>;` and one `server` per host, with the same locations over plain HTTP and no ACME/redirect block.
  - **Empty input** still renders all three files: comment-only, with `Mounts` holding an empty body for every `MountHosts` entry.

- [ ] **Step 1: Write the failing golden tests** `TestRenderNginxGolden`, with these cases:
  - `empty`;
  - `one-host-no-cert`;
  - `one-host-cert`;
  - `no-active`;
  - `mount-hami9-tg`;
  - `host-and-mount`.

  Regenerate them with `-update`, review the output by eye, and commit the goldens.
- [ ] **Step 2: Write `TestRenderNginxRefusesHostileInput`.** These hostnames must return `ErrInvalid`:
  - `"a;b.ir"`, `"a{.ir"`, `"a\n.ir"`, `"$host.ir"`, `"a\".ir"`, `"a b.ir"`.

  These prefixes must also return `ErrInvalid`:
  - `"/tg"`, `"tg/"`, `"/"`, `"//x/"`, `"/../x/"`, `"/x;/"`, `"/X/"`.

  Further refusals:
  - a mount on a host not in `MountHosts` → `ErrInvalid`;
  - a whole-host route on a `MountHosts` host → `ErrInvalid`;
  - a prefix equal to, or under, a reserved path → `ErrInvalid`.
- [ ] **Step 3: Implement `RenderNginx`** with `text/template`, using only validated values. Run `go test ./internal/routing/ -run Nginx`. Expected: PASS.
- [ ] **Step 4:** Commit `nginx render`.

### Task 4: `shipyard-edge-helper`

**Files:**
- Create: `cmd/shipyard-edge-helper/main.go`, `internal/edgehelper/helper.go`, `deploy/sudoers/shipyard-edge`
- Test: `internal/edgehelper/helper_test.go`

**Interfaces:**
- Produces:
  ```go
  type Runner interface { Run(ctx context.Context, name string, args ...string) (output []byte, err error) }
  type Helper struct { ConfDir, ACMEWebroot, LiveDir string; Run Runner; Log *slog.Logger }
  func (h Helper) Apply(ctx context.Context, staging string) error // swap, nginx -t, restore or reload
  func (h Helper) Cert(ctx context.Context, host string) error      // certbot certonly --webroot ...
  func (h Helper) Status() (StatusReport, error)                    // {Certs []string} hosts with live certs
  ```
- **Command-line output:** `status` prints JSON `{"certs":["hami9.ir",…]}`, and exit codes are 0 for OK, 1 for a failed operation and 2 for bad usage.

- **Apply algorithm:**
  1. **Check the staging directory.** Its files must be exactly the allowed set: `sites.conf`, `verify.conf` and `mounts/*.conf`. Each file name must be a valid host followed by `.conf`, no file may be a symlink, and each must be under 1 MiB. If not, refuse.
  2. **Swap in the new files.** Copy the current `ConfDir` files to `ConfDir/.prev/`, then atomically rename the staged files in (write to `ConfDir/.next/`, then `rename` per file).
  3. **Test the config.** Run `nginx -t`. If it fails, restore from `.prev/` and return an error that includes the first 2 KB of nginx's output.
  4. **Reload.** Run `nginx -s reload`, then remove `.prev/`.

- **Cert command:** `certbot certonly --webroot -w <ACMEWebroot> -d <host> --non-interactive --agree-tos --keep-until-expiring --register-unsafely-without-email` only if no account exists. Otherwise drop that flag. The host is re-validated with `app.NormalizeHostname`.

- [ ] **Step 1: Write the failing tests**, using a fake `Runner` and a `t.TempDir()` ConfDir:
  - `TestHelperApplySwapsAndReloads`: the calls are `nginx -t` then `nginx -s reload`, and the files are replaced.
  - `TestHelperApplyRestoresOnFailedTest`: `nginx -t` returns an error, the old files are byte-identical afterwards, and no reload happens.
  - `TestHelperApplyRefusesUnexpectedFiles`: an `evil.conf` at the top level, a symlink, `mounts/../x.conf` and a 2 MiB file are each refused, with no Runner calls.
  - `TestHelperCertValidatesHost`: `"a;b"` is refused, and a valid host produces the exact certbot argv.
  - `TestMainRejectsUnknownVerb`: exit code 2.
- [ ] **Step 2: Implement** the code, the sudoers file (content as in ADR-0017) and the `Makefile` `build` target that adds the binary. Run `go test -race ./internal/edgehelper/ ./cmd/shipyard-edge-helper/`. Expected: PASS.
- [ ] **Step 3:** Commit `Edge helper`.

### Task 5: `NginxEdge`

**Files:**
- Create: `internal/routing/nginx_edge.go`
- Test: `internal/routing/nginx_edge_test.go`, `internal/routing/nginx_edge_docker_test.go` (`//go:build docker`)

**Interfaces:**
- Consumes: `RenderNginx` (Task 3); the helper's CLI contract (Task 4).
- Produces:
  ```go
  type AddrResolver interface { // implemented by a runtime adapter in Task 6
      // Resolve turns "container:port" (routes.upstream) into the container's
      // IP on its app network; not running: ErrNotRunning.
      Resolve(ctx context.Context, upstream string) (netip.AddrPort, error)
  }
  type HelperClient interface {
      Apply(ctx context.Context, stagingDir string) error
      Cert(ctx context.Context, host string) error
      Certs(ctx context.Context) (map[string]bool, error)
  }
  func NewNginxEdge(s NginxSettings, r AddrResolver, h HelperClient, stagingRoot string, log *slog.Logger) (*NginxEdge, error)
  func NewSudoHelper(path string) HelperClient // runs "sudo -n <path> <verb> ..."
  ```

- **Apply:**
  1. Resolve every upstream; an unresolvable one renders as no active deployment.
  2. Query `Certs` and render.
  3. Compare the output with the last applied render, kept in memory.
  4. If it is equal, return `false, nil` without calling the helper.
  5. Otherwise write a fresh staging directory under `stagingRoot` (mode `0750`) and call `helper.Apply`.
  6. After success, start `Cert` in a background goroutine, one at a time, for each non-mount host without a certificate. A host whose `Cert` fails is retried no sooner than 1 hour later, to stay inside Let's Encrypt's limits.
- **Check:** HTTP over `VerifySocket`, the same loop as `CaddyEdge`.

- [ ] **Step 1: Write the failing unit tests** with fakes:
  - `TestNginxEdgeUnchangedRenderSkipsHelper`;
  - `TestNginxEdgeRerendersOnIPChange`: the same routes with a new resolved IP lead to exactly one `Apply`;
  - `TestNginxEdgeApplyFailureKeepsServing`: the helper's error is returned, and the next Apply with the same input calls the helper again (the failed render is not remembered as applied);
  - `TestNginxEdgeRequestsMissingCertsOnce`;
  - `TestNginxEdgeCertRetryBackoff`.
- [ ] **Step 2: Implement and run** `go test -race ./internal/routing/`. Expected: PASS.
- [ ] **Step 3: Write `TestNginxEdgeRealNginx`** (docker tag). It starts an `nginx:1.24-alpine` container that has the ConfDir and verify socket bind-mounted, plus an app container on a shared network. The test helper wraps `docker exec nginx -t` and `-s reload` as its `Runner`. It checks that `Check` succeeds through the socket, and that a broken render leaves the old config serving.
  - Run it on the developer machine: `go test -tags docker ./internal/routing/ -run RealNginx`. Expected: PASS.
  - Record the output in the WORKLOG.
- [ ] **Step 4:** Commit `nginx edge`.

### Task 6: Worker wiring and configuration

**Files:**
- Modify: `internal/config/config.go`, `cmd/shipyard-worker/main.go` (`ensureEdge`, `syncRoutes`, the reconciler), `cmd/shipyard-worker/adapters.go`, `deploy/shipyard.env.example`
- Create: `cmd/shipyard-worker/events.go`
- Test: `internal/config/config_test.go`, `cmd/shipyard-worker/events_test.go`

**Interfaces:**
- Consumes: `NewNginxEdge`, `NewSudoHelper`, `NewCaddyEdge`, `NewRouter`.
- **New environment variables:**

  | Variable | Default | Notes |
  |---|---|---|
  | `SHIPYARD_EDGE` | `caddy` | `caddy` or `nginx` |
  | `SHIPYARD_NGINX_HELPER` | `/usr/local/bin/shipyard-edge-helper` | |
  | `SHIPYARD_NGINX_VERIFY_SOCKET` | `/run/shipyard/nginx/verify.sock` | |
  | `SHIPYARD_NGINX_ACME_WEBROOT` | `/var/lib/shipyard/acme` | |
  | `SHIPYARD_NGINX_MOUNT_HOSTS` | — | comma list, normalized |
  | `SHIPYARD_NGINX_RESERVED_PATHS` | — | comma list, each must match the prefix rule |
  | `SHIPYARD_NGINX_STAGING` | `/var/lib/shipyard/edge-staging` | |

- **Behaviour:**
  - With `nginx`:
    - `ensureEdge` does not create Caddy;
    - `SHIPYARD_API_LISTEN` must be loopback TCP, so nginx can reach it;
    - the runtime adapter implements `AddrResolver` from `Inspect` (`st.IP`, the container's network IP).
  - **Start events:** with either edge, `events.go` subscribes to Docker `start` events filtered on label `io.shipyard.app` and calls `router.Sync`, debounced by 2 s.

- [ ] **Step 1: Write the failing config tests:**
  - `TestWorkerEdgeNginxDefaults`;
  - `TestWorkerEdgeRejectsUnknown` (`SHIPYARD_EDGE=traefik`);
  - `TestWorkerNginxRequiresLoopbackAPI` (`unix:` refused with `nginx`);
  - `TestWorkerReservedPathsValidated`.
- [ ] **Step 2: Write `TestStartEventsTriggerSync`.** A fake event stream yields 3 starts within 1 s, which leads to exactly 1 `Sync`; a start 3 s later leads to a second.
- [ ] **Step 3: Implement.** Run `go test -race ./internal/config/ ./cmd/...` and `go vet ./...`. Expected: PASS.
- [ ] **Step 4:** Commit `Edge config`.

### Task 7: Path mounts in the data model and API; activation by route ID

**Files:**
- Create: `migrations/0006_route_path_prefix.sql`
- Modify:
  - `internal/store/routes.go`, `internal/store/deployments.go:319-370`;
  - `internal/app/deploy.go` (`Router` interface, `switchTraffic`, `activate`);
  - `internal/routing/router.go`;
  - `internal/api/domains.go`;
  - `api/openapi.json`, `web/src/api/schema.ts` (regenerated as ADR-0015 describes);
  - `cmd/shipyard` (CLI `domain add HOST[/PREFIX/]`).
- Test: `internal/store/routes_integration_test.go`, `internal/api/domains_test.go`, `internal/app/deploy_test.go`, `internal/routing/router_test.go`

**Interfaces:**
- **Migration:**
  - `ALTER TABLE routes ADD COLUMN path_prefix text CHECK (path_prefix ~ '^/[a-z0-9][a-z0-9/_-]{0,62}/$' AND path_prefix !~ '//')`;
  - drop the `hostname` UNIQUE constraint;
  - `CREATE UNIQUE INDEX routes_host_prefix ON routes (hostname, coalesce(path_prefix, ''))`.
- `store.Route` and `store.NewRoute` gain `PathPrefix string` (`""` = NULL).
- `store.ActivateDeployment(ctx, id, owner, upstream string, routeIDs []string)` uses `id = ANY($4::uuid[])`. The `RowsAffected` check stays.
- **`app.Router.Switch`** returns `[]RouteTarget`, where `type RouteTarget struct{ ID, Host, Prefix string }`. `Router` checks `Prefix+strings.TrimPrefix(healthPath,"/")` on `Host`, or `healthPath` when there is no prefix.
- **API:**
  - `POST /v1/apps/{app}/domains` takes `{"hostname": "...", "path_prefix": "/tg/"}`, where `path_prefix` is optional.
  - The prefix is allowed only when the hostname is in the worker's mount hosts. The API reads these from the same env (`SHIPYARD_NGINX_MOUNT_HOSTS`, `SHIPYARD_NGINX_RESERVED_PATHS`) through `DomainPolicy.MountHosts` and `ReservedPaths`.
  - A mount host without a prefix → 422.
  - A prefix that is reserved, inside a reserved path, or equal to the panel prefix → 422.
  - A duplicate host+prefix → 409.
  - Delete becomes `DELETE /v1/apps/{app}/domains/{hostname}?path_prefix=/tg/`.

- [ ] **Step 1: Write the failing integration test** `TestRoutesHostPrefixUnique` (the same host with two prefixes is OK, the same pair is refused), then add the migration.
- [ ] **Step 2: Write the failing API tests:**
  - `TestAddDomainWithPrefix`;
  - `TestAddDomainRefusesReservedPrefix`: `/api/`, `/blog/x/`, `/` and `/admin-shipyard/`;
  - `TestAddDomainPrefixNeedsMountHost`;
  - `TestAddDomainMountHostNeedsPrefix`.
- [ ] **Step 3: Write the failing tests** `TestActivateByRouteIDs` (store) and `TestSwitchChecksPrefixedHealthPath` (router: a `/tg/` mount with health `/healthz` → `Check(host, "/tg/healthz")`). Implement the slice.
- [ ] **Step 4: Regenerate the OpenAPI and TypeScript client.** Then run the following and expect PASS:
  - `go test -race ./...`;
  - the integration tests;
  - `cd web && npm test && npm run check`.
- [ ] **Step 5:** Commit `Path mounts`. If it exceeds about 400 lines, split it into `Prefix schema` (Steps 1 and 3) and `Prefix API` (Steps 2 and 4).

### Task 8: Panel locations rendered by the nginx edge

**Files:**
- Modify: `internal/routing/nginx_render.go`, `internal/config/config.go`, `deploy/shipyard.env.example`
- Test: `internal/routing/nginx_render_test.go`, new goldens

**Interfaces:**
- Produces:
  ```go
  type PanelSettings struct {
      Host, Prefix string      // "hami9.ir", "/admin-shipyard/"; Host must be a MountHost
      APIAddr      netip.AddrPort // loopback API listen address
      WebDir       string      // /usr/local/share/shipyard/web
      Htpasswd     string      // /etc/nginx/shipyard/htpasswd
  }
  ```
- **Environment:**
  - `SHIPYARD_PANEL_HOST`;
  - `SHIPYARD_PANEL_PREFIX` (default `/admin-shipyard/`);
  - `SHIPYARD_PANEL_HTPASSWD` (default as above);
  - the WebDir reuses `SHIPYARD_WEB_DIR`.
- **Render:**
  - **In `Mounts[Host]`:**
    - `location = <prefix>hooks/github`: proxy to `/hooks/github`, no auth, 25m;
    - `location <prefix>v1/`: auth, `limit_req zone=shipyard_panel burst=20 nodelay`, 1m, proxy to `http://<APIAddr>/v1/`, `proxy_buffering off` (for SSE);
    - `location <prefix>`:
      - auth and limit_req;
      - `alias <WebDir>/`, `try_files $uri $uri/ <prefix>index.html`;
      - `sub_filter '<base href="/">' '<base href="<prefix>">'` with `sub_filter_once on`;
      - `Cache-Control: no-store` on `index.html`, `immutable` on `assets/`.
    - Every panel location adds the headers `X-Robots-Tag: noindex, nofollow, noarchive`, `Referrer-Policy: no-referrer` and CSP `default-src 'self'; frame-ancestors 'none'; base-uri 'self'; object-src 'none'`, using `always`.
  - **In `Sites`:** `limit_req_zone $binary_remote_addr zone=shipyard_panel:1m rate=5r/s;`, rendered only when the panel is set.

- [ ] **Step 1: Write the failing tests:**
  - golden `TestRenderNginxGolden/panel`;
  - `TestRenderNginxPanelRequiresMountHost`;
  - `TestRenderNginxPanelHookHasNoAuth`, which asserts that the hooks location contains no `auth_basic` and every other panel location does.
- [ ] **Step 2:** Implement, then run `go test ./internal/routing/ ./internal/config/`. Expected: PASS.
- [ ] **Step 3:** Commit `Panel mount`.

### Task 9: Web UI under a base path

**Files:**
- Create: `web/src/base.ts`
- Modify: `web/scripts/build.mjs:40-50`, `web/src/nav.tsx`, `web/src/session.ts:51`, `web/e2e/server.mjs`, `web/playwright.config.ts`
- Test: `web/src/base.test.ts`, the existing Playwright suites run as a second project

**Interfaces:**
- Produces:
  ```ts
  export function basePath(doc?: Document): string // "/" or "/admin-shipyard/" from <base href>, always with trailing slash
  export function stripBase(pathname: string, base: string): string // "/admin-shipyard/apps/x" -> "/apps/x"
  export function withBase(path: string, base: string): string      // "/apps/x" -> "/admin-shipyard/apps/x"
  export function apiBase(base: string): string                     // "/" -> "", "/admin-shipyard/" -> "/admin-shipyard"
  ```
- **Build:** `build.mjs` emits `<base href="/">` and relative asset URLs (`assets/<file>`). Caddy at `/` behaves exactly as before.

- [ ] **Step 1: Write the failing unit tests** in `base.test.ts`, covering both bases, paths with and without trailing slash, `stripBase` of the bare base (`"/admin-shipyard"` maps to `"/"`), and a path outside the base, which is left unchanged and parsed as missing.
- [ ] **Step 2: Implement.**
  - `nav.tsx` parses `stripBase(location.pathname)` and pushes `withBase(href(r))`.
  - `session.ts` passes `baseUrl: apiBase(basePath())`.
- [ ] **Step 3: Add a Playwright project `prefixed`.** `server.mjs` serves the dist directory under `PREFIX=/admin-shipyard/`, applying the same `<base>` rewrite as nginx's `sub_filter`, and proxies `PREFIX+"v1/"` to the API. All existing specs run in both projects.
  - Run `cd web && npm test && npm run check && npx playwright test`. Expected: all projects pass.
- [ ] **Step 4:** Commit `UI base`.

### Task 10: Named volumes

**Files:**
- Create: `migrations/0007_app_volumes.sql`, `internal/store/volumes.go`, `internal/api/volumes.go`
- Modify:
  - `internal/runtime/runtime.go` (`Spec.Volumes`, `createOptions`);
  - `internal/app/deploy.go:463` (pass the volumes);
  - `internal/app/delete.go` (keep the volumes, or purge them);
  - `api/openapi.json`, `web/src/api/schema.ts`;
  - `cmd/shipyard` (CLI `volume set|unset|list`, and `app delete --purge-volumes` with a typed slug confirmation);
  - `docs/OPERATIONS.md`.
- Test: `internal/store/volumes_integration_test.go`, `internal/api/volumes_test.go`, `internal/runtime/runtime_test.go`, `internal/app/delete_test.go`

**Interfaces:**
- **Migration:** `app_volumes(app_id uuid REFERENCES apps ON DELETE CASCADE, name text CHECK (name ~ '^[a-z][a-z0-9-]{0,30}$'), mount_path text NOT NULL, created_at timestamptz DEFAULT now(), PRIMARY KEY (app_id, name), UNIQUE (app_id, mount_path))`, plus a trigger or check that refuses a fifth row per app.
- **Runtime:** `runtime.Spec.Volumes []VolumeMount` (`{Name, Target string}`) becomes `mount.Mount{Type: mount.TypeVolume, Source: "shipyard-vol-<slug>-<name>", Target}`. It is created if missing, with label `io.shipyard.app=<slug>`.
- **API:**
  - `PUT /v1/apps/{app}/volumes/{name}` `{"path":"/data"}` → 200 (idempotent), or 409 when the path is used by another volume.
  - `DELETE` → 204. It only detaches the volume from future deploys; the Docker volume is kept.
  - `GET` lists the volumes.

- [ ] **Step 1: Write the failing tests:**
  - `TestCreateOptionsVolumeIsNamedNeverBind`: every mount is `TypeVolume`, and there is no `Binds`;
  - `TestVolumeLimitFour`;
  - `TestVolumePathValidation`: `/`, `/proc/x`, `relative`, `/a/../b` and `/data/` are refused or normalized as specified;
  - `TestDeleteKeepsVolumesUnlessPurged`.
- [ ] **Step 2: Implement.** Run `go test -race ./...`, the integration tests, and `cd web && npm run check`. Expected: PASS.
- [ ] **Step 3:** Commit `Volumes`. If it is larger than about 400 lines, split it into `Volume schema` (store and migration), `Volume runtime` and `Volume API`.

### Task 11: Serial deploy strategy

**Files:**
- Create: `migrations/0008_deploy_strategy.sql`
- Modify: `internal/app/deploy.go` (`execute`, `start`, new `drain` and `restorePrevious`), `internal/app/janitor.go`, `internal/store/apps.go`, `internal/api/apps.go`, `api/openapi.json`, `web/src/api/schema.ts`, `web/src/AppEditor.tsx` (strategy select), `cmd/shipyard` (`--strategy`)
- Test: `internal/app/deploy_test.go`, `internal/app/janitor_test.go`

**Interfaces:**
- **Migration:** `ALTER TABLE apps ADD COLUMN deploy_strategy text NOT NULL DEFAULT 'overlap' CHECK (deploy_strategy IN ('overlap','serial'))`.
- **New phase:** `PhaseDrain = "drain"`, persisted before the previous container is stopped.
- **Serial flow** (`app.DeployStrategy == "serial"` and the app has an active deployment with a container):
  1. fetch and build (unchanged);
  2. `phase(drain)`, then `Runtime.Stop(prev, app.StopTimeout)`;
  3. start, then the health gate;
  4. on success, switch, then activate.
  5. On any error after the drain:
     1. `Runtime.Start(prev)`;
     2. health-check `prev`;
     3. `Router.Release`;
     4. an event: "candidate failed; previous deployment %s restarted".
     - If the previous container cannot be restarted, the deploy fails with both errors joined, and the app is out of service. This is logged as error level.
- **Resume:** a deploy that resumes at `drain` or later calls `Stop(prev)` again, which is idempotent.
- **Janitor:** a superseded deployment of a serial app is already stopped; the janitor only removes it after the window.

- [ ] **Step 1: Write the failing tests**, using the existing fakes in `deploy_test.go`:
  - `TestSerialDeployStopsPreviousBeforeStart`: the recorded call order is `Stop(prev)` before `Create(candidate)`;
  - `TestSerialDeployRestoresPreviousOnUnhealthy`: the health gate fails, then `Start(prev)` is called, routes never point at the candidate, the deployment is marked failed, and there is no `ActivateDeployment`;
  - `TestSerialFirstDeployHasNoDrain`;
  - `TestSerialResumeAfterDrainIsIdempotent` (crash at the drain fault point);
  - `TestOverlapUnchanged`.
- [ ] **Step 2: Implement.** Run `go test -race ./internal/app/ ./internal/api/` and the integration tests. Expected: PASS.
- [ ] **Step 3:** Commit `Serial deploy`.

### Task 12: Builder idle stop

**Files:**
- Modify: `internal/build/build.go`, `internal/config/config.go`, `deploy/shipyard.env.example`
- Test: `internal/build/build_test.go`

**Interfaces:**
- `build.Builder` gains `IdleStop time.Duration` and an injectable `clock` (`AfterFunc`).
- After each build ends, it resets a timer. When the timer fires, it runs `docker buildx stop <builder>`.
- buildx boots a stopped `docker-container` builder on its next use. Confirm this against the primary docs, add a `[BX-STOP]` entry to `docs/SOURCES.md`, and cite that tag in a code comment.
- A build that is running keeps the timer from firing.

- [ ] **Step 1: Write the failing tests** with a fake clock:
  - `TestIdleStopAfterQuietPeriod`;
  - `TestIdleStopResetByNewBuild`;
  - `TestIdleStopNeverDuringBuild`;
  - `TestIdleStopZeroDisables`.
- [ ] **Step 2:** Implement, then run `go test -race ./internal/build/`. Expected: PASS.
- [ ] **Step 3:** Commit `Builder idle`.

### Task 13: Backups for the nginx edge and volumes

**Files:**
- Modify: `internal/backup/backup.go`, `docs/OPERATIONS.md`, `docs/RESTORE.md`
- Test: `internal/backup/backup_test.go`

- **What is backed up:** with `SHIPYARD_EDGE=nginx`, target A gets:
  - `edge-nginx.tar`, holding `/etc/nginx/shipyard` without `htpasswd`;
  - the `live`, `archive` and `renewal` entries of the Shipyard hosts only.

  It does not get Caddy's data.
- **Volumes:** each app volume becomes `volume-<slug>-<name>.tar`, made with a throwaway container `docker run --rm --network none -v <vol>:/v:ro <pinned busybox digest> tar -C /v -cf - .`. The pinned image must be recorded in `SOURCES.md`.
- **Restore:** `RESTORE.md` documents restoring both.

- [ ] **Step 1: Write the failing tests:**
  - `TestBackupNginxEdgeSkipsCaddy`;
  - `TestBackupArchivesEachVolume`;
  - `TestBackupExcludesHtpasswd`.
- [ ] **Step 2:** Implement, then run `go test -race ./internal/backup/`. Expected: PASS.
- [ ] **Step 3:** Commit `Edge backup`.

### Task 14: Installer support, docs, release prep

**Files:**
- Modify: `deploy/install.sh`, `deploy/README.md`, `docs/OPERATIONS.md` (new section "Behind an existing nginx"), `CHANGELOG.md`, `docs/WORKLOG.md`, `docs/ROADMAP.md`
- Create: `deploy/postgres/shipyard-small.conf`, `deploy/nginx/README.md` (the two include lines and where they go)

- **New installer flags:**
  - `--edge nginx`;
  - `--mount-host HOST`, which can repeat;
  - `--reserved-path PATH`, which can repeat;
  - `--panel-host HOST`;
  - `--api-listen 127.0.0.1:8095`;
  - `--small-postgres`.
- **With `--edge nginx`, it installs:**
  - the helper, the sudoers file (validated with `visudo -c -f`) and the edge directories with their owners and modes (Global Constraints);
  - empty edge files;
  - the PostgreSQL drop-in when `--small-postgres` is given (`shared_buffers=64MB`, `max_connections=20`, `work_mem=4MB`, `effective_cache_size=512MB`).
- **It never:**
  - edits a file under `/etc/nginx/sites-*`;
  - opens ufw ports;
  - starts Caddy.

  It prints the two include lines for the operator to add.
- **`--dry-run`** prints every action.

- [ ] **Step 1:** Implement the flags. Run `bash -n deploy/install.sh` and `shellcheck deploy/install.sh`, if available. Run `sudo deploy/install.sh --dry-run --edge nginx --mount-host hami9.ir --panel-host hami9.ir --api-listen 127.0.0.1:8095 --small-postgres --public-ip 154.91.170.122` in a throwaway `ubuntu:24.04` container. Expected: the action list shows no write to `sites-*`, and no Caddy.
- [ ] **Step 2: Update the docs:**
  - CHANGELOG `[Unreleased]` with every P8 item;
  - ROADMAP P8 ticked;
  - WORKLOG `Current status`, with Next set to "owner cuts `v1.1.0-rc.1`; then plan 2 (VPS install)".
- [ ] **Step 3: Run the full check:**
  - `gofmt -l .` (empty);
  - `go vet ./...`;
  - `staticcheck ./...`;
  - `go test -race ./...`;
  - the integration tests;
  - `cd web && npm test && npm run check && npx playwright test`.

  Expected: all pass. Paste the summary into the WORKLOG.
- [ ] **Step 4:** Commit `Installer nginx`, push `nginx-edge`, and ask the owner to merge and tag (CLAUDE.md §7: never tag without an explicit request).
