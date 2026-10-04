# Architecture Decision Records

An ADR records one significant decision: its context, the choice made, and its consequences. ADRs are immutable once `Accepted`. To change a decision, write a new ADR that supersedes the old one.

- Copy [0000-template.md](0000-template.md) and use the next number.
- Reference sources by tag from [../SOURCES.md](../SOURCES.md).
- Link the ADR from the WORKLOG entry of the session that wrote it.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-go-two-process-monolith.md) | Go codebase, API and worker as separate processes | Accepted |
| [0002](0002-postgresql-operation-queue.md) | PostgreSQL as the durable operation queue | Accepted |
| [0003](0003-caddy-routing-via-admin-socket.md) | Caddy routing via full-config load on a Unix admin socket | Accepted |
| [0004](0004-image-build-and-identity.md) | Dedicated BuildKit builder; image ID as release identity | Accepted |
| [0005](0005-secrets-envelope-encryption.md) | Envelope encryption for app configuration | Accepted |
| [0006](0006-retention-and-backup.md) | Retention defaults and backup/restore procedure | Accepted |
| [0007](0007-mvp-trust-model.md) | MVP trust model: single admin, trusted repositories | Accepted |
| [0008](0008-worker-log-socket.md) | App logs through a worker log socket | Accepted |
| [0009](0009-builder-egress-deferred.md) | Builder egress control deferred; the builder gets its own network | Accepted |
| [0010](0010-rootless-buildkit.md) | Rootless BuildKit for builds; the Docker daemon stays rootful | Accepted |
| [0011](0011-api-limits-and-token-management.md) | Per-client API limits, and token rotation and revocation without SSH | Accepted |
| [0012](0012-kek-rotation-and-asymmetric-sealing.md) | KEK rotation by re-wrapping, and asymmetric sealing so the API cannot decrypt | Accepted |
