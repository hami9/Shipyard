# ADR-0008: App logs through a worker log socket

- **Status:** Accepted
- **Date:** 2026-09-28
- **Deciders:** owner (hami9), Claude Code
- **Sources:** `DK-LOGS`, `MOBY-CLIENT`, `WHATWG-SSE`

## Context

`shipyard logs APP --follow` must show the app's container output (ROADMAP P2.7b). That output lives only in Docker, reached with `GET /containers/{id}/logs`, which streams stdout and stderr multiplexed for containers without a TTY, with optional `follow`, `tail`, and RFC3339Nano `timestamps` `[DK-LOGS]` `[MOBY-CLIENT]`.

Invariant 1 says the API never touches Docker. Only the worker has the Docker socket (ADR-0001), and Docker access is root-equivalent (ADR-0007). So the API cannot read container logs itself.

The owner weighed two options on 2026-09-28:
- **A worker log socket:** the worker reads Docker, and the API authenticates and proxies.
- **Copy logs into PostgreSQL:** the worker writes every line to a bounded table.

The owner chose the worker socket.

## Decision

The worker serves app logs on a private Unix socket, and the API proxies them as SSE.

**The socket**
- It lives at `SHIPYARD_WORKER_SOCKET` (default `/run/shipyard-worker/logs.sock`, a systemd `RuntimeDirectory` with mode 0750). The socket itself has mode 0660.
- It is owned by the worker and the shared `shipyard` group, so only the API and the worker can connect.
- A stale socket from a previous run is replaced. Any other file at that path is an error.
- It speaks plain HTTP/1.1 with one read-only endpoint: `GET /logs?app=<app id>&tail=<n>&follow=<bool>`. Nothing on it starts, stops, or changes anything.

**What the worker serves**
- The logs of the app's **active deployment's** container, looked up in PostgreSQL (invariant 2). A container the database does not name is never read. With no active deployment, the answer is 404.
- One JSON object per line: `{"ts", "stream": "stdout"|"stderr", "line"}`.
  - Lines longer than 16 KiB are cut and marked.
  - The last line is `{"end": "<reason>"}`, so the API can tell a finished stream from a broken one.
- `tail` defaults to 100, and the maximum is 1000. `follow` streams until the container stops or the client leaves.
- **Redaction (best effort).** Every secret value of the deployment's environment revision is replaced with `[REDACTED]` before a line leaves the worker.
  - Only values of at least 6 characters are redacted, because shorter values would mangle ordinary output.
  - Plain (non-secret) variables are not redacted.
  - An app that prints a transformed secret (encoded, split, or partial) is not caught (ARCHITECTURE §6, Log limits).

**What the API serves**
- `GET /v1/apps/{app}/logs?tail=&follow=` with the `read` scope.
- It sends SSE `data:` frames and a keepalive every 15 s, and ends with `event: end` and the reason.
- Frames have no `id`: container logs are not resumable. A reconnect starts from a fresh tail.
- When the worker is down, the answer is 503. With no active deployment, it is 404.

## Consequences

- **Positive:**
  - Invariant 1 holds. The API reaches only the worker's socket, never Docker.
  - Logs are live and add nothing to PostgreSQL.
  - Redaction happens where the secrets are already decrypted for deploys.
- **Negative / risks:**
  - Logs need a running worker.
  - There is no history beyond Docker's rotated files (`local` driver, 100 MB per container `[DK-LOG-LOCAL]`).
  - A new internal socket is a new attack surface for local users in the `shipyard` group. They could already read the database settings, so the trust model (ADR-0007) is unchanged.
  - Redaction is best effort.
- **Follow-ups:**
  - P2.8 publishes the API, and with it this endpoint, through Caddy.
  - Candidate output copied into operation events on a failed health check gets the same redaction.

## Alternatives considered

- **Copy logs into PostgreSQL:** survives worker restarts and keeps history. But it writes every app line to the database, needs pruning, and grows backups (ADR-0006). The owner declined it.
- **Give the API Docker access:** breaks invariant 1 and ADR-0001, and makes the internet-facing process root-equivalent.
- **Read logs from `docker logs` over SSH:** outside the API, with no tokens or scopes, and unusable from CI.
