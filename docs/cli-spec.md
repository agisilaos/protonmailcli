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
- `--expect-account <username>` requires an exact match with the effective Bridge username for draft, message, search, mailbox, and tag commands. A mismatch exits 2 before Bridge access or receipt replay. Local-state mode rejects this option.

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
  follow-up

search
  messages
  drafts

mailbox
  list
  resolve

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

New receipts for the literal `Drafts` mailbox use `imap-v2:Drafts:<uid>` to
distinguish exact names from the older `imap:Drafts:<uid>` draft alias. Other
mailbox names keep `imap:<mailbox>:<uid>`. All ID flags and manifest fields accept
both formats; reuse the returned ID unchanged.

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

### `message get`

- `--message-id <id>` selects the message.
- `--password-file <path>` supplies the Bridge password file for this IMAP read.
  `PMAIL_SMTP_PASSWORD` takes precedence when set.

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

### `message follow-up`

- `--message-id <id>` required
  - accepts `imap:<mailbox>:<uid>`, `imap-v2:<mailbox>:<uid>`, or a raw local-state ID
- optional recipients via repeatable `--to <email>`
  - default recipient resolution:
    - received message: original `From`
    - sent message: original `To`
- optional `--subject <text>` override (default: `Re: <original subject>`)
- body via exactly one of:
  - `--body <text>`
  - `--body-file <path|->`
  - `--stdin`
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

Bridge mailbox discovery accepts both unquoted and quoted names, including the mailbox marked for drafts.

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
- Matching preserves whitespace in server names. Trimming surrounding query
  whitespace is a fallback only when it identifies one mailbox.
- `search messages --mailbox` uses the same lookup and returns the exact server
  name in its mailbox field and message IDs.
- Returns:
  - `mailbox` object (`id`, `name`, `kind`)
  - `matchedBy` (`name_exact`, `id_exact`, `name_casefold`)

Ambiguous mailbox lookups include shell-quoted exact names for a subsequent lookup.

## 7. I/O contract

### stdout

- Human mode prints the result fields as indented JSON. For example, `draft create`
  shows the draft ID you can pass to `draft get`, and `mailbox list` shows the
  available mailbox names and IDs. A fresh `--dry-run draft create` reports a
  creation plan with `wouldCreate`. Local previews retain `createPath: local_state`.
  In every output mode, the local draft plan retains the proposed `draft.to`,
  `draft.subject`, resolved `draft.body` (including file or stdin input), and
  `draft.tags` for review; JSON consumers can still read `.data.draft.to`.
  An untagged draft omits the optional `draft.tags` field in both preview and creation output.
  Only a stored draft has a receipt ID.
  Fresh local `tag create` and `filter create` previews also return plans with
  `action`, `wouldCreate`, and `source: local`, without IDs or creation timestamps.
  Tag plans retain `tag.name` and report `changed: true` for the proposed creation;
  filter plans retain `filter.name`, `filter.contains`, and `filter.addTag`.
  A tag that already exists instead retains its saved `tag.id` and reports `changed: false`.
  A success with no result fields prints `ok`.
  A command-level error shows its code on the first line for both LF- and
  CRLF-delimited messages. Recovery-command text on subsequent lines is preserved
  for copying. Any diagnostic fields
  follow, such as the endpoint checks in a failed `doctor` report.
  When a batch completes with failed items, its result lists each failure in
  `results[].errorCode` and `results[].error`.
- `--plain` prints one tab-delimited line of key/value pairs. Error records
  include `ok`, `error`, `message`, and `hint`. The error text fields escape
  backslash, tab, carriage return, and line feed as `\\`, `\t`, `\r`, and
  `\n`. When a result is available, the final `data` value is compact JSON.
  For an uncertain send batch it identifies the processed rows, including
  confirmed draft IDs and the row whose delivery needs reconciliation.
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
    "category": "usage",
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
