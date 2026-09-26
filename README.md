# Shipyard

[![CI](https://github.com/hami9/Shipyard/actions/workflows/ci.yml/badge.svg)](https://github.com/hami9/Shipyard/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/hami9/Shipyard?include_prereleases&sort=semver)](https://github.com/hami9/Shipyard/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A self-hosted deployment platform for a single VPS. Point it at a GitHub repository that has a Dockerfile, and it builds, health-checks, and serves the app over HTTPS. You also get deployment history and one-command rollback.

> **Status:** Phase 0 (bootstrap) in progress. The binaries build, the migrations run, and `/healthz` and `/readyz` work. See the [roadmap](docs/ROADMAP.md) and [local development](docs/DEVELOPMENT.md).
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

## Install

> The latest release is [`v0.1.0`](https://github.com/hami9/Shipyard/releases/tag/v0.1.0), the Phase 0 bootstrap. It does not deploy apps yet ([version plan](docs/RELEASING.md#version-plan)).

- **Binaries:** download from [Releases](https://github.com/hami9/Shipyard/releases). The CLI is available for Linux, macOS, and Windows. `shipyard-server` (API and worker, with systemd units) is available for Linux amd64 and arm64. Verify downloads with `checksums.txt` and `gh attestation verify`.
- **Container image:** `ghcr.io/hami9/shipyard:<version>` (linux/amd64, linux/arm64), for the CLI and for evaluation. The image has no entrypoint, so name the binary: `docker run --rm ghcr.io/hami9/shipyard:v0.1.0 shipyard version`.
- **From source:** see [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

The supported production setup is the two systemd services in [deploy/](deploy/README.md). A full installer arrives in Phase 5.

## Documentation

| Doc | Purpose |
| --- | --- |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | System design (v2, source-verified) |
| [docs/architecture-review.md](docs/architecture-review.md) | What changed from the v1 proposal, and why |
| [docs/SOURCES.md](docs/SOURCES.md) | Primary sources behind every external claim |
| [docs/adr/](docs/adr/) | Architecture decision records |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Phased delivery plan with exit criteria |
| [docs/WORKLOG.md](docs/WORKLOG.md) | Session-by-session work log and current status |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | Local setup, testing, and troubleshooting |
| [docs/RELEASING.md](docs/RELEASING.md) | Release process, artifacts, and version plan |
| [CHANGELOG.md](CHANGELOG.md) | Notable changes per release |
| [CLAUDE.md](CLAUDE.md) | Instructions for AI coding agents (system prompt) |

## Stack

Go · PostgreSQL 18 · Docker Engine 29 (BuildKit) · Caddy v2 · optional React UI

## Security

Report vulnerabilities privately. See [SECURITY.md](SECURITY.md).

## Contributing

1. Read [CLAUDE.md](CLAUDE.md). The invariants and conventions apply to humans too.
2. Pick the next unchecked task in the active phase of the [roadmap](docs/ROADMAP.md).
3. Add a [work log](docs/WORKLOG.md) entry for every session.
4. Commit subjects are 1–2 words. Branch names are 1–3 words, in kebab-case.

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

[Apache License 2.0](LICENSE). Copyright 2026 The Shipyard Authors.
