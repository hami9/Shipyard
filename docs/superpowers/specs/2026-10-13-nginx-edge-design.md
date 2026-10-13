# Shipyard on the hami9 VPS: nginx edge, persistent apps, and the broadcast bot

- **Status:** Draft for owner review
- **Date:** 2026-10-13
- **Owner decisions so far:** Shipyard may be changed freely; the live site at
  `hami9.ir` must not be touched; Shipyard must work with the existing nginx
  rather than replace it; the panel lives at `hami9.ir/admin-shipyard`, is not
  indexed and is protected; leave room for a future Node.js or Django backend;
  keep RAM and CPU use low; the broadcast bot moves from Cloudflare Workers to
  the VPS and gets a Telegram Mini App that showcases the site's projects.

## 1. Goal

Run Shipyard on the existing VPS (154.91.170.122, 2 vCPU, 3.9 GB RAM, Docker 29.8,
nginx with certbot) **behind the nginx that already serves `hami9.ir`**, and use it
to deploy the GitHub broadcast bot and, later, any new backend.

Success means:

1. `hami9.ir` keeps serving from its current container (`hami9-platform-app-1`,
   `127.0.0.1:3100`) through its current nginx server block. Its code, image,
   compose file and deploy flow are unchanged.
2. `https://hami9.ir/admin-shipyard/` opens the Shipyard UI. It is never indexed,
   and an anonymous visitor cannot reach the API.
3. The broadcast bot runs as a Shipyard app on the VPS with its data on a
   persistent volume. A push to its repository deploys it, and a deploy never runs
   two copies of the bot at once.
4. The Mini App opens from the bot and shows the site's projects and articles plus
   recent GitHub activity.
5. A future backend can be added from the panel with a Dockerfile, on a hostname
   of its own or under a path of `hami9.ir`.

## 2. Non-goals

- Moving the portfolio site into Shipyard.
- Moving Astolfo in this spec. It needs volumes and serial deploys (built here),
  plus a replacement for its root update agent and `server_ops`. It gets its own
  spec once this one has run in production.
- Replacing Caddy for other Shipyard users. Caddy stays the default edge.
- Counting this install as the v1.0 acceptance demo, which requires a fresh VPS
  (docs/ACCEPTANCE.md). Portfolio copy that calls Shipyard a release candidate
  stays true.

## 3. Shape of the system

```text
                        :80 / :443  (host nginx, unchanged owner of the ports)
                              │
   ┌──────────────────────────┼─────────────────────────────────────┐
   │ server hami9.ir (existing)│                                     │
   │   /           → 127.0.0.1:3100 (site, untouched)                │
   │   include shipyard/mounts/hami9.ir.conf   ← one added line      │
   │       /admin-shipyard/      → UI files + API unix socket        │
   │       /tg/                  → broadcast bot container           │
   ├─────────────────────────────────────────────────────────────────┤
   │ include shipyard/sites.conf  (rendered by the worker)           │
   │   server api.hami9.ir (future) → backend container              │
   └─────────────────────────────────────────────────────────────────┘
        ▲ rendered from the routes table, validated, reloaded
        │
   shipyard-worker ── shipyard-edge-helper (root, fixed verbs only)
        │
   PostgreSQL 18 (local, tuned small) ◄── shipyard-api (unix socket)
```

No Caddy container runs. Containers still publish no ports; nginx reaches them on
their bridge IP, the same way the worker already probes their health.

## 4. Shipyard changes

Each item is one or more vertical slices (code, tests, docs) on branch
`nginx-edge`, following CLAUDE.md. Items 4.1 and 4.3 change invariants and need
accepted ADRs before code.

### 4.1 nginx edge driver (ADR-0017)

- **Selection:** `SHIPYARD_EDGE=caddy|nginx`, default `caddy`. Caddy behaviour and
  its tests are unchanged.
- **Edge interface:** the worker gets an `Edge` interface declared by its consumer
  (`routing`): `Apply(ctx, routes) (changed bool, err)`, `Verify(ctx, host, path)`,
  `Ensure(ctx)`. The current Caddy code becomes one implementation, the nginx code
  the other. `Router` keeps its pending-switch logic and calls the interface.
- **Render:** a pure function of the routes table plus pending switches, with
  golden tests, like `render.go`. Output:
  - `/etc/nginx/shipyard/sites.conf`: one `server` block per Shipyard-owned
    hostname (port 80 for ACME and redirect, 443 once a certificate exists).
  - `/etc/nginx/shipyard/mounts/<host>.conf`: `location` blocks for path mounts
    (4.2) on hosts that nginx already serves.
  - `/etc/nginx/shipyard/verify.conf`: a `verify` server on a unix socket
    with the same routes over plain HTTP. This is how ADR-0003's P2.4 verify
    server works, so a deploy is verified before any certificate exists.
  - Every file always exists, possibly empty, so `nginx -t` never fails on a
    missing include.
- **Upstreams:** the container's IP on its `shipyard-app-<slug>` network and the
  app's port. A container that restarts may get a new IP, so the worker:
  - re-renders on every reconcile (60 s) and reloads only when the output changed;
  - also triggers a sync on Docker `start` events for `io.shipyard.*` containers,
    so a crash-restart is routed again within seconds.
- **Apply:** write the files to a temp directory, then `helper apply` swaps them
  in and runs `nginx -t`. If that fails, the helper restores the previous files and
  reports the error; the serving config never changes (invariant 5). If it
  passes, it runs `nginx -s reload`, a graceful reload. Then `Verify` asks the
  verify socket with the app's `Host` header, as today.
- **Input safety:** hostnames and path prefixes are validated before render
  (the API's existing hostname rules, plus `^/[a-z0-9][a-z0-9/_-]*$` for prefixes).
  Nothing from a repository or env var reaches the config. Golden tests include
  hostile values (`;`, `{`, newline, `$`, quotes), which must be refused.
- **Certificates:** for a Shipyard-owned hostname, the worker renders the
  port-80 block with a webroot for `/.well-known/acme-challenge/`, reloads, then
  runs `helper cert <host>`. The helper calls
  `certbot certonly --webroot -w /var/lib/shipyard/acme -d <host> --non-interactive --agree-tos`.
  After that the 443 block is rendered. The certbot timer already installed on the
  server renews certificates. A deploy does not wait for the certificate, as with
  Caddy today.
- **Privilege boundary:** `shipyard-edge-helper` is a small Go binary run through
  one sudoers line for the `shipyard-worker` user. It accepts exactly `apply`,
  `cert <host>` and `status`, re-validates its arguments, touches only
  `/etc/nginx/shipyard/` and `/var/lib/shipyard/acme`, and logs each call.
  - The worker already has Docker access, which is root-equivalent (ADR-0007), so
    this does not widen the trust model. The helper keeps the config files out of
    the API's reach, so invariant 1 still holds.
- **Invariant changes (ADR-0017):**
  - 2 now reads "the edge config is a pure function of the routes table";
  - 11 now reads "no container publishes host ports; the edge owns 80/443";
  - 12 gets an nginx counterpart: only the worker can run the helper, and the
    verify socket's directory is `0750` for the worker's edge group.
- **Backup:** with the nginx edge, the backup includes `/etc/nginx/shipyard` and
  the Shipyard hostnames' directories under `/etc/letsencrypt`, instead of
  Caddy's data volume.

### 4.2 Path mounts on hosts nginx already serves (ADR-0017, same ADR)

- `SHIPYARD_NGINX_MOUNT_HOSTS=hami9.ir` lists hosts that Shipyard does not own but
  may add `location`s to. Their server blocks include `mounts/<host>.conf` once
  (an operator step).
- A domain entry may be `hami9.ir/tg`: the routes table gains a nullable
  `path_prefix` column (migration `0006`). The hostname plus prefix is unique. A
  prefix is only allowed on a mount host, and a mount host is only allowed with a
  prefix.
- nginx strips the prefix (`location /tg/ { proxy_pass http://ip:port/; }`) and
  sends `X-Forwarded-Prefix: /tg`, so the app can build its own links.
- **Reserved prefixes:** `SHIPYARD_NGINX_RESERVED_PATHS` lists the site's
  top-level path segments. For this server that is `/api/`, `/assets/`, `/en/`,
  `/fa/`, `/blog/`, `/about/`, `/_next/`, plus every first segment in the site's
  sitemap, checked at install time. Those prefixes are refused, so a mount can
  never shadow the site.
- Caddy refuses `path_prefix` routes with a clear error, so the feature stays
  nginx-only until someone needs it on Caddy.

### 4.3 Persistent volumes and serial deploys (ADR-0018)

- **Volumes:**
  - An app may declare up to 4 named volumes, each with a mount path:
    `PUT /v1/apps/{app}/volumes/{name}` with body `{"path": "/data"}`.
  - The volumes are stored in a new `app_volumes` table (migration `0007`).
  - Docker named volumes are used, called `shipyard-vol-<slug>-<name>` and
    labelled `io.shipyard.app`.
  - Invariant 10 still forbids host bind mounts. Named volumes live in
    Docker's own storage.
  - Deleting an app keeps its volumes unless `--purge-volumes` is passed. That flag
    needs a typed confirmation of the slug.
- **Rollback and data:** rollback restores the image and env revision, never the
  volume's contents. The docs say this plainly, and the backup includes a
  `docker run --rm -v vol:/v alpine tar` archive of every app volume.
- **Deploy strategy:** a new field, `deploy_strategy`, set to `overlap` (today's
  behaviour and the default) or `serial`. A serial deploy runs these steps in order:
  1. Build the image. A failed build stops here, and the running release stays as
     it is.
  2. Stop the current container with its stop timeout. The phase is persisted
     first.
  3. Start the candidate and health-check it.
  4. If it is healthy, switch the route and commit.
  5. If it is unhealthy, restart the previous container, re-check it and keep its
     route. The deploy is recorded as failed, with downtime equal to the gap.
  - The observation window does not apply, because the old container is already
    stopped.
- **Invariant 5:** for a serial app, invariant 5 reads "only a healthy candidate
  receives traffic, and a failed candidate brings back the previous release". The
  ADR records the downtime window, which is the price of a single-copy app.
- **Health without traffic:** a worker app still answers `health_path` on its
  port. The bot serves `/healthz`. P7.5 (non-HTTP workers) stays in the backlog.

### 4.4 UI under a path (ADR-0016 note)

- `SHIPYARD_WEB_BASE=/admin-shipyard/`, default `/`.
- **Build:** `web/scripts/build.mjs` emits `<base href="/">` and asset URLs
  relative to it.
- **Serving:** nginx rewrites that one tag to `<base href="/admin-shipyard/">`
  with `sub_filter` (Ubuntu's nginx 1.24 has the module). The installed web
  directory is never edited (ADR-0016's update-in-place still holds).
- **Paths:**
  - `nav.tsx` strips and adds the base;
  - `Client` uses the base without its trailing slash as `baseUrl`, so the API is
    reached at `/admin-shipyard/v1/...`;
  - at `/` nothing changes.
- **nginx edge:** the edge renders, inside `mounts/hami9.ir.conf`:
  - `location /admin-shipyard/v1/` → the API on loopback TCP, prefix stripped;
  - `location = /admin-shipyard/hooks/github` → the API's `/hooks/github`;
  - `location /admin-shipyard/` → the web directory, `try_files … index.html`.

  The API listens on `127.0.0.1:8095`, so nginx's `www-data` user needs no
  membership in the `shipyard` group (that group can read the worker's log
  socket).
- **Tests:** the existing Playwright suite runs once at `/` and once under
  `/admin-shipyard/`.

### 4.5 Protection and no-index for the panel

- `X-Robots-Tag: noindex, nofollow, noarchive` on every `/admin-shipyard/` response.
  The path is **not** listed in `robots.txt`, which would advertise it.
- **Access:**
  - nginx `auth_basic` in front of the UI and API. The owner creates the password
    with `htpasswd` on the server; the assistant never sees it.
  - The API keeps its own bearer token on top.
  - `/hooks/github` is exempt from basic auth, because GitHub signs it with HMAC
    (invariant 9).
- **Limits:**
  - `limit_req` of 5 r/s with a burst of 20 per IP on the panel, plus the API's
    own 401 limiter (ADR-0011);
  - `client_max_body_size 1m` for the API and 25m for the hook.
- **Headers:** CSP `default-src 'self'; frame-ancestors 'none'`,
  `Referrer-Policy: no-referrer`, `Cache-Control: no-store` on `index.html`.

## 5. Broadcast bot on the VPS

The current source is `C:\Users\pc city\Desktop\github-telegram-broadcast-worker`,
about 990 lines, not in git. It uses Cloudflare KV, a 1-minute cron, `/telegram`
and `/github` webhooks, and four secrets.

- **Repository:** a new GitHub repository `hami9/github-broadcast`. The owner
  chooses its visibility at review time. A private repository needs Shipyard's
  GitHub App (P4.4) set up first.
- **Runtime:** Node.js 24 LTS, standard library only (`node:http`, global `fetch`).
  - **Storage:** `node:sqlite` if its stability on Node 24 is confirmed during
    planning, otherwise `better-sqlite3`. The decision is recorded in that repo's
    README.
- **Port with minimal rewrite:**
  - The Worker's `fetch(request, env)` and `scheduled()` handlers are kept.
  - A thin adapter maps `node:http` to `Request`/`Response`, and a 60-second timer
    calls `scheduled()`.
  - `env.CHATS` becomes a KV shim over one SQLite table,
    `kv(key PRIMARY KEY, value, metadata, expires_at)`, with `get`, `put`
    (`expirationTtl`), `delete` and `list({prefix, cursor})`. Expired rows are
    swept hourly.
  - Its database lives on the volume `data` at `/data/bot.db`.
- **Deploy settings:** `deploy_strategy=serial` (one poller, no duplicate posts),
  `health_path=/healthz`, memory 128 MB, CPU 0.25. The Dockerfile uses the
  `node:24-alpine` base and runs as a non-root user.
- **Routes:** `hami9.ir/tg` (path mount) serves the Mini App and the webhook
  endpoints, at `/tg/telegram` and `/tg/github`.
- **Secrets:** `GITHUB_TOKEN`, `GITHUB_WEBHOOK_SECRET`, `TELEGRAM_BOT_TOKEN` and
  `TELEGRAM_WEBHOOK_SECRET` are entered by the owner as Shipyard env values, which
  are encrypted (invariant 8).
  - Cloudflare cannot return them. Rotating the GitHub token and both webhook
    secrets at the same time is recommended.
- **Cutover:**
  1. Export the chats and dedupe keys from KV with `wrangler kv key list/get`. This
     uses the existing wrangler login; the credential file itself is never opened.
     The export is imported into SQLite.
  2. Deploy the app to Shipyard and check `/tg/healthz`.
  3. The owner switches the Telegram webhook to `https://hami9.ir/tg/telegram`
     (the bot can do this at startup from its env) and the GitHub webhook to
     `https://hami9.ir/tg/github`.
  4. Remove the Worker's cron trigger. This stops double posting.
  5. Keep the Worker deployed but idle for 7 days as a fallback, then delete it.
     Deleting it is the owner's call.

## 6. Mini App (showcase)

- **Location:** served by the bot at `https://hami9.ir/tg/app/`. It is the
  `web_app` URL of a menu button and of an "Open" button on channel posts.
  - Inline `web_app` buttons work only in private chats, so channel posts use a
    `t.me/<bot>/<app>` direct link instead.
- **Content (read-only, no login):**
  - **Projects and articles:** read from the site's public sitemap and pages
    (title, description, language, Open Graph image) every 6 hours and cached in
    SQLite. The site code is not changed.
  - **GitHub activity:** the latest events the bot already stores.
  - **Contact:** a button that opens the site's contact page.
- **Look:**
  - The site's palette (`--void`, `--photon`, `--amber`, `--slate`), mapped onto
    Telegram's theme colours.
  - Persian first with English, RTL using logical CSS. Fonts are self-hosted and
    served by the bot; nothing loads from external CDNs.
  - Following AGENTS.md: no invented copy. Every text comes from the site's own
    pages or GitHub.
- **Tech:**
  - Static HTML, CSS and a small amount of vanilla TypeScript, built with esbuild.
    No framework: about 30 KB of JS.
  - `telegram-web-app.js` is loaded from `telegram.org`, as Telegram requires.
  - CSP `frame-ancestors https://web.telegram.org https://*.telegram.org`.
- **Data validation:** the app reads its data from a public JSON endpoint, so it
  needs no `initData` check. If a personalised feature is ever added, it will
  validate `initData` with HMAC first.

## 7. Resource budget (estimates, measured after install)

| Component | Idle RAM | Notes |
| --- | --- | --- |
| PostgreSQL 18 | 60–100 MB | Shared by Shipyard and a future backend. Settings: `shared_buffers=64MB`, `max_connections=20`, `work_mem=4MB`, `effective_cache_size=512MB` |
| shipyard-api + shipyard-worker | 40–70 MB | Go |
| BuildKit builder | 0 when idle | `SHIPYARD_BUILDER_MEMORY=1g`. The builder container is stopped after 10 idle minutes (new setting), so it costs nothing between deploys |
| Broadcast bot | 50–80 MB | Capped at 128 MB |
| Caddy | 0 | Not running |

Today about 3.1 GB is available, so the added load is roughly 150–250 MB at idle.
A build briefly uses up to 1 GB.

## 8. Rollout phases

1. **Shipyard code (4.1–4.5).** Done on branch `nginx-edge`, in slices of at most
   about 400 lines.
   - **Tests:** `make lint test`, the integration tests, golden tests for the
     nginx render, and a Docker test that runs a real `nginx:1.x` container as
     the edge.
   - **Release:** the owner cuts `v1.1.0-rc.1`.
2. **Server install.** First, a dry run of `install.sh --edge nginx`, reviewed
   before the real one. Then:
   - PostgreSQL 18 with the small settings above;
   - the include lines are added;
   - `nginx -t` passes, then a reload;
   - the site is checked to answer exactly as before, by comparing headers and
     bodies of the 8 sitemap URLs before and after;
   - the panel login is checked;
   - `curl -I` shows `X-Robots-Tag`.
   - **Rollback:** remove the two include lines and reload nginx.
3. **Bot.** Build the new repository, test the KV shim, deploy, then the cutover
   in §5.
4. **Mini App.** Built with the bot; mockups first, approved by the owner.
5. **Later:** a separate spec for Astolfo and for the future backend.

Every server change is recorded in the portfolio repository, `docs/server-changes.md`
(what changed and why), per the owner's standing rule, and committed and pushed.

## 9. Owner actions

- Approve this spec, then each ADR (0017, 0018) and each release tag.
- **On the server:** create the panel password, e.g.
  `sudo htpasswd -c /etc/nginx/shipyard/htpasswd hami`.
- **Bot setup:**
  - choose the bot repository's visibility;
  - enter the four bot secrets in the Shipyard panel;
  - switch the webhooks, or let the bot do it;
  - delete the Worker after 7 days.
- **For a future backend on its own hostname:** add a DNS record at ParsPack.

## 10. Risks

| Risk | Mitigation |
| --- | --- |
| A bad render breaks `hami9.ir` | Validated input, golden tests, `nginx -t` with restore, mounts only in a separate include file, reserved site paths, and a two-line rollback |
| Container IP changes after a crash | Docker start events trigger a re-render; the reconcile runs every 60 s as backup |
| The helper becomes a root escalation path | Fixed verbs, argument re-validation, a sudoers line for that one binary, and the worker is already root-equivalent through Docker (ADR-0007) |
| A serial deploy causes downtime | Only for apps that opt in; the gap is logged; a failed candidate restores the previous container |
| A volume holds data that rollback cannot restore | Documented; backups archive the volumes |
| The RC software runs next to live services | No ports, and no change to the site's process; the panel sits behind basic auth plus a token |
