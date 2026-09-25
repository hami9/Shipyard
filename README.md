# Shipyard

A self-hosted deployment platform for a single VPS. Point it at a GitHub repository that has a Dockerfile, and it builds, health-checks, and serves the app over HTTPS. You also get deployment history and one-command rollback.

> **Status:** design complete, implementation not started (Phase 0). See the [roadmap](docs/ROADMAP.md).
>
> **Trust model:** Shipyard is for trusted operators and trusted repositories. It is **not** a sandbox for untrusted tenants ([ADR-0007](docs/adr/0007-mvp-trust-model.md)).

## How it works

```text
git push ──► webhook (HMAC-verified) ──► PostgreSQL (operation queue)
                                              │
                              shipyard-worker ┘
                                 ├─ fetch exact SHA (must be on the tracked branch)
                                 ├─ build on a resource-limited BuildKit builder
                                 ├─ start hardened candidate container
                                 ├─ health check
                                 └─ switch Caddy route (atomic) ──► HTTPS traffic
```

A failed build or health check never touches the release that is currently serving. Rollback restarts a retained image and its original configuration. It never rebuilds.

## Documentation

| Doc | Purpose |
| --- | --- |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | System design (v2, source-verified) |
| [docs/architecture-review.md](docs/architecture-review.md) | What changed from the v1 proposal, and why |
| [docs/SOURCES.md](docs/SOURCES.md) | Primary sources behind every external claim |
| [docs/adr/](docs/adr/) | Architecture decision records |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Phased delivery plan with exit criteria |
| [docs/WORKLOG.md](docs/WORKLOG.md) | Session-by-session work log and current status |
| [CLAUDE.md](CLAUDE.md) | Instructions for AI coding agents (system prompt) |

## Stack

Go · PostgreSQL 18 · Docker Engine 29 (BuildKit) · Caddy v2 · optional React UI

## Contributing

1. Read [CLAUDE.md](CLAUDE.md). The invariants and conventions apply to humans too.
2. Pick the next unchecked task in the active phase of the [roadmap](docs/ROADMAP.md).
3. Add a [work log](docs/WORKLOG.md) entry for every session.
4. Commit subjects are 1–2 words. Branch names are 1–3 words, in kebab-case.
