# Changelog

All notable changes to this project will be documented in this file.

## [v0.3.0] - 2026-11-27

No command was added or removed and no exit code changed meaning. Apart from one
new global safety flag, this release is Bridge transport security, correctness of
input and output handling, durability of in-flight mutations, and the development
workflow that produced them.

### Changed

- **IMAP now verifies the Bridge server certificate and the configured hostname before logging in**, including on loopback connections and during send previews. A certificate that v0.2.2 accepted unchecked may now be rejected. For a Bridge certificate outside system trust, set `bridge.tls_cert_file` to an explicitly trusted CA or certificate PEM; it applies to both IMAP and SMTP ([88ca426](https://github.com/agisilaos/protonmailcli/commit/88ca426)).
- Every certificate in a trust bundle is validated instead of the first alone, and validated trust is reused for the remainder of one Bridge operation rather than re-derived per connection ([b2ce5c6](https://github.com/agisilaos/protonmailcli/commit/b2ce5c6), [d9b8336](https://github.com/agisilaos/protonmailcli/commit/d9b8336)).
- A certificate verification failure is reported as a configuration error rather than a transport error, so automation can tell a misconfigured trust store from an unreachable Bridge ([2517c48](https://github.com/agisilaos/protonmailcli/commit/2517c48), [aee780b](https://github.com/agisilaos/protonmailcli/commit/aee780b)).
- A non-regular file given as a certificate is rejected before it is read, and a failed TLS upgrade closes the socket instead of leaving it open ([ea54604](https://github.com/agisilaos/protonmailcli/commit/ea54604), [0d7c8a4](https://github.com/agisilaos/protonmailcli/commit/0d7c8a4)).
- SMTP enforces the configured send policy and reports accepted-send semantics explicitly ([815d1b9](https://github.com/agisilaos/protonmailcli/commit/815d1b9)).
- The toolchain requires Go 1.27.1 throughout, matching `go.mod`, the release scripts and CI ([#2](https://github.com/agisilaos/protonmailcli/pull/2)).

### Added

- **`--expect-account <username>`**, a global flag that refuses to run a Bridge mail command unless that account is the active one, so a script cannot act on the wrong mailbox after an account switch. It applies to `draft`, `message`, `search`, `mailbox` and `tag`; any other command exits 2 with `usage_error`, and local-state mode exits 2 with `validation_error` ([0e4d2c4](https://github.com/agisilaos/protonmailcli/commit/0e4d2c4)).
- Worktree development sessions with direct landing: `scripts/session start` opens a sibling checkout off the latest default branch, `land` rebases, runs a Codex review and qualifies the push, and `end` removes a landed session ([51d48f3](https://github.com/agisilaos/protonmailcli/commit/51d48f3)).
- `scripts/session list` reports each session worktree's state and last commit, and `scripts/session prune` removes landed ones, recording every HEAD under `refs/archive/` first ([29b26d3](https://github.com/agisilaos/protonmailcli/commit/29b26d3)).
- `scripts/docs-reality-check.py` fails when a backticked path, Make target or `PMAIL_*` variable in the documentation names nothing real, and runs as part of `make docs-check`. Pre-commit and commit-msg hooks check staged whitespace, Go formatting and the message shape ([29b26d3](https://github.com/agisilaos/protonmailcli/commit/29b26d3)).

### Fixed

- **Setup no longer loses a working configuration when input fails.** An EOF in an unfinished prompt, a prompt that cannot be written, a failed buffered read and a conflicting `--interactive`/`--non-interactive` pair all stop before anything is saved, and invalid Bridge ports are rejected before the first prompt ([#11](https://github.com/agisilaos/protonmailcli/pull/11), [c3c5e08](https://github.com/agisilaos/protonmailcli/commit/c3c5e08), [b53332f](https://github.com/agisilaos/protonmailcli/commit/b53332f), [ee9876f](https://github.com/agisilaos/protonmailcli/commit/ee9876f), [1e757de](https://github.com/agisilaos/protonmailcli/commit/1e757de)).
- **An uncertain draft creation is no longer reported as a plain failure.** Creation intent is persisted before dispatch, an ambiguous outcome is classified as non-retryable so a replay cannot duplicate a draft, and an uncertain APPEND destination is retained for recovery ([#5](https://github.com/agisilaos/protonmailcli/pull/5), [#6](https://github.com/agisilaos/protonmailcli/pull/6), [f18299a](https://github.com/agisilaos/protonmailcli/commit/f18299a)).
- **State survives a failed save.** A failed write leaves the previous state intact rather than truncating it, checkpoints are pinned, and a confirmed draft or send outcome is kept even when writing the receipt afterwards fails ([#4](https://github.com/agisilaos/protonmailcli/pull/4), [6554b9e](https://github.com/agisilaos/protonmailcli/commit/6554b9e), [caa4a45](https://github.com/agisilaos/protonmailcli/commit/caa4a45)).
- **Batch operations keep their state and recovery context.** A partial batch reports per-item outcomes without losing the results already confirmed ([0e4d2c4](https://github.com/agisilaos/protonmailcli/commit/0e4d2c4)).
- **Human mode shows results, not just status.** Command results and `doctor` checks are printed alongside an error, plain errors stay on one line and carry their diagnostic data, and CRLF separators are stripped before human error codes ([a954aa1](https://github.com/agisilaos/protonmailcli/commit/a954aa1), [a31468d](https://github.com/agisilaos/protonmailcli/commit/a31468d), [237690a](https://github.com/agisilaos/protonmailcli/commit/237690a), [c805d4f](https://github.com/agisilaos/protonmailcli/commit/c805d4f)).
- A closed stdout pipe is reported without retrying the work or losing the command's status ([#10](https://github.com/agisilaos/protonmailcli/pull/10), [483ca74](https://github.com/agisilaos/protonmailcli/commit/483ca74)).
- **Dry-run previews describe what would happen without inventing it.** A local creation preview returns a plan that keeps the proposed inputs and the creation path, omits absent optional tags, and retains nested resource plans without inventing IDs or timestamps ([a1f5016](https://github.com/agisilaos/protonmailcli/commit/a1f5016), [3c589fa](https://github.com/agisilaos/protonmailcli/commit/3c589fa), [deb61b0](https://github.com/agisilaos/protonmailcli/commit/deb61b0), [44b7c30](https://github.com/agisilaos/protonmailcli/commit/44b7c30), [9f56e24](https://github.com/agisilaos/protonmailcli/commit/9f56e24)).
- An interactive send requires a terminal, and preview and local command input contracts are honored consistently ([8b54e63](https://github.com/agisilaos/protonmailcli/commit/8b54e63), [775fb56](https://github.com/agisilaos/protonmailcli/commit/775fb56)).
- Draft retrieval uses the send password file, credential reads are bounded and retain their diagnostic detail, and an undelivered interactive prompt is not read from ([3b27fff](https://github.com/agisilaos/protonmailcli/commit/3b27fff), [26fb930](https://github.com/agisilaos/protonmailcli/commit/26fb930), [fc7acd1](https://github.com/agisilaos/protonmailcli/commit/fc7acd1)).
- An incomplete message collection is rejected rather than returned as a partial result, and retrieval failure is distinguished from a missing message ([#7](https://github.com/agisilaos/protonmailcli/pull/7)).
- IMAP validates command scope and preserves decoded message content ([22b1355](https://github.com/agisilaos/protonmailcli/commit/22b1355)).
- Draft identity and authentication diagnostics are protected against account switches ([#3](https://github.com/agisilaos/protonmailcli/pull/3)).
- Leaf `--help` renders before configuration is loaded, so help works on an unconfigured machine, and Bash completion respects command depth ([#9](https://github.com/agisilaos/protonmailcli/pull/9), [#8](https://github.com/agisilaos/protonmailcli/pull/8)).

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
