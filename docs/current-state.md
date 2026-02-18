# Current State

Status date: 2026-02-18

## Summary

`protonmailcli` is a working Bridge-first CLI with live IMAP/SMTP integration for core mailbox flows and a stable machine-friendly output contract.

## Implemented now

- Global output contract: `--json`, `--plain`, human mode
- Safety controls: `--no-input`, `--dry-run`, `--confirm-send`, exit codes
- Setup flows:
  - `setup --interactive`
  - `setup --non-interactive`
- Auth/session flows:
  - `auth login|status|logout`
- Diagnostics:
  - `doctor`
- Mail operations (live Bridge):
  - `mailbox list` (IMAP)
  - `draft create|get|list|update|delete` (IMAP `Drafts`)
  - `draft create-many --file|--stdin` (batch)
  - `message get` (IMAP `INBOX`)
  - `message send` (IMAP draft read + SMTP send)
  - `message send-many --file|--stdin` (batch)
  - `search messages|drafts` (IMAP SEARCH with `query/subject/from/to/has-tag/unread/since-id/after/before` + pagination)
  - `tag list|add|remove` (IMAP flags/keywords)
- Filter operations (local engine):
  - `filter list|create|delete|test|apply`
- Shell completion output:
  - `completion bash|zsh|fish`

## Data source matrix

- Bridge-backed:
  - `draft`, `message`, `search`, `mailbox`, `tag`
- Local-state-backed:
  - `filter`
  - auth session metadata
- Optional local-only mode for tests:
  - `PMAIL_USE_LOCAL_STATE=1`
  - now includes batch parity for `draft create-many` and `message send-many`

## Command architecture notes

- Resource dispatch (`mailbox`, `draft`, `message`, `search`, `tag`) now uses shared backend-router helpers so local-state and IMAP routing stays consistent.
- Send safety checks (confirm token and force policy) are centralized in one validator used by both local and IMAP send paths.
- IMAP-heavy command responses now use typed response structs instead of ad-hoc `map[string]any`, preserving JSON contract fields while reducing key drift risk.
- Manifest source, required-ID, and date parsing validations are centralized in shared helpers to keep flag behavior consistent across commands.
- Agent smoke workflow is available via `scripts/smoke-agent.sh` (local-state and dry-run only).
- IMAP subcommand help is parsed before Bridge auth/connect, so `--help` works even on un-authenticated environments.
- Batch send semantics: exit `10` on partial success, and non-zero failure (`1`) when all items fail.

## Tests

Automated tests currently cover:

- setup non-interactive
- draft create flow
- non-interactive send confirmation enforcement (`exit 7`)
- auth login/status/logout
- doctor unreachable bridge behavior (`exit 4`)
- completion generation
- executable contract fixtures (`tests/contracts/*.json`) via `TestContractFixtures`
- fallback handling when IMAP APPEND fails (tested path)

Run:

```bash
go test ./...
```

## Security and commit hygiene

- No credentials are stored in repository files.
- Runtime secrets are expected outside repo (for example `~/.config/protonmailcli/bridge.pass`).
- `.gitignore` excludes build artifacts and common secret files (`*.pass`, `*.key`, `*.pem`).

## Known gaps

- Filter actions are not yet backed by server-side Proton filter APIs.
- MIME handling is improved (`quoted-printable`, `base64`, multipart with text/plain preference) but HTML sanitization and attachments are not exposed as first-class structured parts yet.

## Recommended next steps

1. Add structured attachment extraction and optional HTML-to-text normalization.
2. Add CI workflow for `go test ./...`, contract fixtures, and lint/static checks.
3. Add server-backed filter management once Proton API path is selected.

## Release gate

Run this before cutting a tag:

```bash
scripts/release-check.sh
```
