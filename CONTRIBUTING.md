# Contributing to Shipyard

Thanks for your interest. Shipyard is early, in Phase 0/1 of the [roadmap](docs/ROADMAP.md). The design is settled, and changes should follow it.

## Before you start

1. Read [CLAUDE.md](CLAUDE.md). The **invariants in §3** and the conventions apply to every contributor, human or AI.
2. Read the parts of [ARCHITECTURE.md](docs/ARCHITECTURE.md) and the [ADRs](docs/adr/) that touch your change.
3. For anything larger than a bug fix, open an issue first so the approach can be agreed on.
4. Security problems go through [SECURITY.md](SECURITY.md), not public issues.

## Development

Setup, commands, and local testing are covered in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md). Before pushing:

```bash
make lint test            # always
make test-integration     # when touching store, runtime, routing, build, or source
```

## Conventions

- **Commits:** a 1–2 word subject (e.g. `Queue lease`). Put detail in the body. One logical change per commit.
- **Branches:** 1–3 words, kebab-case (e.g. `webhook-hmac`).
- **Tests:** every bug fix starts with a failing test. Security-sensitive code needs negative tests.
- **Changelog:** user-visible changes add a line under `## [Unreleased]` in [CHANGELOG.md](CHANGELOG.md).
- **Decisions:** a change to behavior, a boundary, or a dependency needs an ADR ([template](docs/adr/0000-template.md)).
- **External facts:** vendor limits, flags, and versions must be cited in [docs/SOURCES.md](docs/SOURCES.md).

## License

Shipyard is licensed under [Apache-2.0](LICENSE). Under section 5 of the license, any contribution you submit is licensed under the same terms.
