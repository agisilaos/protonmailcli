# protonmailcli v1 CLI Specification

## 1. Name

`protonmailcli`

## 2. One-liner

Bridge-first Proton Mail automation CLI for humans and agents.

## 3. USAGE

```text
protonmailcli [global flags] <resource> <action> [args]
protonmailcli help [resource|resource action]
```

## 4. Global flags

- `-h, --help` show help and ignore other args
- `--version` print version
- `--json` machine-readable output
- `--plain` stable line-oriented output
- `-q, --quiet` reduce non-data output
- `-v, --verbose` increase diagnostics on stderr
- `--no-color` disable ANSI formatting
- `--no-input` disable prompts and interactive confirmation
- `--profile <name>` select profile
- `--timeout <duration>` command timeout (default: `30s`)
- `-n, --dry-run` preview mutating operations with no side effects

## 5. Command tree

```text
auth
  login
  status
  logout

draft
  create
  update
  get
  list
  delete

message
  send
  get

search
  messages
  drafts

tag
  list
  create
  rename
  delete
  add
  remove

filter
  list
  create
  update
  delete
  test
  apply

mailbox
  list

completion
  bash
  zsh
  fish
  powershell
```

## 6. Subcommand contracts

### `auth login`

Purpose: authenticate against Bridge-backed profile.

Flags:

- `--username <string>`
- `--password-file <path|->` (required in non-interactive mode)
- `--store <keychain|file>` default `keychain`

State change: creates/updates session and credential references.

### `auth status`

Purpose: show profile auth/session health.

State change: none.

### `draft create`

Purpose: create draft message.

Flags:

- `--to <email>` repeatable, min one recipient required
- `--cc <email>` repeatable
- `--bcc <email>` repeatable
- `--subject <text>` optional
- `--body <text>` or `--body-file <path|->` (exactly one required)
- `--attach <path>` repeatable
- `--tag <name>` repeatable

State change: creates draft resource.

### `draft update`

Purpose: partial update of existing draft.

Flags:

- `--draft-id <id>` required
- same mutation flags as `draft create`
- `--if-match <etag>` optional optimistic concurrency
- `--clear-cc`, `--clear-bcc`, `--clear-tags`

State change: updates draft resource.

### `draft get`

Flags:

- `--draft-id <id>` required

State change: none.

### `draft list`

Flags:

- `--mailbox <name>` optional
- `--tag <name>` repeatable
- `--after <date>`
- `--before <date>`
- `--limit <n>` default `50`
- `--cursor <token>` optional

State change: none.

### `draft delete`

Flags:

- `--draft-id <id>` required
- `--hard` permanent delete
- `-f, --force` bypass confirmation

State change: deletes draft (soft by default).

### `message send`

Purpose: send either existing draft or inline compose payload.

Flags:

- `--draft-id <id>` OR full compose fields
- `--schedule-at <rfc3339>` optional
- `--idempotency-key <string>` optional, recommended for automation
- `--confirm-send <token>` required for non-TTY or `--no-input`, unless `--force`
- `-f, --force` bypass confirmation policy (warns on stderr)

State change: sends message (irreversible).

### `message get`

Flags:

- `--message-id <id>` required

State change: none.

### `search messages|drafts`

Flags:

- `--query <text>`
- `--from <email>`
- `--to <email>`
- `--subject <text>`
- `--tag <name>` repeatable
- `--after <date>`
- `--before <date>`
- `--limit <n>` default `50`
- `--cursor <token>`

State change: none.

### `tag create|rename|delete|add|remove`

- `create --name <string>`
- `rename --tag-id <id> --name <string>`
- `delete --tag-id <id> [--force]`
- `add --message-id <id> --tag <name>` (idempotent)
- `remove --message-id <id> --tag <name>` (idempotent)

### `filter create|update|delete|test|apply`

- `create --name <string> --match-file <path|-> --action-file <path|->`
- `update --filter-id <id> ...`
- `delete --filter-id <id> [--force]`
- `test --filter-id <id> --sample-file <path|->`
- `apply --filter-id <id> [--dry-run]`

## 7. I/O contract

### stdout

- Human mode: concise, readable summaries.
- `--plain`: stable line output, tab-delimited where multiple fields are emitted.
- `--json`: exactly one JSON object per invocation.

### stderr

- errors, warnings, diagnostics, retries, progress bars/spinners.
- no diagnostics on stdout.

### TTY policy

- rich output only when stdout is TTY.
- no progress animations in non-TTY mode.

## 8. JSON output contract

Common envelope (all commands):

```json
{
  "ok": true,
  "data": {},
  "meta": {
    "requestId": "req_...",
    "profile": "default",
    "durationMs": 0,
    "timestamp": "2026-02-15T00:00:00Z"
  },
  "warnings": []
}
```

Error envelope:

```json
{
  "ok": false,
  "error": {
    "code": "confirmation_required",
    "message": "--confirm-send is required in non-interactive mode",
    "hint": "Pass --confirm-send <draft-id> or rerun with --force"
  },
  "meta": {
    "requestId": "req_..."
  }
}
```

## 9. Exit codes

- `0` success
- `1` generic runtime failure
- `2` usage/validation
- `3` auth/session failure
- `4` network timeout/unreachable
- `5` not found
- `6` conflict/precondition failed
- `7` confirmation/safety blocked
- `8` rate limit
- `10` partial success

## 10. Config + precedence

Primary config file: `~/.config/protonmailcli/config.toml`

Optional project config: `./.protonmailcli.toml`

Precedence: `flags > env > project config > user config > defaults`

## 11. Examples

```bash
protonmailcli auth login --profile work --username ops@example.com --password-file - < ~/.secrets/proton.pass
protonmailcli draft create --to a@x.com --subject "Spec" --body-file - --json < body.md
protonmailcli draft update --draft-id d_123 --tag urgent --json
protonmailcli draft list --tag urgent --limit 20 --plain
protonmailcli message send --draft-id d_123 --confirm-send d_123 --json --no-input
protonmailcli search messages --query "invoice" --after 2026-01-01 --json
protonmailcli tag add --message-id m_456 --tag finance --json
protonmailcli filter test --filter-id f_123 --sample-file sample.eml --json
protonmailcli filter apply --filter-id f_123 --dry-run --json
protonmailcli completion zsh
```
