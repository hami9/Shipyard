# Acceptance demo (P5.9)

The MVP's exit test (ARCHITECTURE §9): six steps on a fresh VPS, then the `v1.0.0` tag. This page is the runbook, and the record of each run.

**Status.** Not run yet. The steps are covered on one machine by the e2e suite (`TestPhase1ExitCriteria`, `TestCrashSafety`, `TestRestoreDrill`). This run adds what that suite cannot cover: a real server, real DNS, real certificates, GitHub's webhooks, and a second host.

## What you need

| Item | For |
| --- | --- |
| **VPS A**, fresh Ubuntu 24.04 LTS, 2 CPUs, 4 GB, 40 GB ([OPERATIONS.md](OPERATIONS.md#requirements)) | Steps 1–5 |
| **VPS B**, fresh, same size | Step 6, the restore |
| **Two DNS names you control,** e.g. `shipyard.example.com` (the API), `hello.example.com` (the app), with A records to VPS A | Steps 1–2. Step 6 moves them to VPS B |
| **A public GitHub repository** holding [examples/hello](../examples/hello) at its root, e.g. `OWNER/shipyard-hello` (public: the worker fetches it from `https://github.com` without credentials) | The sample app. Its pushes are steps 3 and 5 |
| **A release archive** of the commit under test (below) | Install |
| **The CLI** on your workstation | Every step |

**The archive.** Before `v1.0.0` exists there are two ways to get one:

- **A snapshot (recommended):** nothing is published. On a Linux machine or WSL with Docker and Node.js 24 (it builds the web UI), in the checkout under test:

  ```bash
  make release-snapshot
  ```

  Copy `dist/shipyard-server_<version>_linux_amd64.tar.gz` to VPS A. The CLI is in `dist/shipyard_<version>_<os>_<arch>`.
- **A release candidate,** `v1.0.0-rc.1`, per [RELEASING.md](RELEASING.md). It is published as a pre-release, so it is the owner's call.

**The sample app** answers `/` with `GREETING from VERSION` and `/healthz` with `ok`. Its two constants are the demo's changes: `version` (a working change) and `healthy` (a broken one: the build succeeds and the health check fails).

## Steps

Commands marked **(server)** run on the VPS; the rest run on the workstation. Write down every result in the record below as you go.

### 1. Install and connect the repository

1. **(server)** Extract, dry-run, install ([OPERATIONS.md](OPERATIONS.md#install)):

   ```bash
   tar -xzf shipyard-server_VERSION_linux_amd64.tar.gz
   ```

   ```bash
   sudo shipyard-server_VERSION_linux_amd64/deploy/install.sh --public-ip VPS_A_IP --api-hostname shipyard.example.com --acme-email you@example.com
   ```

   Expect the rootless build check to pass and an admin token at the end. Copy `/etc/shipyard/kek` off the server.
2. **(server)** Webhook secret, then restart the API:

   ```bash
   sudo sh -c 'umask 027; openssl rand -hex 32 > /etc/shipyard/github-webhook.secret' && sudo chgrp shipyard /etc/shipyard/github-webhook.secret
   ```

   ```bash
   echo 'SHIPYARD_GITHUB_WEBHOOK_SECRET_FILE=/etc/shipyard/github-webhook.secret' | sudo tee -a /etc/shipyard/shipyard.env && sudo systemctl restart shipyard-api
   ```

3. **In GitHub,** the repository's Settings → Webhooks → Add webhook: payload URL `https://shipyard.example.com/hooks/github`, content type `application/json`, the secret from step 2, "Just the push event". GitHub's first delivery, a `ping`, must show a 2xx.
4. **Connect the CLI** (paste the token), and create the app with push deploys:

   ```bash
   shipyard login --url https://shipyard.example.com
   ```

   ```bash
   shipyard app create hello --repo OWNER/shipyard-hello --branch main --port 8080 --health-path /healthz --auto-deploy
   ```

   ```bash
   echo salaam | shipyard env set hello GREETING --plain
   ```

**Pass:** `shipyard whoami` answers over HTTPS with a valid certificate (no `-k`), and the webhook's ping is green. Also: `https://shipyard.example.com/` shows the web UI's login, and the same token signs in (ADR-0016).

### 2. Deploy over HTTPS

```bash
shipyard domain add hello hello.example.com
```

```bash
shipyard deploy hello --follow
```

```bash
curl -sS https://hello.example.com/
```

**Pass:** the deploy succeeds, and `curl` prints `salaam from v1` over a publicly trusted certificate.

### 3. A working push, then a broken one

1. In the repository, change `version` to `"v2"` and push to `main`. Watch `shipyard releases hello` until the new release is active.
   **Pass:** `curl` prints `salaam from v2`, and nobody ran `shipyard deploy`.
2. Change `healthy` to `false` (and `version` to `"v3"`) and push.
   **Pass:** the new release fails at its health check (`shipyard events OPERATION` says so), and while it builds, checks, and fails, `curl` keeps printing `salaam from v2`.
3. Set `healthy` back to `true`, keep `"v3"`, and push. **Pass:** `salaam from v3`.

### 4. Roll back

```bash
shipyard releases hello
```

```bash
shipyard rollback hello --to V1_ID --follow
```

**Pass:** `curl` prints `salaam from v1` again, and the operation's events show no build: the retained image ran.

### 5. Kill the worker mid-deploy

1. Turn push deploys off, so you start this one and can follow it, then push a new commit (`version` `"v4"`):

   ```bash
   shipyard app update hello --auto-deploy=false
   ```

   ```bash
   shipyard deploy hello --follow
   ```

   A new commit makes a real build, which leaves time for the next step.
2. **(server)** As soon as the events show the build starting:

   ```bash
   sudo systemctl kill --signal=SIGKILL shipyard-worker
   ```

   systemd restarts it within 2 s. The operation resumes once its lease expires (`SHIPYARD_WORKER_LEASE`, 1m) at the next reconcile, so allow about two minutes.

**Pass:**
- the operation ends `succeeded` (or `failed` with a reason, never stuck in `running`);
- `shipyard releases hello` shows exactly one active release, and `curl` serves it;
- **(server)** `docker ps --filter label=io.shipyard.app=hello` shows only that release's container, plus the previous one until its observation window ends (5m);
- `curl` answered throughout.

Optionally repeat with the kill during the health check, after the build. Then turn push deploys back on (`--auto-deploy=true`).

### 6. Restore onto a fresh host

1. **(VPS A)** Take a backup, and copy it and the KEK off the server ([OPERATIONS.md](OPERATIONS.md#backup-and-restore)):

   ```bash
   sudo systemctl start shipyard-backup.service
   ```

   Note `curl -sS https://hello.example.com/` and the `id` of the active release.
2. **Lose VPS A:** power it off (do not delete it until the demo has passed).
3. **(VPS B)** Follow [RESTORE.md](RESTORE.md) from step 1, with the backup and the KEK files. Move both DNS names to VPS B before its step 6.

**Pass:**
- the app serves the **same commit** at `https://hello.example.com/`, with `GREETING` intact;
- the **old CLI token** still works against the API's hostname;
- `shipyard releases hello` lists the history from VPS A.

## Before tagging `v1.0.0`

- All six steps passed and are recorded below.
- The stacked branches are merged into `main`, and CI is green there, including `make vuln` ([RELEASING.md](RELEASING.md#steps)).
- The owner cuts the tag. An agent never creates, moves, or deletes it without the owner's explicit request.

## Record

Copy this block for each run. Paste real output, never tokens, secrets, or the env file.

```markdown
### Run N: YYYY-MM-DD

- Commit / archive: <sha> / <archive name>
- VPS A: <provider, region, size, image>; VPS B: <same>
- Docker <version>, PostgreSQL <version>, kernel <uname -r>

| Step | Result | Evidence |
| --- | --- | --- |
| 1 Install and connect | pass/fail | install.sh tail, ping delivery status |
| 2 HTTPS deploy | | curl output, certificate issuer |
| 3 Working push / broken push | | releases list, curl during the failure |
| 4 Rollback | | rollback events, curl |
| 5 Worker killed mid-deploy | | operation status, releases, docker ps |
| 6 Restore on VPS B | | restore output, curl, whoami |

Problems found, and the fix or roadmap item for each:
- …
```
