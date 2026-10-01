# Documentation

## Core

- CLI specification: `cli-spec.md`
- Current implementation state: `current-state.md`
- Config and security: `config-and-security.md`
- Testing strategy: `testing-strategy.md`

## Agent and Schema Docs

- Agent manifests and contract schemas: `agent-manifest-schemas.md`
- JSON schemas: `schemas/`

## Help Snapshots

- Command help snapshots: `help/`
- `scripts/help-snapshots.txt` retains the nine public help topics.
- `make update-help` explicitly refreshes stdout/stderr snapshots from a
  temporary binary, cache, config and local state.
- `make help-script-test` checks stream capture, drift detection and cleanup.

## Local verification

- `make verify` runs the full local gate, including executable contract
  fixtures and the isolated local-state send dry run.
- `scripts/release-config.sh` owns release names, metadata and the pinned bundle fingerprint.
- `scripts/cli-shared/` contains the local copy of the common process helpers.

## Release

- Unified release workflow commands and scripts: `../README.md#release`
- Release history: `../CHANGELOG.md`
- [Interrupted release recovery](release-recovery.md) explains reported phases and retained artifacts.
