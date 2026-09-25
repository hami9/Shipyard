# ADR-0001: Go codebase, API and worker as separate processes

- **Status:** Proposed
- **Date:** 2026-09-25
- **Sources:** `GO-REL`, `GO-ROUTING`, `DK-29`, `DK-POSTINSTALL`

## Context

Shipyard needs an HTTP API, a long-running deployment worker, and a CLI. The API is internet-facing through Caddy. The worker needs Docker access, which is root-equivalent `[DK-POSTINSTALL]`. The Docker Engine's first-party Go client is `github.com/moby/moby/client` `[DK-29]`.

## Decision

- Use **one Go module** that produces three binaries: `shipyard` (CLI), `shipyard-api`, and `shipyard-worker`.
- Run the API and the worker as **separate systemd services under separate Unix users**. Only the worker's user joins the `docker` group.
- The processes communicate **only through PostgreSQL** (ADR-0002).
- Use the standard library first: `net/http` routing with method and wildcard patterns `[GO-ROUTING]`, and `log/slog`.
- Target a supported Go release (≥ 1.26 at the time of writing) `[GO-REL]`.

## Consequences

- **Positive:**
  - A compromised API process cannot reach Docker.
  - The code is shared, so domain types and migrations exist once.
  - Deployment is simple: two services and one binary set.
- **Negative:** Two services to supervise, and a slight duplication of config loading.
- **Follow-ups:** P0.1 (module and `cmd/` skeleton), P0.5 (systemd units under `deploy/`).

## Alternatives considered

- **Single process:** simpler, but it puts Docker's root-equivalent power into the internet-facing process.
- **Separate repositories or services in different languages:** no benefit at this scale.
EOF
