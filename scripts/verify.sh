#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
source ./scripts/release-config.sh
source ./scripts/cli-shared/release-core.sh
cli_tooling_check "$CLI_TEMPLATE_FINGERPRINT"

echo "[verify] checking module metadata"
./scripts/cli-shared/module-check.sh
if grep -R -nE '(^|[[:space:]])(r[g]|j[q]|y[q]|f[d])([[:space:]]|$)' scripts >/dev/null; then
  cli_release_die "scripts/ uses non-portable tooling (rg/jq/yq/fd). Use grep/sed/awk or install tools explicitly in workflow."
fi

echo "[verify] checking format"
make fmt-check
echo "[verify] running vet"
make vet
echo "[verify] running tests"
make test
echo "[verify] checking executable contract fixtures"
go test ./internal/app -run TestContractFixtures -v
echo "[verify] checking help generation fixtures"
make help-script-test
echo "[verify] checking docs and help"
make docs-check
echo "[verify] running isolated local-state agent smoke"
./scripts/smoke-agent.sh
echo "[verify] passed"
