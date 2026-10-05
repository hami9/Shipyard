# ADR-0014: Disk and certificate checks, warned in the log and alerted by shipped Prometheus rules

- **Status:** Accepted
- **Date:** 2026-10-05
- **Deciders:** Project owner (alert delivery chosen 2026-10-05), Claude Code
- **Sources:** `CM-RENEW`, `PROM-RULES`, `PROM-TEMPLATE`, `PROM-TEXT`

## Context

- **Roadmap P5.6:** disk usage and certificate expiry metrics, and alerts at an 80% threshold.
- **Disks.** One VPS fills up with images, the BuildKit cache, container logs, checkouts and backups. Retention (ADR-0006) bounds most of them, but nothing warned before a disk was full.
- **Certificates.** Caddy issues and renews them. It renews when a third of a certificate's lifetime remains: certmagic's `DefaultRenewalWindowRatio` is 1/3 `[CM-RENEW]`. A failing renewal was silent until the certificate expired.
- **What clients see.** The certificate that matters is the one Caddy presents, not one in its storage. A TLS handshake with Caddy's published HTTPS port for each hostname reads exactly that.

## Decision

- **Checks (`internal/monitor`):** the worker runs them at start and then every `SHIPYARD_CHECK_INTERVAL`, 5 minutes by default.
  - **Disks:** `statfs` of the filesystems holding Docker's data root (from `docker info`), `SHIPYARD_WORK_DIR`, and `SHIPYARD_BACKUP_DIR` when set. A path not created yet is measured at its nearest existing parent. Usage is df's: used / (used + available to unprivileged users).
  - **Certificates:** for each hostname in `routes` plus `SHIPYARD_API_HOSTNAME`, one TLS handshake with SNI to Caddy's published 443.
    - The chain is not verified: only the leaf's dates are read, and Caddy's internal CA is in no system pool.
    - Lifetime used is (now − NotBefore) / (NotAfter − NotBefore).
    - It runs only with Caddy enabled.
- **Threshold:** 80%, for a disk's usage and for a certificate's lifetime. 80% of a certificate's lifetime is past Caddy's 67% renewal point, so renewal has been failing for a while. A certificate that cannot be read at all counts as over the threshold.
- **Alerts, as the owner chose:** rules and a log line, with no outbound integration.
  - **The worker's log:** a warning when a condition starts, and an info line when it ends. It is logged once per change, not every check, and works with metrics off.
  - **Metrics** (ADR-0013), when on:

    | Metric | Meaning |
    | --- | --- |
    | `shipyard_filesystem_size_bytes{path}` | Filesystem size |
    | `shipyard_filesystem_avail_bytes{path}` | Space available to unprivileged users |
    | `shipyard_filesystem_used_ratio{path}` | df's Use% |
    | `shipyard_certificate_ok{hostname}` | Whether Caddy presented a certificate |
    | `shipyard_certificate_not_after_timestamp_seconds{hostname}` | When it expires |
    | `shipyard_certificate_lifetime_used_ratio{hostname}` | How much of its lifetime has passed |
    | `shipyard_checks_timestamp_seconds` | When the checks last ran |

    Each set is replaced whole, so a removed hostname drops out.
  - **`deploy/prometheus/shipyard-alerts.yml`** `[PROM-RULES][PROM-TEMPLATE]` holds these rules:
    - disk ≥ 0.8 for 10 minutes;
    - certificate lifetime ≥ 0.8 for an hour;
    - no certificate for 30 minutes;
    - checks stale for an hour;
    - an app unhealthy for 5 minutes;
    - database down for 5 minutes;
    - most deploys failing over an hour;
    - an operation waiting more than an hour.

    A unit test fails if a rule names a metric that no registry exposes.

## Consequences

- **Positive:**
  - A filling disk and a failing renewal are visible days ahead, in the journal even without Prometheus.
  - The numbers in the log, in the metrics and in the rules are the same.
  - No new dependency.
- **Negative / risks:**
  - **Log warnings reach no one by themselves.** Someone must read the journal or run Prometheus with Alertmanager. A webhook was offered and declined for now.
  - **PostgreSQL's disk is not watched** when the database is on another filesystem or host. Shipyard does not know where its data lives.
  - **A failed handshake looks like a missing certificate,** whatever its cause (DNS not pointed yet, a stopped edge). The log line carries the error.
  - **The rules are not checked by `promtool` in CI.** Validating them needs the Prometheus image or binary. The metric-name test catches renames only.

## Alternatives considered

- **A webhook notifier** (`SHIPYARD_ALERT_WEBHOOK`): direct notifications, but an outbound integration to configure, secure and test. The owner declined it for now.
- **Reading Caddy's certificate storage** through the archive API: no network needed, but it reports stored certificates, not served ones, and the storage layout is Caddy's internal detail.
- **`docker system df`:** a per-category breakdown, but slow on large hosts. The data root's filesystem covers the same bytes.
- **Checking at scrape time:** handshakes on every scrape, and nothing logged when no one scrapes.
