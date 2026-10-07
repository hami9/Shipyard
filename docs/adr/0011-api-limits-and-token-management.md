# ADR-0011: Per-client API limits, and token rotation and revocation without SSH

- **Status:** Accepted
- **Date:** 2026-10-04
- **Deciders:** Project owner (options chosen 2026-10-04), Claude Code
- **Sources:** `RFC6585`, `RFC9110-RETRY`, `CADDY-RP`, `CADDY-IMAGE`, `RFC6750`

## Context

- **No limits yet.** The API is public through Caddy (P2.8) and has no request or authentication-failure limits.
  - Tokens are 32 random bytes, so guessing one is hopeless.
  - But every attempt costs a database lookup and a log line.
  - A runaway client or script can also saturate the single API process.
- **Client identity.** The API listens only on loopback or a Unix socket, so its peer is always Caddy or a local process. Caddy ignores client-sent `X-Forwarded-For` and sets it to the client's address unless `trusted_proxies` says otherwise `[CADDY-RP]`. Shipyard's rendered config sets none.
- **Tokens today.** Tokens are created, listed and revoked only on the server (`shipyard-api token …`, over SSH, with database access). There is no rotation. A remote operator or a CI token holder cannot rotate a leaked token without SSH.

## Decision

- **Rate limits (P5.3a).** Per client, in the API process:
  - **Requests:** a token bucket with `SHIPYARD_API_RATE` requests per second (default 10) and `SHIPYARD_API_BURST` (default 50).
  - **Authentication failures:** every 401 takes one of `SHIPYARD_AUTH_FAILURES` (default 10), which refill over 15 minutes. A client without any left gets 429 for every request until one refills. Its requests are refused before any token lookup.
  - **Response:** 429 with `Retry-After` in seconds and a problem+json detail `[RFC6585][RFC9110-RETRY]`.
  - **Exempt:** `/healthz` and `/readyz` are not limited.
  - **Off switch:** 0 turns a limit off.
  - **Client key:**
    - the last `X-Forwarded-For` entry, IPv6 per /64;
    - without the header, the peer address;
    - for a Unix-socket peer, `local`.
  - **Memory:** at most 10,000 clients. Clients whose buckets are full again are dropped first. After that, new clients share one bucket, so a flood of addresses cannot grow memory.
  - **Restarts:** state is not persisted; an API restart resets it.
- **Token management (P5.3b).**
  - **Server:** `shipyard-api token rotate PREFIX [--grace D]`:
    - It issues a token with the old one's user, name and scopes, for the old one's lifetime (creation to expiry) counted from now.
    - It revokes the old one at once, or after `--grace` (up to 7 days) by moving its expiry forward.
  - **API and CLI:**
    - `GET /v1/tokens` and `DELETE /v1/tokens/{prefix}`, both `admin`, behind `shipyard token list` and `revoke`.
    - `POST /v1/tokens/self/rotate`, any scope, rotates the calling token the same way. Its body is optional: `{"grace_seconds": N}`.
    - The new plaintext is in that response only. `shipyard token rotate` saves it to the CLI config.
  - **Audit:** each change writes an audit event with the target prefix.

## Consequences

- **Positive:**
  - Floods and credential stuffing are cut off cheaply, before the database.
  - Leaked tokens can be rotated or revoked from anywhere the API is reachable.
  - No new dependency: about 200 lines of standard library.
- **Negative / risks:**
  - **Clients behind one NAT or proxy** share a budget. The defaults leave room for a team behind one address.
  - **A client out of authentication failures is refused even with a valid token** until the window frees one. That includes `local` (the CLI on the host without Caddy) after 10 bad attempts.
  - **The state is per process and resets on restart.** Shipyard runs one API process.
  - **A self-rotation renews the token's lifetime,** so a leaked token can keep itself alive. Its rotation revokes the original, though, which the real holder notices, and admins see the rotation in `token list` and the audit log.
- **Follow-ups:**
  - P5.5: metrics for 429s.
  - P7.1: per-user limits with multi-user.

## Alternatives considered

- **`golang.org/x/time/rate`:** a good limiter, but a dependency for about 30 lines of bucket arithmetic. Its per-key map and eviction would still be ours to write.
- **Rate limiting in Caddy:** the stock Caddy image has no rate-limit module `[CADDY-IMAGE]`, and adding a plugin means a custom image.
- **Persisting failures in PostgreSQL:** survives restarts, but every bad request then writes to the database, which is the load the limit is meant to avoid.
- **Rotation only on the server:** no new API surface, but a leak response then needs SSH.
