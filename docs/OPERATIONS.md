# Operations

How to install, configure, upgrade, back up, monitor, and troubleshoot Shipyard on one server.

**Status.** `deploy/install.sh` has been run in dry-run mode only. The first real install on a fresh VPS is the Phase 5 acceptance demo (P5.9, [ACCEPTANCE.md](ACCEPTANCE.md)). Report anything here that does not match your server.

## Requirements

| Item | Requirement |
| --- | --- |
| Server | One VPS, Ubuntu 22.04, 24.04 or 26.04 LTS, amd64 or arm64 `[DK-INSTALL]`. The installer also accepts Debian, using Docker's and PostgreSQL's Debian repositories; that path is untested |
| Size | 2 CPUs and 4 GB of memory leave room for one build (2 GB by default, `SHIPYARD_BUILDER_MEMORY`) next to the apps |
| Disk | Images, the build cache (10 GB by default, `SHIPYARD_BUILD_CACHE_MAX`), logs, and backups. Start with 40 GB and watch the alerts |
| Network | A public IP, and ports 80 and 443 reachable from the internet |
| DNS | An A (and AAAA) record per hostname, pointing at the server, before the hostname is added |
| Off-host storage | Two places for backups that are not this server: one for the database and certificates, one for the keys (ADR-0006) |

Shipyard is for trusted operators and trusted repositories (ADR-0007). Anyone who can deploy can run code on the server during a build.

## Install

1. **Download and verify** the `shipyard-server` archive for your architecture from [Releases](https://github.com/hami9/Shipyard/releases), with `checksums.txt` ([RELEASING.md](RELEASING.md#verifying-a-release-for-users)):

   ```bash
   sha256sum --ignore-missing -c checksums.txt
   ```

   ```bash
   tar -xzf shipyard-server_X.Y.Z_linux_amd64.tar.gz
   ```

2. **See what the installer will do**, without changing anything:

   ```bash
   sudo shipyard-server_X.Y.Z_linux_amd64/deploy/install.sh --dry-run
   ```

3. **Install.** `--public-ip` lets you add domains (repeat it for IPv6). `--api-hostname` publishes the API over HTTPS. Both, and `--acme-email`, seed `/etc/shipyard/shipyard.env`:

   ```bash
   sudo shipyard-server_X.Y.Z_linux_amd64/deploy/install.sh --public-ip 203.0.113.10 --api-hostname shipyard.example.com --acme-email ops@example.com
   ```

   What it does, in order:
   - **Packages:** Docker Engine (with buildx) and PostgreSQL 18 from their vendors' apt repositories, if they are missing `[DK-INSTALL][PG-APT]`. Docker's `daemon.json` (local log driver, live-restore) is installed if absent; an existing one is reported, not changed.
   - **Users:** `shipyard-api` (no Docker access) and `shipyard-worker` (in the `docker` group, which is root-equivalent `[DK-POSTINSTALL]`), group `shipyard`.
   - **Files:** the binaries in `/usr/local/bin`, the systemd units, the build firewall (ADR-0009), and on Ubuntu 24.04+ the user-namespace sysctl for rootless BuildKit (ADR-0010).
   - **First install only:** `shipyard.env` with a random database password, the `shipyard` role and database, and an HPKE key pair `k1` in `/etc/shipyard/kek`. With it, the API can seal secrets but not read them (ADR-0012).
   - **Every run:** migrations, then the services and the nightly backup timer, then one rootless test build.
   - **First install only, at the end:** an admin API token, **printed once**.

4. **Copy `/etc/shipyard/kek` off the server now.** Without these keys nobody can decrypt the apps' secrets, backups included.

5. **Allow the web ports** if `ufw` is active. Published container ports bypass ufw anyway, but the rules document intent `[DK-FW]`:

   ```bash
   sudo ufw allow 22/tcp && sudo ufw allow 80/tcp && sudo ufw allow 443/tcp && sudo ufw allow 443/udp
   ```

6. **Connect the CLI** from your workstation, pasting the token on stdin:

   ```bash
   shipyard login --url https://shipyard.example.com
   ```

   ```bash
   shipyard whoami
   ```

7. **Deploy an app:**

   ```bash
   shipyard app create web --repo OWNER/NAME --branch main --port 8080 --health-path /healthz
   ```

   ```bash
   shipyard domain add web www.example.com
   ```

   ```bash
   shipyard deploy web --follow
   ```

## Configuration

Both services read `/etc/shipyard/shipyard.env` (root:shipyard, `0640`). Every setting is described in [deploy/shipyard.env.example](../deploy/shipyard.env.example). After a change, restart:

```bash
sudo systemctl restart shipyard-api shipyard-worker
```

The settings you are most likely to touch:

| Setting | What for |
| --- | --- |
| `SHIPYARD_PUBLIC_IPS` | Adding a domain requires its DNS records to point here (`SHIPYARD_DNS_PREFLIGHT`) |
| `SHIPYARD_API_HOSTNAME` | Publish the API at `https://<hostname>` |
| `SHIPYARD_GITHUB_WEBHOOK_SECRET_FILE` | Deploy on push (P4.1) |
| `SHIPYARD_GITHUB_APP_ID`, `SHIPYARD_GITHUB_APP_KEY_FILE` | Private repositories and deploy statuses in GitHub (P4.4, P4.6) |
| `SHIPYARD_BACKUP_HOOK`, `SHIPYARD_BACKUP_KEK_HOOK` | Copy each night's backup off the server |
| `SHIPYARD_WORKER_METRICS_LISTEN`, `SHIPYARD_API_METRICS_LISTEN` | Prometheus metrics (ADR-0013) |
| `SHIPYARD_RETAIN_IMAGES`, `SHIPYARD_BUILD_CACHE_MAX` | Disk use against rollback reach (ADR-0006) |

Worker commands that need its environment run like this. The examples below use this form:

```bash
sudo systemd-run --pipe --wait --collect --uid=shipyard-worker --gid=shipyard -p SupplementaryGroups=docker -p EnvironmentFile=/etc/shipyard/shipyard.env /usr/local/bin/shipyard-worker kek status
```

## Upgrade

1. **Read the release notes** in [CHANGELOG.md](../CHANGELOG.md) for the versions in between.
2. **Back up**, and wait for it to finish:

   ```bash
   sudo systemctl start shipyard-backup.service
   ```

3. **Run the new release's installer.** It keeps `shipyard.env`, the keys and the data. It replaces the binaries and units, migrates, restarts both services, and checks a build:

   ```bash
   sudo shipyard-server_NEW_linux_amd64/deploy/install.sh
   ```

- **Apps keep serving** during an upgrade: their containers are not restarted, only the two Shipyard services. An operation in progress resumes after the worker's restart (ADR-0002).
  - **Exception:** if the release changes the Caddy container (for example a newly pinned Caddy image, or the admin socket's own `shipyard-caddy` group from P5.8b; the changelog lists these), the worker recreates it at start, keeping its data. Traffic stops for the few seconds that takes.
- **Migrations are forward-only.** To go back to an older release, restore the backup from step 2.
- **Docker Engine upgrades** come from apt like any package. `live-restore` keeps containers running while the daemon restarts, but **only across patch releases** `[DK-LIVE]`. Across a minor or major Docker upgrade, containers stop with the daemon. The worker's reconciler starts each active release again within a minute, but expect a short outage per app: plan major Docker upgrades for a quiet hour.
- **PostgreSQL major upgrades** (beyond 18) are not handled by Shipyard. Use your distribution's tools (`pg_upgradecluster` on Debian and Ubuntu), after a backup.

## Backup and restore

- **What and when:** `shipyard-backup.timer` runs `shipyard-worker backup` nightly (ADR-0006, [ARCHITECTURE.md](ARCHITECTURE.md) Reliability):
  - **Target A** (`/var/backups/shipyard`): the database dump and Caddy's certificates. It keeps the newest backup of each of the last 14 days and 8 weeks.
  - **Target B** (`/var/backups/shipyard-kek`): the keys. Keys are only ever added.
- **Off-host:** set `SHIPYARD_BACKUP_HOOK` and `SHIPYARD_BACKUP_KEK_HOOK` to commands that copy `$SHIPYARD_BACKUP_PATH` elsewhere, to two different places. A backup that stays on the server does not survive the server.
- **Check the last run:**

  ```bash
  systemctl status shipyard-backup.service
  ```

  ```bash
  journalctl -u shipyard-backup.service -n 50
  ```

- **Restore** onto a new server: [RESTORE.md](RESTORE.md). It starts with `install.sh --for-restore`. Rehearse it before you need it.

## Keys and tokens

- **Rotate the key** that seals secrets (ADR-0012):
  1. Write a new pair as root. The private key goes to the worker:

     ```bash
     sudo env SHIPYARD_KEK_DIR=/etc/shipyard/kek shipyard-worker kek generate k2
     ```

     ```bash
     sudo chown shipyard-worker:shipyard /etc/shipyard/kek/k2.hpke
     ```

  2. Set `SHIPYARD_KEK_ACTIVE=k2` in `shipyard.env`, then restart both services.
  3. Move every value to it with `kek rewrap`, in the worker form above.
  4. When `kek status` shows `k1` unused, delete its files. The backups keep their copies.
- **API tokens:**
  - `shipyard token rotate [--grace 24h]` replaces the CLI's own token.
  - `shipyard token list` and `shipyard token revoke PREFIX` manage all tokens; they need the admin scope.
  - On the server, `shipyard-api token create|list|revoke|rotate` works without the API (ADR-0011).

## Monitoring

- **Logs:** both services log JSON to the journal.

  ```bash
  journalctl -u shipyard-worker -f
  ```

  Warnings worth a look:
  - "disk is over 80% full";
  - "certificate is over 80% through its lifetime";
  - "no certificate could be read";
  - "operation not finished".
- **Metrics:** set `SHIPYARD_WORKER_METRICS_LISTEN` and `SHIPYARD_API_METRICS_LISTEN` to two loopback ports (ADR-0013). Scrape them with a Prometheus server or agent on this server; they have no authentication.
- **Alerts:** load [deploy/prometheus/shipyard-alerts.yml](../deploy/prometheus/shipyard-alerts.yml) into Prometheus. It covers:
  - disks and certificates past 80% (ADR-0014);
  - unhealthy apps;
  - the database;
  - failing deploys;
  - a stuck queue.

  Check it with `promtool check rules`.
- **Health endpoints:** the API's `/healthz` (process) and `/readyz` (database).

## Troubleshooting

Start with the journal of the service involved, and with the events of the operation (`shipyard events OPERATION`).

| Symptom | Likely cause and fix |
| --- | --- |
| The installer's build check fails, or the worker logs "buildx builder" at start | Rootless BuildKit cannot create user namespaces. On Ubuntu 24.04+, check `sysctl kernel.apparmor_restrict_unprivileged_userns` is 0 (`deploy/sysctl/`) `[UB-USERNS]`. The installer names its log file (`/tmp/shipyard-build-check.*.log`) |
| `shipyard-firewall.service` fails: "nftables firewall backend" | Docker runs with its experimental nftables backend, which has no DOCKER-USER chain `[DK-NFTABLES]`. Use the default iptables backend (remove `firewall-backend` from `daemon.json`) |
| `domain add` is refused: preflight | `SHIPYARD_PUBLIC_IPS` is empty or wrong, or the hostname's A/AAAA records do not all point here yet. Fix DNS or the setting; `SHIPYARD_DNS_PREFLIGHT=false` skips the check |
| A hostname has no certificate | DNS does not point here, or ports 80/443 are blocked upstream (provider firewall). Look at the Caddy container's log: `docker logs shipyard-caddy`. Repeated failures can hit Let's Encrypt's rate limits `[LE-LIMITS]`; test with `SHIPYARD_CADDY_CA=staging` |
| A deploy fails at the health check | The app must listen on `0.0.0.0` at the app's `--port` and answer 2xx or 3xx on its health path within the timeout. `shipyard logs APP` shows its output |
| An app exits at start with a permission error | App containers drop every capability (invariant 10). Images that start as root and then switch users (for example stock nginx) need an unprivileged image or a non-root `USER` |
| An operation stays "running" | Its worker died. Its lease expires within `SHIPYARD_WORKER_LEASE` and the next reconcile pass retries it, up to its attempt limit |
| The disk fills up | Lower `SHIPYARD_RETAIN_IMAGES` or `SHIPYARD_BUILD_CACHE_MAX`, then wait for the next retention run (daily) or restart the worker. Check old backups in target A |
| "permission denied" on `/var/run/docker.sock` for the worker | `shipyard-worker` is not in the `docker` group: `sudo usermod -aG docker shipyard-worker`, then restart it |

## Uninstall

Shipyard keeps everything in a few places:
- **Services and files:** the services and units in `/etc/systemd/system/shipyard-*`, and `/usr/local/bin/shipyard*`, `/usr/local/lib/shipyard`, `/etc/shipyard`.
- **Data:** `/var/lib/shipyard*`, the `shipyard` database, and the backups in `/var/backups/shipyard*`.
- **Docker:** containers, images, networks and volumes labelled `io.shipyard.*`, and the `shipyard` buildx builder.

Removing them is destructive and cannot be undone. Take and copy a backup first, then remove what you no longer need by hand.
