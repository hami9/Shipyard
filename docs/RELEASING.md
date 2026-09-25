# Releasing

Releases are cut by pushing a `vX.Y.Z` tag. [`.github/workflows/release.yml`](../.github/workflows/release.yml) then does the rest with GoReleaser `[GORELEASER]`:

1. Runs `make lint test`.
2. Verifies that the build uses the Go toolchain pinned in `go.mod` (`scripts/check-go-version.sh`).
3. Builds and publishes these artifacts:

| Artifact | Platforms | Contents |
| --- | --- | --- |
| `shipyard_<ver>_<os>_<arch>` | Linux, macOS, Windows × amd64, arm64 | CLI, LICENSE, NOTICE, README, CHANGELOG |
| `shipyard-server_<ver>_linux_<arch>.tar.gz` | Linux amd64, arm64 | `shipyard-api`, `shipyard-worker`, and `deploy/` (systemd units, `daemon.json`, Caddy bootstrap, env example) |
| `checksums.txt` | — | SHA-256 of every archive |
| Build provenance attestation | — | Signed SLSA provenance for every archive `[GH-ATTEST]` |
| `ghcr.io/hami9/shipyard:v<ver>` (and `:latest` for stable releases) | linux/amd64, linux/arm64 | All three binaries on distroless `static:nonroot` `[GHCR]` |

Release notes come from the tag's section in [CHANGELOG.md](../CHANGELOG.md). A tag without a changelog section fails before anything is published.

## Version plan

We use SemVer `[SEMVER]`. Before 1.0, a minor bump can break compatibility.

| Version | Ships when |
| --- | --- |
| `v0.1.0` | Phase 0 exit criteria are met and the branch is merged to `main` |
| `v0.2.0` | P1: the first manual deploy |
| `v0.3.0` | P2: HTTPS and traffic switching |
| `v0.4.0` | P3: rollback, reconciler, backup |
| `v0.5.0` | P4: GitHub integration |
| `v1.0.0` | P5: the acceptance demo passes on a fresh VPS |

Use `-rc.N` suffixes (e.g. `v0.2.0-rc.1`) to test a release. GoReleaser marks them as pre-releases, and they do not move `:latest`.

## Steps

1. On `main`, confirm that CI is green and that the phase's exit criteria in [ROADMAP.md](ROADMAP.md) hold.
2. Rehearse locally. Nothing is published:
   ```bash
   make release-check      # validate .goreleaser.yaml
   make release-snapshot   # build everything into ./dist
   ```
3. In `CHANGELOG.md`, rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD` and add a fresh, empty `## [Unreleased]` above it. Commit with the subject `Release vX.Y.Z`.
4. Tag and push:
   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```
5. Watch the **Release** workflow. Then check the release page, the attached checksums, and the image under the repository's **Packages**.

### First release only (manual, one time)

- GHCR packages are **private by default** when first published `[GHCR]`. Open the package's settings, set its visibility to **Public**, and confirm that it is linked to `hami9/Shipyard`. The `org.opencontainers.image.source` label links it automatically.
- Enable **Private vulnerability reporting** under Settings → Advanced Security, so that [SECURITY.md](../SECURITY.md)'s reporting path works `[GH-PVR]`.

## Verifying a release (for users)

```bash
sha256sum --ignore-missing -c checksums.txt
gh attestation verify shipyard-server_X.Y.Z_linux_amd64.tar.gz --repo hami9/Shipyard
```

## If a release is broken

Do not delete or move a published tag. Fix the problem, add a changelog entry, and release the next patch version. Mark the broken release as a pre-release, or edit its notes to warn users.
