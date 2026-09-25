# Work Log

A chronological record of work on Shipyard, **newest entry first**. Every working session adds one entry. The log is the hand-off between sessions and between people and agents. Someone new should be able to resume from the `Current status` block plus the newest entry alone.

## Current status

> Update this block at the end of every session.

| Field | Value |
| --- | --- |
| **Active phase** | Phase 0: Bootstrap (not started) |
| **Last completed** | Architecture v2, sources, ADRs, roadmap, agent instructions |
| **Next task** | P0.1: initialize the Go module and the `cmd/` skeleton (see ROADMAP) |
| **Blockers** | ADR-0001 to ADR-0007 are `Proposed`; the owner must accept or amend them |
| **Open risks** | Builder egress is unrestricted until Phase 5, and the MVP trust model is documented, not enforced |
| **Last updated** | 2026-09-25 |

## Entry template

Copy this block to the top of the entries section.

```markdown
### YYYY-MM-DD: <short title>

- **Phase / task:** P<n>.<m>: <roadmap item>
- **Author:** <human name or agent + session link>
- **Goal:** one sentence.

**Done**
- …

**Changed files**
- `path`: why

**Decisions**
- … (link ADR if one was written; "none" is valid)

**Verification**
- `command`: result (paste the real summary: pass/fail counts, errors)

**Problems / surprises**
- … (include source tags if vendor behavior differed from the docs)

**Next**
- The exact next step and its command, so the next session can start without guessing.
```

**Rules**

- Record facts, not intentions. If something was not run, say "not run".
- Never paste secrets, tokens, full env files, or customer data.
- Link commits by short hash once pushed.
- Keep entries short, around 10–25 lines. Move long analysis to an ADR or `docs/`.

## Entries

### 2026-09-25: Architecture review, agent instructions, roadmap

- **Phase / task:** Pre-Phase 0: project definition
- **Author:** Claude Code (cloud session)
- **Goal:** Correct the proposed architecture against primary sources, create the agent system prompt and work log, and phase the roadmap.

**Done**
- Archived the original proposal as `docs/archive/ARCHITECTURE-v1.md`.
- Verified v1 claims against GitHub, Docker, Caddy, Let's Encrypt, PostgreSQL, Go, OWASP, WHATWG, MDN, and RFC 9457 docs (38 sources, tagged).
- Wrote `docs/ARCHITECTURE.md` v2 with 19 corrections (R1–R19). High-severity items:
  - Caddy admin API on a Unix socket (R2)
  - `local` log driver, since json-file never rotates (R3)
  - Commit ancestry check against fork-network commits (R4)
  - Envelope encryption moved into Phase 1 (R9)
  - Docker-published ports bypass ufw (R10)
- Wrote proposed ADRs 0001–0007 that answer v1's open questions.
- Wrote `docs/ROADMAP.md` with Phases 0–7, IDs, exit criteria, and a dependency graph.
- Created `CLAUDE.md` (agent system prompt), `AGENTS.md`, this log, `README.md`, `.gitignore`, and `.editorconfig`.
- Adopted the owner's git conventions: commit subjects of 1–2 words, branch names of 1–3 words.

**Changed files**
- `docs/ARCHITECTURE.md`, `docs/architecture-review.md`, `docs/SOURCES.md`, `docs/archive/ARCHITECTURE-v1.md`: `242e169` Architecture v2
- `CLAUDE.md`, `AGENTS.md`: `2622d14` Agent prompt
- `docs/adr/0000`–`0007`: `63c8f8d` ADRs
- `docs/ROADMAP.md`: `24d6ec5` Roadmap
- `README.md`, `.gitignore`, `.editorconfig`: `af133ba` Scaffold
- `docs/WORKLOG.md`: Worklog commit

**Decisions**
- All seven ADRs are `Proposed`, pending the owner's review.
- Notable reversals from v1: secrets move from milestone 5 to Phase 1, "image digest" becomes the Engine image ID plus build metadata, and advisory locks become lease rows plus a partial unique index.

**Verification**
- Documentation only; no code exists yet. Source facts were fetched from primary docs on 2026-09-25 (see `docs/SOURCES.md`).
- A link and tag check script found 0 broken relative links, 38 defined source tags, 0 undefined, and 0 unused.
- Version facts at verification time: Go 1.27.0 (2026-08-19), PostgreSQL 18.6, Docker Engine 29.8.1.

**Problems / surprises**
- `docker/docker` Go module deprecated in Engine 29 → use `github.com/moby/moby/client` `[DK-29]`.
- GitHub compare API docs say `BASE...HEAD` must be branch names, so the ancestry check uses `git merge-base --is-ancestor` rather than relying on the API.
- The session's assigned branch `claude/shipyard-architecture-proposal-j8w56b` exceeds the new 1–3 word branch rule. It was kept because the harness requires it, and future branches follow the rule.

**Next**
- Owner reviews ADR-0001 to ADR-0007 (accept or amend).
- Start P0.1: `go mod init`, `cmd/shipyard{,-api,-worker}` skeletons, Makefile, CI workflow.
