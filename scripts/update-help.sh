#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/docs/help"
BIN_PATH="${ROOT_DIR}/.tmp/protonmailcli-help"
SNAPSHOT_LIST="${ROOT_DIR}/scripts/help-snapshots.txt"

if [[ "${1:-}" == "--out-dir" ]]; then
  if [[ -z "${2:-}" ]]; then
    echo "usage: $0 [--out-dir <path>]" >&2
    exit 2
  fi
  OUT_DIR="${2}"
fi

mkdir -p "${OUT_DIR}" "${ROOT_DIR}/.tmp"
go build -o "${BIN_PATH}" ./cmd/protonmailcli

while IFS=$'\t' read -r out_file cmdline || [[ -n "${out_file:-}" ]]; do
  [[ -z "${out_file:-}" || "${out_file}" =~ ^# ]] && continue
  read -r -a cmd_args <<< "${cmdline}"
  "${BIN_PATH}" "${cmd_args[@]}" > "${OUT_DIR}/${out_file}"
done < "${SNAPSHOT_LIST}"

echo "Updated help snapshots in ${OUT_DIR}"
