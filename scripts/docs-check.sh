#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "error: docs-check.sh must be run on macOS (Darwin)" >&2
  exit 1
fi

[[ -f "${ROOT_DIR}/README.md" ]] || { echo "error: README.md not found" >&2; exit 1; }
[[ -f "${ROOT_DIR}/CHANGELOG.md" ]] || { echo "error: CHANGELOG.md not found" >&2; exit 1; }
[[ -f "${ROOT_DIR}/RELEASING.md" ]] || { echo "error: RELEASING.md not found" >&2; exit 1; }
[[ -f "${ROOT_DIR}/scripts/changelog-context.sh" ]] || { echo "error: scripts/changelog-context.sh not found" >&2; exit 1; }
[[ -f "${ROOT_DIR}/scripts/changelog-section.py" ]] || { echo "error: scripts/changelog-section.py not found" >&2; exit 1; }

for target in changelog-context release-check release-check-ci release-dry-run release; do
  grep -qE "^${target}:" "${ROOT_DIR}/Makefile" || { echo "error: Makefile missing target: $target" >&2; exit 1; }
done

echo "[docs-check] validating shared docs contract"
python3 "${ROOT_DIR}/scripts/docs-contract-check.py" --root "${ROOT_DIR}"

echo "[docs-check] help snapshot drift"
"${ROOT_DIR}/scripts/check-help.sh"

echo "[docs-check] release docs references"
grep -Fq 'make release-check VERSION=vX.Y.Z' "${ROOT_DIR}/README.md" || { echo "error: README missing make release-check usage" >&2; exit 1; }
grep -Fq 'make release-dry-run VERSION=vX.Y.Z' "${ROOT_DIR}/README.md" || { echo "error: README missing make release-dry-run usage" >&2; exit 1; }
grep -Fq 'make release VERSION=vX.Y.Z' "${ROOT_DIR}/README.md" || { echo "error: README missing make release usage" >&2; exit 1; }
grep -Fq 'scripts/release-check.sh' "${ROOT_DIR}/README.md" || { echo "error: README missing scripts/release-check.sh reference" >&2; exit 1; }
grep -Fq 'scripts/release.sh' "${ROOT_DIR}/README.md" || { echo "error: README missing scripts/release.sh reference" >&2; exit 1; }

echo "[docs-check] ok"
