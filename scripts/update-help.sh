#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/docs/help"
BIN_PATH="${ROOT_DIR}/.tmp/protonmailcli-help"
SNAPSHOT_LIST="${ROOT_DIR}/scripts/help-snapshots.txt"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

if [[ "${1:-}" == "--out-dir" ]]; then
  if [[ -z "${2:-}" ]]; then
    echo "usage: $0 [--out-dir <path>]" >&2
    exit 2
  fi
  OUT_DIR="${2}"
fi

cd "${ROOT_DIR}"

if [[ ! -d "${ROOT_DIR}/cmd/protonmailcli" ]]; then
  echo "error: expected command package at ${ROOT_DIR}/cmd/protonmailcli" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}" "${ROOT_DIR}/.tmp"
go build -o "${BIN_PATH}" ./cmd/protonmailcli

CFG_PATH="${WORK_DIR}/config.toml"
STATE_PATH="${WORK_DIR}/state.json"
PMAIL_USE_LOCAL_STATE=1 "${BIN_PATH}" --config "${CFG_PATH}" --state "${STATE_PATH}" setup --non-interactive --username docs@example.com >/dev/null

while IFS=$'\t' read -r out_file cmdline || [[ -n "${out_file:-}" ]]; do
  [[ -z "${out_file:-}" || "${out_file}" =~ ^# ]] && continue
  read -r -a cmd_args <<< "${cmdline}"
  PMAIL_USE_LOCAL_STATE=1 "${BIN_PATH}" --config "${CFG_PATH}" --state "${STATE_PATH}" "${cmd_args[@]}" > "${OUT_DIR}/${out_file}"
done < "${SNAPSHOT_LIST}"

echo "Updated help snapshots in ${OUT_DIR}"
