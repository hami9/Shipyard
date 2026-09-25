# Work Log

A chronological record of work on Shipyard, **newest entry first**. Every working session adds one entry. The log is the hand-off between sessions and between people and agents. Someone new should be able to resume from the `Current status` block plus the newest entry alone.

## Current status

> Update this block at the end of every session.

| Field | Value |
| --- | --- |
| **Active phase** | Phase 0: Bootstrap. The code is done; waiting on two external checks |
| **Last completed** | P0.1–P0.8: bootstrap plus public-repo readiness (license, release pipeline, security and contributing docs) |
| **Next task** | Owner sets up WSL2 (docs/DEVELOPMENT.md §2), then runs §3 and sends the output. Then P1.1 (schema v1) |
| **Blockers** | Owner: (1) WSL2 setup and local verification run; (2) set the repository About text; (3) enable private vulnerability reporting. First release `v0.1.0` after Phase 0 merges to `main` |
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
