# Agent Manifest Schemas

Use these schema files to validate batch manifests before executing CLI commands.

## Files

- `docs/schemas/draft-create-many.schema.json`
- `docs/schemas/message-send-many.schema.json`

## Command syntax

- `protonmailcli draft create-many --file <manifest.json>`
- `protonmailcli message send-many --file <manifest.json>`
- `cat manifest.json | protonmailcli draft create-many --stdin`
- `cat manifest.json | protonmailcli message send-many --stdin`

The following workflow uses Bridge. For an offline simulation with
`PMAIL_USE_LOCAL_STATE=1`, omit the Bridge-only `--expect-account` flag.

## Recommended agent workflow

1. Generate manifest JSON with a distinct item-level `idempotency_key` for each operation.
2. Validate against the schema.
3. Run CLI in dry-run mode first.
4. Execute the real command using those item keys and the same state file.

Before the first Bridge batch, set `BATCH_CONFIG` and `BATCH_STATE` to its
absolute config and state paths, `BATCH_PROFILE` to its profile, and
`BATCH_ACCOUNT` to the exact Bridge username whose drafts you reviewed.
Record all four values with the manifest and restore them before a retry.

To check the username, read `data.active` with
`./protonmailcli --json --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" bridge account list`.
If the active account is empty, read `data.username` from
`./protonmailcli --json --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" auth status`.
Each Bridge batch command below checks the recorded `BATCH_ACCOUNT` again
before it reads drafts, replays a receipt, or dispatches a row.

Use different keys for each draft or send batch and its individual rows. Both preview and
execution flag a row that reuses the command key.

For retrying corrected rows, use item-level keys alone and omit command-level
`--idempotency-key`. In Bridge mode, a command-level key caches the whole result,
including partial failures. Matching input replays that result. Changing a
recipient or body-file path changes the parsed manifest and returns
`idempotency_conflict` (exit 6). Restoring an input file does not resume a
completed batch. Local-state batches accept item keys and reject the command
flag (exit 2). When failed draft rows report only local input errors, correct
them and retry using their item keys and the original state file and account.
Replaying a full manifest with item keys requires the original item keys on
every successful row. If a send batch used only a command key, reconcile it and
submit only rows confirmed unsent. For draft creation, submit only drafts
confirmed missing. New keys cannot identify earlier successes.

Build an item-only send retry manifest from the original results:

- For an undispatched send row with no saved intent, retain its original item key.
- A pending item key stops the whole retry before dispatch, even if SMTP explicitly
  rejected the original send. Keep the state and receipt. Only after independently
  confirming non-delivery, put that row in the retry manifest with a fresh item key.
- Leave accepted sends and rows with unresolved delivery out of the manifest.

For draft rows confirmed safe to retry, run
`./protonmailcli --json --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" draft create-many --file drafts-to-retry.json`.
For send rows confirmed unsent, preview the retry manifest with
`./protonmailcli --json --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" --dry-run message send-many --file sends-to-retry.json`.
After reviewing the preview, run
`./protonmailcli --json --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" message send-many --file sends-to-retry.json`.

For draft creation failures after dispatch, retain the state and keys and
reconcile the outcome before retrying. If SMTP fallback was attempted, inspect
both INBOX and Drafts: accepted delivery can remain in INBOX when the move fails.
After uncertain APPEND or MOVE, also inspect the exact original destination named
in the error or saved recovery guidance, even if the Drafts role has moved.
An empty current Drafts folder alone is not proof of absence. Use a fresh item key only
for a draft confirmed missing from all those locations, and reconcile any existing
copy without dispatching another. See the [IMAP draft recovery
procedure](../README.md#imap-draft-identity-and-diagnostics).

In an item-keyed draft batch, a row whose key belongs to a different operation
reports `idempotency_conflict` before opening its file or consuming stdin.

Each draft row must choose one body source. An inline `body` must contain at
least one character. To create a draft with an empty body, supply `body_file`
pointing to an empty file. `body` and `body_file` are mutually
exclusive even when an explicitly supplied value is an empty string. Both local
and IMAP batches report conflicting sources as per-item validation errors before
reading that row's file or stdin.
With a manifest supplied via `--file`, `"body_file":"-"` reads the remaining
standard input. A second such row sees an empty body once the first row reaches
end of input. This applies with and without a command key. To give multiple
rows the same content, point them at the same regular file. Body files use the
path as supplied, including parent components that follow a symbolic link.
Item keys compare the original body bytes. A key recorded by an older version
for malformed UTF-8 can report a conflict. Inspect the original draft before retrying.

## Example draft-create-many manifest

```json
[
  {
    "to": ["contact@example.com"],
    "subject": "Intro",
    "body_file": "./drafts/intro.md",
    "idempotency_key": "draft-intro-001"
  }
]
```

## Example message-send-many manifest

Copy each returned draft ID unchanged into both `draft_id` and `confirm_send`.
This includes the `imap-v2:Drafts:<uid>` format for the literal Drafts mailbox.

```json
[
  {
    "draft_id": "imap-v2:Drafts:123",
    "confirm_send": "imap-v2:Drafts:123",
    "idempotency_key": "send-123-001"
  }
]
```
