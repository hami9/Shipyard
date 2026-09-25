# ADR-0007: MVP trust model, a single admin and trusted repositories

- **Status:** Proposed
- **Date:** 2026-09-25
- **Sources:** `DK-SEC`, `DK-POSTINSTALL`, `DK-ROOTLESS`, `GH-FORKS`

## Context

Only trusted users should control the Docker daemon, and `docker` group membership is root-equivalent `[DK-SEC][DK-POSTINSTALL]`. Containers on one host share a kernel. v1 asked whether the first release supports one administrator or several trusted users.

## Decision

- **The MVP supports a single administrator.** Access is through scoped, expiring API tokens (`shp_` prefix, SHA-256 hash stored, plaintext shown once).
- The schema still carries `owner_id` and token scopes, so that multi-user RBAC (Phase 7) needs no data migration.
- **Only repositories the admin explicitly registers can be deployed.** Deployed SHAs must be ancestors of the tracked branch `[GH-FORKS]`.
- Shipyard is documented as **not** a sandbox for untrusted tenants, in the README, the installer output, and the operator guide.
- Rootless Docker `[DK-ROOTLESS]`, builder egress control, and gVisor-style runtimes are Phase 5 evaluations, not MVP requirements.

## Consequences

- **Positive:** The simplest authorization model. Security effort goes into the real boundaries: the API/worker split, secrets, webhooks, and containers.
- **Negative:** No team use until Phase 7. Anyone with the admin token effectively controls the host through deployments.
- **Follow-ups:** P1.3 (token auth), P5.x (hardening evaluations), P7.1 (multi-user RBAC).

## Alternatives considered

- **Multi-user from day one:** adds an authorization surface before the core deploy loop is proven.
- **Public multi-tenant hosting:** out of scope. It needs VM-level isolation.
