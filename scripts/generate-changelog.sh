#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_FILE="${ROOT_DIR}/CHANGELOG.md"
VERSION=""
SINCE_REF=""
DATE_STR="$(date +%Y-%m-%d)"

usage() {
  cat <<EOF
Usage: $0 --version <vX.Y.Z> [--since <git-ref>] [--date YYYY-MM-DD] [--output <path>]

Examples:
  $0 --version v0.3.0
  $0 --version v0.3.0 --since v0.2.1
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --since)
      SINCE_REF="${2:-}"
      shift 2
      ;;
    --date)
      DATE_STR="${2:-}"
      shift 2
      ;;
    --output)
      OUTPUT_FILE="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ -z "${VERSION}" ]]; then
  echo "--version is required" >&2
  usage
  exit 2
fi

cd "${ROOT_DIR}"

if [[ -z "${SINCE_REF}" ]]; then
  if git describe --tags --abbrev=0 >/dev/null 2>&1; then
    SINCE_REF="$(git describe --tags --abbrev=0)"
  else
    SINCE_REF="$(git rev-list --max-parents=0 HEAD)"
  fi
fi

if [[ -f "${OUTPUT_FILE}" ]] && grep -q "^## \[${VERSION}\]" "${OUTPUT_FILE}"; then
  echo "changelog already contains ${VERSION}: ${OUTPUT_FILE}" >&2
  exit 1
fi

log_lines="$(git log --pretty=format:'%s|%h' "${SINCE_REF}..HEAD")"
if [[ -z "${log_lines}" ]]; then
  echo "no commits found in range ${SINCE_REF}..HEAD" >&2
  exit 1
fi

tmp_groups="$(mktemp)"
trap 'rm -f "${tmp_groups}" "${tmp_section:-}" "${tmp_out:-}"' EXIT

while IFS= read -r line; do
  [[ -z "${line}" ]] && continue
  subject="${line%%|*}"
  short_hash="${line##*|}"
  lower="$(echo "${subject}" | tr '[:upper:]' '[:lower:]')"
  group="Changed"
  if [[ "${lower}" =~ ^feat(\(|:)|^add(\(|:) ]]; then
    group="Added"
  elif [[ "${lower}" =~ ^fix(\(|:)|^bug(\(|:) ]]; then
    group="Fixed"
  elif [[ "${lower}" =~ ^refactor(\(|:) ]]; then
    group="Refactored"
  elif [[ "${lower}" =~ ^docs(\(|:) ]]; then
    group="Docs"
  elif [[ "${lower}" =~ ^test(\(|:) ]]; then
    group="Tests"
  elif [[ "${lower}" =~ ^chore(\(|:)|^build(\(|:)|^ci(\(|:) ]]; then
    group="Chore"
  fi
  printf '%s|%s (%s)\n' "${group}" "${subject}" "${short_hash}" >> "${tmp_groups}"
done <<< "${log_lines}"

tmp_section="$(mktemp)"
{
  echo "## [${VERSION}] - ${DATE_STR}"
  echo
  for group in Added Fixed Refactored Changed Docs Tests Chore; do
    group_lines="$(awk -F'|' -v g="${group}" '$1==g {print $2}' "${tmp_groups}")"
    [[ -z "${group_lines}" ]] && continue
    echo "### ${group}"
    while IFS= read -r entry; do
      [[ -z "${entry}" ]] && continue
      echo "- ${entry}"
    done <<< "${group_lines}"
    echo
  done
} > "${tmp_section}"

if [[ -f "${OUTPUT_FILE}" ]]; then
  tmp_out="$(mktemp)"
  {
    header_seen=0
    while IFS= read -r l || [[ -n "${l}" ]]; do
      if [[ ${header_seen} -eq 0 ]]; then
        echo "${l}"
        if [[ "${l}" == "# Changelog" ]]; then
          echo
          cat "${tmp_section}"
          header_seen=1
        fi
        continue
      fi
      echo "${l}"
    done < "${OUTPUT_FILE}"
  } > "${tmp_out}"
  mv "${tmp_out}" "${OUTPUT_FILE}"
else
  {
    echo "# Changelog"
    echo
    echo "All notable changes to this project will be documented in this file."
    echo
    cat "${tmp_section}"
  } > "${OUTPUT_FILE}"
fi

echo "Updated ${OUTPUT_FILE} with ${VERSION} from ${SINCE_REF}..HEAD"
