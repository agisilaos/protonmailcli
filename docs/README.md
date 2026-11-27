# Documentation

## Core

- [CLI specification](cli-spec.md) — commands, flags, exit codes and the JSON envelope.
- [Current implementation state](current-state.md) — what is built, what is not, and what comes next.
- [Config and security](config-and-security.md) — configuration files, secret handling and permissions.
- [Testing strategy](testing-strategy.md) — test layers, invariants and gates.

## Behavior reference

- [IMAP workflows](imap-workflows.md) — draft, send, follow-up and batch flows over Bridge.
- [IMAP safety and message decoding](imap-behavior.md) — UID validation, scoped expunge and MIME decoding.
- [Configuration and Bridge transport behavior](config-and-transport.md) — TOML precedence and certificate trust.
- [Local command behavior](local-command-behavior.md) — `PMAIL_USE_LOCAL_STATE` parity and preview semantics.

## Agent and schema docs

- [Agent manifests and contract schemas](agent-manifest-schemas.md)
- JSON schemas: `schemas/`

## Agent configuration

What the engineering skills read to find this repo's conventions. `AGENTS.md`
summarizes each one under "Agent skills".

- [Issue tracker](agents/issue-tracker.md) — issues live as GitHub issues, via `gh`.
- [Triage labels](agents/triage-labels.md) — the five canonical triage roles.
- [Domain docs](agents/domain.md) — single-context `GLOSSARY.md` and `docs/adr/`.

## Help snapshots

- Command help snapshots: `help/`
- `scripts/help-snapshots.txt` retains the public help topics; it is the single
  source for which snapshots exist, so add a topic there rather than adding a
  file by hand.
- `make update-help` explicitly refreshes stdout/stderr snapshots from a
  temporary binary, cache, config and local state.
- `make help-script-test` checks stream capture, drift detection and cleanup.

## Local verification

- [Development sessions](development-sessions.md) covers new chats, worktrees,
  direct landing and archived session refs.
- `make verify` runs the full local gate, including executable contract
  fixtures and the isolated local-state send dry run.
- `scripts/release-config.sh` owns release names, metadata and the pinned bundle fingerprint.
- `scripts/cli-shared/` contains the local copy of the common process helpers.

## Release

- Unified release workflow commands and scripts: `../README.md#release`
- Release history: `../CHANGELOG.md`
- [Interrupted release recovery](release-recovery.md) explains reported phases and retained artifacts.
