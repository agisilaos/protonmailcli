#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "[release-check] go test ./..."
(cd "${ROOT_DIR}" && go test ./...)

echo "[release-check] contract fixtures"
(cd "${ROOT_DIR}" && go test ./internal/app -run TestContractFixtures -v)

echo "[release-check] help snapshot drift"
"${ROOT_DIR}/scripts/check-help.sh"

echo "[release-check] changelog gate"
"${ROOT_DIR}/scripts/check-changelog.sh"

echo "[release-check] agent smoke checks"
"${ROOT_DIR}/scripts/smoke-agent.sh"

echo "release checks passed"
