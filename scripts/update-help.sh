#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
OUT_DIR="${ROOT_DIR}/docs/help"
SNAPSHOT_LIST="${ROOT_DIR}/scripts/help-snapshots.txt"

usage() {
  printf 'Usage: scripts/update-help.sh [--out-dir <path>]\n'
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      [[ -n "${2:-}" ]] || { usage >&2; exit 2; }
      OUT_DIR="$2"
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'error: unknown argument: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

cd "${ROOT_DIR}"
[[ -d cmd/protonmailcli ]] || { echo "error: expected command package at cmd/protonmailcli" >&2; exit 1; }
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/proton-help.XXXXXX")"
trap 'rm -rf "${WORK_DIR}"' EXIT
BIN_PATH="${WORK_DIR}/protonmailcli-help"
export GOCACHE="${WORK_DIR}/gocache"
mkdir -p "${OUT_DIR}"
go build -o "${BIN_PATH}" ./cmd/protonmailcli

CFG_PATH="${WORK_DIR}/config.toml"
STATE_PATH="${WORK_DIR}/state.json"
if PMAIL_USE_LOCAL_STATE=1 "${BIN_PATH}" --config "${CFG_PATH}" --state "${STATE_PATH}" setup --non-interactive --username docs@example.com > "${WORK_DIR}/setup.log" 2>&1; then
  :
else
  status=$?
  cat "${WORK_DIR}/setup.log" >&2
  exit "$status"
fi

while IFS=$'\t' read -r out_file cmdline || [[ -n "${out_file:-}" ]]; do
  [[ -z "${out_file:-}" || "${out_file}" =~ ^# ]] && continue
  read -r -a cmd_args <<< "${cmdline}"
  PMAIL_USE_LOCAL_STATE=1 "${BIN_PATH}" --config "${CFG_PATH}" --state "${STATE_PATH}" "${cmd_args[@]}" > "${OUT_DIR}/${out_file}" 2>&1
done < "${SNAPSHOT_LIST}"

echo "Updated help snapshots in ${OUT_DIR}"
