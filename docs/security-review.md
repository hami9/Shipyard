# Security review (P5.8)

- **Date:** 2026-10-05
- **Scope:** the code on branch `security-review` (after P5.7), checked against the invariants in [CLAUDE.md §3](../CLAUDE.md), the surfaces Phase 5 added, the installer, and the dependencies.
- **Reviewer:** Claude Code. Each finding below is fixed or accepted with a reason. F2 was fixed separately, in P5.8b (2026-10-06).
- **Method:** each invariant is traced to the code that enforces it and the test that proves it. Where only code enforces it, the gap is called out. Tools: `govulncheck` v1.8.0 `[GO-VULNCHECK]`, `go mod verify`, `go version -m` on the built binaries.

## Findings

| # | Severity | Finding | Status |
| --- | --- | --- | --- |
| F1 | Medium | **Known vulnerability.** `golang.org/x/text` v0.29.0 has GO-2026-5970, an infinite loop on invalid input. It is reachable through pgx's connection setup (`norm.Form.*`). | **Fixed:** upgraded to v0.42.0, which also moved `golang.org/x/sync` to v0.23.0. `make vuln` reports nothing for either build-tag set |
| F2 | Medium | **The Caddy admin socket is reachable by the API's user.** Its directory belongs to group `shipyard`, which the API's user shares to read the worker's log socket. A compromised API process could rewrite Caddy's configuration, so invariant 1 and invariant 12 held only in code. This was already in the WORKLOG's open risks. | **Fixed in P5.8b** (the owner's choice): the admin directory belongs to `shipyard-caddy`, which only the worker is in. Caddy keeps the worker's group as a supplementary group to reach the API socket. `TestEdgeAdminGroup` checks the modes and groups on a real container (ADR-0003 note) |
| F3 | Medium | **The GitHub App private key was readable by the API.** `LoadKey` accepted mode 0640, and the documented setup (root:shipyard) let the API's user read it. | **Fixed:** owner-only keys are required (no group bits), as for HPKE private keys (ADR-0012). Docs say `chown shipyard-worker`, mode 0600. Negative test added |
| F4 | Low | **`install.sh` put the new database password on `sed`'s command line,** which any local user can read in `/proc/*/cmdline` while it runs. | **Fixed:** the values reach `awk` through its environment, which only root can read. The SQL already went through stdin |
| F5 | Low | **`install.sh` wrote its build-check log, as root, to a fixed path in `/tmp`.** A planted symlink there could redirect the write. | **Fixed:** the log path comes from `mktemp` and is removed after a passing check |
| F6 | Low | **The apt signing keys** for Docker and PGDG are fetched over HTTPS without a pinned fingerprint. Neither vendor publishes a fingerprint on its install page, so there is nothing independent to pin `[DK-INSTALL][PG-APT]`. | **Accepted:** the same trust as the vendors' own instructions |
| F7 | Info | **Invariant 1 had no test of its own.** | **Fixed:** `TestAPIDependencies` fails if `shipyard-api` ever links the Docker client, `os/exec`, or a worker-side package |
| F8 | Info | **`govulncheck` did not run anywhere.** | **Fixed:** `make vuln`, and a CI step |

## Invariants

| # | Invariant | Enforced by | Proven by | Verdict |
| --- | --- | --- | --- | --- |
| 1 | The API never touches Docker, Caddy, or repository code | `shipyard-api`'s import graph has no `github.com/moby/*`, no `os/exec`, and no `runtime`, `build`, `source`, `routing`, `reconcile`, `github`, `backup`, `health` or `monitor` package | `TestAPIDependencies` (new) | **Holds** in the binary, and in permissions since P5.8b (F2) |
| 2 | PostgreSQL is the source of truth; Caddy's config is a function of `routes` | `routing.Render` from the routes table. The reconciler restores containers and resyncs Caddy every pass | Routing render tests; `TestPhase1ExitCriteria`; `TestRestoreDrill` | Holds |
| 3 | Persist the phase before each side effect; idempotent side effects | Operation phases in `app.Deployer`; deterministic names and `io.shipyard.*` labels | The crash-safety suite (`TestCrashSafety`, P3.7), killing the worker at every fault point | Holds |
| 4 | At most one running operation per app; no long-held advisory locks | Partial unique index `operations_one_running_per_app`; leases | Queue integration tests | Holds. The only advisory lock is `pg_advisory_xact_lock` around migrations, held for one transaction |
| 5 | Only a healthy candidate receives traffic | Health gate, then `ActivateDeployment`; a failure never changes the route | `TestPhase1ExitCriteria` (failed build, failed health, failed switch) | Holds |
| 6 | Rollback uses the retained image ID and environment revision | `app.Rollback` creates from `image_id` and `env_revision_id`; no build step | Rollback tests (P3.3) | Holds |
| 7 | A deployed SHA is an ancestor of the tracked branch | `git merge-base --is-ancestor` in `internal/source` | Source integration tests; off-branch case in the e2e test | Holds |
| 8 | Secrets are envelope-encrypted, and never in logs, API responses, errors, build args or build environment | AES-256-GCM DEKs under a KEK outside PostgreSQL (HPKE by default since P5.4b, so the API cannot decrypt). `cleanEnv` gives the docker CLI no `SHIPYARD_*` variables; builds get no app environment. Errors name keys, never values; logs carry token prefixes only; app logs pass a redactor (ADR-0008) | Secrets and redactor tests; `TestCleanEnvDropsShipyardVariables`; env API returns keys only | Holds |
| 9 | Webhooks: raw body ≤ 25 MB, HMAC verified in constant time before parsing, deduplicated on `X-GitHub-Delivery`, answered within 10 s | `http.MaxBytesReader` with `webhook.MaxPayload`, `hmac.Equal` before any parse, a delivery table, read and sink deadlines | Webhook tests (P4.1–P4.3) | Holds |
| 10 | App containers: no privileged mode, host network, Docker socket, host mounts or published ports; `cap-drop ALL`, `no-new-privileges`, CPU, memory and pids limits, `local` logs, a per-app network | `runtime.Create` sets exactly these and no mounts or port bindings | The e2e hardening assertion on the running container | Holds |
| 11 | Only Caddy publishes host ports | App containers publish none. The builder publishes none. The metrics listeners (P5.5) are loopback-only, refused otherwise | e2e `docker port` check; config tests | Holds |
| 12 | Caddy's admin API only on a permissioned Unix socket | `deploy/caddy/caddy.json` listens on `unix//run/caddy-admin/admin.sock\|0220`; no TCP admin | Edge tests (P2.1, P2.3); `TestEdgeAdminGroup` (P5.8b) | Holds. The socket's group was too wide (F2), fixed in P5.8b |

## Phase 5 surfaces

- **Rate limits and token rotation (ADR-0011):**
  - The client key is the last `X-Forwarded-For` entry, which Caddy sets.
  - A local process that talks to the API's socket directly can choose its own key, but it is already on the host.
  - Self-rotation renews a token's lifetime. That is the documented trade-off.
- **HPKE KEKs (ADR-0012):** private keys must be owner-only. The API loads only the active public key.
- **Metrics (ADR-0013, ADR-0014):**
  - Loopback only, no authentication.
  - They expose app slugs, hostnames and filesystem paths, never secrets, tokens, client addresses or request paths.
  - The certificate check skips chain verification on purpose, since it only reads dates, and sends nothing after the handshake.
- **Build firewall (ADR-0009):** builds cannot reach host services or link-local addresses. Internet access stays open, by decision.
- **Installer (P5.7b):**
  - Files are created with their final modes through `install -m`.
  - Temporary files come from `mktemp`.
  - No secret goes through a command line (F4).

## Accepted risks (unchanged)

- **The worker's `docker` group is root-equivalent** `[DK-POSTINSTALL]` (ADR-0001, ADR-0007).
- **Builds have outbound network access** (ADR-0009). Repositories are trusted (ADR-0007).
- **The BuildKit container is privileged,** though BuildKit itself is rootless (ADR-0010).
- **Images that start as root and drop privileges** may not run under `cap-drop ALL`. There is no per-app capability allow-list.
- **A malformed JSON body's error echoes the parser's message.** For `env set`, that can include one character of the value. The body never reaches a log.

## Dependencies

- **Direct requirements:**
  - `github.com/jackc/pgx/v5` v5.11.0 (MIT);
  - `github.com/moby/moby/client` v0.6.0 and `github.com/moby/moby/api` v1.56.0 (Apache-2.0);
  - `github.com/containerd/errdefs` v1.0.0 (Apache-2.0).
  - Everything else is indirect, through these.
- **Linked into the binaries** (`go version -m`): `shipyard` links none; `shipyard-api` links 6 modules; `shipyard-worker` links 24.
  - Every license file is Apache-2.0, MIT, or BSD-3-Clause, all compatible with Shipyard's Apache-2.0.
  - None ships a NOTICE file, so the project's NOTICE needs no additions.
- **`go mod verify`:** all modules verified.
- **Newer versions available** (2026-10-05), with no known vulnerability in the versions used:
  - `moby/moby/client` v0.6.1 and `moby/moby/api` v1.56.1 (patch releases);
  - `golang.org/x/sys` v0.48.0;
  - OpenTelemetry v1.47.0 (indirect, pulled in by the Docker client).
  - They are left for a routine update with the Docker test suite, not a security fix.
- **Toolchain:** Go 1.26.8. `govulncheck` checks the standard library too, and reports nothing.

## How to repeat

```bash
make vuln
```

```bash
go mod verify
```

```bash
go test ./cmd/shipyard-api -run TestAPIDependencies
```
