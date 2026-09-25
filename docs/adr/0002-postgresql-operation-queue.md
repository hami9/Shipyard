# ADR-0002: PostgreSQL as the durable operation queue

- **Status:** Accepted
- **Date:** 2026-09-25 (proposed and accepted)
- **Deciders:** Project owner
- **Sources:** `PG-SELECT`, `PG-LOCKS`, `PG-VERSIONS`, `GH-BP`

## Context

Deploys are long-running (minutes), must survive process restarts, must be idempotent under duplicate webhooks `[GH-BP]`, and must be serialized per app. Session-level advisory locks ignore transaction rollback, last until the session ends, and consume the shared lock pool `[PG-LOCKS]`.

## Decision

- Store operations in an `operations` table. Workers claim the oldest eligible row with `SELECT … FOR UPDATE SKIP LOCKED` `[PG-SELECT]` and set `status = 'running'`, `lease_owner`, and `lease_expires_at` in the same transaction.
- A heartbeat renews the lease every `lease/3`. The reconciler re-queues expired leases and increments `attempt`. After `max_attempts`, the operation is marked `failed`.
- **One running operation per app** is a database invariant: `CREATE UNIQUE INDEX … ON operations (app_id) WHERE status = 'running'`.
- **Idempotency:** `UNIQUE (idempotency_key)`. The key is `gh:<delivery-id>` for webhooks, or the client's `Idempotency-Key` header for manual deploys. Insert with `ON CONFLICT DO NOTHING` and return the existing row.
- **Coalescing:** a newer deploy request cancels that app's still-`queued` deploy. A running operation is never interrupted.
- `LISTEN/NOTIFY` is an optional wake-up hint. Polling (default 2 s) is always the fallback.
- Target PostgreSQL 18 (17 acceptable) on the latest minor release `[PG-VERSIONS]`.

## Consequences

- **Positive:** One datastore to back up. Queue state and domain state change in the same transaction. No Redis.
- **Negative:** The polling load is small but non-zero. Throughput is far below a dedicated broker, which is acceptable for a single VPS.
- **Follow-ups:** P1.5 (queue package with claim, lease, and heartbeat), P3.2 (lease expiry in the reconciler), plus concurrency tests that race two workers on one app.

## Alternatives considered

- **Redis or a message broker:** an extra stateful service without a need at this scale.
- **Advisory locks held for the whole deploy:** fragile with connection pools and crash recovery `[PG-LOCKS]`.
- **In-memory queue:** loses work on restart.
