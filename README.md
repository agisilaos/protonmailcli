# protonmailcli

Bridge-first CLI for Proton Mail workflows with a strong automation contract.

`protonmailcli` lets humans and agents compose drafts, send messages, search content, manage tags, and apply simple filters from the terminal.

## What is implemented now

- Interactive and non-interactive setup (`setup`)
- Auth session commands (`auth login|status|logout`)
- Bridge connectivity diagnostics (`doctor`)
- Draft lifecycle: create, update, get, list, delete
- Send message from draft with non-interactive safety gate
- Search drafts/messages
- Mailbox listing (`mailbox list`)
- Tag operations: list, create, add, remove
- Filter operations: list, create, test, apply, delete
- Shell completion output (`completion bash|zsh|fish`)
- Stable `--json` and `--plain` output modes
- Persistent local state store
- IMAP + SMTP Bridge path for live mailbox operations

## Backend matrix (current)

- `auth`: local session state + Bridge credentials
- `doctor`: Bridge TCP checks (IMAP/SMTP)
- `draft`: Bridge IMAP (`Drafts`)
- `message get`: Bridge IMAP (`INBOX`)
- `message send`: draft fetch via IMAP + send via SMTP
- `search`: Bridge IMAP (`INBOX`/`Drafts`)
- `mailbox list`: Bridge IMAP (`LIST`)
- `tag add/remove/list`: Bridge IMAP flags/keywords
- `filter`: local state engine (not yet IMAP-server-side rules)

## Build

```bash
cd protonmailcli
go build -o protonmailcli ./cmd/protonmailcli
```

## Setup

### Human interactive setup

```bash
./protonmailcli setup --interactive
```

This prompts for profile + Bridge host/ports/username and writes config to:

- `~/.config/protonmailcli/config.toml`

### Agent non-interactive setup

```bash
./protonmailcli --json --no-input setup --non-interactive \
  --profile default \
  --bridge-host 127.0.0.1 \
  --bridge-smtp-port 1025 \
  --bridge-imap-port 1143 \
  --username you@proton.me \
  --smtp-password-file ~/.config/protonmailcli/bridge.pass
```

## Auth and diagnostics

Login for agent workflows (writes auth session into local state):

```bash
./protonmailcli --json --no-input auth login \
  --username you@proton.me \
  --password-file ~/.config/protonmailcli/bridge.pass
```

Check auth/session:

```bash
./protonmailcli --json auth status
```

Bridge health checks:

```bash
./protonmailcli --json doctor
```

## Core usage

Create draft:

```bash
./protonmailcli --json draft create \
  --to alice@example.com \
  --subject "Weekly sync" \
  --body "status update"
```

List live drafts from Bridge IMAP:

```bash
./protonmailcli --json draft list
```

Draft IDs in IMAP mode look like `imap:Drafts:<uid>` and can be passed back to `draft get`, `draft delete`, and `message send`.

Send draft safely in non-interactive mode:

```bash
export PMAIL_SMTP_PASSWORD="your-bridge-password"
./protonmailcli --json --no-input message send \
  --draft-id d_123 \
  --confirm-send d_123
```

Dry-run send:

```bash
./protonmailcli --json --no-input --dry-run message send \
  --draft-id d_123 \
  --confirm-send d_123
```

Search:

```bash
./protonmailcli --json search drafts --query sync
./protonmailcli --json search messages --query invoice
```

Mailboxes:

```bash
./protonmailcli --json mailbox list
```

Tags:

```bash
./protonmailcli --json tag create --name finance
./protonmailcli --json tag add --message-id m_123 --tag finance
```

Filters:

```bash
./protonmailcli --json filter create --name invoices --contains invoice --add-tag finance
./protonmailcli --json filter test --filter-id f_123
./protonmailcli --json filter apply --filter-id f_123
```

Generate shell completion:

```bash
./protonmailcli completion zsh
./protonmailcli completion bash
./protonmailcli completion fish
```

## Safety model

- `--no-input` disables prompts and forces explicit intent.
- Non-interactive `message send` requires `--confirm-send <draft-id>` unless `--force`.
- `--dry-run` returns planned behavior without state changes.
- Errors return stable exit codes and structured JSON when `--json` is set.

## Config and state

Defaults:

- Config: `~/.config/protonmailcli/config.toml`
- State: `~/.local/share/protonmailcli/state.json`

Selected env vars:

- `PMAIL_SMTP_PASSWORD`
- `PMAIL_PROFILE`
- `PMAIL_OUTPUT`
- `PMAIL_TIMEOUT`

## Output and exit codes

- `stdout`: result payload only
- `stderr`: hints, warnings, diagnostics
- `--json`: single envelope object

Exit codes:

- `0` success
- `1` runtime failure
- `2` usage/validation
- `3` auth/config/session failure
- `4` network/send failure
- `5` not found
- `6` conflict
- `7` safety confirmation required/block
- `8` rate limit
- `10` partial success

## Test

```bash
go test ./...
```

Current automated tests cover:

- non-interactive setup flow
- draft creation flow
- non-interactive send confirmation enforcement (`exit 7`)
- auth login/status/logout flow
- doctor failure exit code (`exit 4`)
- completion output generation

## Docs

- CLI spec: `docs/cli-spec.md`
- Config/security: `docs/config-and-security.md`
- Test strategy: `docs/testing-strategy.md`
- Current implementation status: `docs/current-state.md`
