# ADR-0012: KEK rotation by re-wrapping, and asymmetric sealing so the API cannot decrypt

- **Status:** Accepted
- **Date:** 2026-10-04
- **Deciders:** Project owner (options chosen 2026-10-04), Claude Code
- **Sources:** `OWASP-CRYPTO`, `GO-GCM`, `GO-HPKE`, `RFC9180`

## Context

- **Rotation.** ADR-0005 planned rotation as "add a KEK, re-wrap the DEKs in batches, never re-encrypt values". But a trigger makes `secret_values` fully immutable, so a re-wrap could not be stored.
- **Who can decrypt.** The API seals new values and never opens one. Only the worker opens values, to start containers and to redact logs (ARCHITECTURE §7, ADR-0008). Yet both processes read the same symmetric KEK files (`0640`, group `shipyard`). A compromised API, the internet-facing process, could therefore decrypt every stored secret.
- **Standard library.** Go 1.26 ships `crypto/hpke`, which implements RFC 9180 `[GO-HPKE][RFC9180]`:
  - `Seal(pk, kdf, aead, info, plaintext)` and `Open(sk, kdf, aead, info, ciphertext)`;
  - KEMs including DHKEM(X25519, HKDF-SHA256) (RFC 9180) and X-Wing (ML-KEM-768 with X25519, from draft-ietf-hpke-pq);
  - keys serialized with `Bytes()` and read with `KEM.NewPrivateKey`/`NewPublicKey`.

## Decision

### Rotation (P5.4a)

- **Schema** (migration `0005`): `secret_values` stays immutable except for a re-wrap, meaning a new `wrapped_dek` together with a new `kek_id`. Its ID, app, key, ciphertext and creation time can never change.
  - Revisions keep referencing the same rows, so no value and no revision changes meaning.
  - Any other UPDATE still fails with `SY001` (`ErrImmutable`).
- **Commands** (`shipyard-worker`, the process that may open data keys):
  - `kek status`: the loaded KEKs, the values each wraps, and which are active, need a rewrap, may be retired, or are missing.
  - `kek rewrap`: one pass in ID order, 500 values per page and one row per statement. Each data key is opened with its KEK and sealed with the active one; the ciphertext is untouched.
    - It refuses to start while a KEK that wraps values is not loaded.
    - It stops at the first value that does not open, naming it.
    - A row already moved or deleted meanwhile is skipped, so running it again is safe.
    - It writes an audit event `kek.rewrap`.
- **Procedure:**
  1. Write a new key file (`head -c 32 /dev/urandom > /etc/shipyard/kek/k2.key`).
  2. Set `SHIPYARD_KEK_ACTIVE=k2` and restart both services.
  3. Run `shipyard-worker kek rewrap`.
  4. Once `kek status` shows the old KEK unused, remove its file from the live directory.
- **Backups:** target B never removes a KEK, and each backup's manifest lists the KEKs its dump needs (ADR-0006). So retiring a live KEK never strands an older backup.

### Asymmetric sealing (P5.4b)

- **A second kind of KEK:** an HPKE key pair.
  - `<id>.hpke` holds the private key. Only the worker's user can read it (`0600`).
  - `<id>.pub` holds the public key. The API needs only this.
- **Sealing:**
  - `wrapped_dek = hpke.Seal(pub, HKDF-SHA256, AES-256-GCM, info, dek)`, with `info` = `shipyard-dek-v1|<kek_id>|<value_id>`.
  - The info binds the wrapping to its KEK and row, like the symmetric AAD.
  - The value itself stays AES-256-GCM under its DEK.
- **KEM: DHKEM(X25519, HKDF-SHA256)**, final in RFC 9180. X-Wing is post-quantum but still an IETF draft. Rotation can move values to it later, once it is final.
- **Who holds what:**
  - With an HPKE KEK active, the API loads public keys only. It can seal, but cannot open anything, including values it sealed itself.
  - The worker loads private keys, and any remaining symmetric keys, to open values.
- **Migration:**
  1. Generate the pair (`shipyard-worker kek generate`).
  2. Make it active.
  3. Restart both services.
  4. Run `kek rewrap`.
  5. Delete the symmetric `.key` files.
  After that the API holds nothing that decrypts.
- **Backups:** target B also copies `.hpke` and `.pub` files.

## Consequences

- **Positive:**
  - KEKs can be rotated without touching ciphertexts or revisions, and the procedure is tested.
  - After P5.4b, a compromised API process cannot read stored secrets. It sees only the values set while it is compromised, which it receives in requests anyway.
  - No new dependency.
- **Negative / risks:**
  - **The immutability rule is narrower.** A bug in the rewrap path could replace a wrapping with garbage and lose a value. Every re-wrap is therefore computed from a successful open, and a pass stops at the first failure.
  - **Two kinds of key files** for operators to manage.
  - **The private key is a single point of loss,** like the symmetric KEK. The backup procedure must cover it.
  - **A value set while an old KEK is still active** (the API not yet restarted) is missed by the rewrap pass. `kek status` shows it, and a second run moves it.
- **Follow-ups:**
  - P5.4b (asymmetric KEKs, generate command, backup of the new files).
  - P5.7 (the installer creates an HPKE KEK by default).
  - P5.8 (review).

## Alternatives considered

- **A side table of wrappings:** keeps `secret_values` strictly immutable, but needs more schema and a join in every read.
- **Re-encrypting values under new DEKs:** new rows and revisions for every value, which defeats the immutable history.
- **RSA-OAEP or NaCl box:** more code or a dependency. HPKE is standard and in the standard library.
- **X-Wing now:** post-quantum, but a draft wire format for data that must stay decryptable for years.
