#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "error: docs-check.sh must be run on macOS (Darwin)" >&2
  exit 1
fi

[[ -f "${ROOT_DIR}/README.md" ]] || { echo "error: README.md not found" >&2; exit 1; }
[[ -f "${ROOT_DIR}/CHANGELOG.md" ]] || { echo "error: CHANGELOG.md not found" >&2; exit 1; }

echo "[docs-check] help snapshot drift"
"${ROOT_DIR}/scripts/check-help.sh"

echo "[docs-check] release docs references"
rg -q 'make release-check VERSION=vX.Y.Z' "${ROOT_DIR}/README.md" || { echo "error: README missing make release-check usage" >&2; exit 1; }
rg -q 'make release-dry-run VERSION=vX.Y.Z' "${ROOT_DIR}/README.md" || { echo "error: README missing make release-dry-run usage" >&2; exit 1; }
rg -q 'make release VERSION=vX.Y.Z' "${ROOT_DIR}/README.md" || { echo "error: README missing make release usage" >&2; exit 1; }
rg -q 'scripts/release-check.sh' "${ROOT_DIR}/README.md" || { echo "error: README missing scripts/release-check.sh reference" >&2; exit 1; }
rg -q 'scripts/release.sh' "${ROOT_DIR}/README.md" || { echo "error: README missing scripts/release.sh reference" >&2; exit 1; }

echo "[docs-check] ok"
