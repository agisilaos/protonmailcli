#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXPECTED_DIR="${ROOT_DIR}/docs/help"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

"${ROOT_DIR}/scripts/update-help.sh" --out-dir "${TMP_DIR}" >/dev/null

for file in root.txt draft-create-many.txt message-send-many.txt; do
  if ! diff -u "${EXPECTED_DIR}/${file}" "${TMP_DIR}/${file}"; then
    echo >&2
    echo "help output drift detected in ${file}" >&2
    echo "run: scripts/update-help.sh" >&2
    exit 1
  fi
done

echo "help snapshots are up to date"
