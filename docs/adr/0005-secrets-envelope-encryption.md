# ADR-0005: Envelope encryption for app configuration

- **Status:** Proposed
- **Date:** 2026-09-25
- **Sources:** `OWASP-CRYPTO`, `GO-GCM`, `DK-BUILD-SECRETS`

## Context

Apps need secret configuration from their first deploy, and deployments pin an environment revision. OWASP recommends:

- AES-256 in an authenticated mode (GCM first),
- keys stored apart from the data they protect,
- envelope encryption with the KEK separate from the DEKs,
- rotation processes in place before they are needed `[OWASP-CRYPTO]`.

Go 1.24+ provides `cipher.NewGCMWithRandomNonce`. A single key must not encrypt more than 2³² messages `[GO-GCM]`.

## Decision

- **Data model:**
  - Immutable `secret_values(id, app_id, key, ciphertext, wrapped_dek, kek_id)`.
  - Immutable `env_revisions` whose entries reference `secret_value_id` (or hold a non-secret plain value).
  - Setting one key creates a new revision that **reuses** the other value rows, with no decryption needed.
- **Encryption:**
  - A fresh random 256-bit DEK per value. The value is encrypted with AES-256-GCM via `NewGCMWithRandomNonce`, with AAD `app_id|key|value_id`.
  - The DEK is wrapped with the KEK (AES-256-GCM, AAD `kek_id|value_id`).
  - Per-value DEKs keep each key's message count trivially below the 2³² limit.
- **KEK storage:**
  - A 32-byte key file at `/etc/shipyard/kek/<kek_id>.key` (root-owned, mode `0640`, group `shipyard`), or a systemd credential.
  - **Never** in PostgreSQL, never in the same backup artifact as the database, and never in the repository.
- **Access:** the API seals new values, and the worker opens them only to start containers. Both read the KEK in the MVP. Asymmetric wrapping, so that the API can seal but not open, is a Phase 5 option.
- **Rotation:** `shipyard-admin kek rotate` adds a new `kek_id` and re-wraps DEKs in batches, without re-encrypting values. The old KEK is removed only after all rows are migrated. The procedure is tested before 1.0.
- **Exposure rules:**
  - The API returns only keys and metadata, never values.
  - Logs never include values.
  - Build args and build env never include values `[DK-BUILD-SECRETS]`.
  - Log streaming redacts known values as best effort.
- Env vars are visible to anyone with Docker access, which is root-equivalent. This is accepted under the trust model (ADR-0007).

## Consequences

- **Positive:** A database dump alone reveals nothing. Rotation is cheap. There is no plaintext migration later.
- **Negative:** Losing the KEK means losing all secrets, so the backup procedure is critical (ADR-0006). There is slight complexity in the revision model.
- **Follow-ups:** P1.4 (secrets package with negative tests: wrong AAD, wrong KEK, tampered ciphertext), P3.6 (KEK backup/restore drill), P5.x (rotation command and asymmetric sealing).

## Alternatives considered

- **Plaintext in PostgreSQL with disk encryption:** a database dump or backup leaks everything.
- **HashiCorp Vault or SOPS:** extra operational burden for a single VPS. Could be added behind the same interface later.
- **XChaCha20-Poly1305:** equally valid. AES-GCM was chosen per OWASP's first preference and standard-library support.
