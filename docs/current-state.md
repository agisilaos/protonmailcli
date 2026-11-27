# Current State

Status date: 2026-11-27
Last release: v0.2.2 (2026-02-25)
Unreleased on `master`: 122 commits

## Summary

`protonmailcli` is a working Bridge-first CLI with live IMAP/SMTP integration
for core mailbox flows and a stable machine-friendly output contract. The
command surface has not grown since v0.2.2; the work since then is transport
security, input handling and durability hardening, plus the worktree session
tooling used to develop it.

## Implemented now

- Global output contract: `--json`, `--plain`, human mode
- Safety controls: `--no-input`, `--dry-run`, `--confirm-send`, exit codes
- Setup flows:
  - `setup --interactive`
  - `setup --non-interactive`
- Auth/session flows:
  - `auth login|status|logout`
  - `bridge account list|use`
- Diagnostics:
  - `doctor`
- Mail operations (live Bridge):
  - `mailbox list|resolve` (IMAP, with stable `id/name/kind` mapping)
  - `draft create|get|list|update|delete` (IMAP `Drafts`)
  - `draft create-many --file|--stdin` (batch)
  - `message get` (IMAP `INBOX`)
  - `message send` (IMAP draft read + SMTP send)
  - `message follow-up` (threaded draft creation from existing message IDs)
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
  - active bridge account username selection
- Optional local-only mode for tests:
  - `PMAIL_USE_LOCAL_STATE=1`
  - includes batch parity for `draft create-many` and `message send-many`, plus
    `message follow-up`

## Command architecture notes

- Resource dispatch (`mailbox`, `draft`, `message`, `search`, `tag`) uses shared
  backend-router helpers so local-state and IMAP routing stays consistent.
- Mailbox discovery returns canonical mailbox IDs (`inbox`, `drafts`, `sent`,
  etc.) with `kind=system|custom` so agents can map folders deterministically
  across Bridge variants.
- Send safety checks (confirm token and force policy) are centralized in one
  validator used by both local and IMAP send paths.
- IMAP-heavy command responses use typed response structs instead of ad-hoc
  `map[string]any`, preserving JSON contract fields while reducing key drift.
- Draft/send responses include machine-readable path telemetry:
  - `createPath`: `imap_append` or `smtp_move_fallback` (IMAP), `local_state` (local mode)
  - `sendPath`: `smtp` (IMAP), `local_state` (local mode)
  - batch variants expose the same fields per result item
- Subcommand `--help` in JSON mode is normalized across core agent paths and
  does not require Bridge auth for help-only execution.
- Help snapshot generation is manifest-driven via `scripts/help-snapshots.txt`
  and uses isolated local-state setup for deterministic outputs.
- Batch send semantics: exit `10` on partial success, and non-zero failure (`1`)
  when all items fail.
- Late global flags fail fast with usage guidance (global flags must appear
  before the resource).
- Batch manifests use per-item validation for runtime item errors instead of
  aborting the whole command on the first malformed item.

## Hardening since v0.2.2

- Bridge transport security: IMAP verifies the server certificate and the
  configured hostname before login, including on loopback addresses; every
  certificate in a trust bundle is validated; validated trust is reused across
  one operation; a failed TLS upgrade closes the socket. Certificate
  verification failures are classified as configuration errors. See
  [configuration and Bridge transport behavior](config-and-transport.md).
- Setup and prompt correctness: conflicting input modes are rejected before
  prompting, invalid Bridge ports before any write, and configuration is
  preserved when a prompt cannot be written or read.
- State and recovery durability: prior state survives failed saves, creation
  intent is persisted before dispatch, ambiguous draft creation is classified
  as non-retryable, and checkpoints retain confirmed draft and send outcomes
  across batch operations.
- Output contract: plain errors stay on one line and carry diagnostic data;
  human mode shows command results and doctor checks alongside errors; a closed
  stdout pipe does not retry work or lose command status.
- Development workflow: `scripts/session` provides worktree sessions with
  direct landing, review and qualification gates.

## Tests

110 of the 170 Go files in the repository are tests. They live beside the code
they exercise, with declarative fixtures under `tests/contracts/`. Covered
areas include setup and prompt handling, auth and session flows, doctor exit
codes, draft and send flows with their IMAP fallback and uncertainty paths,
batch checkpointing and recovery, TLS configuration and certificate rejection,
state persistence, locking and durability, output envelope and stream
discipline, and platform-specific state paths. Bridge end-to-end tests are
opt-in and excluded from CI.

Run:

```bash
go test ./...
```

See [testing strategy](testing-strategy.md) for layers, invariants and gates.

## Verification and CI

`make verify` is the full local gate. One required GitHub workflow,
`.github/workflows/release-check.yml`, runs `make release-check-ci` on
`macos-latest` for pull requests and pushes to `master`, reaching the same
`make verify` path.

Two commit-time hooks run earlier: `pre-commit` checks staged whitespace, Go
formatting and the docs-reality check, and `commit-msg` requires a blank line
after the subject. Install them per clone with
`python3 -B scripts/install-hooks.py`.

`scripts/docs-reality-check.py` fails when a backticked path, Make target or
`PMAIL_*` variable in the docs names nothing real. `scripts/session list` and
`scripts/session prune` report and clean up session worktrees, archiving every
HEAD they remove under `refs/archive/`.

## Security and commit hygiene

- No credentials are stored in repository files.
- Runtime secrets are expected outside the repository (for example
  `~/.config/protonmailcli/bridge.pass`).
- `.gitignore` excludes build artifacts and common secret files (`*.pass`,
  `*.key`, `*.pem`).

## Known gaps

- Filter actions are not backed by server-side Proton filter APIs; `filter` is
  a local engine only, and no Proton API path has been selected yet.
- MIME handling covers `quoted-printable`, `base64`, declared character sets and
  multipart with inline text preference, but attachments are not exposed as
  first-class structured parts and HTML is a body fallback rather than a
  normalized text rendering.
- No release has been cut since v0.2.2, so none of the hardening above has
  reached a tag, the Homebrew tap or GitHub Release notes.

## Recommended next steps

1. Cut the next release: generate the changelog section from the 122 unreleased
   commits, then run `scripts/release-check.sh vX.Y.Z` and
   `scripts/release.sh`. See [RELEASING.md](../RELEASING.md).
2. Add structured attachment extraction and optional HTML-to-text
   normalization.
3. Select a Proton API path for server-backed filter management, or document
   `filter` as permanently local.

## Release gate

Run this before cutting a tag:

```bash
scripts/release-check.sh vX.Y.Z
```
