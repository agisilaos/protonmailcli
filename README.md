# protonmailcli

Bridge-first CLI for Proton Mail workflows with a strong automation contract.

`protonmailcli` lets humans and agents compose drafts, send messages, search content, manage tags, and apply simple filters from the terminal.

## What is implemented now

- Interactive and non-interactive setup (`setup`)
- Auth session commands (`auth login|status|logout`)
- Bridge account selection (`bridge account list|use`)
- Bridge diagnostics (`doctor`: config/auth prerequisites + Bridge TCP checks)
- Draft lifecycle: create, update, get, list, delete
- Bulk draft creation: `draft create-many --file|--stdin`
- Send message from draft with non-interactive safety gate
- Bulk send workflow: `message send-many --file|--stdin`
- Search drafts/messages
- Mailbox listing (`mailbox list`)
- Tag operations: list, create, add, remove
- Filter operations: list, create, test, apply, delete
- Shell completion output (`completion bash|zsh|fish`)
- Stable `--json` and `--plain` output modes
- Idempotency keys on mutating commands
- Persistent local state store
- IMAP + SMTP Bridge path for live mailbox operations

## Backend matrix (current)

- `auth`: local session state + Bridge credentials
- `bridge account`: active Bridge username selection state
- `doctor`: config + auth prerequisites + Bridge TCP checks (IMAP/SMTP)
- `draft`: Bridge IMAP (`Drafts`)
- `message get`: Bridge IMAP (`INBOX`)
- `message send`: draft fetch via IMAP + send via SMTP
- `search`: Bridge IMAP (`INBOX`/`Drafts`)
- `mailbox list`: Bridge IMAP (`LIST`)
- `tag add/remove/list`: Bridge IMAP flags/keywords
- `filter`: local state engine (not yet IMAP-server-side rules)
- local-state mode parity: supports `draft create-many` and `message send-many` for offline/agent contract testing

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

List/select Bridge account profile:

```bash
./protonmailcli --json bridge account list
./protonmailcli --json bridge account use --username you@proton.me
```

Bridge health checks:

```bash
./protonmailcli --json doctor
```

`doctor` now returns grouped diagnostics in JSON:

- `summary.bridge`
- `summary.authPrereqs`
- `summary.config`
- `doctor.bridge.checks` (IMAP/SMTP TCP checks)

## Core usage

Create draft:

```bash
./protonmailcli --json draft create \
  --to alice@example.com \
  --subject "Weekly sync" \
  --body "status update" \
  --idempotency-key draft-weekly-sync-001
```

Create draft from stdin (agent-safe piping):

```bash
cat ./drafts/weekly-sync.md | ./protonmailcli --json --no-input draft create \
  --to alice@example.com \
  --subject "Weekly sync" \
  --stdin
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
  --confirm-send d_123 \
  --idempotency-key send-d123-001
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
./protonmailcli --json search messages --from billing@example.com --after 2026-01-01 --limit 25
./protonmailcli --json search messages --query invoice --limit 50 --cursor 50
./protonmailcli --json search messages --mailbox "All Mail" --to info@example.com --before 2026-03-01
./protonmailcli --json search messages --subject "invoice" --has-tag invoices --unread --since-id 1000
```

Mailboxes:

```bash
./protonmailcli --json mailbox list
```

`mailbox list` returns stable mapping metadata per mailbox:

- `id`: canonical key (`inbox`, `drafts`, `sent`, `all_mail`, or sanitized custom ID)
- `name`: Bridge mailbox name
- `kind`: `system` or `custom`

Tags:

```bash
./protonmailcli --json tag create --name finance
./protonmailcli --json tag add --message-id m_123 --tag finance
```

In IMAP mode, tags are IMAP keywords/flags on messages (not folder names).

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
- Unset optional timestamps are omitted (for example unsent drafts omit `sentAt`)
- Path telemetry for agents:
  - `draft create` / `draft create-many[*]`: `createPath`
  - `message send` / `message send-many[*]`: `sendPath`
  - IMAP values: `imap_append`, `smtp_move_fallback`, `smtp`
  - local-state value: `local_state`

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

Run executable contract fixtures only:

```bash
go test ./internal/app -run TestContractFixtures -v
```

Run optional real-Bridge integration test (manual/gated):

```bash
PMAIL_E2E_BRIDGE=1 \
PMAIL_E2E_USERNAME=you@proton.me \
PMAIL_E2E_PASSWORD='bridge-password' \
go test -tags integration ./internal/app -run TestBridgeE2EDraftCreateSearchSend -v
```

Validate CLI help contracts (recommended before release):

```bash
scripts/check-help.sh
```

If help output changed intentionally, refresh snapshots:

```bash
scripts/update-help.sh
```

Run agent smoke checks (no real sends):

```bash
scripts/smoke-agent.sh
```

Run full pre-release checks:

```bash
scripts/release-check.sh
```

Batch manifest schemas:

- `docs/schemas/draft-create-many.schema.json`
- `docs/schemas/message-send-many.schema.json`
- `docs/agent-manifest-schemas.md`

## Agent usage pattern

The CLI is designed for deterministic agent loops:

1. Start with `--json --no-input`.
2. Read `nextCursor` and continue until it is empty.
3. Use returned IDs directly (`imap:Drafts:<uid>`, `imap:INBOX:<uid>`).
4. Use `--dry-run` before mutating commands in planning phases.

Example pagination loop:

```bash
./protonmailcli --json --no-input search messages --query invoice --limit 100 --cursor 0
# parse .data.nextCursor, then call again with --cursor <nextCursor> until empty
```

## For Coding Agents

If this repo is handed to an autonomous agent (Codex, Claude Code, GitHub agents), use this operating pattern:

1. Bootstrap and verify:
```bash
go test ./...
./protonmailcli --json doctor
./protonmailcli --json auth status
```
2. Run only machine mode in automations:
```bash
./protonmailcli --json --no-input <command> ...
```
3. Read IDs from output and feed them back without transformation:
  `imap:Drafts:<uid>` for draft operations, `imap:INBOX:<uid>` for message operations.
4. Route by path telemetry when needed:
  use `data.createPath` / `data.sendPath` (or per-item `results[].createPath` / `results[].sendPath`) for deterministic branching.
5. Use safety-first execution for mutating actions:
  run with `--dry-run` first, then execute with explicit confirmation flags.
6. Page deterministically:
  use `--limit` and `--cursor`; stop when `nextCursor` is empty.
7. Validate behavior contracts before proposing changes:
```bash
go test ./internal/app -run TestContractFixtures -v
```

Recommended agent sequence for “find and send draft”:

```bash
./protonmailcli --json --no-input search drafts --query "KAOS" --limit 1 --cursor 0
./protonmailcli --json --no-input message send --draft-id imap:Drafts:1 --confirm-send imap:Drafts:1
```

## Agent Playbooks

Below are practical patterns an agent can run reliably.

### 1. File-based draft creation (recommended)

When an LLM generates long email content, write it to a file and pass it with `--body-file`.

```bash
./protonmailcli --json --no-input draft create \
  --to info@example.com \
  --subject "Partnership follow-up" \
  --body-file ./drafts/partnership-followup.md
```

Why: avoids shell escaping issues and keeps prompt/content separation clean.

### 2. Batch draft ingestion from a manifest

Maintain a manifest (`drafts.json`) and create drafts in a loop.

Example manifest shape:

```json
[
  {"to":["a@example.com"],"subject":"Hello A","body_file":"./drafts/a.md"},
  {"to":["b@example.com"],"subject":"Hello B","body_file":"./drafts/b.md"}
]
```

Example execution loop:

```bash
jq -c '.[]' drafts.json | while read -r item; do
  to=$(echo "$item" | jq -r '.to')
  subject=$(echo "$item" | jq -r '.subject')
  body_file=$(echo "$item" | jq -r '.body_file')
  ./protonmailcli --json --no-input draft create --to "$to" --subject "$subject" --body-file "$body_file"
done
```

### 3. Search with filters and pagination

Use deterministic pagination for large inboxes.

```bash
./protonmailcli --json --no-input search messages \
  --mailbox "All Mail" \
  --from billing@example.com \
  --after 2026-01-01 \
  --limit 100 \
  --cursor 0
```

Then continue with returned `nextCursor` until it is empty.

### 4. Safe non-interactive send

Always send with explicit confirmation token in automation.

```bash
./protonmailcli --json --no-input message send \
  --draft-id imap:Drafts:123 \
  --confirm-send imap:Drafts:123
```

Preflight before bulk sends:

```bash
./protonmailcli --json --no-input --dry-run message send \
  --draft-id imap:Drafts:123 \
  --confirm-send imap:Drafts:123
```

### 5. Tag-driven triage

Attach tags to messages as an agent post-processing step.

```bash
./protonmailcli --json --no-input tag add \
  --message-id imap:INBOX:845 \
  --tag invoices
```

### 6. Contract-safe changes in CI

Before changing behavior, run executable contract fixtures:

```bash
go test ./internal/app -run TestContractFixtures -v
```

Treat fixture breaks as API contract changes and update fixtures intentionally.

### 7. Native batch operations

Create many drafts from a single manifest:

```bash
./protonmailcli --json --no-input draft create-many --file drafts.json --idempotency-key batch-drafts-001
```

Or pass manifest via stdin:

```bash
cat drafts.json | ./protonmailcli --json --no-input draft create-many --stdin --idempotency-key batch-drafts-001
```

Send many drafts with explicit confirmations:

```bash
./protonmailcli --json --no-input message send-many --file sends.json --idempotency-key batch-send-001
```

Or pass send manifest via stdin:

```bash
cat sends.json | ./protonmailcli --json --no-input message send-many --stdin --idempotency-key batch-send-001
```

### 8. Retry-aware errors for agents

In `--json` mode, all failures include machine-readable error fields:

- `error.code` (stable programmatic code)
- `error.retryable` (`true` for transient/network-class failures)

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

## License

MIT (`LICENSE`)
