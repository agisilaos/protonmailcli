# AGENTS.md

## Sessions

- New implementation chats start with `scripts/session start <name>` and work in the checkout it prints.
- Commit and land when authorized for each step. Land with `scripts/session land` and close a landed session with `scripts/session end`.
- For kickoff prompts, verification, or a stopped landing, read [Development sessions](docs/development-sessions.md).

## Change Discipline

For any code change that affects behavior, commands, output, exit codes, parsing, or safety policy:

1. Update or add automated tests.
2. Update relevant documentation (`README.md` and/or `docs/*.md`).
3. Run `scripts/release-check.sh` before merging or tagging. Session landing runs it in CI mode.

## Safety Default

- Prefer `--dry-run` first for mutating commands in automation.
- Do not send real emails in exploratory or validation runs unless explicitly requested.
