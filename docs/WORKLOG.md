# Work Log

A chronological record of work on Shipyard, **newest entry first**. Every working session adds one entry. The log is the hand-off between sessions and between people and agents. Someone new should be able to resume from the `Current status` block plus the newest entry alone.

## Current status

> Update this block at the end of every session.

| Field | Value |
| --- | --- |
| **Active phase** | Phase 0: Bootstrap. The code is done; waiting on two external checks |
| **Last completed** | P0.1, P0.2, P0.4, P0.5, P0.6, P0.7 (P0.3 CI is written; its first green run is pending) |
| **Next task** | Owner runs docs/DEVELOPMENT.md §3 locally and reports their OS. Then P1.1 (schema v1) |
| **Blockers** | (1) The owner's local verification run. (2) The first GitHub Actions run of `ci.yml`, which had not registered right after the push |
| **Open risks** | Builder egress is unrestricted until Phase 5. On Docker Desktop (macOS/Windows), Phase 1+ health probes cannot reach container IPs `[DK-DESKTOP-NET]` |
| **Last updated** | 2026-09-25 |

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
- Not run: GitHub Actions (no run registered right after the push), and the owner's local run.

**Problems / surprises**
- Docker in the cloud container defaults to `json-file` logging, which confirms that the `daemon.json` change is needed `[DK-LOG]`.
- Docker Desktop cannot reach container bridge IPs from the host `[DK-DESKTOP-NET]`. Added to the risk register and DEVELOPMENT.md.

**Next**
- Owner: follow `docs/DEVELOPMENT.md` §3 and send back the output and their OS.
- Agent: confirm the CI run is green, tick P0.3, close Phase 0, and start P1.1 (schema v1 migration `0002_schema_v1.sql` plus store tests).

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
