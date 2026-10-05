# deploy/

Host configuration for running Shipyard on a single VPS. `install.sh` puts every file below in place (P5.7b): run `sudo deploy/install.sh --dry-run` to see what it would do. It has been run in dry-run mode only so far; the first real install is on a fresh VPS (P5.9).

| File | Installs to | Purpose |
| --- | --- | --- |
| `install.sh` | Run from here, as root | Installs or upgrades Shipyard on Ubuntu or Debian. It installs Docker Engine and PostgreSQL 18 from their apt repositories when missing `[DK-INSTALL][PG-APT]`. It creates the users, directories, database role and an HPKE KEK. It installs the binaries, units, sysctl and firewall, runs the migrations, starts the services, tries a rootless build, and prints the first admin token. Running it again upgrades: it keeps `shipyard.env`, the KEKs and the data |
| `shipyard.env.example` | `/etc/shipyard/shipyard.env` (root:shipyard, `0640`) | Environment for both services. Contains the DB password |
| `systemd/shipyard-api.service` | `/etc/systemd/system/` | API as user `shipyard-api`, **not** in the `docker` group, listening on a Unix socket |
| `systemd/shipyard-worker.service` | `/etc/systemd/system/` | Worker as user `shipyard-worker`, in the `docker` group, which is root-equivalent |
| `systemd/shipyard-backup.service`, `systemd/shipyard-backup.timer` | `/etc/systemd/system/` | Nightly `shipyard-worker backup` as user `shipyard-worker` (ADR-0006). Create `/var/backups/shipyard` and `/var/backups/shipyard-kek` first, owned by `shipyard-worker`, mode `0700`, then `systemctl enable --now shipyard-backup.timer` |
| `docker/daemon.json` | `/etc/docker/daemon.json` | `local` log driver (rotates by default) and `live-restore` `[DK-LOG][DK-LIVE]` |
| `sysctl/60-shipyard-buildkit.conf` | `/etc/sysctl.d/` (Ubuntu 24.04+ only), then `sudo sysctl --system` | Lets the rootless BuildKit builder create user namespaces (ADR-0010) `[UB-USERNS][BK-ROOTLESS]`. The Shipyard units set `RestrictNamespaces=yes` in return |
| `firewall/shipyard-firewall.sh`, `systemd/shipyard-firewall.service` | `/usr/local/lib/shipyard/` (mode `0755`), `/etc/systemd/system/`, then `systemctl enable --now shipyard-firewall` | Builds (bridges `sybuild-*`) cannot reach the host's services or cloud metadata (ADR-0009). Needs Docker's iptables firewall backend. `shipyard-firewall.sh status` shows the rules |
| `prometheus/shipyard-alerts.yml` | Your Prometheus server's `rule_files` | Alerts at 80% of a disk or of a certificate's lifetime, and on app health, the database, failing deploys, and a stuck queue (ADR-0013, ADR-0014). Needs `SHIPYARD_WORKER_METRICS_LISTEN` (and the API's for its metrics) scraped. Check with `promtool check rules` |
| `caddy/caddy.json` | Caddy container bootstrap config | Admin API on a permissioned Unix socket only `[CADDY-API]` |
| `dev/compose.yaml` | Nowhere (development only) | Local PostgreSQL 18 for `make dev-up` |

## Decisions still open (tracked in the roadmap)

- **P2.1 Caddy container user and group.** The admin socket uses mode `0220` (owner and group may connect). The Caddy container must run with a group that the worker also belongs to, and `/run/caddy-admin` must be a bind mount shared with the host.
- **P2.1 Config persistence.** Choose between Caddy `--resume` (serve the last config after a Caddy restart) and re-render from the DB only. Either way the DB stays the source of truth.

Settled: **P2.8 API socket.**
- The API listens on `unix:/run/shipyard-api/api.sock`, with `SHIPYARD_API_LISTEN` set in `shipyard.env` because both services read it.
- The worker mounts that directory read-only into the Caddy container, which runs with the `shipyard` group.
- Set `SHIPYARD_API_HOSTNAME` to publish the API over HTTPS. Point its A/AAAA records at the server first.

## Invariants these files must keep

- Only Caddy publishes host ports. PostgreSQL and the API never do `[DK-FW]`.
- Only `shipyard-worker` is in the `docker` group.
- `shipyard.env` and KEK files are never committed.
