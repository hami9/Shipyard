# Changelog

All notable changes to Shipyard are recorded here.

- **Format:** [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).
- **Versioning:** [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).
- **Before 1.0**, a minor bump (`0.x.0`) may contain breaking changes.
- **Release notes:** each GitHub Release uses the section for its tag (`scripts/release-notes.sh`).

## [Unreleased]

### Added

- **Prometheus metrics** (ADR-0013): set `SHIPYARD_WORKER_METRICS_LISTEN` to a loopback `host:port` and the worker serves `GET /metrics`. It covers finished operations by kind and result with their durations (`shipyard_operations_total`, `shipyard_operation_duration_seconds`), the queue (`shipyard_queue_operations`, `shipyard_queue_oldest_wait_seconds`), and `shipyard_database_up`. Off by default.
- **Database schema v1** (migration `0002`): users and API tokens, apps, encrypted secret values and immutable environment revisions, the operations queue and its events, deployments, routes, webhook deliveries, and audit events. The database itself enforces one running operation and one active deployment per app, unique idempotency keys and hostnames, and same-app references.
- **API tokens:** `shipyard-api token create|list|revoke` bootstraps and manages `shp_` tokens on the server. Tokens have scopes (`read`, `deploy`, `admin`) and an expiry of 1h to 366d (default 90d). Only a SHA-256 hash is stored, the plaintext is printed once, and every create and revoke is audited.
- **KEK rotation** (ADR-0012):
  - `shipyard-worker kek status` shows how many secret values each KEK wraps.
  - `shipyard-worker kek rewrap` moves them all to `SHIPYARD_KEK_ACTIVE`. It never re-encrypts a value or changes a revision, and can be run again safely.
  - Migration `0005` allows exactly that one change to stored secrets.
  - To rotate: add a key file, make it active, restart both services, run `kek rewrap`, then retire the old file once `kek status` shows it unused.
- **Asymmetric KEKs** (ADR-0012):
  - `shipyard-worker kek generate ID` writes an HPKE key pair: `ID.hpke`, the worker's private key (mode 0600), and `ID.pub`.
  - With it active, the API loads only the public key: it can seal new secret values but can no longer decrypt any stored one.
  - Move an existing install with `kek rewrap`, then delete the old `.key` files.
  - The API now reads only the active KEK file. Backups copy `.hpke` and `.pub` files too.
- **Token rotation and remote revocation** (ADR-0011):
  - **On the server:** `shipyard-api token rotate PREFIX [--grace D]` replaces a token with one of the same user, name, scopes, and lifetime. The old token is revoked, or keeps working for up to 7 days.
  - **Remotely:** `shipyard token rotate [--grace D]` does the same for the CLI's own token and saves the new one; with `SHIPYARD_TOKEN` set, it prints it instead. `shipyard token list` and `shipyard token revoke PREFIX` need the admin scope.
  - **API:** `GET /v1/tokens`, `DELETE /v1/tokens/{prefix}`, `POST /v1/tokens/self/rotate`.
- **API authentication:** every `/v1` route requires a bearer token with the right scope (401 or 403 per RFC 6750). Every authenticated mutation is audited, allowed or denied. `GET /v1/whoami` describes the calling token.
- **CLI (`shipyard`):** `login` (token from stdin, verified, saved with mode 0600), `whoami`, `app create|list|show`, `ps`, `env set|unset|list` (values from stdin, never printed), `deploy [--ref SHA] [--idempotency-key K]`, and `operation ID`. It refuses plain `http://` to a non-loopback host and supports `unix://` sockets. `SHIPYARD_URL` and `SHIPYARD_TOKEN` override the config, for CI.
- **Deploy API:** `POST /v1/apps/{app}/deployments` queues a deploy (idempotent with `Idempotency-Key`; a newer request cancels older queued ones), and `GET /v1/operations/{id}` reports its status and phase.
- **Deployments (worker):** `shipyard-worker run` executes deploys. It fetches the pinned or head commit, refusing one that is not on the tracked branch, then builds it on a resource-limited buildx builder. It then starts a hardened container on the app's own network, with the environment decrypted and injected. Next comes a health gate: 3 consecutive 2xx/3xx on `health_path`, while the container keeps running. Last, it marks the deployment active; the previous one is drained after the observation window (below).
  - A failed build, start, or health check leaves the running release untouched and removes the candidate. The last lines of the candidate's output are recorded.
  - A worker that crashes or restarts resumes the deploy where it stopped.
  - There is no public route until Phase 2.
- **Caddy edge:** at start, the worker keeps a Caddy container (`caddy:2.11.4-alpine`, pinned by digest) running.
  - It is the only container that publishes ports (80/tcp, 443/tcp, 443/udp), and it joins every app network.
  - Its admin API is available only on a Unix socket (mode 0660) that the worker's group can use.
  - Certificates and the last loaded config persist in volumes, and a restart resumes that config.
  - Configured with `SHIPYARD_CADDY`, `SHIPYARD_CADDY_NAME`, `SHIPYARD_CADDY_IMAGE`, `SHIPYARD_CADDY_ADMIN_DIR`, `SHIPYARD_CADDY_BIND`, `SHIPYARD_CADDY_HTTP_PORT`, and `SHIPYARD_CADDY_HTTPS_PORT`.
- **Caddy config from the database:** at start, the worker renders Caddy's whole config from the routes table. It loads the config only if it differs, as a conditional replace (`If-Match`) that fails safely on a concurrent change. The admin API always stays on its socket. A config Caddy rejects leaves the running one in place. Certificates are configured with `SHIPYARD_CADDY_CA` (default, `staging`, or `internal`) and `SHIPYARD_ACME_EMAIL`.
- **Traffic switching:** once a candidate is healthy, the worker points the app's hostnames at it in Caddy. It verifies each hostname through Caddy (a private plain-HTTP listener with the same routes), and only then commits the new routes together with the active deployment. If loading, verifying, or committing fails, the previous routes are restored before the candidate is removed, so the running release keeps serving.
- **Domains:** `shipyard domain add|remove|list APP [HOSTNAME]` and `/v1/apps/{app}/domains`. An app can have several hostnames; each hostname belongs to one app.
  - **Checks:** hostnames are normalized and must be exact FQDNs, optionally under `SHIPYARD_DOMAIN_SUFFIXES`. A DNS preflight requires every A/AAAA record to be one of `SHIPYARD_PUBLIC_IPS` (`SHIPYARD_DNS_PREFLIGHT=false` skips it).
  - **Serving:** a hostname added to a running app serves it at once. The worker applies added and removed hostnames within `SHIPYARD_RECONCILE_INTERVAL` (default 60 s).
- **Live events:** `shipyard events ID` and `shipyard deploy APP --follow` stream an operation's events until it ends (`GET /v1/operations/{id}/events`, SSE). A dropped connection resumes where it stopped. `--follow` exits non-zero unless the deploy succeeded, so it can gate CI.
- **Rollback:** `shipyard rollback APP --to DEPLOYMENT` runs an earlier release's image again, with the configuration it ran with, through the usual health gate and traffic switch. Nothing is rebuilt.
  - If a secret changed since, the rollback is refused and names the keys. Pick `--with-old-config` or `--with-current-config`.
  - If the image is gone from the host, the rollback fails at once as unavailable.
  - `shipyard releases` marks it "rollback of …".
- **Image retention:** the worker removes images a rollback can no longer need. Each app keeps the running release's image and those of its last `SHIPYARD_RETAIN_IMAGES` (default 5) earlier releases; failed deploys' images and older ones go. Images a container still uses stay, and images Shipyard did not record are never touched.
- **Backups:** `shipyard-worker backup`, run nightly by the new `shipyard-backup.timer`.
  - It writes a `pg_dump` archive, Caddy's certificates and keys, and a manifest with checksums to `SHIPYARD_BACKUP_DIR`, one directory per backup. It keeps 14 dailies and 8 weeklies (`SHIPYARD_BACKUP_KEEP_DAILY`, `SHIPYARD_BACKUP_KEEP_WEEKLY`).
  - It copies the KEK files to `SHIPYARD_BACKUP_KEK_DIR`, which must be a separate location.
  - `SHIPYARD_BACKUP_HOOK` and `SHIPYARD_BACKUP_KEK_HOOK` are optional commands that copy each target off-host.
  - A failed backup leaves no partial files and removes no older backup.
- **Deleting an app:** `shipyard app delete APP --yes [--follow]` (`DELETE /v1/apps/{app}`, `admin` scope) queues a delete that the worker carries out. It takes the app out of Caddy, stops and removes its containers, removes its network and images, and then removes the app with its configuration and history. An audit event records it. While the delete is pending the app accepts no deploy. It cannot be undone. Needs migration `0003`.
- **GitHub webhook receiver:** `POST /hooks/github` verifies `X-Hub-Signature-256` over the raw body (at most 25 MB) before reading it, against the secret in `SHIPYARD_GITHUB_WEBHOOK_SECRET_FILE`. A bad signature is a 401 with nothing recorded. It answers `ping`, and ignores other events, deleted refs, and tags.
- **Deploy on push:** a verified push deploys its commit to every app that tracks the repository (`owner/name`, any case) and the branch with auto-deploy on. Each delivery is recorded once: GitHub's redelivery of it deploys nothing new. A newer push replaces a deploy that has not started. Pushes for unknown repositories or untracked branches are recorded as ignored, with the reason.
- **CLI:** `shipyard app update APP [--branch B] [--auto-deploy=true|false]`, and `app create --auto-deploy`.
- **Private repositories through a GitHub App:** set `SHIPYARD_GITHUB_APP_ID` and `SHIPYARD_GITHUB_APP_KEY_FILE` on the worker, and `--github-installation ID` on the app (`app create` or `app update`). Each fetch uses a new token that can only read that one repository and expires within the hour; it is never stored or logged. `app show` names the installation.
- **Catch-up of missed pushes:** at worker start and every `SHIPYARD_CATCHUP_INTERVAL` (default 5m), an auto-deploy app whose branch head is a commit it never tried deploys it, so a push made while Shipyard was down is not lost. A rollback is not undone, and a commit that failed is not retried.
- **Deploys show in GitHub:** for an app with a GitHub App installation, every deploy and rollback is a GitHub Deployment of its commit in an environment named after the app: in progress, then successful with the app's address, or failed (naming the phase and the Shipyard operation, not the error). The release it replaced is marked inactive. The App needs the Deployments (read & write) permission. Needs migration `0004`.
- **`SHIPYARD_WORKER_LEASE`** (default 1m): how long a crashed worker's operation waits before another worker, or the restarted one, resumes it.
- **Restore:** `shipyard-worker restore --from DIR` loads a backup onto a new server: Caddy's certificates and the database, after checking the backup's checksums, the encryption keys, and that the database is empty. Started afterwards, the worker rebuilds each app from its recorded commit. The steps are in [docs/RESTORE.md](docs/RESTORE.md). `SHIPYARD_BACKUP_PG_RESTORE` sets the `pg_restore` to use.
- **Build cache and log caps:** at start and daily (`SHIPYARD_RETENTION_INTERVAL`), the worker prunes the build cache down to `SHIPYARD_BUILD_CACHE_MAX` (default 10 GiB). It also keeps build and deploy logs only for each app's newest `SHIPYARD_RETAIN_OPERATIONS` (default 20) operations, each capped at `SHIPYARD_OPERATION_LOG_MAX` (default 5 MiB). A capped log keeps its beginning, its end, and every warning and error, and says what was removed.
- **Self-healing:** the worker brings the running release back when its container disappears. It recreates the container from the same image and environment, or starts it if it was stopped. The check runs within `SHIPYARD_RECONCILE_INTERVAL`, and no change to Caddy is needed.
  - If the image is gone too (after a restore onto a new server), the worker rebuilds the release's commit with the configuration it ran with, through the usual health gate. It tries once; if that fails, deploy or roll back.
- **Release history:** `shipyard releases APP [--limit N] [--before ID]` (`GET /v1/apps/{app}/deployments`) lists an app's deployments, newest first, with commit, image ID, environment revision, status, and failure reason. Pages continue with `--before`.
- **Published API:** set `SHIPYARD_API_HOSTNAME` and Caddy serves the API there over HTTPS (HTTP/2): `/v1/*` and `/hooks/github` only. The API must listen on a Unix socket (`SHIPYARD_API_LISTEN=unix:/run/shipyard-api/api.sock`, now in `shipyard.env` for both services), which the worker mounts read-only into Caddy. Apps cannot take the API's hostname.
- **App logs:** `shipyard logs APP [--tail N] [--follow]` shows the running release's output (`GET /v1/apps/{app}/logs`, SSE), with stdout and stderr kept apart. The worker reads the logs and serves them to the API on a private socket (`SHIPYARD_WORKER_SOCKET`). The API never touches Docker.
  - **Redaction:** the app's secret values (6 characters or longer) are shown as `[REDACTED]`. This is best effort: a secret printed in another form is not caught.
  - The candidate output that a failed deploy copies into the operation's events is redacted the same way.
- **Observation window:** after a switch, the previous release keeps running for `SHIPYARD_OBSERVATION_WINDOW` (default 5 min; `0s` to `24h`). The worker then stops it gracefully with the app's `stop_timeout` (`SIGTERM`, then `SIGKILL`) and removes it. The image stays. The worker also removes containers of failed deployments that a crash left behind. It never touches containers whose deployment is not in its database.
- **Worker configuration:** `SHIPYARD_WORK_DIR`, `SHIPYARD_SOURCE_BASE_URL`, `SHIPYARD_BUILDER`, `SHIPYARD_BUILDER_MEMORY`, and `SHIPYARD_BUILDER_CPUS`. The worker now requires `SHIPYARD_KEK_ACTIVE` and access to Docker.
- **Apps API:** `/v1/apps` create, list, show (by slug or ID), update, and delete. Every field is validated before it reaches the database: git branch rules, repository-relative paths without `..`, ports, and limits. All invalid fields are reported at once as `application/problem+json`.
- **Environment API:** `GET /v1/apps/{app}/env` lists keys only; `PUT` and `DELETE /v1/apps/{app}/env/{key}` create new revisions. Values are secret (encrypted) by default.
- **Configuration:** `SHIPYARD_KEK_DIR` and `SHIPYARD_KEK_ACTIVE`. `shipyard-api serve` refuses to start without the active KEK, or with a KEK file other users can read.
- **Secret encryption and environment revisions** (`internal/secrets`): envelope encryption with a per-value AES-256-GCM key wrapped by a file-based KEK. Every change creates an immutable, numbered revision that reuses unchanged values without decrypting them.

### Security

- **Builder network:** the build container now joins a Docker network of its own (`shipyard-build`, labelled `io.shipyard.role=build`) instead of the default bridge, so builds cannot reach other containers by address. Outbound access from builds stays open, by decision: see ADR-0009.
- **Rootless builds:** builds run on rootless BuildKit (`moby/buildkit:v0.33.1-rootless`, pinned by digest), so a build step runs as an unprivileged host user, not as root (ADR-0010).
  - At start, the worker replaces a builder that runs another image, including one created by an earlier Shipyard. The replacement also gets the builder network. The first build after the upgrade starts with an empty cache.
  - **Ubuntu 24.04 and later:** install `deploy/sysctl/60-shipyard-buildkit.conf` and run `sudo sysctl --system`, or builds fail.
- **Namespaces:** the API, worker and backup units set `RestrictNamespaces=yes`. Reinstall the units from `deploy/systemd/`.
- **API limits** (ADR-0011): the API now limits each client IP (IPv6 per /64) on every route except `/healthz` and `/readyz`.
  - The limits are 10 requests per second with bursts of 50, and 10 failed authentications per 15 minutes.
  - A client over a limit gets 429 with `Retry-After`. One out of authentication failures is refused even with a valid token until the window frees one.
  - Tune with `SHIPYARD_API_RATE`, `SHIPYARD_API_BURST`, and `SHIPYARD_AUTH_FAILURES`; 0 turns one off.

### Removed

- **`SHIPYARD_API_ALLOW_PUBLIC_LISTEN`.** The API now listens only on loopback or a Unix socket, and public traffic reaches it through Caddy. A non-loopback `SHIPYARD_API_LISTEN` is refused at startup, even with the old setting.

## [0.1.0] - 2026-09-26

First release: the Phase 0 bootstrap. An empty but fully wired project; it does not deploy apps yet.

### Added

- **Project definition:** architecture v2 verified against primary sources, 7 accepted ADRs, a phased roadmap, and agent instructions (`CLAUDE.md`).
- **Binaries:**
  - `shipyard` (CLI; `version`),
  - `shipyard-api` (`serve`, `migrate`, `version`),
  - `shipyard-worker` (`run`, `version`).
  All shut down gracefully on SIGTERM.
- **API:**
  - `GET /healthz` (liveness) and `GET /readyz` (database readiness, `application/problem+json` on failure).
  - Request IDs and a structured access log.
- **API listen address:** loopback TCP or a Unix socket (mode 0660). Public addresses are refused unless explicitly allowed.
- **Configuration and logging:** `SHIPYARD_*` environment variables, validated at startup with every error reported at once. Structured JSON logs carry correlation IDs.
- **Migrations:** forward-only runner with embedded SQL. It runs in one transaction, is safe under concurrency, and refuses unknown or renamed applied migrations.
- **Tooling:**
  - Makefile targets, CI (lint, race-enabled unit tests, integration tests on PostgreSQL 18), and a local PostgreSQL 18 dev environment.
  - The `deploy/` skeleton: systemd units, Docker `daemon.json`, and the Caddy admin-socket bootstrap.
- **Release pipeline:** GoReleaser binaries (Linux, macOS, Windows CLI; Linux API and worker), SHA-256 checksums, build provenance attestations, and a multi-arch image at `ghcr.io/hami9/shipyard`.
- **Licensing and security:** Apache-2.0 license, `SECURITY.md`, and `CONTRIBUTING.md`.
