#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_FILE="${ROOT_DIR}/internal/app/app.go"

version="$(sed -n 's/.*protonmailcli v\([0-9][0-9.]*\).*/v\1/p' "${APP_FILE}" | head -n1)"
if [[ -z "${version}" ]]; then
  echo "failed to detect version from ${APP_FILE}" >&2
  exit 1
fi

echo "${version}"
