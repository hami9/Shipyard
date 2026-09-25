# ADR-0004: Dedicated BuildKit builder; image ID as release identity

- **Status:** Proposed
- **Date:** 2026-09-25
- **Sources:** `DK-BUILDKIT`, `DK-BX-CONTAINER`, `DK-BX-BUILD`, `DK-BUILD-SECRETS`, `DK-29`, `DK-CONTAINERD`, `GH-FORKS`

## Context

- Dockerfiles are untrusted code. BuildKit is Docker's default builder `[DK-BUILDKIT]`.
- A `docker-container` buildx builder can be capped with `memory` and `cpu-quota` driver options, but its results are not loaded into the image store without `--load` `[DK-BX-CONTAINER]`.
- Whether a local manifest digest exists depends on the image store backend. The containerd store is the default only on fresh Engine 29 installs `[DK-29][DK-CONTAINERD]`.
- Fork commits are fetchable from the upstream repository `[GH-FORKS]`.

## Decision

- **Scope:** Dockerfile builds only. The repository must contain the Dockerfile at `dockerfile_path` (default `Dockerfile`) inside `build_context` (default `.`). Both paths are validated to stay inside the checkout after resolving symlinks.
- **Source:**
  - Fetch the exact SHA into a fresh per-operation workspace.
  - **Require `git merge-base --is-ancestor <sha> origin/<branch>`** before building.
  - Credentials go only in a git HTTP header for the fetch, never in the URL, workspace, or image.
- **Builder:**
  - A dedicated buildx builder named `shipyard` (`docker-container` driver), created at install with `--driver-opt memory=<M>,cpu-quota=<Q>`.
  - Builds run with `--load`, a hard deadline (default 15 min), and bounded log capture (default 5 MB per build).
  - One build at a time by default.
- **Secrets:** no Shipyard credentials are passed as build args or environment variables `[DK-BUILD-SECRETS]`. If private build dependencies are supported later, they use `--secret` mounts.
- **Network:** builds keep network access in the MVP. Egress restriction (a proxy or allow-list) is Phase 5 work.
- **Identity:**
  - Tag images `shipyard/<slug>:<sha12>` for humans, and label them `io.shipyard.app`, `io.shipyard.commit`, and `io.shipyard.deployment`.
  - Persist the Engine **image ID** from image inspect. All container creation uses the image ID.
  - Persist the `--metadata-file` output (`containerimage.digest`, `containerimage.config.digest`) as provenance `[DK-BX-BUILD]`.

## Consequences

- **Positive:**
  - Resource-bounded builds that cannot starve the running apps.
  - Rollback artifacts are unambiguous.
  - No dependency on a registry.
- **Negative:**
  - Build network access is unrestricted in the MVP (documented risk).
  - Local-only images mean losing the host means losing the rollback artifacts, until a registry is added (post-MVP).
- **Follow-ups:** P1.8 (fetch and ancestry), P1.9 (builder and metadata), P5.1 (egress control), P7 (optional registry push).

## Alternatives considered

- **The default `docker` build driver:** no builder-level resource caps.
- **Buildpacks or Nixpacks:** out of MVP scope.
- **Kaniko or rootless BuildKit:** a stronger isolation option to evaluate in Phase 5.
