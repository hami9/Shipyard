# CLAUDE.md: Shipyard agent instructions

This file is the system prompt for AI coding agents working in this repository. Read it fully at the start of every session. `AGENTS.md` points here.

## 1. Mission

Shipyard is a self-hosted deployment platform for a **single VPS**. It turns a GitHub repository with a Dockerfile into a running, HTTPS-routed application. Every deployment has a pinned commit, an immutable image ID, an encrypted configuration revision, a health result, a route, and a rollback target.

The MVP targets **trusted operators and trusted repositories**. It is not a multi-tenant sandbox.

## 2. Session protocol (do this every time)

1. **Orient.** Read, in this order:
   - the `Current status` block and newest entry in [docs/WORKLOG.md](docs/WORKLOG.md),
   - the active phase in [docs/ROADMAP.md](docs/ROADMAP.md),
   - the parts of [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and [docs/adr/](docs/adr/) that touch the task.
2. **Pick one task.** Take the first unchecked item of the active phase unless the owner says otherwise. State it in one line before starting.
3. **Plan small.** Deliver one vertical slice per commit: code, test, and docs together. If the task needs more than about 400 changed lines, split it and add the sub-tasks to the roadmap.
4. **Implement and verify.** Run the checks in §6. Never claim something works without running it. Paste the actual results into the work log.
5. **Record.**
   - Tick the roadmap item.
   - Add a WORKLOG entry at the top (template inside the file) and update `Current status`.
   - Write an ADR for any decision that changes behavior, a boundary, or a dependency.
6. **Commit and push** following §7.

If a session ends mid-task, the WORKLOG entry must say exactly where you stopped and what the next command is.

## 3. Non-negotiable invariants

Breaking any of these is a bug, even if tests pass. Changing one requires an accepted ADR.

**Boundaries**

1. **The API never touches Docker, Caddy, or repository code.** It validates and writes rows to PostgreSQL. Only `shipyard-worker` talks to Docker, BuildKit, the Caddy admin socket, and git.
2. **PostgreSQL is the source of truth.** Docker and Caddy state is derived and must be reproducible from the database. The Caddy config is a pure function of the `routes` table.

**Operations**

3. **Persist the phase before each side effect. Make every side effect idempotent.** Use deterministic names and labels (`io.shipyard.*`) and idempotency keys.
4. **At most one running operation per app.** A partial unique index enforces it. Do not use long-held advisory locks.
5. **Only a healthy candidate receives traffic.** A failed build, start, health check, or route load never changes the serving route.
6. **Rollback uses the retained image ID and environment revision.** It never rebuilds from a branch.
7. **A deployed SHA must be an ancestor of the app's tracked branch.** Fork commits are reachable upstream.

**Security**

8. **Secrets** are envelope-encrypted (AES-256-GCM, versioned KEK outside PostgreSQL). They never appear in logs, API responses, error messages, build args, or build environment.
9. **Webhooks:** read the raw body (≤ 25 MB), verify `X-Hub-Signature-256` with a constant-time compare **before** parsing, deduplicate on `X-GitHub-Delivery`, and respond within 10 s.
10. **App containers:**
    - never `--privileged`, `--network host`, the Docker socket, host bind mounts, or published ports;
    - always `--cap-drop ALL`, `no-new-privileges`, CPU, memory, and pids limits, the `local` log driver, and a per-app network.
11. **Only Caddy publishes host ports.** Docker-published ports bypass ufw.
12. **The Caddy admin API is reachable only through a permissioned Unix socket.**

Source evidence for each invariant is in [docs/SOURCES.md](docs/SOURCES.md) and [docs/architecture-review.md](docs/architecture-review.md).

## 4. Stack and versions

| Area | Choice |
| --- | --- |
| Language | Go ≥ 1.26 (keep `go.mod` on a supported release) |
| HTTP | `net/http` `ServeMux` with method and wildcard patterns. No router framework. |
| Logging | `log/slog`, JSON in production, with `request_id`, `operation_id`, `app`, and `deployment_id` fields |
| Database | PostgreSQL 18 via `pgx/v5`. Forward-only SQL migrations in `migrations/`. |
| Docker | Engine 29.x through `github.com/moby/moby/client`. **Not** the deprecated `github.com/docker/docker`. |
| Builds | `docker buildx` with a dedicated `docker-container` builder named `shipyard` |
| Edge | Caddy v2, JSON config through `POST /load` on the admin Unix socket |
| Errors | `application/problem+json` (RFC 9457) |
| Streaming | SSE with `id` and `Last-Event-ID` resume. WebSockets only if bidirectional traffic is needed. |
| UI | React in `web/`, Phase 6 only. It must never be required for a deploy. |

Add a dependency only when the standard library is clearly insufficient. Record why in the WORKLOG entry, or in an ADR if it is architectural.

## 5. Code conventions

- **Layout.** Follow ARCHITECTURE §8.
  - `internal/app` holds pure state-transition logic with no I/O.
  - Adapters (`store`, `runtime`, `routing`, `source`, `build`) implement interfaces **declared by the consumer**.
- **Context.** `context.Context` is the first parameter of anything that does I/O or may block. Respect cancellation and deadlines, since builds and probes always have deadlines.
- **Errors.** Wrap with `%w` and context (`fmt.Errorf("start candidate %s: %w", id, err)`). No `panic` outside `main`. Map domain errors to problem+json in one place.
- **State.** No global mutable state. Configuration is an explicit struct loaded in `cmd/*` and passed down.
- **SQL.** Parameterized queries only. Every state change that has a side effect happens in an explicit transaction. New constraints go in migrations, not in application code alone.
- **Tests.**
  - Table-driven unit tests next to the code.
  - Integration tests (real PostgreSQL and Docker) use the `integration` build tag.
  - End-to-end tests live in `test/e2e`.
  - Every bug fix starts with a failing test.
  - Security-sensitive code (webhook verification, secrets, token auth, path validation) needs negative tests.
- **Logs.** Never log request bodies, env values, tokens, or ciphertext. Log IDs, not payloads.
- **Comments.** Explain *why*, not *what*. Link the ADR or source tag when behavior depends on a vendor fact, e.g. `// [GH-BP] must answer within 10s`.

## 6. Commands

These are the planned targets, which Phase 0 creates. Until then, say which command you would have run.

```bash
make build              # build all binaries into ./bin
make test               # unit tests (go test ./... -race)
make lint               # gofmt check, go vet, staticcheck
make test-integration   # needs Docker + PostgreSQL (go test -tags integration ./...)
make dev-up / dev-down  # local PostgreSQL (+ Caddy) for development
make migrate            # apply migrations to $SHIPYARD_DATABASE_URL
```

A task is not done while `make lint test` fails. Run `make test-integration` for anything touching `store`, `runtime`, `routing`, `build`, or `source`.

## 7. Git conventions

- **Commit subject: 1–2 words**, imperative or noun phrase, no trailing period. Examples: `Webhook verify`, `Queue lease`, `Roadmap`.
  - Put detail, if needed, in the body after a blank line. Keep required trailers (e.g. `Co-Authored-By`) at the end.
- **Branch names: 1–3 words**, kebab-case, e.g. `webhook-hmac`, `caddy-routing`, `phase1-queue`. A harness-assigned branch name takes precedence when one is given.
- One logical change per commit. Never commit secrets, `.env` files, keys, or generated binaries.
- Do not rewrite pushed history on shared branches. Do not open pull requests unless asked.

## 8. Definition of done

- The code implements the roadmap item, and the invariants in §3 hold.
- Lint, unit, and (where relevant) integration tests pass, with the output recorded in the WORKLOG.
- The docs are updated: ARCHITECTURE if behavior changed, an ADR if a decision was made, and the roadmap item is ticked.
- New external facts have a tagged entry in `docs/SOURCES.md` with a verification date.

## 9. Verifying external facts

Vendor behavior (Docker, Caddy, GitHub, PostgreSQL, Let's Encrypt, Go) changes often.

- Before relying on a limit, default, flag, or API shape, check the primary source and cite its tag.
- If a source contradicts the docs here, stop. Update `SOURCES.md` and the affected doc, and note the change in the WORKLOG before writing code on top of it.
- Never invent flags, API fields, or version numbers.

## 10. When to stop and ask the owner

- Any change to an invariant in §3, the trust model, or the data model's immutability rules.
- Adding a runtime dependency with network, crypto, or parsing responsibility.
- Destructive operations on real hosts or data: pruning images or volumes, dropping tables, or deleting backups.
- Ambiguous requirements where two reasonable readings lead to different schemas or APIs.

For anything else, choose the conventional option, note it in the WORKLOG, and proceed.

## 11. Communication style

- Reply to the owner in the language they write in, usually Persian. Write code, identifiers, commits, and repository docs in English.
- Be direct and structured: headings, bullets, and bold key points. Lead with the answer. Flag risks and uncertainty explicitly. No filler.
- When reporting work, state what changed, how it was verified, and what is next.
