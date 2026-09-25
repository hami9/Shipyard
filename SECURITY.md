# Security Policy

Shipyard deploys code, holds secrets, and controls a Docker daemon, so security reports are taken seriously.

## Supported versions

| Version | Supported |
| --- | --- |
| Latest release | ✅ |
| Older releases | ❌ Upgrade first. Before 1.0, only the newest `0.x` release receives fixes |
| Unreleased branches | Best effort |

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately through GitHub: go to the repository's **Security** tab, open **Advisories**, and click **Report a vulnerability**. Include:

- the affected version or commit,
- steps to reproduce, or a proof of concept,
- the impact you observed or expect.

The maintainers aim to acknowledge reports within 7 days. Fixes are coordinated with the reporter, and the reporter is credited unless they prefer otherwise.

## Scope and trust model

Shipyard's MVP is built for **trusted operators deploying trusted repositories** ([ADR-0007](docs/adr/0007-mvp-trust-model.md)). It is not a sandbox for hostile tenants. Access to the Docker daemon is root-equivalent by design.

**In scope** (examples):

- Bypassing API authentication or token scopes.
- Forged or replayed GitHub webhooks being accepted.
- Deploying a commit that is not on the app's tracked branch.
- Secret values leaking through the API, logs, errors, images, or build args.
- An app container reaching the Docker socket, the Caddy admin socket, the host network, or another app's network.
- Traffic reaching a candidate release that failed its health check.

**Out of scope** (examples):

- Actions by an authenticated administrator. They control the host by design.
- Attacks that require a malicious Dockerfile from a repository the operator chose to trust. Build isolation hardening is tracked in the [roadmap](docs/ROADMAP.md), Phase 5.
- Vulnerabilities in Docker, Caddy, PostgreSQL, or Go themselves. Report those upstream. Shipyard issues triggered by them are in scope.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) §7 for the security defaults Shipyard commits to.
