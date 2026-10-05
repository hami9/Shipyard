# ADR-0013: Prometheus metrics without a client library, read from PostgreSQL

- **Status:** Accepted
- **Date:** 2026-10-05
- **Deciders:** Claude Code (conventional options under CLAUDE.md §10; the owner may revise)
- **Sources:** `PROM-TEXT`, `PROM-PORTS`

## Context

- **Roadmap P5.5:** Prometheus metrics on an internal listener for deploy duration, failure rate, queue depth and health. ADR-0011 adds 429s.
- **Format.** Prometheus scrapes a plain-text format over HTTP `[PROM-TEXT]`:
  - `Content-Type: text/plain; version=0.0.4`;
  - `# HELP` and `# TYPE` lines, one group per metric;
  - escaping for help text and label values;
  - histograms as cumulative `_bucket{le=…}` lines, a `+Inf` bucket, then `_sum` and `_count`.
- **Library.** The official client library (`prometheus/client_golang`) would be Shipyard's first dependency with network and parsing duties. CLAUDE.md §4 and §10 make that an owner decision.
- **Where the facts are.** The queue and every operation's outcome are rows in PostgreSQL (invariant 2). An operation finishes on several paths:
  - a deploy, rollback or delete in the worker;
  - a cancel by a newer request, in the API;
  - the reconciler failing an expired lease.
  - Counting in the code would need a hook on each path.
- **Ports.** Prometheus's exporter range, 9100–9999, is fully allocated `[PROM-PORTS]`, so there is no port to claim as a default.

## Decision

- **`internal/metrics`:** counters, gauges and histograms with fixed label names, written in the text format, version 0.0.4. It is about 250 lines of standard library, with no OpenMetrics and no content negotiation.
- **Worker listener (P5.5a):** `SHIPYARD_WORKER_METRICS_LISTEN`.
  - It takes a loopback `host:port` only, because metrics carry no authentication and name apps.
  - It is off when empty, the default, so no port is taken unasked.
  - It serves `GET /metrics`.
- **Worker metrics.** Each scrape reads PostgreSQL with a 5 s limit:

  | Metric | Type | Meaning |
  | --- | --- | --- |
  | `shipyard_operations_total{kind,result}` | counter | Operations finished since the worker started. Kind is `deploy`, `rollback` or `delete`; result is `succeeded`, `failed` or `cancelled`. |
  | `shipyard_operation_duration_seconds{kind,result}` | histogram | Time from request to finish, queueing included. Buckets run from 1 s to 1 h. |
  | `shipyard_queue_operations{status}` | gauge | Operations `queued` or `running` now. |
  | `shipyard_queue_oldest_wait_seconds` | gauge | How long the longest-waiting due operation has waited. |
  | `shipyard_database_up` | gauge | Whether the scrape could read PostgreSQL. |
  | `shipyard_build_info{version,commit}` | gauge | Always 1. |

  - **Failure rate** is a query, not a metric: `sum(rate(shipyard_operations_total{kind="deploy",result="failed"}[1h])) / sum(rate(shipyard_operations_total{kind="deploy"}[1h]))`.
  - **Counting finished operations:**
    - The worker keeps a cursor, starting at the newest `finished_at` when it starts.
    - Each scrape reads the operations that finished after the cursor, less one minute, and remembers which IDs it has already counted.
    - A transaction that commits a little after its `now()` is still counted, and nothing is counted twice.
    - Counters start at zero with each worker, which Prometheus's `rate` handles.
  - **When PostgreSQL cannot be read:** `shipyard_database_up` is 0, and the queue gauges are left out rather than shown stale.
- **Next (P5.5b):**
  - each active app's health, from the reconciler: container running, and one probe of its health path;
  - the API's own listener, with requests by status class and 429s by reason (ADR-0011).

## Consequences

- **Positive:**
  - No new dependency.
  - The metrics follow the database, so they are correct whichever path finished an operation, and even with the worker idle.
  - The listener cannot be exposed beyond loopback by mistake.
- **Negative / risks:**
  - **Loopback only.** A Prometheus server on another host needs a local agent or a tunnel, and one inside a container needs host networking.
  - **Cost.** Each scrape scans `operations` for finishes, and `finished_at` has no index. That is cheap at single-VPS volumes. If it is not, an index is a later migration.
  - **A late commit can be missed.** An operation whose transaction commits more than a minute after its `finished_at` is not counted. Shipyard's finishing transactions are short.
  - **A hand-written format** must keep to the text format's rules. Tests pin its escaping and histogram output.

## Alternatives considered

- **`prometheus/client_golang`:** complete, including OpenMetrics, but a large dependency tree and an owner decision for a few metric types.
- **Counting in the code paths:** exact without a cursor, but it needs a hook on every path that finishes an operation, including the API's cancel and the reconciler's SQL. A path added later would be missed silently.
- **`count(*)` per scrape as a gauge:** simple, but app deletion removes rows, so the totals could fall. Prometheus counters must not fall.
- **The metrics in the API process:** the API reads PostgreSQL too, but the health of running apps needs Docker, which only the worker may reach (invariant 1).
