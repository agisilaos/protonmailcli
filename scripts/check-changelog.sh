#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHANGELOG_FILE="${ROOT_DIR}/CHANGELOG.md"
VERSION="$("${ROOT_DIR}/scripts/current-version.sh")"

if [[ ! -f "${CHANGELOG_FILE}" ]]; then
  echo "missing ${CHANGELOG_FILE}" >&2
  echo "run: scripts/generate-changelog.sh --version ${VERSION}" >&2
  exit 1
fi

if ! grep -q "^## \[${VERSION}\]" "${CHANGELOG_FILE}"; then
  echo "missing changelog section for ${VERSION} in ${CHANGELOG_FILE}" >&2
  echo "run: scripts/generate-changelog.sh --version ${VERSION}" >&2
  exit 1
fi

echo "changelog entry exists for ${VERSION}"
