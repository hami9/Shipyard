# Architecture Review: v1 → v2

A review of the [original proposal](archive/ARCHITECTURE-v1.md) against official documentation. The result is [ARCHITECTURE.md](ARCHITECTURE.md). Source tags refer to [SOURCES.md](SOURCES.md).

**Reviewed:** 2026-09-25

**Verdict:** v1's direction holds up well. It uses one host, PostgreSQL as the source of truth, and Caddy for TLS, with health-gated switching, rollback by immutable artifact, and a reconciler.

The corrections below fall into three groups:

- places where v1 contradicts vendor behavior,
- places where v1 names a goal without a mechanism that works,
- places where v1 leaves a security gap.

## Summary

Severity key:

- **High:** security gap or wrong assumption that breaks correctness.
- **Med:** operational failure likely in production.
- **Low:** clarification or modernization.

| # | Area | Severity | Change |
| --- | --- | --- | --- |
| R1 | System diagram | Low | API and worker communicate only through PostgreSQL. Caddy also fronts the API and webhook. |
| R2 | Caddy admin API | **High** | Bind the admin API to a permissioned Unix socket, not TCP `localhost:2019`. Apply changes with a full `POST /load` and `If-Match`. |
| R3 | Container logs | **High** | The default `json-file` driver never rotates. Set the `local` log driver in `daemon.json`. |
| R4 | Commit provenance | **High** | Verify the SHA is reachable from the tracked branch, because fork commits can be fetched from the upstream repository. |
| R5 | Image identity | Med | "Image digest" is replaced by the Engine **image ID** plus the recorded buildx metadata digests. |
| R6 | Build limits | Med | BuildKit limits are set on a dedicated `docker-container` builder or with `--resource`, not with `docker run`-style flags. Use `--load`. |
| R7 | Webhook semantics | Med | Respond within 10 s. There is no automatic redelivery, so the reconciler catches up. Redeliveries reuse the delivery ID. 25 MB body cap. Ignore `deleted` pushes. |
| R8 | Per-app locking | Med | Use a lease plus a partial unique index instead of long-held advisory locks. Claim with `SKIP LOCKED`. |
| R9 | Secrets | **High** | Specify envelope encryption (AES-256-GCM, KEK outside the DB, AAD, key IDs) and move it from milestone 5 to Foundation. |
| R10 | Firewall and ports | **High** | Docker-published ports bypass ufw, so only Caddy publishes ports. PostgreSQL and the API are never published. |
| R11 | Domains and TLS | Med | ACME already proves control. The real risks are rate limits and hijacking, so add a DNS preflight, the staging CA in tests, `ask` if on-demand TLS is used, and back up Caddy data. |
| R12 | Container hardening | Med | Replace "disable dangerous capabilities" with concrete defaults: `cap-drop ALL`, `no-new-privileges`, pids/mem/cpu limits, restart policy, per-app networks. |
| R13 | Daemon restarts | Low | Enable `live-restore` (patch upgrades only). |
| R14 | Old-release shutdown | Low | Make the stop timeout configurable per app. Docker sends `SIGTERM`, then `SIGKILL` after 10 s by default. |
| R15 | SSE | Low | Event IDs with `Last-Event-ID`, a keepalive every ~15 s, and HTTP/2 to avoid the browser's 6-connection limit. |
| R16 | Build secrets | Med | Never pass tokens as build args or environment variables, because they persist in the image. Use secret mounts only if builds need private dependencies. |
| R17 | Versions and libraries | Low | Pin baselines: Go 1.26+, PostgreSQL 18, Docker 29 with the containerd store, and the `moby/moby/client` SDK. |
| R18 | Rollback and rotated secrets | Low | Warn when the target revision contains rotated secrets and offer `--with-current-config`. |
| R19 | Milestones | Med | Reorder the milestones and add a restore drill to the acceptance demo. Resolve the open decisions as proposed ADRs. |

## Details

### R1. System diagram showed API → worker coupling

- **v1:** `A["Shipyard API"] --> Q["Persistent deployment worker"]`.
- **Issue:** The prose says intent is recorded durably, but the diagram implies a direct call. A direct call would create a second, non-durable path. The diagram also omitted Caddy in front of the API and webhook, so the TLS for Shipyard itself was unspecified.
- **v2:** The API and worker share only PostgreSQL. Caddy terminates TLS for apps, the API, and `/hooks/github`. A privilege table now names what each process may and must never reach.

### R2. Caddy admin endpoint was an unguarded control plane

- **v1:** "Adds/removes Caddy routes and checks configuration changes."
- **Evidence:** The admin API defaults to `localhost:2019`. Its docs say to use a permissioned Unix socket when untrusted code runs on the host `[CADDY-API][CADDY-OPTIONS]`.
- **Issue:** Caddy must share a Docker network with the app containers to reach them. A TCP admin listener inside that container is therefore reachable by every app, and any app could rewrite routing.
- **v2:**
  - The admin API listens on a Unix socket that only Caddy and the worker can access.
  - The route manager renders the **whole** config from the `routes` table and applies it with `POST /load`. Caddy applies it atomically with zero downtime and rolls back on failure. `If-Match`/`Etag` guards against concurrent writers `[CADDY-API]`.
  - Because the config is a pure function of the database, reconciliation is re-render plus load.

### R3. Log limits were stated but the default driver ignores them

- **v1:** "Apply resource and log limits."
- **Evidence:** "By default, no log-rotation is performed." With `json-file`, this can exhaust the disk. The `local` driver is recommended because it rotates by default: 5 × 20 MB, compressed `[DK-LOG][DK-LOG-LOCAL][DK-LOG-JSON]`.
- **v2:** Set `"log-driver": "local"` in `daemon.json`, and set the log driver explicitly on every app container.

### R4. A commit SHA alone does not prove it belongs to the repository

- **v1:** "Resolve the exact commit SHA… Check out the exact commit."
- **Evidence:** "Commits pushed to any repository in a network can be accessible from other repositories in that network, including the upstream repository" `[GH-FORKS]`.
- **Issue:** `shipyard deploy --ref <sha>` could deploy code from a fork's pull request that was never merged.
- **v2:** Before building, the fetcher verifies the SHA is an ancestor of the tracked branch, for example with `git merge-base --is-ancestor`. If it is not, the operation fails.

### R5. "Image digest" is ambiguous for local builds

- **v1:** "Build an image tagged by application and commit. Store its digest."
- **Evidence:** Engine 29 uses the containerd image store only on **fresh** installs, and upgraded hosts keep overlay2 `[DK-29][DK-CONTAINERD]`. buildx metadata reports `containerimage.digest` and `containerimage.config.digest` `[DK-BX-BUILD]`.
- **Issue:** What "digest" means, and whether a repository digest exists, depends on the store backend and on whether the image was pushed.
- **v2:**
  - Run containers by the Engine **image ID** returned by inspection.
  - Store the buildx metadata digests for provenance.
  - Tags are for humans only.

### R6. Build resource limits need BuildKit-specific mechanisms

- **v1:** "Bound CPU, memory, disk, network access, output size, and duration."
- **Evidence:**
  - BuildKit is the default builder `[DK-BUILDKIT]`.
  - A `docker-container` builder takes `memory`, `cpu-quota`, and related driver options, but does **not** load results into the image store unless you pass `--load` `[DK-BX-CONTAINER]`.
  - `buildx build --resource` limits `RUN` steps, and `--network none` exists `[DK-BX-BUILD]`.
- **v2:**
  - Use a dedicated `shipyard` builder with CPU and memory caps, `--load`, a context deadline, and bounded logs.
  - Leave network access on in the MVP, because most Dockerfiles download dependencies. Egress control is a Phase 5 task, recorded as a known risk.

### R7. Webhook behavior underspecified

- **Evidence:**
  - GitHub expects a 2XX within 10 s and recommends asynchronous processing `[GH-BP]`.
  - Failed deliveries are **not** automatically redelivered, and manual or API redelivery covers only the past 3 days `[GH-REDELIVER]`.
  - Redeliveries keep the same `X-GitHub-Delivery` `[GH-BP]`.
  - Payloads are capped at 25 MB. Push events carry `deleted`, and GitHub skips events for pushes of more than 3 tags or more than 5000 branches `[GH-EVENTS]`.
  - The signature is `sha256=` plus hex HMAC-SHA256, compared in constant time `[GH-VALIDATE]`.
- **v2:**
  - The receiver verifies, records, enqueues, and returns. Deduplication uses the delivery ID as the primary key.
  - Deleted-ref pushes are ignored.
  - The reconciler compares branch heads to catch missed deliveries.
  - The body limit is 25 MB.

### R8. "Lock the application" needs a mechanism that survives long builds

- **Evidence:** Session-level advisory locks ignore transaction rollback and last until the session ends. They also consume the shared lock pool `[PG-LOCKS]`. `SKIP LOCKED` is designed for queue consumers `[PG-SELECT]`.
- **Issue:** A build can take minutes. Holding a session lock across it ties correctness to one pooled connection, and a crashed worker's lock depends on connection teardown.
- **v2:**
  - Claim jobs with `FOR UPDATE SKIP LOCKED` and set `lease_owner` and `lease_expires_at`, renewed by a heartbeat.
  - A **partial unique index** `(app_id) WHERE status = 'running'` makes "one active operation per app" a database invariant.
  - Expired leases are re-queued by the reconciler.

### R9. Secrets: mechanism and timing

- **v1:** Authenticated encryption with a separate master key, delivered in milestone 5.
- **Evidence:** OWASP recommends AES-256 in GCM (or CCM) first, keys stored separately from the data they protect, envelope encryption with the KEK apart from DEKs, and rotation in place before it is needed `[OWASP-CRYPTO]`. Go's `NewGCMWithRandomNonce` handles nonces, with a limit of 2³² messages per key `[GO-GCM]`.
- **v2:**
  - Each value gets its own DEK wrapped by a versioned KEK, with AAD binding `(app_id, key, value_id)`.
  - Environment revisions reference immutable value rows, so the API can create a revision without decrypting anything.
  - This lands in **Phase 1**, because deployments reference revisions from the first deploy.

### R10. Published ports bypass the host firewall

- **Evidence:** "When you publish a container's ports using Docker, traffic … gets diverted before it goes through the ufw firewall settings" `[DK-FW]`.
- **Issue:** A PostgreSQL or API container started with `-p 5432:5432` would be internet-reachable despite ufw.
- **v2:** Only Caddy publishes ports (80/tcp, 443/tcp, 443/udp). PostgreSQL and the API bind to localhost or Unix sockets.

### R11. Domain "proof" is performed by ACME; guard the rate limits

- **v1:** "Prove hostname control or restrict allowed domains before issuing certificates."
- **Evidence:** Caddy obtains certificates automatically when DNS points to the host and ports 80/443 are reachable. It needs a persistent data directory, and on-demand TLS must be restricted with `ask` `[CADDY-HTTPS]`. Let's Encrypt allows 5 authorization failures per identifier per hour and 5 certificates per identical identifier set per 7 days `[LE-LIMITS]`.
- **v2:**
  - Run a DNS preflight before creating a route, and keep one app per hostname.
  - Use the staging CA in development and CI.
  - Keep on-demand TLS off, or gated by `ask`.
  - Back up the Caddy data directory, because re-issuing everything after a host loss can hit the limits.

### R12. Container hardening made concrete

- **Evidence:** Docker already keeps an allowlist of capabilities `[DK-SEC]`. `--cap-drop`, `no-new-privileges`, `--pids-limit`, `--memory`, `--cpus`, `--read-only`, and `--init` are documented in `[DK-RUN]`. User-defined bridges isolate their members, while the default bridge is shared by all `[DK-BRIDGE]`.
- **v2:** Default to `--cap-drop ALL` with a per-app add-back allowlist, `no-new-privileges`, and pids, memory, and CPU limits. Use `--restart unless-stopped` and **one network per app**, so apps cannot reach each other while bypassing Caddy.

### R13. Daemon restarts and upgrades

- **Evidence:** `live-restore` keeps containers running while the daemon is down, for patch upgrades only `[DK-LIVE]`.
- **v2:** Enable it and document the major-upgrade limitation in the operator guide.

### R14. Graceful stop of the previous release

- **Evidence:** `docker stop` sends the stop signal, waits 10 s by default, then sends `SIGKILL` `[DK-RUN]`.
- **v2:** Add a per-app `stop_timeout`. Stop the old container only after the observation window. Caddy's config swap is graceful `[CADDY-API]`.

### R15. SSE details

- **Evidence:** `Last-Event-ID` on reconnect, a comment line every ~15 s for proxies `[WHATWG-SSE]`, and a 6-connection limit over HTTP/1.1 `[MDN-SSE]`. Caddy flushes `text/event-stream` immediately `[CADDY-RP]`.
- **v2:** Operation events carry a `seq` that serves as the SSE `id`. The server sends keepalives, and HTTP/2 comes through Caddy.

### R16. Credentials must never enter builds

- **Evidence:** Build args and environment variables persist in the final image. Use secret mounts instead `[DK-BUILD-SECRETS]`.
- **v2:** GitHub tokens are used only by the worker's git fetch, as a header rather than in the URL. They are never written to the context or passed as build args. Build secrets for private dependencies are a later feature and use `--secret`.

### R17. Versions and libraries pinned to current releases

- **Evidence:**
  - Go 1.27 is current and 1.26 is supported `[GO-REL]`. Standard-library routing supports methods and wildcards `[GO-ROUTING]`.
  - PostgreSQL 18 is current `[PG-VERSIONS]`.
  - Engine 29 deprecates `github.com/docker/docker` in favor of `github.com/moby/moby/client` and requires API ≥ 1.44 `[DK-29]`.
- **v2:** See the platform baseline table in ARCHITECTURE §1.

### R18. Rollback may resurrect a revoked secret

- **Issue:** This is a design gap rather than a vendor fact. Rolling back to the original environment revision is correct for reproducibility. It is wrong if a secret in that revision was rotated because it leaked.
- **v2:** Rotated keys are tracked. Rollback warns and offers `--with-current-config`.

### R19. Milestones and open questions

- **v2:**
  - Secrets, baseline hardening, and the audit log move into Foundation.
  - Leases and phases land in Phase 1, and the reconciler with crash tests in Phase 3.
  - The acceptance demo adds a restore drill.
  - Each open question now has a proposed default in an ADR: [ADR-0003](adr/0003-caddy-routing-via-admin-socket.md), [ADR-0004](adr/0004-image-build-and-identity.md), [ADR-0006](adr/0006-retention-and-backup.md), and [ADR-0007](adr/0007-mvp-trust-model.md).

## What v1 got right (kept unchanged)

- A single host, with PostgreSQL as the source of truth and no Redis until throughput demands it.
- The API never executes repository code. Docker access belongs only to the worker.
- Health-gated traffic switching, with a failed deploy leaving the current route untouched.
- Rollback from a retained artifact that never rebuilds a moving branch.
- SSE before WebSockets. Interfaces defined where they are used. No Kubernetes abstractions without a second runtime.
- An explicit trust model: the MVP is trusted-user and trusted-repository software.
