# Shipyard Roadmap

The phased delivery plan for [ARCHITECTURE.md](ARCHITECTURE.md).

- **Status legend:** `[ ]` todo · `[~]` in progress · `[x]` done. Agents tick items here and log details in [WORKLOG.md](WORKLOG.md).
- **Task IDs** (`P<phase>.<n>`) are stable. Reference them in commits' bodies, work log entries, and ADRs.
- **Estimates** assume one full-time developer with AI assistance. They are sizing guides, not commitments. Re-estimate at each phase start.

## Overview

```mermaid
flowchart LR
  P0["P0 Bootstrap<br/>~1 wk"] --> P1["P1 Foundation<br/>first deploy<br/>~3-4 wk"]
  P1 --> P2["P2 Safe releases<br/>HTTPS + traffic<br/>~2-3 wk"]
  P2 --> P3["P3 Recovery<br/>rollback, reconcile, backup<br/>~2-3 wk"]
  P3 --> P4["P4 GitHub<br/>webhooks, private repos<br/>~2 wk"]
  P4 --> P5["P5 Hardening<br/>+ install<br/>~2-3 wk"]
  P5 --> V1(["v1.0 MVP"])
  P3 -.parallel track.-> P6["P6 Web UI<br/>~3 wk"]
  P6 -.optional for 1.0.-> V1
  V1 --> P7["P7 Post-MVP backlog"]
```

| Phase | Theme | Est. | Cumulative | Demo at exit |
| --- | --- | --- | --- | --- |
| **P0** | Bootstrap: repo, tooling, CI, dev env, release pipeline → `v0.1.0` | ~1 wk | 1 wk | `make lint test test-integration` green in CI |
| **P1** | Foundation: first manual deploy | 3–4 wk | 4–5 wk | CLI deploys a public repo at a pinned SHA into a hardened, healthy container |
| **P2** | Safe releases: HTTPS and traffic switching | 2–3 wk | 6–8 wk | App on HTTPS. A broken deploy never interrupts the serving release |
| **P3** | Recovery: rollback, reconciler, retention, backup | 2–3 wk | 8–11 wk | Rollback, kill mid-deploy and recover, restore onto a fresh host |
| **P4** | GitHub integration | ~2 wk | 10–13 wk | Push to deploy. Redelivery is deduplicated. Private repo works |
| **P5** | Hardening, install, docs → **v1.0** | 2–3 wk | 12–16 wk | Full acceptance demo on a fresh VPS |
| **P6** | Web UI (parallel after P3) | ~3 wk | — | All CLI flows in the browser |
| **P7** | Post-MVP backlog | — | — | — |

### What changed vs v1 milestones

| v1 milestone | Now | Why |
| --- | --- | --- |
| 1 Foundation | P0 + P1 | Tooling and CI get their own phase. **Secrets, container hardening, and audit move in here** (R9, R12). |
| 2 Usable deployment | P2 | Same scope, plus the Caddy admin socket, DNS preflight, and SSE resume (R2, R11, R15). |
| 3 Recovery | P3 | Adds crash-injection tests and a **restore drill** (R19). |
| 4 GitHub integration | P4 | Adds the ancestry check, catch-up for missed deliveries, and the 25 MB cap (R4, R7). |
| 5 Hardening and UI | P5 + P6 | The UI is split into a parallel track so it cannot block the MVP. |

## P0: Bootstrap `[x]`

**Goal:** an empty but fully wired project, so every later task is only "add code and tests".

- [x] **P0.1** Run `go mod init`. Add `cmd/shipyard`, `cmd/shipyard-api`, and `cmd/shipyard-worker` with `--version` and graceful shutdown (ADR-0001).
- [x] **P0.2** `Makefile`: `build`, `test` (`-race`), `lint` (gofmt, vet, staticcheck), `test-integration`, `dev-up/down`, `migrate`.
- [x] **P0.3** CI workflow (`.github/workflows/ci.yml`, green since run #1): lint and unit tests on every push. A separate integration job runs with a PostgreSQL 18 service and Docker.
- [x] **P0.4** `internal/config` (environment variables only, validated at startup, supplied by systemd `EnvironmentFile=`) and a `log/slog` JSON logger with request and operation IDs.
- [x] **P0.5** `deploy/` skeleton:
  - systemd units for the API and worker (separate users),
  - `daemon.json` (`local` log driver, `live-restore`),
  - a Caddy bootstrap config with the admin Unix socket.
- [x] **P0.6** Dev environment: PostgreSQL 18 via compose, bound to `127.0.0.1` only, with `make dev-up`.
- [x] **P0.7** Migration tool: an in-house forward-only runner (`internal/store`, embedded SQL plus pgx, one transaction per run, advisory-lock serialized). Add the first migration and wire up `make migrate`.
- [x] **P0.8** Public-repository readiness:
  - Apache-2.0 `LICENSE` and `NOTICE`, `SECURITY.md`, `CONTRIBUTING.md`, `CHANGELOG.md`.
  - A release pipeline (GoReleaser: binaries, checksums, provenance, a GHCR image) and [docs/RELEASING.md](RELEASING.md).

**Exit criteria**

- A fresh clone passes `make lint test`. CI is green. ✅
- `make dev-up && make migrate && make test-integration` passes locally. ✅ (owner's WSL2 machine, 2026-09-26)
- Merged to `main` and released as [`v0.1.0`](https://github.com/hami9/Shipyard/releases/tag/v0.1.0) (2026-09-26).

## P1: Foundation, the first manual deploy `[~]`

**Goal:** `shipyard deploy` builds a public repository at an exact SHA and runs it in a hardened container that passes a health check. There is no public routing yet.

- [x] **P1.1** Schema v1 (`migrations/0002_schema_v1.sql`, with constraint tests in `internal/store/schema_integration_test.go`):
  - Tables: users, api_tokens, apps, secret_values, env_revisions and entries, deployments, operations, operation_events, audit_events, routes, webhook_deliveries.
  - Constraints: `UNIQUE(idempotency_key)`, the partial unique index for one running operation per app, and a unique `hostname`.
- [x] **P1.2** `internal/store` core on pgx:
  - `Store` and `InTx` (a nested call joins the outer transaction).
  - Database errors mapped to `ErrNotFound`, `ErrConflict`, `ErrInvalid`, `ErrReference`, and `ErrImmutable`, naming the constraint but never the values.
  - Users and apps repositories.
  - Each later task adds the queries it consumes, with integration tests: tokens and audit (P1.3), secrets and revisions (P1.4), operations and events (P1.5), deployments (P1.11).
- [x] **P1.3** Token auth:
  - A bootstrap admin token command (`shipyard-api token create|list|revoke`).
  - `shp_` tokens stored as a SHA-256 hash, with scopes (`read` ⊂ `deploy` ⊂ `admin`) and expiry.
  - Middleware, plus an audit event on every mutation (ADR-0007). `GET /v1/whoami`.
- [x] **P1.4** `internal/secrets`: envelope encryption and environment revisions (ADR-0005).
  - Negative tests: wrong AAD, wrong KEK, tampered ciphertext.
  - A test proving that a DB dump contains no plaintext.
  - Wiring the keyring into the API and worker (config `SHIPYARD_KEK_DIR` and the active KEK id) arrives with the first consumers: the env endpoints (P1.6) and container start (P1.10).
- [x] **P1.5** `internal/queue`: claim with `SKIP LOCKED`, lease and heartbeat, complete and fail, idempotent insert, and coalescing of queued deploys (ADR-0002). Race tests put two workers on one app.
  - Also in `internal/store`: operation events with gapless `seq` for SSE resume, and `RequeueExpired` for the reconciler (called periodically from P3.2).
- [x] **P1.6** App CRUD API: validation of slug, port, and paths (no escape from the repository), plus `application/problem+json` errors.
  - Also the env endpoints (`GET`, `PUT`, `DELETE /v1/apps/{app}/env[/{key}]`) and the KEK configuration (`SHIPYARD_KEK_DIR`, `SHIPYARD_KEK_ACTIVE`, `make dev-kek`), deferred from P1.4.
- [x] **P1.7** CLI: `app create|list`, `env set` (value read from stdin), `env list` (keys only), `deploy`, `ps`, and `whoami`. Config holds the API URL and token.
  - Also `login`, `app show`, `env unset`, `operation ID`, and the API's `POST /v1/apps/{app}/deployments` and `GET /v1/operations/{id}`. Deploys stay `queued` until the worker executes them (P1.11).
- [x] **P1.8** `internal/source`: fetch the exact SHA, resolve the branch head, **check ancestry against the tracked branch**, and give each operation its own workspace (ADR-0004).
  - Also `Checkout.Path`, which resolves the Dockerfile and context paths through symlinks. The workspace root setting (`SHIPYARD_WORK_DIR`) is wired with the worker in P1.11. GitHub App tokens arrive in P4; `Request.Token` is already sent as a header.
- [x] **P1.9** `internal/build`:
  - A `shipyard` buildx builder with CPU and memory caps.
  - `--load`, a deadline, and `--metadata-file`.
  - Bounded log capture into operation events. The line sink is a callback; P1.11 connects it to `AppendOperationEvent`.
  - Docker tests use the `docker` build tag and `make test-docker`, which run on the owner's machine, not in CI.
- [x] **P1.10** `internal/runtime` on `moby/moby/client`:
  - A per-app network, the hardened flag set, `io.shipyard.*` labels, and environment injection.
  - Create, start, inspect, stop, and remove.
  - The automated `docker inspect` check from the exit criteria is `TestHardenedContainer` (`make test-docker`).
- [x] **P1.11** `internal/app` deploy use case covering the phases `queued → building → starting → health_checking → active (no route)`, with each phase persisted before its side effect. Also implement the health probe.
  - The worker runs the queue loop, holds leases, and requeues expired operations.
  - `test/e2e` (`make test-e2e`) drives the real binaries through every exit criterion below.

**Exit criteria**

- On a dev VM:
  - `app create`, `env set`, and `deploy` produce a running container from the pinned SHA, and the health check passes.
  - `docker inspect` confirms `CapDrop=ALL`, `no-new-privileges`, the limits, the `local` log driver, and no published ports (automated test).
- A broken Dockerfile produces a `failed` operation with a bounded build log.
- Repeating a request with the same `Idempotency-Key` produces one deployment.
- A SHA that is not on the tracked branch is rejected.

## P2: Safe releases, HTTPS, and traffic switching

**Goal:** apps are served over HTTPS through Caddy, and only healthy candidates ever receive traffic.

- [x] **P2.1** Caddy container bootstrap:
  - It is the only container with published ports (80/tcp, 443/tcp, 443/udp).
  - The admin API is on a Unix socket (`0660`, worker group only), and the data directory is a persistent volume.
  - It attaches to app networks (ADR-0003).
  - The worker ensures it at start (`runtime.EnsureEdge`).
- [x] **P2.2** `internal/routing` renderer: `routes` table → full Caddy JSON config, including the API and `/hooks/github` routes. Golden-file tests.
  - Also a `docker` test that loads the rendered config into a real Caddy and routes HTTPS through it.
- [x] **P2.3** Admin socket client: `GET` the config with its `Etag`, then `POST /load` with `If-Match`. Handle 412 and other errors.
  - Changed to `POST /config/`, because `/load` ignores `If-Match` (ADR-0003 note of 2026-09-28).
  - The worker renders and applies the routes table at start.
- [x] **P2.4** The `switching` phase: load, then verify through Caddy with the `Host` header, then commit route and status in one transaction. Failure paths re-render the previous state.
  - Verification uses a plain-HTTP Caddy server on a Unix socket with the same routes, so it does not wait for certificates.
  - Only verified hostnames are committed.
  - `test/e2e` checks over HTTPS through Caddy which container serves.
- [x] **P2.5** Domain API: DNS preflight (A/AAAA must resolve to the host), unique hostnames, an optional suffix allow-list, and a toggle for the Let's Encrypt staging CA.
  - `shipyard domain add|remove|list`, with several hostnames per app. A new hostname targets the running deployment.
  - The worker's reconciler applies route changes.
  - The staging toggle is `SHIPYARD_CADDY_CA=staging` (P2.3).
- [x] **P2.6** Observation window, then graceful stop of the previous container with the per-app `stop_timeout`.
- SSE, split in two (each needs about 400 lines):
  - [x] **P2.7a** Operation events with `id` and `Last-Event-ID` resume, and a keepalive every 15 s. Also `shipyard events ID` and `deploy --follow`.
  - [x] **P2.7b** `logs --follow` with a bounded tail and best-effort secret redaction. The owner chose (2026-09-28) that the worker serves logs on a private Unix socket, and the API authenticates and proxies them (ADR-0008).
- [x] **P2.8** The API listens on localhost or a Unix socket only, and is published through Caddy over HTTPS (HTTP/2).
  - `SHIPYARD_API_HOSTNAME` publishes it. Caddy reaches the socket through a read-only bind mount.
  - The public-listen override is removed (the owner's choice, 2026-09-28).

**Exit criteria**

- The sample app is reachable at `https://<domain>` with a valid certificate (staging CA in CI).
- An e2e test probes continuously while it deploys a health-failing release: zero non-2xx responses, and the old release keeps serving.
- A route verification failure restores the previous config (fault-injection test).
- `shipyard logs --follow` resumes after a dropped connection without losing events.

## P3: Recovery and durability

**Goal:** Shipyard survives crashes, restores from backup, and rolls back without rebuilding.

- [x] **P3.1** Deployment history: `GET /v1/apps/{id}/deployments` and `shipyard releases APP`.
- [x] **P3.2** `internal/reconcile`, run at startup and every 60 s. "Remove orphans" covers containers of failed deployments; containers whose deployment is not in the database are left alone (P2.6), and a deleted app's containers wait for P3.8:
  - Re-queue or fail expired leases.
  - Reconcile containers by label: remove orphans, recreate missing active containers.
  - Re-render and load the Caddy config on drift.
- [x] **P3.3** Rollback operation (the image check is the worker's, so "unavailable" is the operation's failure, not an API answer; see ARCHITECTURE §5 Rollback):
  - Uses the target's image ID and environment revision.
  - Returns "unavailable" if the image is gone.
  - Warns about rotated secrets and offers `--with-current-config`.
- [x] **P3.4** Retention job per ADR-0006, split in two:
  - [x] **P3.4a** Images: per app, the active release's and the last `SHIPYARD_RETAIN_IMAGES` (default 5) earlier releases' images are kept; other images this database recorded are removed, never forced. Reconciler step 5.
  - [x] **P3.4b** The BuildKit cache cap (daily `buildx prune` down to 10 GiB) and the operation event cap (the last 20 operations per app, 5 MiB each). A worker job, at start and every `SHIPYARD_RETENTION_INTERVAL`.
- [x] **P3.5** Backup (`shipyard-worker backup`; targets are local directories with an off-host hook each):
  - `pg_dump -Fc` and a Caddy data tarball to target A, and the KEK to separate target B.
  - Run by systemd timers.
- [x] **P3.6** Restore runbook plus an automated drill on a fresh VM or container host: restore the DB, KEK, and Caddy data, then the reconciler converges every app. Split in two:
  - [x] **P3.6a** The reconciler rebuilds an active deployment whose container and image are both gone: one deploy of its recorded commit, with the environment revision it ran with.
  - [x] **P3.6b** `shipyard-worker restore` (verify the manifest, `pg_restore`, put back Caddy's data), the runbook ([RESTORE.md](RESTORE.md)), and the automated drill (`TestRestoreDrill`, on one machine). The first drill on a real VPS is the owner's, with the Phase 3 exit criteria.
- [x] **P3.7** Crash-safety suite: `kill -9` the worker at every phase boundary through fault-injection hooks, then assert a consistent state. `TestCrashSafety`: 11 fault points of a deploy (`SHIPYARD_TEST_CRASH_AT`), all recovered. A crash during a rollback, in the reconciler, or of the API is not covered.
- [x] **P3.8** Delete app as an operation: containers, network, route, and images, with an audit event. `DELETE /v1/apps/{id}` now queues it (it used to delete an idle app's rows at once and refuse a serving one); `shipyard app delete APP --yes`.

**Exit criteria** (acceptance demo steps 4–6)

- Rolling back to an earlier release takes effect without a rebuild.
- Killing the worker mid-deploy leaves the system consistent after restart, verified for every phase.
- A restore onto a fresh host brings all apps back on their active SHAs, with certificates intact.

## P4: GitHub integration

**Goal:** a push to the tracked branch deploys automatically, safely, and exactly once.

- [x] **P4.1** `internal/webhook` (the receiver is `POST /hooks/github` in the API; the secret is the file `SHIPYARD_GITHUB_WEBHOOK_SECRET_FILE`; a verified push goes to a sink that P4.2 provides, until then it is answered `ignored`):
  - Read the raw body (≤ 25 MB) and verify `X-Hub-Signature-256` with `hmac.Equal` **before** parsing.
  - Respond 2XX in < 10 s. Accept only `push`, and ignore `deleted` pushes and other refs.
- [x] **P4.2** Delivery deduplication (`webhook_deliveries` primary key), with idempotent enqueue under the key `gh:<delivery-id>` and coalescing. The key is `gh:<delivery-id>:<slug>`: one push can deploy several apps. Done together with P4.3, which it needs (`store.RecordPush`).
- [x] **P4.3** Repository-to-app mapping and branch policy (`auto_deploy`, tracked branch). Unknown repositories are logged and ignored. Matched by `owner/name`, case-insensitively (the owner's choice, 2026-10-04); GitHub's repository id waits for P4.4. CLI: `app update APP --branch B --auto-deploy`.
- [x] **P4.4** GitHub App (`internal/github`, worker only; tested against a fake GitHub API and git server in `test/e2e`; a real App on a private repository is the owner's check with the exit criteria):
  - RS256 JWT (`exp` ≤ 10 min, `iat` −60 s).
  - A per-operation installation token scoped to one repository with `contents: read`.
  - The private key is stored outside the DB. Private repository fetch works.
- [x] **P4.5** Missed-delivery catch-up in the reconciler: compare the branch head with the last deployed SHA. As built: a worker job beside the reconciler (it calls GitHub), at start and every `SHIPYARD_CATCHUP_INTERVAL`; the head deploys when the app never tried that commit (`app.CatchUp`, `store.EnqueueCatchUp`).
- [ ] **P4.6** (Optional) Report deployment status back to GitHub.

**Exit criteria**

- Pushing a working change deploys automatically. Pushing a broken change leaves the prior release serving.
- Redelivering the same delivery from GitHub's UI creates no second deployment.
- A bad signature returns 401 with no DB write (test).
- A private repository deploys through the GitHub App.
- A push made while Shipyard was down is deployed by the catch-up.

## P5: Hardening, install, and v1.0

**Goal:** a stranger can install Shipyard on a fresh VPS and run the full acceptance demo.

- [ ] **P5.1** Builder egress control: evaluate a proxy or allow-list, then implement it or record an explicit deferral in an ADR.
- [ ] **P5.2** Rootless Docker and stronger build isolation: evaluation ADR.
- [ ] **P5.3** API rate limiting, auth-failure throttling, and token revoke and rotate commands.
- [ ] **P5.4** KEK rotation command with a test, and an evaluation of asymmetric sealing so the API cannot decrypt.
- [ ] **P5.5** Prometheus metrics on an internal listener: deploy duration, failure rate, queue depth, and health.
- [ ] **P5.6** Disk usage and certificate expiry metrics and alerts (80% threshold).
- [ ] **P5.7** `deploy/install.sh` and an operator guide covering install, upgrade (including the major-upgrade limit of Docker live-restore), backup, restore, and troubleshooting.
- [ ] **P5.8** Security review against the invariants in CLAUDE.md §3, plus `govulncheck` and a dependency audit.
- [ ] **P5.9** Run the full acceptance demo on a fresh VPS, record it, and tag `v1.0.0`.

**Exit criteria**

- All six acceptance demo steps in ARCHITECTURE §9 pass on a fresh VPS.
- `govulncheck` is clean, and the operator guide is complete.

## P6: Web UI (parallel track, starts after P3)

The UI is never required for a deploy (ARCHITECTURE §1).

- [ ] **P6.1** An OpenAPI description of `/v1` and a typed TypeScript client.
- [ ] **P6.2** A React and TypeScript app in `web/`, served as static files by Caddy, with a token login.
- [ ] **P6.3** App list, app detail, deployment history, and rollback.
- [ ] **P6.4** Live operation events and logs through `EventSource`.
- [ ] **P6.5** Environment management (write-only values) and domains.
- [ ] **P6.6** Accessibility pass and Playwright e2e tests.

**Exit criteria:** every CLI flow except install is available in the UI, and the e2e suite is green.

## P7: Post-MVP backlog (unscheduled)

- **P7.1** Multi-user accounts with app-level RBAC (the schema is ready, per ADR-0007).
- **P7.2** Push images to a registry, for durable rollback after a host loss.
- **P7.3** Preview deployments for same-repository branches. PRs from forks are never built.
- **P7.4** Notifications: outgoing webhooks, email, chat.
- **P7.5** Non-HTTP worker processes and scheduled jobs.
- **P7.6** PostgreSQL PITR (WAL archiving).
- **P7.7** A second runtime backend, which is the point to introduce the `Runtime` interface.
- **P7.8** Build secrets for private dependencies via BuildKit `--secret`.

## Acceptance demo → phase map

| Demo step (ARCHITECTURE §9) | Proven in |
| --- | --- |
| 1. Install on a fresh VPS and connect a repository | P5.7 (install), P4.4 (repository) |
| 2. Deploy over HTTPS | P2 |
| 3. Push a working change, then a broken one, and the prior release keeps serving | P4 plus P2 |
| 4. Roll back to an earlier release | P3.3 |
| 5. Kill mid-operation and reach a consistent state | P3.2, P3.7 |
| 6. Restore from backup onto a fresh host | P3.5, P3.6 |

## Risk register

| Risk | Impact | Mitigation | Phase |
| --- | --- | --- | --- |
| Builder has unrestricted egress | A malicious Dockerfile can exfiltrate or abuse the network | Trusted-repositories model (ADR-0007). Egress control evaluated in P5.1 | P5 |
| Losing the KEK | All secrets become unrecoverable | Separate off-host KEK backup and a restore drill | P3 |
| Let's Encrypt rate limits during tests | Certificates blocked for up to 7 days | Staging CA in development and CI, DNS preflight, Caddy data backup | P2 |
| Owner's local machine uses Docker Desktop (macOS or Windows) | Phase 1+ health probes cannot reach container IPs `[DK-DESKTOP-NET]` | Test on Linux, on WSL2 with native Docker Engine, or in a Linux VM (docs/DEVELOPMENT.md) | P1 |
| Disk exhaustion (images, cache, logs) | Deploys and apps fail | `local` log driver, retention job, disk alerts | P0, P3, P5 |
| Vendor behavior drift (Docker, Caddy, GitHub) | Wrong assumptions in code | Re-verify `SOURCES.md` at each phase start | All |
| Scope creep into the UI before the core loop is proven | Delayed MVP | UI is a parallel track and optional for 1.0 | P6 |
