# Sources

Authoritative references behind [ARCHITECTURE.md](ARCHITECTURE.md) and [architecture-review.md](architecture-review.md).
Documents cite a source by its tag, for example `[DK-LOG]`.

**Last verified:** 2026-09-25. Re-verify versions and limits before each phase starts. Vendor docs change.

**Rules**

- Prefer primary sources: vendor docs, specifications, RFCs, and standards bodies. Blog posts and forum answers are leads, not evidence.
- Every design claim that depends on external behavior needs a tag here. Add the tag before you rely on the claim.
- When a source changes, update this file, the affected docs, and add a `docs/WORKLOG.md` entry.

## GitHub

| Tag | Source | What it supports |
| --- | --- | --- |
| `GH-BP` | [Best practices for using webhooks](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks) | Return a 2XX within 10 s and process asynchronously. Use `X-GitHub-Delivery` against replay; a redelivery keeps the same delivery ID. Use a high-entropy secret, HTTPS, and `GET /meta` for IP ranges. |
| `GH-VALIDATE` | [Validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries) | `X-Hub-Signature-256` is an HMAC-SHA256 hex digest of the payload, prefixed `sha256=`. Compare in constant time. `X-Hub-Signature` (SHA-1) exists only for legacy use. |
| `GH-REDELIVER` | [Redelivering webhooks](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks) | GitHub does **not** automatically redeliver failed deliveries. Deliveries from the past 3 days can be redelivered manually or through the REST API. |
| `GH-EVENTS` | [Webhook events and payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads) | Payloads are capped at 25 MB. Lists the delivery headers. In `push` events, `after` is the head SHA and `deleted` marks a deleted ref. No push event is created when more than 5000 branches, or more than three tags, are pushed at once. |
| `GH-APP-TOKEN` | [Generating an installation access token](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app) | Installation tokens expire after 1 hour. They can be narrowed with `repositories`/`repository_ids` and `permissions`. |
| `GH-APP-JWT` | [Generating a JWT for a GitHub App](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app) | RS256, `exp` at most 10 minutes ahead, `iat` backdated by 60 s for clock drift. |
| `GH-FORKS` | [Forks reference](https://docs.github.com/en/pull-requests/reference/forks) | Commits pushed to any repository in a fork network can be accessible from the upstream repository, even after the fork is deleted. |

## Docker

| Tag | Source | What it supports |
| --- | --- | --- |
| `DK-29` | [Docker Engine 29 release notes](https://docs.docker.com/engine/release-notes/29/) | The `github.com/docker/docker` Go module is deprecated in favor of `github.com/moby/moby/client` and `.../api`. Minimum API version is 1.44. The containerd image store is the default on fresh installs. The latest patch at verification time was 29.8.1. |
| `DK-CONTAINERD` | [containerd image store](https://docs.docker.com/engine/storage/containerd/) | Default storage backend on fresh Engine 29+ installs. Upgraded hosts keep overlay2 until switched. |
| `DK-LOG` | [Configure logging drivers](https://docs.docker.com/engine/logging/configure/) | The default `json-file` driver does no log rotation and can exhaust the disk. The `local` driver is recommended because it rotates by default. |
| `DK-LOG-LOCAL` | [Local file logging driver](https://docs.docker.com/engine/logging/drivers/local/) | Default rotation is 5 files × 20 MB (100 MB) per container, compressed. |
| `DK-LOG-JSON` | [JSON File logging driver](https://docs.docker.com/engine/logging/drivers/json-file/) | `max-size` defaults to unlimited (-1). `max-file` requires `max-size`. |
| `DK-SEC` | [Docker Engine security](https://docs.docker.com/engine/security/) | Only trusted users should control the daemon. Docker keeps an allowlist of capabilities by default. |
| `DK-POSTINSTALL` | [Linux post-installation steps](https://docs.docker.com/engine/install/linux-postinstall/) | Membership in the `docker` group grants root-level privileges. |
| `DK-FW` | [Packet filtering and firewalls](https://docs.docker.com/engine/network/packet-filtering-firewalls/) | Traffic to published container ports is diverted before ufw's INPUT/OUTPUT chains, so ufw rules do not protect it. |
| `DK-BRIDGE` | [Bridge network driver](https://docs.docker.com/engine/network/drivers/bridge/) | User-defined bridges provide DNS resolution by container name and isolate their members. Containers without `--network` join the shared default bridge. |
| `DK-RUN` | [`docker container run` reference](https://docs.docker.com/reference/cli/docker/container/run/) | `--security-opt no-new-privileges`, `--cap-drop`, `--read-only`, `--pids-limit`, `--memory`, `--cpus`, `--restart unless-stopped`, `--init`. Stop timeout defaults to 10 s, then `SIGKILL`. |
| `DK-LIVE` | [Live restore](https://docs.docker.com/engine/daemon/live-restore/) | Keeps containers running while the daemon is down. Works for patch upgrades only, and a long daemon outage can block container logging. |
| `DK-ROOTLESS` | [Rootless mode](https://docs.docker.com/engine/security/rootless/) | Runs the daemon and containers without root. This is a hardening option to evaluate. |
| `DK-BUILDKIT` | [BuildKit](https://docs.docker.com/build/buildkit/) | BuildKit is the default builder for Docker Engine. |
| `DK-BX-CONTAINER` | [Docker container build driver](https://docs.docker.com/build/builders/drivers/docker-container/) | `--driver-opt` accepts `memory`, `memory-swap`, `cpu-quota`, `cpu-period`, `cpu-shares`, `cpuset-cpus`, and `network`. Results are **not** loaded into the image store unless you pass `--load` or set `default-load=true`. |
| `DK-BX-BUILD` | [`docker buildx build` reference](https://docs.docker.com/reference/cli/docker/buildx/build/) | `--resource` limits `RUN` steps. `--network default\|none\|host`. `--secret`. `--metadata-file` records `containerimage.digest` and `containerimage.config.digest`. |
| `DK-BUILD-SECRETS` | [Build secrets](https://docs.docker.com/build/building/secrets/) | Build args and environment variables are inappropriate for secrets because they persist in the final image. Use secret or SSH mounts. |
| `DK-DESKTOP-NET` | [Docker Desktop networking how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/) | On Docker Desktop, "the Docker `bridge` network is not reachable from the host", so the host cannot reach container IPs. Local testing of health probes needs native Docker Engine on Linux or WSL2. (Verified 2026-09-25) |
| `DK-INSTALL-UBUNTU` | [Install Docker Engine on Ubuntu](https://docs.docker.com/engine/install/ubuntu/) | The apt repository steps (keyring, `docker.sources`, `docker-ce` plus the buildx and compose plugins). Supports Ubuntu 26.04, 24.04, and 22.04. (Verified 2026-09-25) |
| `MS-WSL-SYSTEMD` | [Use systemd with WSL](https://learn.microsoft.com/en-us/windows/wsl/systemd) | systemd is the default for current Ubuntu installed with `wsl --install`. Otherwise use `/etc/wsl.conf` `[boot] systemd=true` plus `wsl.exe --shutdown`, which needs WSL ≥ 0.67.6. (Verified 2026-09-25) |
| `MS-WSL-FS` | [Working across file systems (WSL)](https://learn.microsoft.com/en-us/windows/wsl/filesystems) | Store project files in the Linux file system (`/home/<user>/…`), not `/mnt/c`, for performance. (Verified 2026-09-25) |
| `GO-INSTALL` | [Go: download and install](https://go.dev/doc/install) | The Linux tarball install into `/usr/local/go` plus PATH. Never untar over an existing tree. The go1.26.8 linux-amd64 SHA-256 comes from `go.dev/dl/?mode=json`. (Verified 2026-09-25) |
| `PG-IMAGE` | [Official postgres image docs](https://github.com/docker-library/docs/blob/master/postgres/content.md) | For PostgreSQL 18+ images, `PGDATA` is `/var/lib/postgresql/18/docker` and the `VOLUME` is `/var/lib/postgresql`. `POSTGRES_PASSWORD` is required. (Verified 2026-09-25) |
| `DK-RESOURCES` | [Resource constraints](https://docs.docker.com/engine/containers/resource_constraints/) | `--memory` has a minimum of `6m`. `--cpus` accepts fractional values such as `1.5`. (Verified 2026-09-26) |
| `PG-IMAGE-INIT` | [postgres image `docker-entrypoint.sh`](https://github.com/docker-library/postgres/blob/master/18/bookworm/docker-entrypoint.sh) | On first start, `docker_temp_server_start` runs a temporary server with `listen_addresses=''` (Unix socket only) for init, then restarts it. A socket-based `pg_isready` can pass before the real server accepts TCP, so the dev healthcheck probes `127.0.0.1`. (Verified 2026-09-26) |
| `MS-WSL-CONF` | [Advanced settings configuration in WSL](https://learn.microsoft.com/en-us/windows/wsl/wsl-config) | `wsl.conf` `[network] generateResolvConf=false` stops WSL from generating `/etc/resolv.conf` so you can write your own (e.g. `nameserver 1.1.1.1`). `dnsTunneling` and `mirrored` networking are Windows 11 22H2+ only. (Verified 2026-09-26) |

## Tooling

| Tag | Source | What it supports |
| --- | --- | --- |
| `GH-ACTIONS-PG` | [PostgreSQL service containers](https://docs.github.com/en/actions/tutorials/use-containerized-services/create-postgresql-service-containers) | Service container with a `pg_isready` health check. Jobs on the runner connect through `localhost` and the mapped port. |
| `SETUP-GO` | [actions/setup-go README](https://github.com/actions/setup-go) | `go-version-file: go.mod` uses the `toolchain` directive when present, otherwise `go`. The `v7` major tag exists (checked with `git ls-remote`, 2026-09-25). |
| `STATICCHECK` | [staticcheck releases](https://staticcheck.dev/changes/) | 2026.2.1 (module `honnef.co/go/tools` v0.8.1) requires Go ≥ 1.26. It could not analyze Go 1.27.1's standard library when tested on 2026-09-25, so the toolchain is pinned to go1.26.8. |
| `SYSTEMD-EXEC` | [systemd.exec(5)](https://man7.org/linux/man-pages/man5/systemd.exec.5.html) | `NoNewPrivileges=`, `ProtectSystem=strict`, `ProtectHome=`, `PrivateTmp=`, `RuntimeDirectory=`/`RuntimeDirectoryMode=`, `StateDirectory=`, `SupplementaryGroups=`. |
| `GORELEASER` | [GoReleaser `dockers_v2`](https://goreleaser.com/customization/package/dockers_v2/) | Multi-arch images via buildx from prebuilt binaries (`COPY $TARGETPLATFORM/...`). No longer experimental since v2.16. Snapshot builds make per-platform tags and push nothing. v2.18.2 requires Go 1.27.1 to build. (Verified 2026-09-25) |
| `GHCR` | [Working with the Container registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry) | Publish with `GITHUB_TOKEN` and `packages: write`. `org.opencontainers.image.source` links the image to the repository. A new package is **private by default**. Description and license labels (SPDX) are supported. (Verified 2026-09-25) |
| `GH-ATTEST` | [actions/attest](https://github.com/actions/attest) | Needs `id-token: write`, `attestations: write`, and `artifact-metadata: write`. `subject-checksums` accepts a GoReleaser checksums file. Users verify with `gh attestation verify`. `attest-build-provenance` v4 is now a wrapper around it. (Verified 2026-09-25) |
| `GH-PVR` | [Configuring private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/working-with-repository-security-advisories/configuring-private-vulnerability-reporting-for-a-repository) | Enable under Settings → Advanced Security → Private vulnerability reporting. Reporters get a "Report a vulnerability" button on the Advisories page. (Verified 2026-09-25) |
| `SEMVER` | [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html) | Version numbering. Before 1.0, anything may change. |
| `KEEPACHANGELOG` | [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) | CHANGELOG.md format, with an `Unreleased` section. |
| `APACHE-2` | [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0) | Project license. `LICENSE` is the canonical text (SHA-256 `cfc7749b…d30`). §5 covers contributions. |
| `CADDY-CONV` | [Caddy conventions: network addresses](https://caddyserver.com/docs/conventions) | Unix socket addresses (`unix//path`) accept a permission suffix `\|0220`. The default is `0200`. |

## Caddy and ACME

| Tag | Source | What it supports |
| --- | --- | --- |
| `CADDY-API` | [Caddy admin API](https://caddyserver.com/docs/api) | Default address is `localhost:2019`. `POST /load` replaces the config with zero downtime and rolls back on failure. `Etag`/`If-Match` give optimistic concurrency (412 on conflict). `@id` enables `/id/...` addressing. When untrusted code runs on the host, bind the admin API to a permissioned Unix socket. |
| `CADDY-OPTIONS` | [Caddyfile global options](https://caddyserver.com/docs/caddyfile/options) | The `admin` option supports Unix sockets with file-permission access control, plus `origins` and `enforce_origin`. On-demand TLS `ask` gates issuance. |
| `CADDY-HTTPS` | [Automatic HTTPS](https://caddyserver.com/docs/automatic-https) | Let's Encrypt and ZeroSSL are enabled by default. HTTPS needs A/AAAA records pointing to the host, ports 80/443 reachable, and a persistent, writable data directory. On-demand TLS must be restricted with `ask`. |
| `CADDY-RP` | [`reverse_proxy` directive](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy) | `text/event-stream` responses are flushed immediately. Documents active health checks (`health_uri`) and dynamic upstreams. |
| `LE-LIMITS` | [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/) | 50 certificates per registered domain per 7 days, 5 per exact identifier set per 7 days, 5 authorization failures per identifier per account per hour, and 300 new orders per account per 3 hours. Use staging for tests. |

## PostgreSQL

| Tag | Source | What it supports |
| --- | --- | --- |
| `PG-SELECT` | [SELECT … locking clause](https://www.postgresql.org/docs/current/sql-select.html) | `SKIP LOCKED` suits multiple consumers of a queue-like table and is not for general-purpose reads. |
| `PG-LOCKS` | [Explicit locking: advisory locks](https://www.postgresql.org/docs/current/explicit-locking.html) | Session-level advisory locks ignore transaction semantics and last until the session ends. They share the lock memory pool. |
| `PG-UUID` | [UUID functions](https://www.postgresql.org/docs/18/functions-uuid.html) | `gen_random_uuid()` returns a version 4 (random) UUID and is in core since PostgreSQL 13. `uuidv7()` (time-ordered) is new in 18. (Verified 2026-09-26) |
| `PG-VERSIONS` | [Versioning policy](https://www.postgresql.org/support/versioning/) | 18 is the current major (18.6), supported until 2030-11-14. 17 is supported until 2029-11-08. Run the latest minor release. |

## Go

| Tag | Source | What it supports |
| --- | --- | --- |
| `GO-REL` | [Go release history](https://go.dev/doc/devel/release) | go1.27.0 (2026-08-19) and go1.26.0 (2026-02-10). Each major release is supported until two newer major releases exist. |
| `GO-GCM` | [`cipher.NewGCMWithRandomNonce`](https://pkg.go.dev/crypto/cipher#NewGCMWithRandomNonce) | Added in Go 1.24. Uses a random 96-bit nonce. One key must not encrypt more than 2³² messages. |
| `GO-ROUTING` | [Routing enhancements for Go 1.22](https://go.dev/blog/routing-enhancements) | `net/http.ServeMux` supports method matching and wildcards such as `GET /posts/{id}`. |

## Security guidance and web standards

| Tag | Source | What it supports |
| --- | --- | --- |
| `OWASP-CRYPTO` | [OWASP Cryptographic Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html) | AES-256 with GCM or CCM as first preference. Store keys separately from data (not in the same DB). Envelope encryption with the KEK stored apart from DEKs. Put rotation in place before it is needed. |
| `WHATWG-SSE` | [HTML Standard: Server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html) | `text/event-stream` (UTF-8), `Last-Event-ID` on reconnect, the `retry` field, and a comment line about every 15 s to keep proxies from closing the stream. |
| `MDN-SSE` | [MDN: EventSource](https://developer.mozilla.org/en-US/docs/Web/API/EventSource) | Without HTTP/2, browsers allow 6 SSE connections per browser and domain. HTTP/2 negotiates streams (default 100). |
| `RFC9457` | [RFC 9457: Problem Details for HTTP APIs](https://www.rfc-editor.org/rfc/rfc9457.html) | `application/problem+json` error bodies. Obsoletes RFC 7807. |
