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
