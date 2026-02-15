# Testing Strategy

This document defines the v1 test strategy before implementation starts.

## Goals

- Guarantee stable machine contracts (`--json`, `--plain`).
- Prevent unsafe send behavior in automation.
- Verify deterministic exit codes and stderr/stdout separation.
- Ensure config/env/flag precedence remains predictable.

## Test layers

### 1) Unit tests

Scope:

- argument parsing and validation
- config loading and precedence
- output renderer (human/json/plain)
- confirmation policy evaluator
- idempotency key handling logic

### 2) Contract tests

Scope:

- exact JSON shapes for representative commands
- exact exit codes for core error classes
- exact non-interactive safety behavior

Method:

- golden fixtures in `tests/contracts/*.json`
- compare normalized JSON output to fixture snapshots

### 3) Integration tests (Bridge mocked)

Scope:

- draft create/update/list/get/delete flows
- message send flow with confirm token
- tag/filter/search flows

Method:

- local fake Bridge adapter with deterministic responses
- verify generated adapter requests and returned state changes

### 4) End-to-end tests (real Bridge, optional CI stage)

Scope:

- auth/login status
- draft create/send in a dedicated test account

Constraints:

- isolated mailbox/account
- replay-safe idempotency keys
- cleanup job after each run

## Required invariants

- stdout contains only result payload.
- stderr contains only diagnostics/errors/progress.
- `--json` emits exactly one JSON object.
- non-TTY `message send` without confirm token exits `7`.
- `--dry-run` never mutates remote or local persistent state.

## Critical test matrix

1. `draft create --json` returns valid envelope and `ok=true`.
2. `message send --no-input` without `--confirm-send` exits `7`.
3. `message send --no-input --confirm-send <id>` succeeds.
4. `message send --dry-run` does not send and returns planned action.
5. invalid email in `--to` exits `2` with actionable hint.
6. unknown draft id exits `5`.
7. stale etag in `draft update --if-match` exits `6`.
8. bridge timeout exits `4`.
9. `search messages --plain` is parseable and stable field order.
10. `tag add` on existing tag is idempotent (`changed=false`, exit `0`).

## Suggested directory layout

```text
tests/
  unit/
  integration/
  e2e/
  contracts/
    send_requires_confirm_non_tty.json
    send_dry_run.json
    output_stream_discipline.json
```

## CI gates

- Gate 1: unit + contract tests (required)
- Gate 2: integration tests with fake adapter (required)
- Gate 3: e2e against real Bridge (optional/nightly)

## Release criteria for v1

- all required gates green
- no breaking change to JSON contract fixtures
- exit code table matches `docs/cli-spec.md`
