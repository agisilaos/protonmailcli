#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/docs/help"
BIN_PATH="${ROOT_DIR}/.tmp/protonmailcli-help"

if [[ "${1:-}" == "--out-dir" ]]; then
  if [[ -z "${2:-}" ]]; then
    echo "usage: $0 [--out-dir <path>]" >&2
    exit 2
  fi
  OUT_DIR="${2}"
fi

mkdir -p "${OUT_DIR}" "${ROOT_DIR}/.tmp"
go build -o "${BIN_PATH}" ./cmd/protonmailcli

"${BIN_PATH}" --help > "${OUT_DIR}/root.txt"
"${BIN_PATH}" draft create-many --help > "${OUT_DIR}/draft-create-many.txt"
"${BIN_PATH}" message send-many --help > "${OUT_DIR}/message-send-many.txt"

echo "Updated help snapshots in ${OUT_DIR}"
