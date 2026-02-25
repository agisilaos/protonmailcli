# Changelog

All notable changes to this project will be documented in this file.

## [v0.2.2] - 2026-02-25

### Added
- Add `message follow-up` command to create follow-up drafts from existing messages (thread-aware in IMAP mode)
- Add threading header support (`In-Reply-To`, `References`) for IMAP follow-up draft creation
- Add contract and unit test coverage for follow-up draft flows and dry-run behavior

### Changed
- Accept mailbox-qualified IMAP message IDs (`imap:<mailbox>:<uid>`) for message retrieval and follow-up flows
- Update CLI docs and help snapshots for the new `message follow-up` command and usage examples

## [v0.2.1] - 2026-02-19

### Added
- Add canonical mailbox mapping metadata for agents (33daaf4)
- Add deterministic error categories for JSON failures (c217f97)
- Add mailbox resolve command with deterministic matching (a292894)
- Add draft/send path telemetry for agent workflows (fde8ff6)

### Refactored
- Refactor mailbox actions and centralize error classification (467a0f1)
- Refactor local draft/message responses to typed structs (7d79d20)
- Refactor command parsing helpers and manifest-driven help snapshots (16f82c5)

### Changed
- Normalize JSON help handling across mailbox/search/tag (4f5723e)
- Normalize help behavior for tag/filter and harden help snapshots (2c48456)
- Expand contract fixtures for mailbox resolve and path telemetry (e0df062)
