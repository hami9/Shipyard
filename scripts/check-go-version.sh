#!/usr/bin/env bash
# Fail unless the active Go toolchain matches the `toolchain` directive in go.mod.
# Release binaries must be built with the pinned toolchain; `go run`-ing a tool
# that needs a newer Go can silently switch the toolchain for child builds.
set -euo pipefail
want="$(awk '/^toolchain /{print $2}' go.mod)"
got="$(go env GOVERSION)"
if [[ -z "$want" ]]; then
  echo "error: go.mod has no toolchain directive" >&2
  exit 1
fi
if [[ "$got" != "$want" ]]; then
  echo "error: building with $got but go.mod pins $want" >&2
  exit 1
fi
echo "go toolchain OK: $got"
