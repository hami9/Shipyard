# deploy/

Host configuration for running Shipyard on a single VPS. **This is a skeleton (P0.5).** The installer and operator guide come in P5.7. Until then, treat these files as reference, not a tested install.

| File | Installs to | Purpose |
| --- | --- | --- |
| `shipyard.env.example` | `/etc/shipyard/shipyard.env` (root:shipyard, `0640`) | Environment for both services. Contains the DB password |
| `systemd/shipyard-api.service` | `/etc/systemd/system/` | API as user `shipyard-api`, **not** in the `docker` group, listening on a Unix socket |
| `systemd/shipyard-worker.service` | `/etc/systemd/system/` | Worker as user `shipyard-worker`, in the `docker` group, which is root-equivalent |
| `docker/daemon.json` | `/etc/docker/daemon.json` | `local` log driver (rotates by default) and `live-restore` `[DK-LOG][DK-LIVE]` |
| `caddy/caddy.json` | Caddy container bootstrap config | Admin API on a permissioned Unix socket only `[CADDY-API]` |
| `dev/compose.yaml` | Nowhere (development only) | Local PostgreSQL 18 for `make dev-up` |

## Decisions still open (tracked in the roadmap)

- **P2.1 Caddy container user and group.** The admin socket uses mode `0220` (owner and group may connect). The Caddy container must run with a group that the worker also belongs to, and `/run/caddy-admin` must be a bind mount shared with the host.
- **P2.1 Config persistence.** Choose between Caddy `--resume` (serve the last config after a Caddy restart) and re-render from the DB only. Either way the DB stays the source of truth.
- **P2.8 API socket.** Bind-mount `/run/shipyard-api` into the Caddy container and align group ownership.

## Invariants these files must keep

- Only Caddy publishes host ports. PostgreSQL and the API never do `[DK-FW]`.
- Only `shipyard-worker` is in the `docker` group.
- `shipyard.env` and KEK files are never committed.
