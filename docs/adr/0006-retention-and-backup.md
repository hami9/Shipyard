# ADR-0006: Retention defaults and backup/restore procedure

- **Status:** Accepted
- **Date:** 2026-09-25 (proposed and accepted)
- **Deciders:** Project owner
- **Sources:** `DK-LOG`, `DK-LOG-LOCAL`, `CADDY-HTTPS`, `LE-LIMITS`, `OWASP-CRYPTO`, `PG-VERSIONS`

## Context

- A single VPS has limited disk. Images, the build cache, and container logs grow without bound unless capped. `json-file` logs never rotate by default `[DK-LOG]`.
- Rollback depends on retained images.
- Recovery depends on the database, the KEK (stored separately `[OWASP-CRYPTO]`), and Caddy's certificate storage. Re-issuing certificates at scale can hit Let's Encrypt limits `[LE-LIMITS][CADDY-HTTPS]`.

## Decision

**Retention defaults (all configurable)**

| Item | Default |
| --- | --- |
| Deployment rows, operation rows, audit events | Kept indefinitely (small) |
| Operation events (build/deploy logs) | Last 20 operations per app, max 5 MB each |
| Images | Images of the last **5** successful deployments per app, plus the active one |
| BuildKit cache | Pruned daily down to a cap (default 10 GB) |
| Container logs | `local` driver defaults: 5 × 20 MB per container, compressed `[DK-LOG-LOCAL]` |
| Stopped previous containers | Removed after the observation window (default 5 min) |

A disk-usage alarm fires at 80% of the Docker data root filesystem.

*Note 2026-09-30 (P3.4a):* "the last 5 successful deployments" counts distinct images, ranked by when each last served, so rolling back and forth does not use up the rollback depth. Images of deployments in progress and a pending rollback's target are kept too. Image retention runs on every reconcile pass; the BuildKit prune stays daily. The decision is unchanged.

*Note 2026-09-30 (P3.4b):* the 5 MB per operation is enforced by trimming the middle of a finished operation's log (warnings and errors stay, and a marker names what went), not by refusing writes. Sizes are binary (10 GiB, 5 MiB).

**Backup**

| What | How | Where | Frequency |
| --- | --- | --- | --- |
| PostgreSQL | `pg_dump -Fc` | Off-host location A | Nightly, 14 dailies + 8 weeklies |
| KEK files | Copy of `/etc/shipyard/kek/` | Off-host location B, **separate from A** | On every change |
| Caddy data dir | Tarball | Off-host location A | Nightly |
| Shipyard config | `/etc/shipyard/shipyard.env` (contains the DB password; encrypt the copy) | Off-host location A | On change |

*Note 2026-10-01 (P3.5), decided with the owner:*

- The backup is a worker subcommand (`shipyard-worker backup`) run by a systemd timer, not shell scripts: it reuses the validated configuration and is unit-tested.
- "Off-host location A/B" are two local directories, each with an optional hook command that copies it away (rsync, rclone, restic, or a mounted remote). Shipyard depends on none of those tools.
- The KEKs are copied on every run (nightly), which covers "on every change" within a day. Target B only gains keys.
- `shipyard.env` is not backed up yet: encrypting the copy needs a tool and a key that P3.5 does not add. P3.6 documents recreating it.

*Note 2026-10-01 (P3.6):* the restore is `shipyard-worker restore` plus [RESTORE.md](../RESTORE.md). The drill is automated on one machine (`TestRestoreDrill`). The drill on a fresh VPS, the production-ready gate below, is still to be run by the owner.

Images are not backed up in the MVP. After a restore, active apps are rebuilt from their recorded SHAs, and rollback history before the restore becomes unavailable until an optional registry exists (post-MVP).

**Production-ready gate:** a documented restore drill onto a fresh VPS must succeed. The drill restores the database, KEK, and Caddy data, then the reconciler brings every app back to its active SHA. It is repeated each release.

## Consequences

- **Positive:** Bounded disk. A tested recovery path. No certificate storm after a restore.
- **Negative:** Rollback depth after a host loss is limited to rebuildable SHAs. Two backup destinations to manage.
- **Follow-ups:** P3.4 (retention job), P3.5 (backup scripts), P3.6 (restore drill), P5.6 (disk metrics and alerts).

## Alternatives considered

- **WAL archiving / PITR (`pg_basebackup`):** better RPO, but more moving parts. Revisit after 1.0.
- **Pushing images to a registry for durability:** post-MVP (P7).
