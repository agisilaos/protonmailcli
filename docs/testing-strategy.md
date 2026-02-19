# Testing Strategy

This document describes the current test strategy and release gates.

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
- executed by `TestContractFixtures` in `internal/app/contracts_test.go`
- fixture command strings are normalized for CLI global flag placement
- fixtures may provide `stdin` payloads for `--stdin` command coverage

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

Current gated E2E entrypoint:

- `go test -tags integration ./internal/app -run TestBridgeE2EDraftCreateSearchSend -v`
- requires: `PMAIL_E2E_BRIDGE=1`, `PMAIL_E2E_USERNAME`, `PMAIL_E2E_PASSWORD`
- optional: `PMAIL_E2E_HOST`, `PMAIL_E2E_IMAP_PORT`, `PMAIL_E2E_SMTP_PORT`, `PMAIL_E2E_REAL_SEND=1`

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
5. missing required fields in batch manifests exit `2` with `validation_error`.
6. unknown draft id exits `5`.
7. idempotency payload mismatch exits `6`.
8. bridge timeout exits `4`.
9. `search messages --plain` is parseable and stable field order.
10. `tag add` on existing tag is idempotent (`changed=false`, exit `0`).
11. IMAP draft create falls back correctly when APPEND fails.
12. Invalid `--after/--before` or `--since-id` values fail with exit `2`.
13. local batch parity contracts (`draft create-many`, `message send-many`) stay aligned with machine output expectations.
14. path telemetry fields (`createPath`, `sendPath`) remain present in contract fixtures.
15. `mailbox resolve --name <id>` returns canonical mailbox metadata and deterministic match strategy.

## Suggested directory layout

```text
tests/
  unit/
  integration/
  e2e/
  contracts/
    local_draft_create_many_partial.json
    send_requires_confirm_non_tty.json
    send_dry_run.json
    mailbox_resolve_id.json
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

## Running contract tests locally

```bash
go test ./internal/app -run TestContractFixtures -v
```

## Agent contract workflow

For agent-safe releases:

1. Add/update fixture(s) in `tests/contracts/*.json` for the scenario.
2. Run `go test ./internal/app -run TestContractFixtures -v`.
3. Treat fixture failures as contract breaks unless intentionally migrated.
4. Run `scripts/release-check.sh` before tagging a release.
