# Restore

How to bring Shipyard back on a new server from its backups (ADR-0006). The backups are written by `shipyard-worker backup` ([ARCHITECTURE.md](ARCHITECTURE.md), Reliability).

**Status.** `shipyard-worker restore` and the recovery that follows it are tested end to end by an automated drill (`TestRestoreDrill`, below). The host commands in steps 1 to 4 and 6 follow the `deploy/` skeleton and have **not** been run on a real server yet: the installer arrives with P5.7. Check each against your install.

## What you need

| Item | Where it comes from |
| --- | --- |
| One backup directory of target A, e.g. `20261001T023000Z/` | Your off-host copy of `SHIPYARD_BACKUP_DIR` |
| The KEK files (`*.key`) | Your off-host copy of `SHIPYARD_BACKUP_KEK_DIR` (target B), kept apart from A |
| The GitHub repositories of your apps, reachable | Images are not backed up: each app is rebuilt from its recorded commit |
| DNS you can change | The hostnames must point at the new server |

A backup directory holds `database.dump`, `caddy-data.tar.gz`, and `manifest.json`. The manifest lists each file's SHA-256 and the IDs of the KEKs the database needs (`kek_ids`).

**Without the KEKs, the secrets in the database cannot be read by anyone.** The restore refuses to start until every KEK the manifest names is in place.

## Steps

1. **Prepare the server.** Install Docker Engine, PostgreSQL 18, the Shipyard binaries, and the systemd units, as for a new install. Use a Shipyard version equal to or newer than the one that wrote the backup (`shipyard` in the manifest). Do not start `shipyard-api` or `shipyard-worker` yet.

2. **Write `/etc/shipyard/shipyard.env`** from `deploy/shipyard.env.example`. The backup does not contain this file.
   - `SHIPYARD_DATABASE_URL`: the new database and its password (step 4).
   - `SHIPYARD_KEK_ACTIVE`: one of the `kek_ids` in the manifest. Normally the newest key.
   - `SHIPYARD_PUBLIC_IPS`: the new server's addresses.
   - `SHIPYARD_API_HOSTNAME`, `SHIPYARD_DOMAIN_SUFFIXES`, and the Caddy settings: as before.

3. **Put the KEKs back**, from target B:

   ```bash
   sudo install -d -o root -g shipyard -m 0750 /etc/shipyard/kek
   ```

   ```bash
   sudo install -o root -g shipyard -m 0640 /path/to/target-b/*.key /etc/shipyard/kek/
   ```

4. **Create an empty database** owned by the role in `SHIPYARD_DATABASE_URL`. Do not run `shipyard-api migrate` on it: the restore needs a database without tables.

   ```bash
   sudo -u postgres createuser --pwprompt shipyard
   ```

   ```bash
   sudo -u postgres createdb --owner shipyard shipyard
   ```

5. **Restore**, as the worker's user and with its environment:

   ```bash
   sudo systemd-run --pipe --wait --collect --uid=shipyard-worker --gid=shipyard -p SupplementaryGroups=docker -p EnvironmentFile=/etc/shipyard/shipyard.env /usr/local/bin/shipyard-worker restore --from /path/to/20261001T023000Z
   ```

   It checks everything first and changes nothing if a check fails:
   - every file against the manifest's size and SHA-256;
   - every KEK in `kek_ids` is in `SHIPYARD_KEK_DIR`;
   - the database has no tables.

   Then it creates the Caddy container, puts its data back (certificates, their keys, ACME accounts), and loads the database with `pg_restore` in one transaction. The database comes last, so a run that failed can be repeated as is.

6. **Point DNS at the new server**, then apply migrations (a no-op for the same version) and start the services:

   ```bash
   sudo systemd-run --pipe --wait --collect --uid=shipyard-api --gid=shipyard -p EnvironmentFile=/etc/shipyard/shipyard.env /usr/local/bin/shipyard-api migrate
   ```

   ```bash
   sudo systemctl enable --now shipyard-api shipyard-worker shipyard-backup.timer
   ```

7. **Watch the apps come back.** Nothing else is done by hand. For each app, the worker finds that the active release's container and image are gone and queues one deploy of its recorded commit, with the configuration that release ran with. It goes through the usual health gate and traffic switch.

   ```bash
   shipyard ps
   ```

   ```bash
   shipyard releases APP
   ```

   The history shows a new active deployment on the same commit, and the lost one as superseded. Its events start with "rebuilding deployment …".

## What comes back, and what does not

| | After the restore |
| --- | --- |
| Apps, domains, environment, history, API tokens | As in the backup. Existing tokens keep working |
| Running releases | Rebuilt from the active commit. The commit must still be on the app's tracked branch |
| Configuration of each release | The revision the active release ran with. A value changed with `env set` after the last deploy is still the latest revision, and applies at the next deploy |
| Certificates | The same ones: Caddy reads them from its restored data, so nothing is reissued and Let's Encrypt's limits are not touched `[LE-LIMITS]` |
| Rollback | Not available for releases from before the restore: their images are gone. Roll back by deploying the commit |
| Build and deploy logs | As in the backup |
| App container logs | Lost with the old server |
| `shipyard.env` | Not in the backup (step 2) |

## If something goes wrong

- **"lacks the KEKs …"**: copy those keys from target B (step 3) and run the restore again.
- **"does not match the manifest … the backup is damaged"**: the copy is incomplete or corrupt. Fetch it again, or use an older backup.
- **"the database already has tables"**: drop the database, create it again (step 4), and run the restore again.
- **`pg_restore` fails**: nothing was written (one transaction). Its message is in the error. Use the `pg_restore` of the server's PostgreSQL major version; `SHIPYARD_BACKUP_PG_RESTORE` sets its path.
- **An app does not come back**: `shipyard releases APP` and `shipyard events OPERATION` show why its rebuild failed (repository unreachable, commit no longer on the branch, health check). The worker tries once and then reports it on every reconcile pass. Fix the cause and run `shipyard deploy APP --ref COMMIT`.
- **An operation was running when the backup was taken**: its lease has expired, so the worker retries it like any interrupted operation, up to its attempt limit. This case is not covered by the drill.

## The drill

`TestRestoreDrill` (`test/e2e/restore_test.go`, run by `make test-e2e`) does the above on one machine:

1. It deploys an app with a secret and a hostname, then takes a backup.
2. It "loses the host": both services stop, and the app's container and images, the Caddy container with its volumes, the networks, and the builder are removed. The new host is an empty database and an empty KEK directory.
3. It checks that the restore is refused without the KEK and into a database with tables, with nothing touched.
4. It copies the KEK back, runs `shipyard-worker restore` and `shipyard-api migrate`, and starts the services.
5. It asserts that the app serves again from a new container on the **same commit**, with the **secret value it ran with**, the **same certificate** as before, and the **same API token**.

ADR-0006 asks for this drill on a fresh VPS before the project is called production-ready, and again each release. The automated drill covers the Shipyard side. The first run on a real VPS, with real DNS and certificates, is still to do and needs the owner.
