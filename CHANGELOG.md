# Changelog

All notable changes to Shipyard are recorded here.

- **Format:** [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).
- **Versioning:** [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).
- **Before 1.0**, a minor bump (`0.x.0`) may contain breaking changes.
- **Release notes:** each GitHub Release uses the section for its tag (`scripts/release-notes.sh`).

## [Unreleased]

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
