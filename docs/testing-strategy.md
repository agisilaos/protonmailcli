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

On Unix, a named-pipe fixture checks that `auth login` and `doctor` reject a
nonregular password file without blocking.

### 2) Contract tests

Scope:

- exact JSON shapes for representative commands
- exact exit codes for core error classes
- deterministic error taxonomy fields (`error.category`, `error.retryable`)
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

`TestDialIMAPRejectsInvalidServerCertificateBeforeLogin` exercises the real IMAP
STARTTLS handshake against local certificates with an untrusted issuer or wrong
hostname. It supplies no credentials and requires a certificate verification
error before any LOGIN command. Trusted local workflow fixtures cover the
successful path.

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
- optional: `PMAIL_E2E_HOST`, `PMAIL_E2E_IMAP_PORT`, `PMAIL_E2E_SMTP_PORT`, `PMAIL_E2E_TLS_CERT_FILE`, `PMAIL_E2E_REAL_SEND=1`

Set `PMAIL_E2E_TLS_CERT_FILE` to the trusted public CA/certificate PEM when Bridge
uses a certificate outside system trust. The E2E setup saves this path (expanding
`~/`) in its temporary config for IMAP and SMTP; it does not copy the normal CLI
config or disable TLS verification. The local
`TestBridgeE2EExplicitCertificateConfiguration` test covers this setup without
connecting to Bridge. Without `PMAIL_E2E_REAL_SEND=1`, the test creates its
draft through IMAP and previews the send. If IMAP rejects the draft, the test
stops before SMTP fallback. The tagged `TestBridgeE2EFallbackRequiresExplicitRealSend`
test exercises both opt-in settings against a loopback IMAP peer and fake SMTP.

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

## Directory layout

Go tests live beside the code they exercise, one package at a time. Only the
declarative contract fixtures sit under `tests/`.

```text
internal/app/          command dispatch, batch, recovery and contract tests
internal/bridge/        IMAP, SMTP and TLS tests against in-process peers
internal/store/         state persistence, locking and durability tests
internal/config/        TOML precedence and validation tests
internal/output/        envelope, stream and exit-code tests
cmd/protonmailcli/      process-boundary tests (pipes, prompts, terminals)
tests/contracts/        executable JSON fixtures run by TestContractFixtures
```

Integration and end-to-end coverage is selected by environment opt-in rather
than by directory. Bridge end-to-end tests live in
`internal/app/bridge_e2e_test.go` and skip unless `PMAIL_E2E_BRIDGE=1`;
a real send additionally requires `PMAIL_E2E_REAL_SEND=1`, and trusting a
Bridge certificate requires `PMAIL_E2E_TLS_CERT_FILE`.

## CI gates

One required gate runs on pull requests and on pushes to `master`:
`.github/workflows/release-check.yml` executes `make release-check-ci` on
`macos-latest` with the Go toolchain pinned to match `go.mod`. That target
reaches the same `make verify` path as a local run, so formatting, vet, unit
and mocked-integration tests, the executable contract fixtures, help and
session script fixtures, the docs contract, help-snapshot drift and the
isolated local-state agent smoke run are all enforced by the single gate.

Bridge end-to-end tests against a real Bridge are not part of CI. They run
locally on request, with credentials and certificate trust supplied by the
operator.

## Release criteria for v1

- all required gates green
- no breaking change to JSON contract fixtures
- exit code table matches `docs/cli-spec.md`

## Running contract tests locally

```bash
go test ./internal/app -run TestContractFixtures -v
```

The local draft, tag, and filter creation-preview fixtures check the nested
proposed properties and plan metadata in the JSON envelope, including a resolved
stdin draft body. The fixture matcher checks expected fields; the app preview
tests separately check that previews omit unsaved IDs and timestamps and leave
state unchanged.

## Agent contract workflow

For agent-safe releases:

1. Add/update fixture(s) in `tests/contracts/*.json` for the scenario.
2. Run `go test ./internal/app -run TestContractFixtures -v`.
3. Treat fixture failures as contract breaks unless intentionally migrated.
4. Run `scripts/release-check.sh` before tagging a release.
