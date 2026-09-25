#!/usr/bin/env bash
# Print the CHANGELOG.md section for a release tag, e.g. v0.1.0 -> "## [0.1.0] ...".
# Used by .github/workflows/release.yml. Fails when the section is missing or empty,
# so a tag cannot ship without release notes.
set -euo pipefail

tag="${1:?usage: release-notes.sh <tag>}"
version="${tag#v}"
changelog="${2:-CHANGELOG.md}"

notes="$(awk -v heading="## [${version}]" '
  index($0, heading) == 1 { found = 1; next }
  found && /^## \[/        { exit }
  found                    { print }
' "$changelog")"

if [[ -z "${notes//[[:space:]]/}" ]]; then
  echo "error: ${changelog} has no section for ${version}" >&2
  exit 1
fi
printf '%s\n' "$notes"
