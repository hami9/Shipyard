# Changelog

All notable changes to Shipyard are recorded here.

- **Format:** [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).
- **Versioning:** [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).
- **Before 1.0**, a minor bump (`0.x.0`) may contain breaking changes.
- **Release notes:** each GitHub Release uses the section for its tag (`scripts/release-notes.sh`).

## [Unreleased]

### Added

- **Database schema v1** (migration `0002`): users and API tokens, apps, encrypted secret values and immutable environment revisions, the operations queue and its events, deployments, routes, webhook deliveries, and audit events. The database itself enforces one running operation and one active deployment per app, unique idempotency keys and hostnames, and same-app references.
- **API tokens:** `shipyard-api token create|list|revoke` bootstraps and manages `shp_` tokens on the server. Tokens have scopes (`read`, `deploy`, `admin`) and an expiry of 1h to 366d (default 90d). Only a SHA-256 hash is stored, the plaintext is printed once, and every create and revoke is audited.
- **API authentication:** every `/v1` route requires a bearer token with the right scope (401 or 403 per RFC 6750). Every authenticated mutation is audited, allowed or denied. `GET /v1/whoami` describes the calling token.
- **CLI (`shipyard`):** `login` (token from stdin, verified, saved with mode 0600), `whoami`, `app create|list|show`, `ps`, `env set|unset|list` (values from stdin, never printed), `deploy [--ref SHA] [--idempotency-key K]`, and `operation ID`. It refuses plain `http://` to a non-loopback host and supports `unix://` sockets. `SHIPYARD_URL` and `SHIPYARD_TOKEN` override the config, for CI.
- **Deploy API:** `POST /v1/apps/{app}/deployments` queues a deploy (idempotent with `Idempotency-Key`; a newer request cancels older queued ones), and `GET /v1/operations/{id}` reports its status and phase.
- **Deployments (worker):** `shipyard-worker run` executes deploys. It fetches the pinned or head commit, refusing one that is not on the tracked branch, then builds it on a resource-limited buildx builder. It then starts a hardened container on the app's own network, with the environment decrypted and injected. Next comes a health gate: 3 consecutive 2xx/3xx on `health_path`, while the container keeps running. Last, it marks the deployment active and drains the previous one.
  - A failed build, start, or health check leaves the running release untouched and removes the candidate. The last lines of the candidate's output are recorded.
  - A worker that crashes or restarts resumes the deploy where it stopped.
  - There is no public route until Phase 2.
- **Caddy edge:** at start, the worker keeps a Caddy container (`caddy:2.11.4-alpine`, pinned by digest) running.
  - It is the only container that publishes ports (80/tcp, 443/tcp, 443/udp), and it joins every app network.
  - Its admin API is available only on a Unix socket (mode 0660) that the worker's group can use.
  - Certificates and the last loaded config persist in volumes, and a restart resumes that config.
  - Configured with `SHIPYARD_CADDY`, `SHIPYARD_CADDY_NAME`, `SHIPYARD_CADDY_IMAGE`, `SHIPYARD_CADDY_ADMIN_DIR`, `SHIPYARD_CADDY_BIND`, `SHIPYARD_CADDY_HTTP_PORT`, and `SHIPYARD_CADDY_HTTPS_PORT`.
- **Caddy config from the database:** at start, the worker renders Caddy's whole config from the routes table. It loads the config only if it differs, as a conditional replace (`If-Match`) that fails safely on a concurrent change. The admin API always stays on its socket. A config Caddy rejects leaves the running one in place. Certificates are configured with `SHIPYARD_CADDY_CA` (default, `staging`, or `internal`) and `SHIPYARD_ACME_EMAIL`.
- **Worker configuration:** `SHIPYARD_WORK_DIR`, `SHIPYARD_SOURCE_BASE_URL`, `SHIPYARD_BUILDER`, `SHIPYARD_BUILDER_MEMORY`, and `SHIPYARD_BUILDER_CPUS`. The worker now requires `SHIPYARD_KEK_ACTIVE` and access to Docker.
- **Apps API:** `/v1/apps` create, list, show (by slug or ID), update, and delete. Every field is validated before it reaches the database: git branch rules, repository-relative paths without `..`, ports, and limits. All invalid fields are reported at once as `application/problem+json`.
- **Environment API:** `GET /v1/apps/{app}/env` lists keys only; `PUT` and `DELETE /v1/apps/{app}/env/{key}` create new revisions. Values are secret (encrypted) by default.
- **Configuration:** `SHIPYARD_KEK_DIR` and `SHIPYARD_KEK_ACTIVE`. `shipyard-api serve` refuses to start without the active KEK, or with a KEK file other users can read.
- **Secret encryption and environment revisions** (`internal/secrets`): envelope encryption with a per-value AES-256-GCM key wrapped by a file-based KEK. Every change creates an immutable, numbered revision that reuses unchanged values without decrypting them.
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
