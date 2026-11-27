# AGENTS.md

## Sessions

- New implementation chats start with `scripts/session start <name>` and work in the checkout it prints.
- Commit and land when authorized for each step. Authorization for one does not cover the other.
- Land with `scripts/session land` and close a landed session with `scripts/session end`.
- `scripts/session list` shows every session worktree with its state and last commit. `scripts/session prune` proposes removing the landed ones and `--apply` carries it out, archiving each HEAD under `refs/archive/` first. It keeps anything dirty or unlanded.
- For kickoff prompts, verification, or a stopped landing, read [Development sessions](docs/development-sessions.md).

## Change Discipline

Run `python3 -B scripts/install-hooks.py` once per clone. The pre-commit hook checks staged whitespace, Go formatting and the docs-reality check; the commit-msg hook requires a blank line after the subject. Both are shared by every worktree.

A commit message is a subject, a blank line, then a body with **What changed**, **Why**, **Alternatives considered** and **Validation**.

For any code change that affects behavior, commands, output, exit codes, parsing, or safety policy:

1. Update or add automated tests.
2. Update relevant documentation (`README.md` and/or `docs/*.md`).
3. Run `scripts/release-check.sh` before merging or tagging. Session landing runs it in CI mode.

## Safety Default

- Prefer `--dry-run` first for mutating commands in automation.
- Do not send real emails in exploratory or validation runs unless explicitly requested.

## Agent skills

### Issue tracker

Issues live as GitHub issues in `agisilaos/protonmailcli`, read and written with the `gh` CLI. See [issue tracker](docs/agents/issue-tracker.md).

### Triage labels

The five canonical roles, each label string equal to its name: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See [triage labels](docs/agents/triage-labels.md).

### Domain docs

Single-context: one `GLOSSARY.md` and one `docs/adr/` at the root. See [domain docs](docs/agents/domain.md).

## Read before

The page that governs each kind of change. Read the one the step needs, not all of them.

- Commands, flags, exit codes or the JSON envelope: [CLI specification](docs/cli-spec.md).
- Draft, send, follow-up or batch behavior over Bridge: [IMAP workflows](docs/imap-workflows.md).
- UID handling, scoped expunge or MIME decoding: [IMAP safety and decoding](docs/imap-behavior.md).
- Configuration precedence, secrets or certificate trust: [config and security](docs/config-and-security.md) and [Bridge transport behavior](docs/config-and-transport.md).
- `PMAIL_USE_LOCAL_STATE` parity or preview semantics: [local command behavior](docs/local-command-behavior.md).
- Writing or changing a test: [testing strategy](docs/testing-strategy.md).
- Batch manifests or contract schemas: [agent manifests and schemas](docs/agent-manifest-schemas.md).
- Cutting or recovering a release: [RELEASING.md](RELEASING.md) and [release recovery](docs/release-recovery.md).
- Where the project stands and what is next: [current state](docs/current-state.md).
