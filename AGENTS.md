# AGENTS.md

## Change Discipline

For any code change that affects behavior, commands, output, exit codes, parsing, or safety policy:

1. Update or add automated tests.
2. Update relevant documentation (`README.md` and/or `docs/*.md`).
3. Run `scripts/release-check.sh` before merging or tagging.

## Safety Default

- Prefer `--dry-run` first for mutating commands in automation.
- Do not send real emails in exploratory or validation runs unless explicitly requested.
