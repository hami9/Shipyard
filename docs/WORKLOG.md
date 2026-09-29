# Work Log

A chronological record of work on Shipyard, **newest entry first**. Every working session adds one entry. The log is the hand-off between sessions and between people and agents. Someone new should be able to resume from the `Current status` block plus the newest entry alone.

## Current status

> Update this block at the end of every session.

| Field | Value |
| --- | --- |
| **Active phase** | Phase 2: Safe releases, HTTPS, and traffic switching. Phase 1 is done, pending the owner's merge. Stacked PRs, merge in order: #1 `schema-v1` (P1.1–P1.3) → `main`; #2 `env-secrets` (P1.4); #3 `op-queue` (P1.5); #4 `app-api` (P1.6); #5 `cli` (P1.7); #6 `source-fetch` (P1.8); #7 `image-build` (P1.9); #8 `container-runtime` (P1.10); #9 `deploy-worker` (P1.11); #10 `caddy-edge` (P2.1); #11 `route-render` (P2.2); #12 `caddy-admin` (P2.3); #13 `traffic-switch` (P2.4); #14 `domain-api` (P2.5); #15 `drain-window` (P2.6); #16 `event-stream` (P2.7a); #17 `app-logs` (P2.7b); `api-edge` (P2.8). Merging the stack is the owner's step: an agent-run merge was blocked by the permission classifier on 2026-09-28 |
| **Last completed** | P2.8: the API is published through Caddy (`SHIPYARD_API_HOSTNAME`, HTTPS with HTTP/2) from its Unix socket; the public-listen override is removed |
| **Next task** | Phase 2 exit criteria. One criterion needs the owner: it asks `logs --follow` to resume without loss, but ADR-0008 makes logs non-resumable (only `events` resumes). Then an e2e that probes continuously through a health-failing deploy, and a fault-injected route-verification failure through Caddy. After that, P3.1 |
| **Blockers** | None |
| **Open risks** | Builder egress is unrestricted until Phase 5. On Docker Desktop (macOS/Windows), Phase 1+ health probes cannot reach container IPs `[DK-DESKTOP-NET]`. Images that start as root and drop privileges (e.g. stock nginx) may need allowlisted capabilities, which have no per-app setting yet. The API and the worker share the `shipyard` group, so the API user can also open the Caddy admin socket (mode 0660, worker group); invariant 1 holds only in code there. Deleting an app leaves its containers running: the janitor cannot tell them from another database's without an installation label |
| **Last updated** | 2026-09-28 |

## Entry template

Copy this block to the top of the entries section.

```markdown
### YYYY-MM-DD: <short title>

- **Phase / task:** P<n>.<m>: <roadmap item>
- **Author:** <human name or agent + session link>
- **Goal:** one sentence.

**Done**
- …

**Changed files**
- `path`: why

**Decisions**
- … (link ADR if one was written; "none" is valid)

**Verification**
- `command`: result (paste the real summary: pass/fail counts, errors)

**Problems / surprises**
- … (include source tags if vendor behavior differed from the docs)

**Next**
- The exact next step and its command, so the next session can start without guessing.
```

**Rules**

- Record facts, not intentions. If something was not run, say "not run".
- Never paste secrets, tokens, full env files, or customer data.
- Link commits by short hash once pushed.
- Keep entries short, around 10–25 lines. Move long analysis to an ADR or `docs/`.

## Entries

### 2026-09-29: Phase 2 exit checks and review fixes

- **Phase / task:** Phase 2 exit criteria (PR #19), plus fixes from the CodeRabbit review of the whole stack.
- **Author:** Claude Code (desktop session)

**Done**
- **e2e exit checks:**
  - A client probes the app through Caddy every 50 ms during the health-failing deploy. Every answer was 2xx from the running release.
  - A fault-injected route-verification failure (`PROBE_FAIL_BY_NAME`: `/healthz` fails by hostname only) makes the deploy fail with "switch traffic", and Caddy goes back to the old release. 40 of 207 probes, about 2 s, were answered by the candidate before the restore. That is the known "briefly on candidate" window, and it lasts as long as the verification retries.
- **Review fixes** (each bug fix started with a failing test):
  - `RequeueExpired`: when an operation fails on its last attempt, its in-progress deployment now fails in the same statement. Before, the deployment stayed `building` and so on forever, the janitor kept its container, and the app stayed busy.
  - `queue.Hold`: each heartbeat has a deadline at the lease's expiry. A query stuck on a dead connection could keep the work running past the lease, with two owners (invariant 3).
  - `parseTTL`: the day count is bounded to 1–366 before multiplying. `213505d` overflowed and wrapped to about a day.
  - Smaller fixes: an ignored error in the applogs test, the last `Encode` in the applogs server, a context-bound request in `TestServeEndsStreams`, the `/load` label in the state diagram, and the duplicate `CADDY-OPTIONS` and `SYSTEMD-EXEC` tags in SOURCES (merged).
- **Not changed:** `DeleteIdleApp` still allows deleting an app whose superseded container is in its observation window. The store does not know the worker's window, and deleting an app's containers belongs to P3.8 (delete app as an operation). It is recorded in the open risks.

**Verification**
- The three new tests failed before the fixes and pass after: `TestParseTTL` (`213505d`), `TestHoldGivesUpWhenHeartbeatHangs` (a synctest deadlock before), and `TestRequeueExpired` (the deployment stayed `building`).
- `make lint`, `make test`, `make test-integration`: ok, except for a pre-existing flake in `TestLeaseHandover`. It has a 600 ms lease and fails under load: the unmodified `queue.go` failed on a 20-run batch too, and 20 runs with the fix passed. `-race -count=10` on `queue`: ok.
- Exit checks: `make test-docker` ok, `make test-e2e` PASS (207 s).

**Next**
- Owner: confirm that the logs-resume exit criterion means `events`, and merge #1–#19 in order.

### 2026-09-28: P2.8 API published through Caddy

- **Phase / task:** P2.8: the API listens on localhost or a Unix socket only, and is published through Caddy over HTTPS (HTTP/2)
- **Author:** Claude Code (desktop session)
- **Goal:** Operators and CI reach the API at `https://<API hostname>`. The API itself never listens publicly.

**Done**
- **Config:**
  - `Listen` moved to `Common`, since the worker reads it too. It allows loopback or a clean absolute Unix socket path only.
  - `SHIPYARD_API_ALLOW_PUBLIC_LISTEN` is **removed** (the owner's choice).
  - New `SHIPYARD_API_HOSTNAME`, normalized and checked like an app hostname. On the worker with Caddy enabled, it requires a Unix socket in a directory of its own (not `/`, not the admin directory).
- **Edge:** `EdgeSpec.APISocketDir` is bind-mounted **read-only** at the same path. `EnsureEdge` reports a missing directory clearly. The field is `omitempty`, so existing edges keep their spec hash.
- **Worker:** `ensureEdge` mounts the API socket's directory, and `syncRoutes` renders the API route (`unix//<socket>`, only `/v1/*` and `/hooks/github`; the renderer is from P2.2).
- **API:** adding the API hostname as an app domain is a 409 ("reserved for the Shipyard API").
- **systemd and env:**
  - `SHIPYARD_API_LISTEN=unix:/run/shipyard-api/api.sock` moved from the API unit into `shipyard.env`, which both services read. Before, the worker would have seen `127.0.0.1:8080`.
  - The API unit gets `RuntimeDirectoryPreserve=yes`, and the worker starts `After=shipyard-api.service`.

**Decisions** (ADR-0003 dated note)
- **Socket only for publishing:** Caddy runs in a container and cannot reach the host's loopback.
- **A read-only mount is enough:** Linux refuses writes on a read-only mount only for regular files, directories, and symlinks, so connecting to a socket works. e2e confirms it.
- **`RuntimeDirectoryPreserve=yes`:** a recreated `/run/shipyard-api` would leave Caddy's bind mount pointing at the deleted directory `[SYSTEMD-EXEC]`.

**Problems / surprises**
- The shipped env example set `SHIPYARD_API_LISTEN=127.0.0.1:8080`, and only the API unit set the socket, which the worker could not see. It is fixed by the move above.
- **An exit criterion conflicts with ADR-0008:** "`logs --follow` resumes after a dropped connection without losing events" cannot hold for container logs, which have no ids. Only `events` resumes. The owner should confirm the criterion means `events`, or reopen ADR-0008.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `config_test.go`), `make test`, `make test-integration`: all ok.
  - `TestLoadAPIListenValidation`: 4 accepted, 9 refused, and the removed override no longer opens `0.0.0.0`.
  - `TestAPIHostname`: normalization, 7 refusals, and Caddy off.
  - `TestEdgeSpecValidate` (+4), `TestEdgeCreateOptions` (the read-only mount), `TestEdgeSpecHash` (a new field; the old JSON is unchanged).
  - `TestDomainsPolicy`: a 409 for the API hostname in two spellings.
- `make test-docker`: all ok.
- **`make test-e2e`: PASS (210 s).**
  - The API listens only on a Unix socket, and the CLI uses `unix://`.
  - Through Caddy at `https://api.e2e.example`: `/v1/whoami` is 200 over **HTTP/2** (`ProtoMajor` 2); `/readyz` is 404; the events SSE streams to `event: end`.
  - `domain add api.e2e.example` is refused as reserved.
- No leftovers. Secret scan: see the PR.

**Next**
- Phase 2 exit criteria (see Current status), after the owner answers on the logs-resume criterion. Then P3.1.

### 2026-09-28: P2.7b app logs through the worker socket

- **Phase / task:** P2.7b: `logs --follow` with a bounded tail and best-effort secret redaction
- **Author:** Claude Code (desktop session)
- **Goal:** Operators read the running release's output through the API, while the API still never touches Docker.

**Done**
- **ADR-0008** (the owner's choice): the worker serves `GET /logs` on `SHIPYARD_WORKER_SOCKET` (default `/run/shipyard-worker/logs.sock`, mode 0660, shared group), and the API proxies it as SSE.
- **`runtime.StreamLogs`:**
  - reads Docker logs with timestamps, optional follow, and a tail;
  - splits frames into lines per stream, with CRLF trimmed and lines cut at about 16 KiB;
  - stops when the consumer fails.
- **`internal/applogs`:**
  - `Server`: only the active deployment's container, named by the database; secrets are redacted, and nothing is streamed if they cannot be read; NDJSON with a final `end` line.
  - `Client`: 404 becomes `ErrNoDeployment`, an unreachable socket becomes `ErrUnavailable`, and a stream without its end line becomes `ErrUnexpectedEOF`.
- **`app.Redactor`:** every secret value of 6 or more characters, longest first, exact matches only. `secrets.Env.SecretValues` returns only the secret values.
- **API** `GET /v1/apps/{app}/logs?tail=&follow=` (`read` scope): SSE with keepalives and `event: end` with the reason. It answers 404, 503, 502, or 422 as appropriate.
- **Client and CLI:** the SSE reader is shared by events and logs. `shipyard logs APP [--tail N] [--follow|-f]` writes stdout and stderr lines to the matching output, like `docker logs`.
- **Wiring:**
  - the worker service gets `RuntimeDirectory=shipyard-worker` (0750);
  - `make run-api` and `make run-worker` use `.dev/logs.sock`;
  - the env example and the probe (`/say`) are updated.

**Decisions**
- **Redaction lives in the worker,** where secrets are already decrypted for deploys. Only secret values of at least 6 characters are redacted, because shorter ones would mangle normal output.
- **Logs are not resumable** (no SSE `id`). A reconnect starts a fresh tail.
- **Bug fixed while here (invariant 8):** a failed deploy copied the candidate's last output lines into the operation's events **unredacted**. A failing test came first (`TestDeployUnhealthy` saw `token s3cret rejected`). Those lines now go through the same redactor, and are withheld if the secrets cannot be read.
- **Found, not fixed (open risk, and a separate task was offered):** the API user shares the `shipyard` group with the worker, so it can open the Caddy admin socket.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `config.go`), `make test`, `make test-integration`: all ok. `-race -count=5` on `applogs`, `client`, `app`, and `runtime`; `-race -count=3` on `TestAppLogs` and `TestOperationEvents`: ok.
  - Unit tests:
    - `TestLineWriter`: split frames, CRLF, truncation, a line without a timestamp, a double space;
    - `TestLineWriterStops`, `TestRedactor` (6 cases);
    - `TestLogsOverSocket`: over a real Unix socket; redacted; tail and follow; no deployment; bad ids; a tail over the maximum; unreadable secrets stream nothing; no socket;
    - `TestStreamCutShort`, `TestParseTail`, the client's `TestLogs`, `TestWorkerSocket`.
  - Integration: `TestAppLogs` (the SSE body exactly, keepalive while following, a worker cut, and 7 negative cases) and `TestEnvRevisions` with `SecretValues`.
- **`make test-docker`: all ok.** `TestStreamLogs` shows the Engine 29.8.1 facts in `[DK-LOGS]`: an RFC3339Nano timestamp and one space before each line, stdout and stderr apart, `tail=1`, a live line under follow, follow ending when the container stops, and a consumer error.
- **`make test-e2e`: PASS (151 s).** `shipyard logs` through the real API and worker socket shows `greeting is [REDACTED]` (the secret `GREETING`) and the probe's stderr startup line.
- No leftovers. Secret scan: see the PR.

**Next**
- P2.8: the API listens on localhost or a Unix socket only, and is published through Caddy over HTTPS (HTTP/2).

### 2026-09-28: P2.7a operation event stream

- **Phase / task:** P2.7a: operation events as SSE (P2.7 split in two; P2.7b is logs)
- **Author:** Claude Code (desktop session)
- **Goal:** Operators and CI watch a deploy live, and a dropped connection loses nothing.

**Done**
- **API** `GET /v1/operations/{id}/events` (`read` scope):
  - `id` = `seq` with JSON data; `retry: 2000`; a keepalive comment every 15 s.
  - `Last-Event-ID` resume, and 422 if it is malformed.
  - `event: end` with the operation once it has finished and two polls came back empty.
- **Serve:** a `stopping` channel, closed by `RegisterOnShutdown` and passed in through `BaseContext`, ends open streams at shutdown. Ordinary requests still finish.
- **Client:** `FollowEvents` parses SSE and reconnects with `Last-Event-ID`. A 45 s idle timeout drops a dead connection. It gives up after 5 failed reconnects in a row; any event or keepalive resets that count.
- **CLI:** `events ID`, and `deploy --follow` (exit 1 unless the operation succeeded).

**Decisions**
- **Split P2.7** (CLAUDE.md §2: each half is about 400 lines). For logs, the owner chose **a worker log socket that the API proxies** over copying logs into PostgreSQL. It changes a boundary, so P2.7b starts with an ADR.
- **Poll, not LISTEN/NOTIFY:** one indexed query per stream every 500 ms, which is simple and negligible for a handful of operators.
- **End after two empty polls:** `activate` appends "is active" and the drain plan after its commit, so ending at the first finished poll would drop them. The e2e run shows both arrive.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `api_test.go`), `make test`, `make test-integration`: all ok. `-race -count=5` on `api` and `client`; `-race -count=3` on the stream and CLI integration tests: ok.
  - `TestOperationEventsStream`: headers, the retry line, multi-line messages, a keepalive, a live event, the end on cancellation, two resumes, and 5 negative cases (422 ×2, 404 ×2, 401).
  - `TestServeEndsStreams`: shutdown returns at once while a stream is open, and an in-flight request still completes.
  - `TestFollowEventsResumes`: data split across lines, a cut mid-event, and a resume at `Last-Event-ID: 2`. `TestFollowEventsErrors`: 404 is final, and a server that keeps failing is given up after 6 connections.
  - `TestCLIEndToEnd`: `events` prints aligned multi-line events and fails with "cancelled: superseded by".
- `make test-docker`: all ok.
- **`make test-e2e`: PASS (122 s).** Deploy 5 runs with `deploy --follow`. Its output has the health check, the Caddy verification, "is active", the drain plan, and "succeeded".
- No leftovers. Secret scan: see the PR.

**Next**
- Write the ADR for the worker log socket (path, mode, who connects, what it serves). Then P2.7b: a bounded tail, `--follow`, and best-effort redaction of the app's secret values.

### 2026-09-28: P2.6 observation window and drain

- **Phase / task:** P2.6: observation window, then graceful stop of the previous container with the per-app `stop_timeout`
- **Author:** Claude Code (desktop session)
- **Goal:** Keep the previous release running after a switch, then stop it gracefully, driven by the database so a worker restart loses nothing.

**Done**
- **Deploy:** activation no longer stops the previous container. It logs the window and the app's stop timeout to the operation's events.
- **`app.Janitor`** (Reconciler step 2, run after the Caddy sync), with the database deciding:
  - a `superseded` deployment past `ended_at` + window: `docker stop` with the app's `stop_timeout`, then removal;
  - `failed`/`cancelled`: removed;
  - active, in-progress, or within the window: kept.
  - It carries on past a failing container. A database error removes nothing.
- **`runtime.ListManaged`:** app containers by the `io.shipyard.managed` label, running or not. It skips containers with an invalid slug or deployment label, and the edge.
- **Config:** `SHIPYARD_OBSERVATION_WINDOW` (default 5m, 0s–24h, 0 allowed).

**Decisions**
- **A container whose deployment is not in this database is never removed.** My first draft removed it as an orphan. The e2e case showed that an e2e or test worker would then delete the owner's development containers on the same engine. Cost: a deleted app's containers stay (app delete cascades its deployments). This is recorded as an open risk, and the fix is an installation label.
- **The drain lives in the reconciler, not the deploy.** A 5-minute wait inside the operation would hold the app's operation slot and the lease. No schema change: `ended_at` is the window's start.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `config.go` and `janitor_test.go`), `make test`, `make test-integration`: all ok. `-race -count=5` on `app` and `config`: ok.
  - `TestJanitorSweep` (9 containers: drain at and past the window, kept within it, failed removed, foreign kept, exited not stopped again), `TestJanitorCarriesOn`, `TestLoadWorkerObservationWindow` (5 valid, 3 invalid), and the deploy tests updated (no stop/remove at activation; the event is logged).
- `make test-docker`: all ok, including the new `TestListManaged` (a running and a created container listed, a forged label skipped).
- **`make test-e2e`: PASS (134 s).** With a 6 s window, the first container is still running right after deploy 5 succeeds, then removed by the reconciler within 30 s. Both hostnames serve the second container.
- No leftovers. Secret scan: clean (see the PR).

**Next**
- The owner merges #1–#15 in order.
- P2.7: SSE for operation events (`id`, `Last-Event-ID`) and logs.

### 2026-09-28: P2.5 domain API

- **Phase / task:** P2.5: domain API
- **Author:** Claude Code (desktop session)
- **Goal:** Operators add and remove hostnames through the API and CLI, safely (DNS preflight, allow-list, uniqueness), and Caddy follows without the API touching it (invariant 1).

**Done**
- **`internal/app`:**
  - `NormalizeHostname`: lowercase, trailing dot removed, exact FQDN; no wildcards, IPs, or single labels.
  - `SuffixAllowed`: matches at a label boundary.
  - `PointsHere`: every record must be ours, at least one record, v4-mapped addresses unmapped.
  - `ContainerName`/`Upstream`: the deterministic convention. A worker test keeps it equal to `runtime.ContainerName`.
- **API `/v1/apps/{app}/domains`** (list: read; add and remove: admin):
  - normalize → allow-list (422) → preflight (NXDOMAIN or no records 422, a foreign record 422 naming it, lookup failure 503, preflight on without IPs 503) → `CreateRoute`;
  - a duplicate is 409 "already used by an app".
- **Store:**
  - `CreateRoute`, under the app lock: it targets the active deployment, using the upstream its other routes use, or else the convention.
  - `RoutesByApp`, `DeleteRoute`.
  - `ActivateDeployment` also moves routes on the superseded deployment, or on none.
- **Config:** `SHIPYARD_PUBLIC_IPS` (public only), `SHIPYARD_DOMAIN_SUFFIXES`, `SHIPYARD_DNS_PREFLIGHT`, and on the worker `SHIPYARD_RECONCILE_INTERVAL`.
- **Worker:** the reconciler now also runs `Router.Sync` every interval. A deploy calls `Release` after commit or failure, so followers load right away.
- **Client/CLI:** `domain add|remove|list`. The Makefile's `run-api` turns the preflight off.

**Decisions** (dated note in ADR-0003; ARCHITECTURE §6/§7)
- **Several hostnames per app**, where ARCHITECTURE v2 sketched `PUT …/domain` (one). The schema allows it, and one app per hostname still holds.
- **A hostname added to a serving app targets the running deployment at once**, by convention. The alternative, a new operation kind with its own verification, needs a migration for little gain in the MVP.
- **Route changes reach Caddy through the periodic reconcile** (default 60 s) plus a sync after each activation. No LISTEN/NOTIFY yet.

**Problems / surprises**
- **I found a race in my own P2.4 code while adding the periodic sync.**
  - A `Sync` during a switch's verification would revert Caddy to the old container, and the check would then pass against it (invariant 5).
  - Fix: `routing.Router` keeps pending switches in every render until the deploy's `Release`.
  - `TestRouterSyncKeepsPendingSwitch` covers it: the failed switch stays through `Sync` until `Release`.
  - The app port changed from `Restore` to `Release(app)`.
- `TestActivateMovesVerifiedRoutes` from P2.4 expected an unverified route with no deployment to keep its target. With followers it now follows the app; the test was updated on purpose.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `config.go`), `make test`, `make test-integration`: all ok. `-race -count=5` on `app` and `routing`.
  - Unit: hostname normalization (5 valid, 13 invalid), suffixes, `PointsHere` (7 cases), config (6 negative cases), the router regression test, and the container names agreeing.
  - Integration:
    - `TestDomainsAPI`: normalization, 403 for read tokens, 409, five 422 cases including a stray AAAA, 503 when DNS is down, list, delete, and a second delete (404).
    - `TestDomainsPolicy`: allow-list; preflight on without IPs is 503; preflight off does no lookup.
    - `TestDomainOnActiveDeployment`: the upstream is `shipyard-web-<dep>:3000`.
    - `TestCreateRoute`, the updated `TestActivateMovesVerifiedRoutes`, and `TestCLIEndToEnd` with the domain commands.
- `make test-docker` (all packages): all ok.
- **`make test-e2e`: PASS (106 s):**
  - the first hostname is added with `domain add` before the deploy;
  - **a second is added while the app serves** ("serving deployment"), and **Caddy serves it from the first container** after the reconcile;
  - after deploy 5, **both hostnames serve the second container**;
  - `domain remove` → Caddy stops routing it.
- No leftovers. Secret scan: see the PR.

**Next**
- The owner merges #1–#14 in order.
- P2.6: the observation window, then a graceful stop of the previous container.

### 2026-09-28: P2.4 traffic switching

- **Phase / task:** P2.4: the `switching` phase
- **Author:** Claude Code (desktop session)
- **Goal:** A healthy candidate takes over the app's hostnames only after Caddy verifiably routes to it, and any failure restores the previous routes (invariant 5, ARCHITECTURE §5 step 7).

**Done**
- **The renderer's `Settings.VerifySocket`** adds a `verify` server: the same routes, plain HTTP, on `caddy-verify.sock|0660`, with `automatic_https.skip` for all hosts. That shape comes from `caddy adapt`. There are two new golden files.
- **`routing.Router`:**
  - `Switch(appID, upstream, healthPath)` renders the table with that app's routes on the candidate, applies it, then requests `health_path` for each hostname through the verify socket: 5 attempts 0.5 s apart, 2xx/3xx, no redirects or proxy. It returns the verified hostnames; with none, it loads nothing.
  - `Restore` re-renders the table as committed. The worker's startup sync now uses it.
- **Store:**
  - `MarkSwitching`, and `switching_at` read back.
  - `ActivateDeployment(…, upstream, hostnames)` moves **only the verified hostnames**, in the activation transaction. A verified route that vanished returns `ErrConflict` and rolls everything back.
- **`internal/app` (`switchTraffic`):**
  - The `switch` phase runs after the health gate. The upstream is `<container name>:<port>`, since `runtime.State` now carries the name.
  - **Restore comes before removing the candidate** on failure. A lost lease or shutdown also restores, but records nothing.
  - With no routes, it logs "no routes yet".
- **Worker:** one `Router` for both the startup sync and deploys. `make test-docker` now runs `-p 1` (see below).

**Decisions** (dated note in ADR-0003; ARCHITECTURE §5)
- **Verify on a private plain-HTTP Caddy listener, not over public HTTPS.** Routing is proven through Caddy by `Host` header, with no dependence on ACME timing for a first deploy.
- Commit only verified hostnames.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4)
- `make lint`, `make test`, `make test-integration`: all ok. `-race -count=5` on `app` and `routing`: ok.
  - Unit: 5 new use-case tests (switch, switch failure restores before remove, activation failure restores, lost lease after the switch restores but records nothing, no routes), 4 Router tests against a fake Caddy whose verify server answers from the loaded config, and 2 new rejection cases.
  - Integration: `TestActivateMovesVerifiedRoutes` (only verified and own routes move; a vanished route aborts the commit, leaving the deployment not active and the operation running).
- **`make test-docker` (all packages, `-p 1`): all ok.** `TestRouterAgainstCaddy` with the real Caddy and v1/v2 upstreams on an app network:
  - the verify socket is 0660;
  - v1, then after `Switch` **v2 through both the verify socket and public HTTPS**, then v1 again after `Restore`;
  - an unreachable candidate → `status 502` after 3.5 s, and v1 again after `Restore`.
- **`make test-e2e`: PASS (105 s).** A route row is added before the first deploy, and **HTTPS through the worker's Caddy** reads the serving container's `/etc/hostname`:
  - the first container after deploy 1;
  - still the first after the broken, off-branch, and unhealthy deploys;
  - the second after deploy 5;
  - the injected secret env through Caddy.
- No leftovers.

**Problems / surprises**
- **Parallel docker test packages interfered.** Each package's edge joins every app network on the host, as the one production edge must. With `routing` and `runtime` run together, a network got a foreign edge endpoint, so a network removal and an edge `docker restart` failed and one test network was left behind (removed afterwards).
  - Alone, the edge tests pass twice in a row.
  - Fix: `make test-docker` uses `-p 1`. Nothing changes in production.

**Next**
- The owner merges #1–#13 in order (retarget each to `main`, merge commits).
- P2.5: the domain API.

### 2026-09-28: P2.3 Caddy admin client

- **Phase / task:** P2.3: admin socket client
- **Author:** Claude Code (desktop session)
- **Goal:** Replace Caddy's config safely over the admin socket: read with an `Etag`, write conditionally, and handle 412 and rejections (ADR-0003, invariants 5 and 12).

**Done**
- **`routing.Admin`** (Unix socket only, no proxy, bounded responses, a 1-minute default deadline):
  - `Config` returns the body and `Etag`, and requires the `Etag`.
  - `Load(cfg, etag)` is a whole replace via **`POST /config/`** with `If-Match`. A 412 is `ErrConflict`; any other non-200 is a `LoadError{Status, Message}`, with Caddy's `{"error"}` extracted and bounded.
  - `Apply(cfg)` reads, compares semantically (Caddy re-encodes its stored config), and reloads only if different. After a 412 it re-reads and retries, up to 3 attempts.
- **Worker:** after `EnsureEdge`, `syncRoutes` runs `ListRoutes` → `Render` → `Apply` (reconciler step 3, at start). New config: `SHIPYARD_CADDY_CA` (``, `staging`, `internal`) and `SHIPYARD_ACME_EMAIL`. `make run-worker` and the e2e test use `internal`.

**Problems / surprises → decision**
- **`POST /load` ignores `If-Match`**, contradicting ADR-0003, SOURCES, ARCHITECTURE, and CLAUDE.md.
  - Found by the first real-Caddy run: a load with a stale `Etag` succeeded.
  - Confirmed in the source of Caddy v2.11.4: `handleLoad` never reads the header. Only `/config/…` requests (`handleConfig` → `changeConfig`) check it, returning 412.
  - Stopped per CLAUDE.md §9, recorded `CADDY-ADMIN-SRC`, and corrected `CADDY-API`, ADR-0003 (dated note; the decision is unchanged), ARCHITECTURE §3/§5, ROADMAP, and CLAUDE.md §4.
  - Then `Load` moved to `POST /config/`: the same replace, no-op-if-unchanged, and rollback (`changeConfig`), plus the 412.
- A rejected config returns **500** via `/config/` (400 via `/load`).
- The real-Caddy test keeps a check that `/load` still ignores `If-Match`, so a Caddy upgrade that changes this is noticed.

**Verification** (WSL2, Caddy 2.11.4)
- `make lint`, `make test`, `make test-integration`: all ok. 10 unit tests against a fake Caddy on a real Unix socket: Etag, If-Match, no reload when equal, a 412 retry with a fresh Etag, giving up after 3, the rejection message, a stale Etag, a missing socket, and context cancel. `-race -count=5`: ok.
- **`TestAdminAgainstCaddy` (docker)**, against the real P2.1 edge:
  - the Etag has the form `"/config/ …"`;
  - the first `Apply` reloads and the second does not;
  - **`Load` with a stale Etag → `ErrConflict`, and the config is unchanged**;
  - `/load` with a stale `If-Match` → 200 (the vendor fact);
  - a broken config → `LoadError` 500 "unknown module", and **the previous config keeps running**.
- `TestRenderedConfigServes` still passes, and **`make test-e2e` passes (217 s)**: the worker starts with `syncRoutes`.
  - This run was slower than earlier ones (111 s), and the real-Caddy test took 42 s instead of 6 s; nothing failed.
- No leftovers.
- **Merging PRs #1–#11** (requested by the owner) was **blocked by the permission classifier**. No PR was changed; all are still open with their original bases.

**Next**
- The owner merges #1–#12 in order. Each PR's base must be retargeted to `main` before its merge; use merge commits, not squash.
- Then P2.4: the `switching` phase.

### 2026-09-27: P2.2 route renderer

- **Phase / task:** P2.2: `internal/routing` renderer
- **Author:** Claude Code (desktop session)
- **Goal:** Caddy's complete config as a pure function of the `routes` table (invariant 2, ADR-0003).

**Done**
- **`routing.Render(Settings, []Route) ([]byte, error)`:**
  - `admin.listen` on the edge socket (`|0660`), and one server `shipyard` on `:443`.
  - The optional API hostname proxies only `/v1/*` and `/hooks/github`; anything else there is 404.
  - One terminal route per hostname: `reverse_proxy` to its upstream, or `503 no active deployment`.
  - Issuers: default, Let's Encrypt with an email, LE staging, or internal.
  - Output is sorted by hostname, so it is byte-for-byte deterministic, and the caller's slice is never modified.
- **Validation refuses to render anything** for invalid input:
  - hostnames that are not the schema's lowercase FQDN form (no wildcards or placeholders), duplicates, or an app on the API hostname;
  - upstreams other than `host:port` with a real port (no network prefixes, port ranges, placeholders, or sockets for apps);
  - an unclean admin socket path, an unknown CA, or a bad email.
- `store.ListRoutes` (ordered by hostname) and `routing.FromStore`.

**Decisions**
- **The JSON shape comes from Caddy itself** (`caddy adapt` of an equivalent Caddyfile on 2.11.4), because the JSON docs pages render client-side `[CADDY-JSON]`.
- **Apps can never proxy to a Unix socket.** Only the API upstream may be `unix//…`, so a route row cannot point Caddy at, say, the Docker socket.
- **Settings passed in, not new config.** The API hostname, the API upstream, the CA, and the email come in as `Settings`. Wiring them from config comes with the load path (P2.3/P2.4) and P2.8.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4)
- `make lint`, `make test`, `make test-integration`: all ok.
  - Unit: 5 golden files, determinism, admin socket kept, `FromStore`, and 19 rejection cases.
  - Integration: `TestListRoutes`, covering order, and upstream and deployment present or absent.
- **`TestRenderedConfigServes` (docker, 7 s):** the empty config loads into the real P2.1 edge. Then the full config loads, with the internal CA and a `caddy respond` upstream on an app network:
  - `web` → 200 "app ok", `idle` → 503 "no active deployment";
  - `api/v1/whoami` and `api/hooks/github` → proxied, and `api/admin` and `api/v1` → 404;
  - an unrouted hostname fails the TLS handshake;
  - `http://` → 308 to `https://`;
  - the admin socket still answers after the load.
- No leftovers.

**Next**
- P2.3: the admin socket client: `GET /config/` with `Etag`, `POST /load` with `If-Match`, and handling of 412 and other errors.

### 2026-09-27: P2.1 Caddy edge

- **Phase / task:** P2.1: Caddy container bootstrap
- **Author:** Claude Code (desktop session)
- **Goal:** One Caddy container that alone publishes host ports, joins every app network, and exposes its admin API only on a permissioned Unix socket (ADR-0003, invariants 11 and 12).

**Done**
- **`runtime.EnsureEdge(EdgeSpec)`** (idempotent) prepares the admin directory, then ensures the edge network, the `-data` and `-config` volumes, and the container (pulled if missing). It then starts it, joins every app network, and waits until the socket accepts connections.
  - A spec-hash label recreates the container when the spec changes, keeping the volumes.
  - A foreign container or network with the name is refused.
- **The container** (one place, `EdgeSpec.createOptions`):
  - `caddy:2.11.4-alpine@sha256:6aeddd44…`, running `caddy run --resume`, with `CADDY_ADMIN=unix/<dir>/caddy-admin.sock|0660`;
  - user `0:<worker gid>`, `--cap-drop ALL` plus `NET_BIND_SERVICE`, `no-new-privileges`, and a read-only rootfs with a `/tmp` tmpfs;
  - 512 MiB, 1 CPU, 512 pids, the `local` log driver, `unless-stopped`;
  - ports 80/tcp, 443/tcp, and 443/udp;
  - the socket directory is the only bind mount.
- **Admin directory:** the worker makes it group = its own gid with mode 2770 (setgid), or checks that an existing directory is set up that way.
- **App networks:** `Runtime.Edge` makes `EnsureNetwork` (and so `Create`) join the edge to new app networks. `RemoveNetwork` detaches the edge first. `AttachEdge` and `RemoveEdge` complete the set.
- **Worker:** calls `EnsureEdge` at start. Config: `SHIPYARD_CADDY[_NAME|_IMAGE|_ADMIN_DIR|_BIND|_HTTP_PORT|_HTTPS_PORT]`. `make run-worker` publishes only on 127.0.0.1:18081/18443.

**Decisions** (dated note in ADR-0003; ARCHITECTURE §7)
- The socket gets its own directory, `/run/shipyard/caddy/`, so Caddy never sees the API socket in `/run/shipyard`.
- **Root with the worker's gid**, not a non-root user:
  - binding 80/443 then needs only `NET_BIND_SERVICE`;
  - the socket can be created in the group-writable directory without `CAP_DAC_OVERRIDE`;
  - `no-new-privileges` would block the file capability for a non-root user.
- `--resume` plus the `/config` volume keeps serving after restarts. P2.2 must render `admin.listen` on the socket, because a loaded config overrides `CADDY_ADMIN`.

**Verification** (WSL2, Engine 29.8.1)
- `make lint`, `make test`, `make test-integration`: all ok. Unit: `TestEdgeSpecValidate` (11 negative cases), `TestEdgeCreateOptions`, `TestEdgeSpecHash`, `TestLoadWorkerCaddy` (8 negative cases).
- Docker, `internal/runtime` (16 tests, 31 s):
  - **`TestEdgeBootstrap`:**
    - the socket is a socket, mode 0660, with the worker's gid;
    - `GET /config/` over the socket gives `null`;
    - **TCP 2019 on the container IP is refused**;
    - hardening is confirmed by inspect, and the only bind mount is the socket directory.
  - After `POST /load` of a static response, it is served through the published loopback port.
  - A second `EnsureEdge` returns the same ID.
  - **`docker restart` resumes the loaded config**, and a spec change recreates the container with the config kept.
  - `TestEdgeJoinsAppNetworks`: networks both before and after `EnsureEdge` are joined, and `RemoveNetwork` detaches.
  - `TestEdgeRefusesForeignContainer`.
- **Mutation checks:**
  - socket `|0666` → the test fails on the mode;
  - no `NET_BIND_SERVICE` → Caddy never comes up.
- `make test-e2e`: PASS (111 s). The worker now starts its own Caddy, which joins the app network, and the app container publishes no ports.
- No leftovers: only the pre-existing dev PostgreSQL.

**Problems / surprises**
- **The official image's `caddy` binary has `cap_net_bind_service=ep`.** Without that capability, the exec itself fails: `exec /usr/bin/caddy: operation not permitted` (recorded in `CADDY-IMAGE`).
- **Engine 29 reports `CapAdd` as `CAP_NET_BIND_SERVICE`**, while the request says `NET_BIND_SERVICE` (recorded in `MOBY-CLIENT`).

**Next**
- P2.2: `internal/routing`: render the full Caddy JSON from `routes` (with `admin.listen` on the socket, the API, and `/hooks/github`); golden-file tests.

### 2026-09-27: P1.11 deploy worker

- **Phase / task:** P1.11: the deploy use case, the health probe, and the worker loop
- **Author:** Claude Code (desktop session)
- **Goal:** `shipyard deploy` really runs: fetch → build → start → health → active, with each phase persisted before its side effect and a crash resuming where it stopped.

**Done**
- **Store (`deployments.go`):**
  - `CreateDeployment`, `DeploymentByOperation`/`ByID`, `ActiveDeployment`, `RecordImage`, `RecordContainer`, `MarkHealthChecking`, `FailDeployment`.
  - `ActivateDeployment` supersedes the old active, marks this one active, and completes the operation in one transaction.
  - **Every write joins the operation's lease** (`lease_owner`, `running`); zero rows is `ErrLeaseLost`.
- **`internal/app/deploy.go` (`Deployer.Run`):**
  - The operation phases are `fetch`, `build`, `start`, `health`, `activate`.
  - The deployment row is created after the verified fetch and pins the latest env revision.
  - The container ID is persisted before start.
  - The health gate requires the container to stay running without restarts.
  - Activation then drains the previous container.
  - **Resume:** a recorded image skips fetch and build, the commit stays pinned, and the container is re-created idempotently.
  - **Failure:** final. It captures the candidate's last 50 lines, removes the candidate, and fails the deployment and the operation.
  - A lost lease or shutdown records nothing, so the next owner resumes.
- **`internal/health`:** 3 consecutive 2xx/3xx, 1 s interval, 2 s per request, no proxy, no redirects. A dead container stops the gate at once, and a caller's cancel cause (lost lease) is passed through.
- **Worker:**
  - The queue loop with `Hold` (lease heartbeats), plus `RequeueExpired` at start and every lease.
  - Adapters for source, build, and runtime.
  - `Ensure` on the builder at start.
  - New config: `SHIPYARD_WORK_DIR`, `SHIPYARD_SOURCE_BASE_URL`, `SHIPYARD_BUILDER[_MEMORY|_CPUS]`. The KEK is now required.
- `runtime.Logs` (bounded, demultiplexed). The API uses `app.DeployPayload`.
- **`test/e2e` + `make test-e2e`:** real binaries, a local git HTTP server, and the CLI (details under Verification).

**Decisions** (no ADR; within ARCHITECTURE §5)
- **Ports live in `internal/app`; adapters live in `cmd/shipyard-worker`.** `internal/source` imports `internal/app` (a cycle otherwise), and the API must not link the Docker client.
- **No automatic retry of deploy failures.** Only lease loss or shutdown leads to a retry.
- With no route yet, the previous container is drained right away, with no observation window.
- The e2e test uses its own builder name (`SHIPYARD_BUILDER`), so it never touches the owner's `shipyard` builder.

**Verification** (WSL2: Engine 29.8.1, buildx 0.37.1, git 2.43.0, PostgreSQL 18)
- `make lint` (including the `e2e` tag): exit 0. `make test`, `make test-integration`, and `make test-docker`: all ok. `-race -count=5` on `app` and `health`: ok. `go mod verify`: ok.
- Unit tests: 11 use-case scenarios (happy path, pinned ref, fetch, build, unhealthy, 4 container deaths, resume after and before the build, lost lease, resumed failure, bad payload), 7 health cases, and 3 store integration tests (lifecycle, lease guard including requeue, failure).
- **`make test-e2e`: PASS (107 s)**, covering every Phase 1 exit criterion:
  1. A pinned SHA deploys, and an `Idempotency-Key` replay returns the same operation. The container runs the pinned commit, and `docker inspect` shows `["ALL"] ["no-new-privileges"] local 536870912 1000000000 512 false {}`. The secret env value is readable inside.
  2. A broken Dockerfile gives `failed` with `missing-file` in the error.
  3. A SHA off the branch gives `failed`, "commit is not on the tracked branch".
  4. An unhealthy release gives `failed` with `status 500`, and the same container is still the only one running.
  5. A healthy release replaces it, and the old container is removed.
- No leftovers: no containers, networks, `shipyard*` images, or extra builders.

**Problems / surprises**
- **A partial clone downloads off-branch commits** `[GIT-PARTIAL]`.
  - In the e2e test, a feature-branch SHA got past `cat-file -e`: git lazily fetched it from the server. Only `merge-base` refused it, so invariant 7 held, but ARCHITECTURE's promise that nothing else is fetched was false.
  - Confirmed with a standalone script, and in git's docs.
  - Fix: `cat-file` and `merge-base` run with `GIT_NO_LAZY_FETCH=1`.
  - Failing-first: the extended `TestFetchRejectsCommitsOffBranch` fails on the old code ("the feature-branch commit is in the workspace") and passes on the new.
- **The e2e test first failed on a race in the test itself.** The operation succeeds at activation, and draining the old container comes after, so the test now waits for the drain. Failure warnings are now also logged by the worker, not only stored as events.

**Next**
- The owner merges #1–#9 in order. Then P2.1: the Caddy container bootstrap.

### 2026-09-27: P1.10 container runtime

- **Phase / task:** P1.10: `internal/runtime`
- **Author:** Claude Code (desktop session)
- **Goal:** Run a deployment's image as a hardened container on its app's own network, safely repeatable after a crash (invariants 3 and 10).

**Done**
- `EnsureNetwork` / `RemoveNetwork`: the bridge network `shipyard-app-<slug>`, labelled `io.shipyard.{managed,app}`. Both are idempotent, and both refuse a same-named network without those labels.
- `Create(Spec)` returns the ID without starting, so the caller persists it first.
  - It ensures the network, then creates `shipyard-<slug>-<deployment-id>` from the image ID.
  - Labels are `io.shipyard.{managed,app,deployment,commit}`. The env is injected as sorted `KEY=value`.
  - A retry finds the existing container and returns it only if deployment and image match; otherwise `ErrNameTaken`.
- `Start`, `Inspect` (status, restarts, OOM, exit code, IP on the app network), `Stop` (SIGTERM, then SIGKILL after the rounded-up timeout), `Remove` (force, anonymous volumes).
  - All are idempotent, with `ErrNotFound` for a missing container.
  - All refuse unlabelled containers (`ErrNotManaged`).
- The hardened set is built in one function (ARCHITECTURE §7): `CapDrop ALL` plus an allowlist, `no-new-privileges`, `Memory`, `NanoCPUs`, `PidsLimit`, `unless-stopped`, the `local` log driver, one network, and no ports, mounts, or host namespaces. `Spec` has no field for anything forbidden.
- `Spec.Validate` checks everything; its errors name env keys, never values (invariant 8).

**Changed files**
- `internal/runtime/`: the package, unit tests (CI), `docker`-tagged tests, and the `testdata/probe` helper.
- `go.mod`/`go.sum`: new dependency, `github.com/moby/moby/client` v0.6.0, pulling in `moby/moby/api` v1.56.0 and `containerd/errdefs` v1.0.0. It is the approved Docker client (CLAUDE.md §4); shelling out to the CLI, as `build` does, would give no typed inspect data. It also pulls in OpenTelemetry HTTP instrumentation, which exports nothing unless configured.
- ARCHITECTURE §7, SOURCES (`MOBY-CLIENT`), ROADMAP, DEVELOPMENT.

**Decisions** (no ADR; these apply ARCHITECTURE §7)
- **Capability allowlist:** `CHOWN DAC_OVERRIDE FOWNER NET_BIND_SERVICE SETGID SETUID`. **Pids default:** 512. Neither has a per-app column yet; add one when an app needs it.
- **`Create` calls `EnsureNetwork`**, and every lifecycle call checks the labels, so the worker cannot hit a foreign container even when given a wrong ID.

**Verification** (WSL2: Engine 29.8.1, API 1.56, cgroup v2)
- `make lint`: exit 0. `make test` and `make test-integration`: all ok. `go mod verify`: all modules verified.
- `make test-docker`: all ok. `internal/runtime` has 6 Docker tests, 23 s.
  - **Exit criterion (`TestHardenedContainer`)**: the API inspect and `docker inspect` both show `["ALL"] ["no-new-privileges"] local 67108864 500000000 64 false false`. `PortBindings` is empty, with no bindings even though the image `EXPOSE`s 8080. Exactly one network.
  - **From inside the container:** `NoNewPrivs: 1` and `CapPrm/CapEff/CapBnd` all zero; `pids.max=64`, `memory.max=67108864`, `cpu.max=50000 100000`; the injected env is readable, including an empty value.
  - An allowlisted `NET_BIND_SERVICE` gives `CapBnd 0x400` exactly.
  - **Idempotency:** create is idempotent and gives `ErrNameTaken` for another image; a missing image gives `ErrNotFound`. Start, stop, and remove are idempotent, with `ErrNotFound` after removal.
  - **Label guard:** a foreign network or container is refused by every call and survives.
- **Mutation check:** removing `no-new-privileges` and `CapDrop` makes `TestHardenedContainer` fail on 4 assertions.
- No leftovers after the tests: no `io.shipyard` containers, `shipyard-app-*` networks, or `shipyard-test/*` images.

**Problems / surprises**
- Engine 29.8.1 **creates a container on a network that does not exist** and fails only at start. The first test run caught this, so `Create` now ensures the network (recorded in `MOBY-CLIENT`).
- The WSL daemon's default log driver is `json-file`, so the per-container `local` setting is required, not redundant.

**Next**
- P1.11: the deploy use case: fetch → build → `runtime.Create` (persist the container ID) → `Start` → health probe on `State.IP:internal_port`, with phases persisted before each side effect.

### 2026-09-27: P1.9 image build

- **Phase / task:** P1.9: `internal/build`
- **Author:** Claude Code (desktop session)
- **Goal:** Build a verified checkout into a local image on a resource-limited BuildKit builder (ADR-0004).

**Done**
- `Builder.Ensure(Limits{Memory, CPUQuota})`: creates the `docker-container` builder with `--driver-opt memory=…,cpu-quota=…,cpu-period=100000` if missing, then bootstraps it `[DK-BX-CONTAINER]`. `Remove`.
- `Builder.Build(Request)`:
  - `buildx build --builder shipyard --load --provenance=false --sbom=false --progress=plain --metadata-file … --tag shipyard/<slug>:<sha12>`, plus the `io.shipyard.{managed,app,commit,deployment}` labels.
  - A deadline (15 min default); on expiry the CLI gets SIGINT so BuildKit cancels.
  - One build at a time.
  - Result: `ImageID` from `image inspect`, and the metadata file.
- The docker CLI gets a whitelisted environment, so no `SHIPYARD_*` variable reaches a build `[DK-BUILD-SECRETS]`.
- Bounded log capture: at most `MaxLog` bytes (5 MB default) go to the sink, then one truncation notice. The last 20 lines always end up in the failure error.

**Decisions**
- **Attestations are off (`--provenance=false --sbom=false`)**, because `--load` of an attested image fails on the classic image store `[DK-ATTEST]`. The image ID is the identity, and the metadata file (which includes buildx's own provenance) is kept.
- **Docker tests get their own build tag, `docker`, and `make test-docker`.** They run on the owner's machine (CLAUDE.md §6); CI only compiles them through `make lint`, which now vets and staticchecks the `docker` tag too.
- The sink is a callback. The worker (P1.11) connects it to `AppendOperationEvent`.

**Verification** (WSL2: Engine 29.8.1, buildx 0.37.1)
- `make lint`: exit 0. `make test` and `make test-integration`: all ok.
- `make test-docker` (8 tests, 40 s):
  - **the builder container really has Memory=512 MiB, CpuQuota=100000, CpuPeriod=100000**, and `Ensure` is idempotent;
  - a build gives `sha256:` ID, the tag, all four labels on the image ID, a captured log, and `ImageID == containerimage.digest` on the containerd store;
  - a broken Dockerfile (`COPY missing-file`) returns `ErrBuildFailed` naming the missing file, with the log within the 400-byte budget;
  - a 1 ms deadline returns `deadline of 1ms exceeded`.
- After the tests, `docker buildx ls` shows only `default` and there are 0 `shipyard/web` images: no leftovers.

**Problems / surprises**
- **A documented fact did not hold.** With `--load`, buildx 0.37.1 writes `containerimage.digest` and `containerimage.descriptor` but **no `containerimage.config.digest`**, which SOURCES, ARCHITECTURE, and ADR-0004 expected. I recorded the observation in `DK-BX-BUILD`, updated ARCHITECTURE §4, and added a dated note to ADR-0004 (the decision is unchanged, and the whole file is persisted).

**Next**
- P1.10: `internal/runtime` (moby client, per-app network, hardened flags, and the automated `docker inspect` checks from the exit criteria).

### 2026-09-27: P1.8 source fetch

- **Phase / task:** P1.8: `internal/source`
- **Author:** Claude Code (desktop session)
- **Goal:** Put the exact commit into a fresh per-operation workspace, and prove it is on the tracked branch (invariant 7).

**Done**
- `Fetcher.Fetch(Request{OperationID, Repo, Branch, Ref, Token})` returns `Checkout{Dir, SHA, BranchHead}`:
  1. Validate every input first (UUID, owner/name, git ref rules plus no leading `-`, full SHA).
  2. Empty `<Root>/op-<id>` (a retry starts clean); the root is mode 0700.
  3. `git clone --no-checkout --filter=blob:none --single-branch --no-tags --branch B -- URL DIR`.
  4. Resolve the branch head; the target is `Ref` or the head.
  5. `cat-file -e` in the branch history (otherwise `ErrNotOnBranch`, fetching nothing more), then `merge-base --is-ancestor` `[GIT-MERGE-BASE]`.
  6. Detached checkout, and verify that `HEAD` equals the SHA.
- git runs via `exec` (no shell) with a locked-down environment:
  - no host git config, no prompts, `core.hooksPath=/dev/null`, no credential helper;
  - `protocol.allow=never` plus only the base URL's scheme;
  - the token becomes `http.extraHeader: Authorization: Basic x-access-token:…` through `GIT_CONFIG_COUNT` `[GIT-CONFIG]`, never in argv (world-readable in `/proc`), the URL, or `.git/config`;
  - stderr in errors is truncated and the token redacted.
- The base URL must be `https`; `http` is allowed only on loopback (tests). URLs with user info are refused.
- `Checkout.Path(rel)` resolves through symlinks and returns `ErrEscapes` outside the checkout (ADR-0004). `Cleanup(opID)`.

**Decisions**
- **Blobless single-branch clone:** it has the full commit history, so ancestry is exact, but downloads file contents for one commit only.
- **A SHA absent from the branch history is refused without fetching it.** Fork commits reachable through GitHub's network `[GH-FORKS]` therefore never enter the workspace.
- **Tests run against a real git smart-HTTP server** (`git http-backend` behind `net/http/cgi` on loopback), under the `integration` tag because they need the git binary. They need no network, so CI runs them too.

**Verification** (WSL2, git 2.43.0)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- `internal/source` integration (7 tests):
  - branch head checkout; a pinned ancestor with the **clone confirmed blobless** (missing blobs present);
  - **a feature-branch commit and an unknown SHA are refused as `ErrNotOnBranch`**, while the same commit works for its own branch; a missing branch returns `ErrBranchNotFound`;
  - separate workspaces per op, a retry wipes stale files, `Cleanup` touches only its op;
  - **the token reached the server as Basic `x-access-token`**, appears in no workspace file, and is not in error text;
  - `Path`: `evil -> /etc` and `../x` are refused, `link -> Dockerfile` is allowed;
  - bad inputs and base URLs (`ext::`, `file://`, plain `http` to a remote host, user info) fail before git is executed (checked with a nonexistent git binary).

**Next**
- P1.9: `internal/build`. It needs Docker and BuildKit, so it runs locally on WSL2.

### 2026-09-27: P1.7 CLI and deploy endpoint

- **Phase / task:** P1.7: CLI
- **Author:** Claude Code (desktop session)
- **Goal:** Operate Shipyard from a terminal (apps, environment, deploys) through the API only.

**Done** (commits `adceac8` Deploy endpoint, then CLI)
- API:
  - `POST /v1/apps/{app}/deployments` (`deploy` scope): an optional full commit `ref` (40 or 64 lowercase hex) and an `Idempotency-Key` (1–200 visible ASCII, stored as `api:<key>`, generated as `api:auto:…` when absent).
  - 202 for a new operation, 200 for a replay of the same request, 409 when the key was used for a different app or ref. The response lists superseded operations.
  - `GET /v1/operations/{id}`.
- `internal/client`: a typed client.
  - It refuses plain `http` to non-loopback hosts (the token would travel in clear) and supports `unix://` sockets.
  - It decodes problem+json into `*client.Error` with field errors, and path-escapes every segment.
- `cmd/shipyard`:
  - `login --url` reads the token from stdin, checks it with `whoami`, and only then saves `config.json` (mode 0600, written atomically). A config readable by others is refused.
  - `whoami`, `app create|list|show`, `ps`, `env set|unset|list`, `deploy`, `operation`.
  - Flags may come before or after positional arguments. Values are read from stdin with one trailing newline (LF or CRLF) dropped.
  - `SHIPYARD_URL`, `SHIPYARD_TOKEN`, and `SHIPYARD_CONFIG` override the config.

**Decisions**
- **`internal/client` is a new package** (ARCHITECTURE §8 updated). It keeps the CLI thin and testable.
- **`ps` equals `app list` for now.** It gains deployment status when deployments exist (P1.11).
- **An idempotent replay must be the same request**, not just the same key (payloads compared semantically), as with common payment APIs.

**Verification** (WSL2 as `hami`, plus native Windows)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- API integration `TestDeployEndpoint`: 202 → replay 200 → other ref 409 → other app 409; a keyless deploy supersedes; `GET` shows `cancelled` / `superseded by`; 422 for short or uppercase SHAs and bad keys; 400, 404, and 403 cases.
- Client unit tests: URL rules (7 accepted, 6 refused, including `http://10.0.0.5`); problem+json field errors; a non-JSON 502; `a/b` escaped as one segment.
- `TestCLIEndToEnd` against a real API and PostgreSQL:
  - a bad token login saves nothing; login, whoami;
  - two app creates (flags before and after the slug); 422 with field lines; ps, app show;
  - env set (secret and plain), list, unset, 404 on a missing key;
  - deploy, supersede, replay, short-ref error, operation;
  - **the token and the secret never appear in CLI output**.
- Native Windows: `go test ./cmd/shipyard ./internal/client ./internal/app` ok (local go1.27.1).
- **A Windows `shipyard.exe` against the API in WSL** (`http://127.0.0.1:18080`):
  - login, whoami, create, env set (CRLF input), env list, deploy, and ps all work;
  - a 422 lists both field errors; `http://example.com` is refused;
  - the secret appears 0 times in the API log.

**Problems / surprises**
- **Test harness, not product:**
  - Piping a script into `bash` lets WSL interop hand the rest of the script to the Windows exe as its stdin. Scripts now run from a file with `< /dev/null`.
  - `pkill -f 'shipyard-api serve'` matched its own shell; use `'[b]in/shipyard-api serve'`.
  - One run's `token create` returned nothing with stderr hidden. The rerun with stderr shown succeeded, so the cause is unknown.

**Next**
- P1.8: `internal/source`.

### 2026-09-27: P1.6 apps and env API

- **Phase / task:** P1.6: App CRUD API (plus the env endpoints and KEK wiring deferred from P1.4)
- **Author:** Claude Code (desktop session)
- **Goal:** Manage apps and their environment over the API, with validation before the database and uniform problem+json errors.

**Done** (commits `57137b1` App API, `92b2291` Sources dedupe, then Env API)
- `internal/app` (pure logic): `ValidateNew` and `ValidateUpdate` report every invalid field at once.
  - Branches follow git's ref-name rules `[GIT-REFNAME]` plus **no leading `-`** (option injection into git commands).
  - Paths are relative with no `..` component, backslash, or control character. The worker still resolves symlinks (ADR-0004).
  - Ports, durations, CPU, and memory are bounded.
- `internal/api`:
  - `/v1/apps` create (201 + Location), list, get, `PATCH` (slug and repo immutable), and `DELETE`. `{app}` is an ID or a slug.
  - Env: `GET /env` (keys and whether secret), `PUT` and `DELETE /env/{key}`. Values are secret by default and never echoed.
  - Strict JSON: `application/json` only (415), 1 MiB cap (413), unknown fields rejected, a single object (400).
  - `errors.go` is the single error → problem+json mapping. Validation errors return 422 with an `errors` list (an RFC 9457 extension member). Unexpected errors return a logged, generic 500.
- `store.DeleteIdleApp`: under the app lock, refuses (`ErrAppBusy` → 409) while an operation runs or a deployment is live.
- Config `SHIPYARD_KEK_DIR` (default `/etc/shipyard/kek`) and `SHIPYARD_KEK_ACTIVE`. `serve` loads the keyring before opening the DB or listener, and refuses to start without the active KEK or with a KEK file other users can read.
- `make dev-kek`: a dev-only KEK in the ignored `.dev/kek/`; `run-api` depends on it. `deploy/shipyard.env.example` documents KEK files.

**Decisions**
- **Validation lives in `internal/app`** (ARCHITECTURE §8: domain rules, no I/O). The database checks stay as the last line.
- **`migrate` and `token` do not need a KEK.** `SHIPYARD_KEK_ACTIVE` is required only by `serve` (and later by the worker).
- **Fix:** `GO-GCM` was defined twice in SOURCES.md, because P1.4 added a second row. The rows were merged, and a check for duplicate or undefined tags now passes.

**Verification** (WSL2 as `hami`)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- Unit: `CheckBranch` (7 good, 27 bad, including `--upload-pack=…`), `CheckRepoPath`, `ValidateNew` (11 field errors at once), `ValidateUpdate`, and the KEK config.
- API integration against real PostgreSQL:
  - CRUD by slug and ID; 409 on a duplicate slug; 422 with the exact field list;
  - 400, 413, and 415 cases; a read token gets 403; no ERROR logs for client errors;
  - delete refused while an op runs, then allowed; audit trail;
  - env set, list, unset; `Resolve` sees the sealed value; 404, 422, and 403 cases;
  - negative: the secret appears in no log line and no audit row.
- E2E with the real binary on `127.0.0.1:18080`:
  - `dev-kek` is idempotent, mode 600, 32 bytes;
  - `serve` without a KEK, or with a KEK file mode 644, is refused;
  - create 201; invalid input 422 listing `branch` and `build_context`; env set, list, patch;
  - **the secret appears 0 times in the API log and in a real `pg_dump`**; audit rows for every mutation.

**Next**
- P1.7: CLI. It needs `POST /v1/apps/{app}/deployments` (enqueue with `Idempotency-Key`) for `shipyard deploy`, so add that endpoint first.

### 2026-09-27: P1.5 operation queue

- **Phase / task:** P1.5: `internal/queue`
- **Author:** Claude Code (desktop session)
- **Goal:** The durable per-app operation queue of ADR-0002, safe under racing workers and crashes.

**Done** (commits `e728f97` Queue store, then Queue lease)
- `internal/store/operations.go`:
  - `EnqueueOperation`: `ON CONFLICT` idempotency. A key reused for another app or kind returns `ErrIdempotencyMismatch`. Latest-wins coalescing returns the IDs it superseded. An app row lock serializes admissions.
  - `ClaimOperation`: `FOR UPDATE SKIP LOCKED` that skips busy apps, `attempt < max_attempts`, and `attempt + 1`. It retries up to 3 times after losing a same-app race to the unique index.
  - `HeartbeatOperation`, `SetOperationPhase`, `CompleteOperation`, `FailOperation`: every write checks the lease owner, so a stale worker gets `ErrLeaseLost`. `FailOperation` can retry with a delay while attempts remain.
  - `RequeueExpired`: expired leases go back to the queue, or fail once the attempts are used up.
- `internal/store/events.go`: `AppendOperationEvent` with gapless per-operation `seq` (row lock, then the next seq read in a fresh statement). Messages are sanitized (NUL and invalid UTF-8 become U+FFFD) and truncated at 16384 characters. `OperationEvents(after, limit)` supports resume.
- `internal/queue`: `Queue.Next` polls every 2 s and logs DB errors without dying. `Queue.Hold` heartbeats every lease/3 and cancels the work (cause `ErrLeaseLost`) when the lease is lost or a whole lease passes without renewal.

**Decisions**
- **Admission creates only the operation**, per ARCHITECTURE §5 step 1. The worker creates the deployment at start (P1.11), so cancelled requests have no deployment rows. This replaces the P1.1 rationale for `deployments.operation_id`, which is still the link from a deployment to its operation.
- **`attempt` counts starts** and is incremented at claim. ADR-0002's "the reconciler increments attempt" is equivalent: an expired lease is simply claimed again.
- **Coalescing cancels queued operations of any kind** (deploy or rollback), since both change the serving release.

**Verification** (WSL2 as `hami`)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- Store integration (8 tests), including two race tests:
  - 12 workers against 4 apps, one with two queued ops: each op claimed once, one running per app.
  - 10 concurrent admissions for one app: exactly 1 queued and 9 cancelled.
  - Both passed **20 times in a row under `-race`**.
- Mutation checks:
  - without the app lock in admission, 4 ops stay queued instead of 1;
  - without conflict handling in claim, a worker returns a unique-violation error.
- `internal/queue` unit tests in `testing/synctest` bubbles (exact virtual time):
  - poll cadence and error logging, cancel;
  - heartbeats every 20 s for a 60 s lease;
  - stop on `ErrLeaseLost`;
  - give up at exactly 80 s after a last success at 20 s;
  - follow the parent context.
- Integration `TestLeaseHandover` (10 repeats, `-race`): while A holds, nothing expires and B claims nothing. When A's heartbeats fail, A stops itself, the op is requeued, B claims attempt 2, and A's `Complete` is refused.
- `TestEvents`: 20 concurrent appends produce gapless seqs 1–25; resume and limit work; NUL and invalid UTF-8 are stored sanitized.

**Problems / surprises**
- The first events test sent NUL bytes, and PostgreSQL rejected them (`22021`). Build output can contain NUL and invalid UTF-8, so the store now sanitizes messages.
- A lock plus `max(seq)` in a single statement does not serialize writers under READ COMMITTED, because the snapshot predates the lock wait. That is why events use two statements in a transaction.

**Next**
- P1.6: app CRUD API with validation and problem+json, plus `PUT/DELETE /v1/apps/{id}/env/{key}` and the keyring config (`SHIPYARD_KEK_DIR`, active KEK id).

### 2026-09-27: P1.4 secrets and environment revisions

- **Phase / task:** P1.4: `internal/secrets`
- **Author:** Claude Code (desktop session)
- **Goal:** Envelope-encrypt environment values and version them as immutable revisions (ADR-0005).

**Done** (commits `2a0ae5d` Envelope crypto, then Env revisions)
- `internal/secrets/envelope.go`: `Keyring` (`NewKeyring`, `LoadKeyring`), `Seal` and `Open`, `GenerateKey`, `NewValueID`.
  - A per-value DEK sealed with AES-256-GCM `NewGCMWithRandomNonce` `[GO-GCM]`, AAD `app_id|key|value_id`. The DEK is wrapped by the KEK with AAD `kek_id|value_id`.
  - Every open failure is `ErrDecrypt`, so the causes are indistinguishable.
  - KEK files are `<id>.key`, exactly 32 bytes, and must not be accessible to other users.
- `internal/secrets/env.go`: `Env.Set` (secret or plain), `Unset`, `Keys` (never values), and `Resolve` (worker only).
  - Each write locks the app row, then creates revision N+1 that references the unchanged entries.
  - Values are at most 64 KiB and contain no NUL.
- `internal/store/env.go`: `LockApp`, `InsertSecretValue`, `SecretValuesByID`, `CreateEnvRevision`, `LatestEnvRevision`, `EnvRevisionByID`.

**Decisions**
- **Value IDs are generated in Go** (UUID v4 from `crypto/rand`), because the ID is in the AAD and must exist before sealing.
- **`internal/secrets` uses `*store.Store` directly**, not an interface: it needs `InTx`, and its tests run against real PostgreSQL anyway.
- **Keyring wiring is deferred.** The config (`SHIPYARD_KEK_DIR`, the active id) arrives with the first consumers (P1.6 env endpoints, P1.10 container start). The roadmap says so.
- The KEK is below the 2³² message limit by many orders of magnitude, since each wrap is one message `[GO-GCM]`.

**Verification** (WSL2 as `hami`)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- Unit (8):
  - round trip including empty and 4 KiB values; no nonce or DEK reuse;
  - wrong AAD in 5 variants (other app, key, row; a DEK or ciphertext swapped in from another row);
  - wrong KEK and unknown KEK id; every single-byte tamper of the ciphertext and the wrapped DEK; truncation;
  - rotation (old values open, new ones seal with the new KEK); keyring validation; file mode refusal; UUID format.
- Integration (5):
  - revisions 1–4 with reuse by reference, an old revision still resolving the old secret, `Keys`, and `Unset`;
  - rejections (NUL, oversize, bad key, unknown app) leave no revision;
  - **Set with a keyring that cannot open the existing secret still works**, which proves Set never decrypts;
  - 8 concurrent Sets produce revisions 1–8 with all keys;
  - **dump of every table (`row_to_json`) contains neither the secret nor its hex**, while the plain value is present, which proves the check sees entry rows.
- Mutation check: without `LockApp`, `TestConcurrentSet` fails with `env_revisions_app_id_number_key` (3/3 runs).

**Next**
- P1.5: `internal/queue`.

### 2026-09-26: P1.3 token auth

- **Phase / task:** P1.3: Token auth
- **Author:** Claude Code (desktop session)
- **Goal:** Bootstrap the first admin token and protect every `/v1` route with scoped, expiring bearer tokens and an audit trail.

**Done** (commits `17c37fb` Token command, then Auth middleware)
- Tokens: `shp_` + 32 random bytes in unpadded base64url (47 characters). The display prefix is `shp_` plus 8 characters. Only the SHA-256 hash is stored `[GO-RAND]`.
- `shipyard-api token create|list|revoke`:
  - `create` makes the user if missing, prints the token alone on stdout and details on stderr, and audits `token.create` in the same transaction.
  - `--ttl` accepts 1h–366d (default 90d).
- Store:
  - Tokens: `CreateToken`, `ActiveTokenByHash` (revoked, expired, and unknown are all `ErrNotFound`), `TouchToken` (at most one write per minute), `ListTokens`, `RevokeToken` (idempotent).
  - Audit: `RecordAudit`, `AuditEvents`.
- `internal/api`:
  - `protect(scope, h)` wraps each route. 401 or 403 per `[RFC6750]`, same 401 for every bad token, well-formedness checked before any DB lookup, 503 without internal detail when the store is down.
  - An audit event after every authenticated mutation (actor `token:<prefix>`, action `r.Pattern`, target path, success/failure/denied, request ID), written with `context.WithoutCancel`.
- `GET /v1/whoami`.

**Decisions**
- **Scopes nest:** `read` ⊂ `deploy` ⊂ `admin`. `deploy` exists for CI tokens.
- **Anonymous failures are not audited**, only logged, so unauthenticated clients cannot grow the audit table.
- **The audit write is best-effort after the handler.** A failure is logged at ERROR and does not change the response. Writing audit in the mutation's own transaction is possible later, per use case.
- **`api_tokens.prefix` is `UNIQUE`** (edited in unreleased `0002`), so revoke-by-prefix is unambiguous.
- `internal/audit` (ARCHITECTURE §8) is not created yet: recording is one store call made by the middleware.

**Verification** (WSL2 as `hami`)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- Unit: 6 rejection cases (challenge header and whether the DB was queried), whoami with 3 scheme spellings, scope denial plus 3 audit results, audit surviving client cancel, audit and store failures. Negative: the plaintext is never in responses or logs.
- Integration: token lookups (active, no expiry, expired, unknown, revoked), idempotent revoke, touch throttle, unique prefix, hash, and non-empty scopes. `TestTokenCommand` bootstraps, lists, revokes, audits, and checks that the plaintext appears in no DB row.
- E2E with the real binary on `127.0.0.1:18080`:
  - no token → 401 `Bearer realm="shipyard"`; read token → 200 whoami; revoked → 401 `error="invalid_token"`;
  - token in the API log: 0 occurrences; audit rows for create and revoke; API exits 0 on SIGTERM.

**Problems / surprises**
- Port 8080 inside WSL was taken by the owner's `hamicloud-keycloak` container in Docker Desktop. All WSL2 distros share one network namespace. The e2e run used 18080, and DEVELOPMENT.md §5 has a row for it.
- WSL stops idle distros, which stops the dev PostgreSQL container. Run `make dev-up` again after a pause.

**Next**
- P1.4: `internal/secrets`. KEK file loading, per-value DEK, AES-256-GCM with AAD `app_id|key|value_id`, revision creation that reuses rows, plus store queries and the negative tests from the roadmap.

### 2026-09-26: P1.2 store core

- **Phase / task:** P1.2: `internal/store` on pgx
- **Author:** Claude Code (desktop session)
- **Goal:** One place for transactions and database error mapping, plus the first repositories.

**Done**
- `store.New`, `(*Store).InTx`. The same query methods run on the pool or in a transaction; a nested `InTx` joins the outer one.
- `errors.go`: SQLSTATE → `ErrNotFound`, `ErrConflict`, `ErrInvalid`, `ErrReference`, `ErrImmutable`, wrapped in `*ConstraintError{Table, Constraint, Column}`. PostgreSQL's message and detail are dropped because they quote values (`Key (slug)=(…)`), so the error is safe to log.
- `users.go`: `CreateUser`, `UserByName`. `apps.go`: `CreateApp`, `UpdateApp`, `AppByID`, `AppBySlug`, `ListApps`, `DeleteApp`. A nil `AppSettings` field keeps the DB default or the current value, so defaults live only in the migration.

**Changed files**
- `internal/store/{store,errors,users,apps}.go`, `errors_test.go`, `apps_integration_test.go`
- `migrations/0002_schema_v1.sql`: immutability SQLSTATE and owner FK (below). `docs/SOURCES.md`: `PG-RAISE`. `docs/ROADMAP.md`

**Decisions**
- **Scope:** P1.2 is the core plus users and apps. Each later task adds the queries it consumes (CLAUDE.md §5: interfaces are declared by the consumer). The roadmap item says so.
- **Durations** are exchanged as microseconds (`extract(epoch …)` / `$n * interval '1 microsecond'`), independent of the driver's interval mapping.

**Verification** (WSL2 as `hami`, PostgreSQL 18)
- `make lint`: exit 0. `make test`: ok, including `TestMapError` and `TestConstraintErrorOmitsValues` (negative test: the rejected value never appears in `Error()`).
- `make dev-reset && make dev-up && make migrate`: `applied=2`, then `applied=0`.
- `go test -race -tags integration ./internal/store/`: all pass (users, app defaults, round trip, 9 rejection cases, update/list/delete, `InTx` rollback, nested join and commit, owner delete refused). `make test-integration`: all ok.

**Problems / surprises**
- **A test caught a real bug.** `ON DELETE RESTRICT` raises `23001 restrict_violation`, the same code the immutability trigger used, so "owner still has apps" would have surfaced as "row is immutable". Fixed in `0002`:
  - the trigger now raises Shipyard's own `SY001` `[PG-RAISE]`;
  - `apps.owner_id` uses the default `NO ACTION` (`23503`).
  `0002` was edited in place because it is unreleased and only on this branch. Any dev database that applied the old `0002` needs `make dev-reset`.

**Next**
- P1.3: token auth. Add `api_tokens` and `audit_events` queries to `internal/store` with integration tests.

### 2026-09-26: P1.1 schema v1

- **Phase / task:** P1.1: Schema v1
- **Author:** Claude Code (desktop session)
- **Goal:** Turn the ARCHITECTURE §4 data model into migration `0002` with database-enforced invariants.

**Done**
- `migrations/0002_schema_v1.sql`: 12 tables (the roadmap's 11 plus `env_revision_entries`), 2 trigger functions, and the indexes the queue needs.
- Enforced in the database:
  - `UNIQUE(idempotency_key)`; one running operation per app; one active deployment per app; unique lowercase `hostname`.
  - A running operation needs a lease; `finished_at` matches terminal status; `failed` needs a reason; serving states need `image_id` and `container_id`.
  - Composite `(app_id, id)` foreign keys, so no cross-app operation, env revision, rollback source, route target, or secret. An entry's secret must also carry the entry's key (it is in the AAD, ADR-0005).
  - Immutability triggers on secret values, revisions, and entries; append-only operation events; audit events reject UPDATE and DELETE.
  - Cheap path checks: `dockerfile_path` and `build_context` cannot be absolute or contain a `..` segment (the worker still resolves symlinks, ADR-0004).
- `internal/store/schema_integration_test.go`: 33 subtests asserting SQLSTATE codes.

**Changed files**
- `migrations/0002_schema_v1.sql`, `internal/store/schema_integration_test.go`: the slice
- `docs/ARCHITECTURE.md` §4: ID and timestamp conventions, new columns; `docs/SOURCES.md`: `PG-UUID`, `DK-RESOURCES`; `CHANGELOG.md`; `docs/ROADMAP.md`

**Decisions**
- **IDs are `uuid` via `gen_random_uuid()`**, not `uuidv7()`: `uuidv7()` needs PostgreSQL 18, while ADR-0002 still accepts 17. They are not enumerable through the API.
- **Immutable and append-only tables have no `updated_at`**, which is a deviation from "every table has `updated_at`". ARCHITECTURE §4 is updated.
- **Added columns** not listed in §4: `deployments.operation_id` (a deployment row exists from admission, so coalescing can mark it `cancelled`), `deployments.source_deployment_id` (rollback target), and `api_tokens.name`.
- **`slug` is limited to 40 characters** (one DNS label) so that container and network names stay short.
- Enumerations are `text` plus `CHECK`, so adding a value takes a one-line migration.

**Verification** (WSL2, as `hami`, PostgreSQL 18 in `make dev-up`)
- `make lint`: exit 0 (gofmt, vet, and staticcheck, including integration files). `make test`: ok.
- `make migrate`: applied versions 1 and 2, then `applied=0`.
- `go test -tags integration -run TestSchema ./internal/store/`: 33/33 subtests PASS. `make test-integration`: all packages ok.
- Mutation check: after removing the running-op index, the active-deployment index, and the audit DELETE guard, exactly those 3 subtests failed.

**Next**
- P1.2: `internal/store` repositories on pgx (apps, operations, deployments, env revisions, tokens, audit) with integration tests.

### 2026-09-26: Release v0.1.0

- **Phase / task:** P0 close: merge and first release
- **Author:** Claude Code (desktop session), on the owner's explicit request
- **Goal:** Merge Phase 0 to `main` and cut `v0.1.0` per docs/RELEASING.md.

**Done**
- Fast-forward merge of `claude/shipyard-architecture-proposal-j8w56b` into `main` (`73fa9e4`).
- `CHANGELOG.md`: `[Unreleased]` → `[0.1.0] - 2026-09-26` (`3000c74 Release v0.1.0`), annotated tag `v0.1.0` pushed.
- WSL user `hami` added to the `docker` group (already in `sudo`); repo cloned to `/home/hami/shipyard`.
- README: release pointer and image usage (no entrypoint; name the binary).

**Verification**
- Rehearsal in WSL as `hami`: `make release-check` ok; `make release-snapshot` ok (8 archives, checksums, amd64/arm64 images); binaries report `go1.26.8`.
- CI on `main`: `73fa9e4` and `3000c74` both green. [Release run](https://github.com/hami9/Shipyard/actions/runs/36252517257): success.
- Release page: not a draft or pre-release, 8 archives plus `checksums.txt`. `sha256sum -c` OK for the Linux server and Windows CLI archives; `gh attestation verify` exit 0; downloaded `shipyard.exe version` → `v0.1.0 (commit 3000c741483a, go1.26.8)`.
- GHCR: anonymous `docker manifest inspect` works (amd64, arm64, plus attestation manifests); `shipyard`, `shipyard-api`, and `shipyard-worker` all report `v0.1.0` from the image; `:latest` resolves.

**Problems / surprises**
- The GHCR package was already publicly pullable after the first push, so the manual "make it public" step was not needed this time. Package settings could not be read here (`gh` token lacks `read:packages`).

**Next**
- P1.1: `git switch -c schema-v1 main`, then write `migrations/0002_schema_v1.sql` plus store integration tests.

### 2026-09-26: Owner local run (WSL2), repo settings

- **Phase / task:** P0 exit criteria (local verification), owner blockers
- **Author:** Claude Code (desktop session on the owner's Windows 10 machine)
- **Goal:** Clear the owner-side Phase 0 blockers and run DEVELOPMENT.md §2–§3 locally.

**Done**
- GitHub, via `gh`: set the About description and 10 topics; enabled private vulnerability reporting (`{"enabled":true}`).
- WSL2: installed `Ubuntu-24.04` (systemd on), Docker Engine 29.8.1, Compose v5.5.1, and go1.26.8 (checksum OK), following §2.3–§2.4. Docker Desktop stays installed; its WSL integration is not enabled for Ubuntu.
- Fixed a dev-env race: on a fresh volume, `make dev-up && make migrate` failed with `57P03 the database system is starting up`. The healthcheck probed the Unix socket, which the image's init-only temp server already serves `[PG-IMAGE-INIT]`. It now probes `127.0.0.1`.
- Added WSL troubleshooting rows (VPN DNS, apt IPv6, Docker Desktop CLI on PATH) and tags `PG-IMAGE-INIT`, `MS-WSL-CONF`.

**Changed files**
- `deploy/dev/compose.yaml`: TCP healthcheck
- `docs/DEVELOPMENT.md`, `docs/SOURCES.md`, `docs/ROADMAP.md`: troubleshooting, tags, exit criteria

**Decisions**
- none (dev-only config fix)

**Verification** (inside Ubuntu-24.04 on WSL2, as root, commit `b254f91` plus the fix)
- §2.5: `docker info` → `Ubuntu 24.04.5 LTS`; probe of container IP `172.17.0.2` → `HTTP 200`.
- `make lint`: exit 0. `make test`: 8 packages ok under `-race`.
- `make build && ./bin/shipyard-api version` → `shipyard-api b254f91 (commit b254f91ae4c5, go1.26.8)`.
- Before the fix: first `make migrate` after `dev-up` → 57P03; container log showed the Unix-socket-only temp server.
- After the fix: `make dev-reset`, then `dev-up && migrate && test-integration` **3/3 PASS**; second `migrate` → `applied=0`.
- `curl /healthz` → `200 {"status":"ok"}` with `X-Request-Id`; `curl /readyz` → `200 {"status":"ready"}`.
- API binary exits 0 on SIGINT and on SIGTERM, logging `api shutting down`.

**Problems / surprises**
- With the Windscribe VPN connected, WSL's NAT DNS proxy did not resolve anything, although IPs were reachable. Fixed per distro with `generateResolvConf=false` plus public resolvers `[MS-WSL-CONF]`. `dnsTunneling` is not available on Windows 10.
- apt tried IPv6 and got `Ign:` on every package; forced IPv4 (`99force-ipv4`, `gai.conf`).
- No regular Linux user exists in the distro yet; everything ran as root. The owner creates one (see Next).

**Next**
- Owner: create the WSL user (`wsl -d Ubuntu-24.04`, then `adduser <name>`, `usermod -aG sudo,docker <name>`, and set `[user] default=<name>` in `/etc/wsl.conf`).
- Owner: merge this branch to `main`, then cut `v0.1.0` per docs/RELEASING.md and make the GHCR package public.
- Agent: P1.1, schema v1 migration `0002_schema_v1.sql` plus store tests.

### 2026-09-25: Owner platform, WSL2 guide

- **Phase / task:** P0 (exit criterion: local verification)
- **Author:** Claude Code (cloud session)
- **Goal:** Tailor local testing to the owner's machine: **Windows with WSL2**, and no Docker installed yet.

**Done**
- `docs/DEVELOPMENT.md` §2 is now a WSL2 guide, sourced step by step:
  - WSL version check, systemd check,
  - Docker Engine from Docker's apt repository (not Docker Desktop),
  - Go 1.26.8 from the official tarball with its SHA-256,
  - sanity checks, including a host-to-container-IP probe.
- 4 new source tags: `DK-INSTALL-UBUNTU`, `MS-WSL-SYSTEMD`, `MS-WSL-FS`, `GO-INSTALL`.
- CLAUDE.md: a rule to create files with the editor tools, never shell heredocs (see below).

**Decisions**
- Docker Engine runs natively inside WSL2 Ubuntu, and Docker Desktop is excluded. Its bridge network is unreachable from the host `[DK-DESKTOP-NET]`, which would break Phase 1 health probes.

**Verification**
- CI [run #4](https://github.com/hami9/Shipyard/actions/runs/36200413332) on `d4c3a5a`: **success**, including the new GoReleaser config check.
- The probe from §2.5 was run on native Docker Engine in the cloud container: `HTTP 200` from the container's bridge IP (172.17.0.2).
- The go1.26.8 tarball checksum in the guide matches `go.dev/dl/?mode=json`, and `sha256sum -c` passed.
- Not run: the guide itself on WSL2 (the owner will).

**Problems / surprises**
- **Incident (cloud container only):** a shell heredoc used to insert the Markdown section contained its own `EOF` line. That ended the outer heredoc early, and bash executed the rest of the section as commands.
  - Effects: Docker packages were upgraded mid-run, `/usr/local/go` was replaced with go1.26.8, and a PATH line was added to `~/.profile`.
  - There was no effect on the repository (`git status` clean), GitHub, or the owner's machine.
  - Cleaned up: the profile line was reverted, the mismatched dockerd was stopped, and the dev PostgreSQL was removed.
  - Prevention: the CLAUDE.md rule above.

**Next**
- Owner: DEVELOPMENT.md §2 (setup) and §3 (verification). Send back §2.5, `make lint`, `make test`, `make test-integration`, and the two curl outputs.

### 2026-09-25: Public-repo readiness (P0.8)

- **Phase / task:** P0.8 (added at the owner's request: releases, packages, description, license)
- **Author:** Claude Code (cloud session)
- **Goal:** Make the public repository complete: license, release artifacts and packages, and community files.

**Done**
- Owner decisions: **Apache-2.0**, and **binaries plus a GHCR image** per release.
- `LICENSE` is the canonical Apache-2.0 text (SHA-256 `cfc7749b…d30`). Added `NOTICE` ("The Shipyard Authors").
- `.goreleaser.yaml` (GoReleaser v2.18.2):
  - CLI for Linux, macOS, and Windows on amd64 and arm64.
  - `shipyard-server` for Linux amd64 and arm64, bundling `deploy/`.
  - `checksums.txt`.
  - `dockers_v2` image `ghcr.io/hami9/shipyard`, on distroless `static-debian13:nonroot` pinned by digest, with OCI labels (source, license).
- `.github/workflows/release.yml`: runs on a `v*` tag.
  - Lint and test, then release notes from CHANGELOG.
  - QEMU and buildx, a GHCR login, and GoReleaser.
  - `actions/attest@v4` over `checksums.txt`.
- CI now also validates the GoReleaser config.
- `scripts/release-notes.sh`: fails if CHANGELOG has no section for the tag.
- `scripts/check-go-version.sh`: fails the build unless Go matches go.mod's toolchain.
- `SECURITY.md` (private reporting, scope aligned with the trust model), `CONTRIBUTING.md`, `CHANGELOG.md` (Keep a Changelog), and `docs/RELEASING.md` (steps, version plan `v0.1.0`…`v1.0.0`).
- README: badges, Install, Security, and License sections.
- CLAUDE.md: a changelog rule, and agents never tag or release unless the owner asks.

**Changed files** (commits)
- `7230bbb` License · `3dfcede` Release pipeline · `343d549` Community files

**Decisions**
- Release notes come from CHANGELOG.md rather than commit messages. The 1–2 word commit subjects are too terse for users.
- The image has no ENTRYPOINT (CMD `shipyard help`). The supported production install stays systemd. The image is for the CLI and for evaluation.
- GoReleaser is pinned to exactly v2.18.2 (in the workflow and the Makefile) for reproducible releases.

**Verification**
- `make release-check`: 1 configuration file validated, with no deprecation warnings.
- `make release-snapshot` (local, nothing published): 8 archives plus checksums. amd64 and arm64 images were built.
  - `sha256sum -c` OK.
  - The server archive contains the binaries and `deploy/`.
  - The image runs as `nonroot:nonroot` with source and license labels.
  - `shipyard-api version` inside the image works.
- **Bug found and fixed:** the first snapshot built binaries with **go1.27.1**. GoReleaser v2.18.2 requires Go 1.27.1, and `go run` passed its toolchain to the builds. The fix installs GoReleaser as a binary and adds the toolchain guard hook. After the fix, `go version` on the binaries shows go1.26.8. The guard was tested negatively: with `GOTOOLCHAIN=go1.27.1` the release fails with "building with go1.27.1 but go.mod pins go1.26.8".
- `scripts/release-notes.sh`: fails on a missing section (exit 1) and extracts the right section from a sample changelog.
- `make lint test`: clean, 8 packages ok. All YAML files parse.
- Not run: the real tag-triggered release (no tag was pushed, per the owner's release process).

**Problems / surprises**
- GitHub gives new GHCR packages **private** visibility, so the owner must make the package public after the first release `[GHCR]`.
- The repository About text (description, topics) cannot be set from this session. The owner sets it in the GitHub UI.

**Next**
- Owner: local run (docs/DEVELOPMENT.md §3) and OS; About text; enable private vulnerability reporting.
- Then close Phase 0, merge to `main`, cut `v0.1.0` (docs/RELEASING.md), and start P1.1.

### 2026-09-25: Phase 0 bootstrap (P0.1–P0.7)

- **Phase / task:** P0.1–P0.7
- **Author:** Claude Code (cloud session)
- **Goal:** Record the owner's approvals and build the fully wired, empty-but-working project skeleton.

**Done**
- The owner accepted ADR-0001 to ADR-0007. Docker- and Caddy-dependent tests now run on the owner's local machine (CLAUDE.md §6).
- Go module `github.com/hami9/shipyard` with three binaries:
  - `shipyard` (version),
  - `shipyard-api` (`serve`, `migrate`, `version`),
  - `shipyard-worker` (`run`, `version`).
  All shut down gracefully on SIGTERM.
- `internal/config`: environment-only, validated, and reporting all errors at once. The API refuses a non-loopback listen address unless explicitly overridden.
- `internal/logging`: slog JSON or text, with `request_id`, `operation_id`, `app`, and `deployment_id` taken from the context.
- `internal/api`:
  - `GET /healthz` (liveness) and `GET /readyz` (DB ping, 503 problem+json).
  - Request IDs, with client IDs accepted only from a safe charset.
  - An access log that omits query strings.
  - Unix socket listen (mode 0660, stale-socket safe).
- `internal/store`: `Open` (ping; errors redact the password), plus an in-house migration runner (`embed` + pgx).
  - One transaction per run, serialized by `pg_advisory_xact_lock`.
  - Refuses renamed or unknown applied migrations.
  - `storetest.NewDatabase` gives each integration test a throwaway database.
- `migrations/0001_baseline.sql`.
- Makefile (`help`, `build`, `test`, `lint`, `fmt`, `test-integration`, `dev-up/down/reset`, `migrate`, `run-api`, `run-worker`, `clean`) and CI (`.github/workflows/ci.yml`).
- `deploy/`: hardened systemd units, `daemon.json` (`local` log driver, `live-restore`), Caddy bootstrap with the admin socket at `|0220`, `shipyard.env.example`, and the dev compose file (PostgreSQL 18 on 127.0.0.1:54320).
- `docs/DEVELOPMENT.md` (local testing guide) and 7 new source tags.

**Changed files** (commits)
- `ea9afba` Accept ADRs: ADR statuses, CLAUDE.md testing policy
- `2cf3a7c` Config logging · `2df3f84` Migrations · `6730d1c` API server · `d8605b2` Binaries
- `2b40117` Tooling · `f770e7c` Deploy skeleton · `93108e9` Dev docs

**Decisions**
- **Config is environment variables only**, supplied by systemd `EnvironmentFile=`. This avoids a parser dependency (TOML was implied before).
- **In-house migration runner** instead of goose or golang-migrate. It is about 150 lines on pgx, with no extra dependency, and is fully tested.
- **Toolchain pinned to `go1.26.8`.** staticcheck 2026.2.1 fails on Go 1.27.1's standard library (`method must have no type parameters`) `[STATICCHECK]`.
- **Dropped a public `GET /version` endpoint**, to avoid exposing version and commit without authentication.
- Only one new dependency: `github.com/jackc/pgx/v5` v5.11.0, which was already in the approved stack.

**Verification** (all run in the cloud container)
- `make lint`: gofmt clean, `go vet` (plus `-tags integration`) clean, staticcheck (plus `-tags integration`) clean.
- `make test`: 8 packages ok under `-race`.
- `make dev-up`: postgres:18 healthy (`PostgreSQL 18.6`).
- `make migrate` twice: `applied=1`, then `applied=0`.
- `make test-integration`: all ok. The store integration tests passed: applies once, rollback on failure, refuses a newer schema, refuses a rename, concurrency × 5, embedded migrations. They also passed on PostgreSQL 16.13.
- Smoke tests:
  - `/healthz` and `/readyz` return 200 over TCP and over a Unix socket (mode 660).
  - A `0.0.0.0` listen address is refused with exit 1.
  - The API and worker exit 0 on SIGTERM.
- `systemd-analyze verify`: no syntax errors (it only reported that the binaries are not installed).
- GitHub Actions: [run #1](https://github.com/hami9/Shipyard/actions/runs/36199600490) (`93108e9`) and [run #2](https://github.com/hami9/Shipyard/actions/runs/36199667568) (`c70dc39`) both **success**, covering both jobs (lint/unit/build and integration on PostgreSQL 18).
- Not run yet: the owner's local run.

**Problems / surprises**
- Docker in the cloud container defaults to `json-file` logging, which confirms that the `daemon.json` change is needed `[DK-LOG]`.
- Docker Desktop cannot reach container bridge IPs from the host `[DK-DESKTOP-NET]`. Added to the risk register and DEVELOPMENT.md.

**Next**
- Owner: follow `docs/DEVELOPMENT.md` §3 and send back the output and their OS.
- Agent: once the owner's run passes, close Phase 0 and start P1.1 (schema v1 migration `0002_schema_v1.sql` plus store tests).

### 2026-09-25: Architecture review, agent instructions, roadmap

- **Phase / task:** Pre-Phase 0: project definition
- **Author:** Claude Code (cloud session)
- **Goal:** Correct the proposed architecture against primary sources, create the agent system prompt and work log, and phase the roadmap.

**Done**
- Archived the original proposal as `docs/archive/ARCHITECTURE-v1.md`.
- Verified v1 claims against GitHub, Docker, Caddy, Let's Encrypt, PostgreSQL, Go, OWASP, WHATWG, MDN, and RFC 9457 docs (38 sources, tagged).
- Wrote `docs/ARCHITECTURE.md` v2 with 19 corrections (R1–R19). High-severity items:
  - Caddy admin API on a Unix socket (R2)
  - `local` log driver, since json-file never rotates (R3)
  - Commit ancestry check against fork-network commits (R4)
  - Envelope encryption moved into Phase 1 (R9)
  - Docker-published ports bypass ufw (R10)
- Wrote proposed ADRs 0001–0007 that answer v1's open questions.
- Wrote `docs/ROADMAP.md` with Phases 0–7, IDs, exit criteria, and a dependency graph.
- Created `CLAUDE.md` (agent system prompt), `AGENTS.md`, this log, `README.md`, `.gitignore`, and `.editorconfig`.
- Adopted the owner's git conventions: commit subjects of 1–2 words, branch names of 1–3 words.

**Changed files**
- `docs/ARCHITECTURE.md`, `docs/architecture-review.md`, `docs/SOURCES.md`, `docs/archive/ARCHITECTURE-v1.md`: `242e169` Architecture v2
- `CLAUDE.md`, `AGENTS.md`: `2622d14` Agent prompt
- `docs/adr/0000`–`0007`: `63c8f8d` ADRs
- `docs/ROADMAP.md`: `24d6ec5` Roadmap
- `README.md`, `.gitignore`, `.editorconfig`: `af133ba` Scaffold
- `docs/WORKLOG.md`: Worklog commit

**Decisions**
- All seven ADRs are `Proposed`, pending the owner's review.
- Notable reversals from v1: secrets move from milestone 5 to Phase 1, "image digest" becomes the Engine image ID plus build metadata, and advisory locks become lease rows plus a partial unique index.

**Verification**
- Documentation only; no code exists yet. Source facts were fetched from primary docs on 2026-09-25 (see `docs/SOURCES.md`).
- A link and tag check script found 0 broken relative links, 38 defined source tags, 0 undefined, and 0 unused.
- Version facts at verification time: Go 1.27.0 (2026-08-19), PostgreSQL 18.6, Docker Engine 29.8.1.

**Problems / surprises**
- `docker/docker` Go module deprecated in Engine 29 → use `github.com/moby/moby/client` `[DK-29]`.
- GitHub compare API docs say `BASE...HEAD` must be branch names, so the ancestry check uses `git merge-base --is-ancestor` rather than relying on the API.
- The session's assigned branch `claude/shipyard-architecture-proposal-j8w56b` exceeds the new 1–3 word branch rule. It was kept because the harness requires it, and future branches follow the rule.

**Next**
- Owner reviews ADR-0001 to ADR-0007 (accept or amend).
- Start P0.1: `go mod init`, `cmd/shipyard{,-api,-worker}` skeletons, Makefile, CI workflow.
