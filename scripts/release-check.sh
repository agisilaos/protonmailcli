#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
source ./scripts/release-config.sh
export GOTOOLCHAIN="$RELEASE_GO_TOOLCHAIN"
source ./scripts/cli-shared/release-core.sh
cli_release_preflight "$@"
make verify
cli_release_build_check "$version" "${PROTONMAILCLI_RELEASE_CHECK_DIR:-dist/release-check}"
