# deploy/

Host configuration for running Shipyard on a single VPS. **This is a skeleton (P0.5).** The installer and operator guide come in P5.7. Until then, treat these files as reference, not a tested install.

| File | Installs to | Purpose |
| --- | --- | --- |
| `shipyard.env.example` | `/etc/shipyard/shipyard.env` (root:shipyard, `0640`) | Environment for both services. Contains the DB password |
| `systemd/shipyard-api.service` | `/etc/systemd/system/` | API as user `shipyard-api`, **not** in the `docker` group, listening on a Unix socket |
| `systemd/shipyard-worker.service` | `/etc/systemd/system/` | Worker as user `shipyard-worker`, in the `docker` group (root-equivalent) and the `shipyard-edge` group |
| `tmpfiles/shipyard.conf` | `/etc/tmpfiles.d/` | Caddy's admin directory `/run/shipyard/caddy`, `root:shipyard-edge`, mode `2770`, recreated at boot `[SYSTEMD-TMPFILES]` |
| `docker/daemon.json` | `/etc/docker/daemon.json` | `local` log driver (rotates by default) and `live-restore` `[DK-LOG][DK-LIVE]` |
| `caddy/caddy.json` | Caddy container bootstrap config | Admin API on a permissioned Unix socket only `[CADDY-API]` |
| `dev/compose.yaml` | Nowhere (development only) | Local PostgreSQL 18 for `make dev-up` |

## Users and groups

| Account | Primary group | Supplementary groups | Reaches |
| --- | --- | --- | --- |
| `shipyard-api` | `shipyard` | none | PostgreSQL, the KEKs, the worker's log socket |
| `shipyard-worker` | `shipyard` | `docker`, `shipyard-edge` | Docker, Caddy's admin and verify sockets, the API socket's group |

- `shipyard` is shared on purpose: the API reads the worker's log socket through it (ADR-0008), and Caddy reaches the API socket through it (P2.8).
- `shipyard-edge` is the worker's alone: it owns Caddy's admin directory, so the API cannot reach Caddy's admin API (invariants 1 and 12, ADR-0003). The worker refuses to start if `SHIPYARD_CADDY_GROUP` is its primary group or a group it is not in.

## Install steps (manual, until P5.7)

Run as root, after Docker and PostgreSQL are installed:

```bash
groupadd --system shipyard
groupadd --system shipyard-edge
useradd --system --no-create-home --shell /usr/sbin/nologin --gid shipyard shipyard-api
useradd --system --no-create-home --shell /usr/sbin/nologin --gid shipyard --groups docker,shipyard-edge shipyard-worker
install -m 0644 deploy/tmpfiles/shipyard.conf /etc/tmpfiles.d/shipyard.conf
systemd-tmpfiles --create /etc/tmpfiles.d/shipyard.conf
install -m 0644 deploy/systemd/shipyard-api.service deploy/systemd/shipyard-worker.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now shipyard-api shipyard-worker
```

`SupplementaryGroups=` extends the groups in `/etc/group`, it does not replace them `[SYSTEMD-EXEC]`. The worker unit grants `docker` and `shipyard-edge` either way, but the API gets every group `/etc/group` gives `shipyard-api`. So **never add `shipyard-api` to `shipyard-edge` or `docker`**.

**Upgrading an existing install:** create `shipyard-edge`, install the tmpfiles file and run `systemd-tmpfiles --create`, install the new worker unit, then `systemctl daemon-reload && systemctl restart shipyard-worker`. The edge container is recreated with the new group, since the group is part of its spec.

**Check the boundary** (the first command must fail with "Permission denied"):

```bash
sudo -u shipyard-api curl -s --unix-socket /run/shipyard/caddy/caddy-admin.sock http://localhost/config/
sudo -u shipyard-worker curl -s --unix-socket /run/shipyard/caddy/caddy-admin.sock http://localhost/config/ | head -c 80
stat -c '%U:%G %a' /run/shipyard/caddy /run/shipyard/caddy/caddy-admin.sock
```

Expected `stat` output: `root:shipyard-edge 2770`, then `root:shipyard-edge 660`.

## Settled decisions

- **P2.1 Caddy user and group.** Caddy runs as uid 0 with gid `shipyard-edge`, `--cap-drop ALL` plus `NET_BIND_SERVICE`. Its admin socket is `/run/shipyard/caddy/caddy-admin.sock`, mode `0660`, in that directory, bind-mounted at the same path.
- **P2.1 Config persistence.** `caddy run --resume` with a persistent `/config` volume; the DB stays the source of truth, and the worker re-renders from it.
- **P2.8 API socket.**
  - The API listens on `unix:/run/shipyard-api/api.sock`, with `SHIPYARD_API_LISTEN` set in `shipyard.env` because both services read it.
  - The worker mounts that directory read-only into the Caddy container, which gets the `shipyard` group as a supplementary group for it.
  - Set `SHIPYARD_API_HOSTNAME` to publish the API over HTTPS. Point its A/AAAA records at the server first.

## Invariants these files must keep

- Only Caddy publishes host ports. PostgreSQL and the API never do `[DK-FW]`.
- Only `shipyard-worker` is in the `docker` group.
- Only `shipyard-worker` is in the `shipyard-edge` group.
- `shipyard.env` and KEK files are never committed.
