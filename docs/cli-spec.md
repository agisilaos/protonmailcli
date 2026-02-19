# protonmailcli CLI Specification (Current)

## 1. Name

`protonmailcli`

## 2. One-liner

Bridge-first Proton Mail automation CLI for humans and agents.

## 3. Usage

```text
protonmailcli [global flags] <resource> <action> [args]
protonmailcli setup [flags]
protonmailcli doctor
protonmailcli completion <bash|zsh|fish>
```

## 4. Global flags

- `-h, --help`
- `--version`
- `--json`
- `--plain`
- `--no-input`
- `-n, --dry-run`
- `--profile <name>`
- `--config <path>`
- `--state <path>`

## 5. Command tree

```text
auth
  login
  status
  logout

draft
  create
  create-many
  update
  get
  list
  delete

message
  send
  send-many
  get

search
  messages
  drafts

mailbox
  list

tag
  list
  create
  add
  remove

filter
  list
  create
  delete
  test
  apply

completion
  bash
  zsh
  fish

bridge
  account list
  account use
```

## 6. Key subcommand contracts

### `setup`

- `--interactive`
- `--non-interactive`
- `--bridge-host <host>`
- `--bridge-smtp-port <port>`
- `--bridge-imap-port <port>`
- `--username <email>`
- `--smtp-password-file <path>`
- `--profile <name>`

### `bridge account list`

- no flags
- lists known usernames and current active username

### `bridge account use`

- `--username <email>` required
- sets active Bridge account username in state

### `doctor`

- no sub-action
- returns grouped diagnostics for:
  - config prerequisites
  - auth prerequisites
  - bridge TCP checks (`imap`, `smtp`)

Exit behavior:

- `0`: all groups pass
- `3`: config/auth prerequisite failure (`doctor_prereq_failed`)
- `4`: bridge connectivity failure (`bridge_unreachable`)

### `draft create`

- `--to <email>` repeatable (required)
- `--subject <text>`
- exactly one of:
  - `--body <text>`
  - `--body-file <path|->`
  - `--stdin`
- `--idempotency-key <string>`

### `draft create-many`

- exactly one of:
  - `--file <path|->`
  - `--stdin`
- `--idempotency-key <string>`

### `draft update`

- `--draft-id <id>` required
- `--subject <text>`
- optional body mutation via one of:
  - `--body <text>`
  - `--body-file <path|->`
  - `--stdin`

### `message send`

- `--draft-id <id>` required
- `--confirm-send <token>` required in non-interactive mode unless `--force`
- `--force` (subject to safety policy)
- `--smtp-password-file <path>`
- `--idempotency-key <string>`

### `message send-many`

- exactly one of:
  - `--file <path|->`
  - `--stdin`
- `--smtp-password-file <path>`
- `--idempotency-key <string>`

### `search messages|drafts`

- `--query <text>`
- `--from <email>`
- `--to <email>`
- `--subject <text>`
- `--has-tag <name>`
- `--unread`
- `--since-id <uid>`
- `--after <date>` (`YYYY-MM-DD` or RFC3339)
- `--before <date>` (`YYYY-MM-DD` or RFC3339)
- `--limit <n>`
- `--cursor <token>`
- `--mailbox <name>` (messages only)

### `mailbox list`

- Returns mailbox objects with:
  - `id` (stable canonical ID)
  - `name` (server mailbox name)
  - `kind` (`system` or `custom`)

### `mailbox resolve`

- `--name <mailbox-id-or-name>` required
- Resolves by:
  - exact mailbox name
  - exact canonical mailbox ID
  - case-insensitive name (only when unique)
- Returns:
  - `mailbox` object (`id`, `name`, `kind`)
  - `matchedBy` (`name_exact`, `id_exact`, `name_casefold`)

## 7. I/O contract

### stdout

- Human mode: concise text output.
- `--plain`: stable tab-delimited line output.
- `--json`: exactly one JSON envelope object.

### stderr

- diagnostics, warnings, and hints.

## 8. JSON contract

Success:

```json
{
  "ok": true,
  "data": {},
  "meta": {
    "requestId": "req_...",
    "profile": "default",
    "durationMs": 0,
    "timestamp": "2026-02-18T00:00:00Z"
  }
}
```

Command-specific telemetry fields for agents:

- `draft create`: `data.createPath`
- `draft create-many`: `data.results[].createPath`
- `message send`: `data.sendPath`
- `message send-many`: `data.results[].sendPath`

Error:

```json
{
  "ok": false,
  "error": {
    "code": "validation_error",
    "message": "...",
    "hint": "...",
    "retryable": false
  },
  "meta": {
    "requestId": "req_...",
    "profile": "default",
    "durationMs": 0,
    "timestamp": "2026-02-18T00:00:00Z"
  }
}
```

## 9. Exit codes

- `0` success
- `1` runtime failure
- `2` usage/validation
- `3` auth/config/session
- `4` network/send failure
- `5` not found
- `6` conflict
- `7` confirmation/safety blocked
- `8` rate limit
- `10` partial success
