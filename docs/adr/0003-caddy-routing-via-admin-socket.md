# ADR-0003: Caddy routing via full-config load on a Unix admin socket

- **Status:** Accepted
- **Date:** 2026-09-25 (proposed and accepted)
- **Deciders:** Project owner
- **Sources:** `CADDY-API`, `CADDY-OPTIONS`, `CADDY-HTTPS`, `CADDY-RP`, `LE-LIMITS`, `DK-FW`, `DK-BRIDGE`

## Context

- Caddy terminates TLS and must reach app containers on their private networks. App containers therefore share networks with Caddy.
- Caddy's admin API defaults to `localhost:2019`. Caddy recommends a permissioned Unix socket when untrusted code runs on the host `[CADDY-API][CADDY-OPTIONS]`.
- Docker-published ports bypass ufw `[DK-FW]`.
- Let's Encrypt limits authorization failures: 5 per identifier per account per hour `[LE-LIMITS]`.

## Decision

- **Topology:**
  - Run Caddy as a container that publishes 80/tcp, 443/tcp, and 443/udp. It is the **only** container with published ports.
  - Connect Caddy to each app's user-defined bridge network `shipyard-app-<slug>` `[DK-BRIDGE]`.
  - Mount the data directory as a persistent volume `[CADDY-HTTPS]`.
- **Admin API:**
  - Configure `admin unix//run/shipyard/caddy-admin.sock` (mode `0660`, group `shipyard-worker`). No TCP admin listener exists.
  - The API process has no access to the socket.
- **Config ownership:**
  - The worker renders the **complete** Caddy JSON config from the `routes` table: apps, the Shipyard API upstream, and `/hooks/github`.
  - The worker applies it with `POST /load` and `If-Match: <etag>`. Caddy applies it atomically without downtime or rolls back `[CADDY-API]`.
  - Every change goes through this path, and nothing patches Caddy by hand.
- **Verification:** after a load, request the route through Caddy with the app's `Host` header and require the health response before committing the new active deployment.
- **Domains:**
  - One app per hostname (unique constraint).
  - A DNS preflight requires the A/AAAA records to resolve to the host's public IPs before a route is created.
  - An optional allow-list of domain suffixes restricts what can be claimed.
  - On-demand TLS stays **off**. If it is ever enabled, it must use an `ask` endpoint `[CADDY-HTTPS]`.
  - Development and CI use the Let's Encrypt staging CA `[LE-LIMITS]`.
- SSE through Caddy needs no special config, because `text/event-stream` is flushed immediately `[CADDY-RP]`.

## Consequences

- **Positive:**
  - Routing state is reproducible from the database, so reconciliation is re-render and load.
  - App containers cannot reach the admin API.
  - Rollback of a bad config is built into Caddy.
- **Negative:**
  - Caddy joins N networks. The Docker network address pools must be sized as the app count grows.
  - Every config change is a full reload, which is cheap at single-VPS scale.
- **Follow-ups:** P2.2 (config renderer with golden tests), P2.3 (admin socket client), P2.5 (DNS preflight), P3.x (Caddy data backup).

## Alternatives considered

- **Caddy on the host (systemd):** workable, since the host can reach bridge IPs, but it loses name-based upstreams and couples Caddy to host networking. Kept as a fallback.
- **Traefik with Docker labels:** Traefik would need Docker socket access, which violates the worker-only Docker boundary.
- **Incremental `PATCH /config/...` updates:** more moving parts, and drift is harder to detect than with a full render.
