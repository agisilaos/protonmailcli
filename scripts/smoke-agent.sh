#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_PATH="${ROOT_DIR}/.tmp/protonmailcli-smoke"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

echo "[smoke] build binary"
mkdir -p "${ROOT_DIR}/.tmp"
go build -o "${BIN_PATH}" ./cmd/protonmailcli

CFG_PATH="${TMP_DIR}/config.toml"
STATE_PATH="${TMP_DIR}/state.json"

echo "[smoke] setup local-state mode"
PMAIL_USE_LOCAL_STATE=1 "${BIN_PATH}" --json --config "${CFG_PATH}" --state "${STATE_PATH}" \
  setup --non-interactive --username "agent@example.com" >/dev/null

echo "[smoke] create draft from stdin"
draft_out="$(printf 'Hello from smoke test\n' | PMAIL_USE_LOCAL_STATE=1 "${BIN_PATH}" --json --no-input --config "${CFG_PATH}" --state "${STATE_PATH}" \
  draft create --to "to@example.com" --subject "smoke" --stdin)"
if [[ "${draft_out}" != *"\"ok\":true"* ]]; then
  echo "draft create did not return ok=true" >&2
  exit 1
fi
if [[ "${draft_out}" == *"\"sentAt\":"* ]]; then
  echo "draft create unexpectedly included sentAt for unsent draft" >&2
  exit 1
fi

draft_id="$(sed -n 's/.*"\(d_[0-9][0-9]*\)".*/\1/p' "${STATE_PATH}" | head -n1)"
if [[ -z "${draft_id}" ]]; then
  echo "could not determine draft id from state" >&2
  exit 1
fi

echo "[smoke] send dry-run (no real send)"
send_out="$(PMAIL_USE_LOCAL_STATE=1 "${BIN_PATH}" --json --no-input --dry-run --config "${CFG_PATH}" --state "${STATE_PATH}" \
  message send --draft-id "${draft_id}" --confirm-send "${draft_id}")"
if [[ "${send_out}" != *"\"wouldSend\":true"* ]]; then
  echo "dry-run send did not report wouldSend=true" >&2
  exit 1
fi

echo "[smoke] verify help snapshots"
"${ROOT_DIR}/scripts/check-help.sh" >/dev/null

echo "agent smoke checks passed"
