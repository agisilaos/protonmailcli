# Current State

Status date: 2026-02-15

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
  - `message get` (IMAP `INBOX`)
  - `message send` (IMAP draft read + SMTP send)
  - `search messages|drafts` (IMAP SEARCH with `query/from/to/after/before` + pagination)
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

## Tests

Automated tests currently cover:

- setup non-interactive
- draft create flow
- non-interactive send confirmation enforcement (`exit 7`)
- auth login/status/logout
- doctor unreachable bridge behavior (`exit 4`)
- completion generation
- executable contract fixtures (`tests/contracts/*.json`) via `TestContractFixtures`

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
- IMAP message parsing is basic plaintext extraction for now.
- Contract fixture runner (`tests/contracts/*.json`) is defined but not yet executable as a test harness.

## Recommended next steps

1. Add full MIME parsing for text/plain + text/html + multipart bodies.
2. Add paginated listing and richer search flags (`from`, `to`, date ranges) on IMAP paths.
3. Introduce a contract-test runner for `tests/contracts/*.json`.
4. Add CI workflow for `go test ./...` and static checks.
