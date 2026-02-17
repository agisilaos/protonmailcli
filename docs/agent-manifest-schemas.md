# Agent Manifest Schemas

Use these schema files to validate batch manifests before executing CLI commands.

## Files

- `docs/schemas/draft-create-many.schema.json`
- `docs/schemas/message-send-many.schema.json`

## Commands that consume these manifests

- `protonmailcli draft create-many --file <manifest.json>`
- `protonmailcli message send-many --file <manifest.json>`

## Recommended agent workflow

1. Generate manifest JSON.
2. Validate against the schema.
3. Run CLI in dry-run mode first.
4. Execute real command with `--idempotency-key`.

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

```json
[
  {
    "draft_id": "imap:Drafts:123",
    "confirm_send": "imap:Drafts:123",
    "idempotency_key": "send-123-001"
  }
]
```
