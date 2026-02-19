#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHANGELOG_FILE="${ROOT_DIR}/CHANGELOG.md"

if [[ ! -f "${CHANGELOG_FILE}" ]]; then
  echo "missing ${CHANGELOG_FILE}" >&2
  exit 1
fi

version="$(sed -n 's/^## \[\(v[0-9][0-9.]*\)\].*/\1/p' "${CHANGELOG_FILE}" | head -n1)"
if [[ -z "${version}" ]]; then
  echo "failed to detect version from ${CHANGELOG_FILE}" >&2
  exit 1
fi

echo "${version}"
