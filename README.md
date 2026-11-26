# protonmailcli

Bridge-first CLI for Proton Mail workflows with a strong automation contract.

`protonmailcli` lets humans and agents compose drafts, send messages, search content, manage tags, and apply simple filters from the terminal.

## What is implemented now

- Interactive and non-interactive setup (`setup`)
- Auth session commands (`auth login|status|logout`)
- Bridge account selection (`bridge account list|use`)
- Bridge diagnostics (`doctor`: config/auth prerequisites + Bridge TCP checks)
- Draft lifecycle: create, update, get, list, delete
- Bulk draft creation: `draft create-many --file|--stdin`
- Send message from draft with non-interactive safety gate
- Threaded follow-up draft creation from an existing message (`message follow-up`)
- Bulk send workflow: `message send-many --file|--stdin`
- Search drafts/messages
- Mailbox discovery (`mailbox list`, `mailbox resolve`)
- Tag operations: list, create, add, remove
- Filter operations: list, create, test, apply, delete
- Shell completion output (`completion bash|zsh|fish`)
- Stable `--json` and `--plain` output modes
- Idempotency keys on mutating commands
- Persistent local state store
- IMAP + SMTP Bridge path for live mailbox operations

## Backend matrix (current)

- `auth`: local session state + Bridge credentials
- `bridge account`: active Bridge username selection state
- `doctor`: config + auth prerequisites + Bridge TCP checks (IMAP/SMTP)
- `draft`: Bridge IMAP (`Drafts`)
- `message get`: Bridge IMAP (`INBOX`)
- `message send`: draft fetch via IMAP + send via SMTP
- `message follow-up`: build a threaded draft (`In-Reply-To`/`References`) from an existing message
- `search`: Bridge IMAP (`INBOX`/`Drafts`)
- `mailbox list`: Bridge IMAP (`LIST`)
- `tag add/remove/list`: Bridge IMAP flags/keywords
- `filter`: local state engine (not yet IMAP-server-side rules)
- local-state mode parity: supports `draft create-many` and `message send-many` for offline/agent contract testing

With `PMAIL_USE_LOCAL_STATE=1`, all sends are local simulations and make no SMTP
connection. Live draft operations discover the server's Drafts mailbox. Reads
preserve unread state, and deletion expunges only the requested UID. See
[IMAP workflow behavior](docs/imap-workflows.md) and
[IMAP safety and decoding](docs/imap-behavior.md) for identity and recovery rules.

## Install

```bash
cd protonmailcli
go build -o protonmailcli ./cmd/protonmailcli
```

Homebrew (after release):

```bash
brew tap agisilaos/tap
brew install protonmailcli
```

## Setup

### Human interactive setup

```bash
./protonmailcli setup --interactive
```

Combining `setup --interactive` with global `--no-input` or setup's
`--non-interactive` exits 2 before reading input or writing configuration.

Setup writes prompts to stderr. Press Enter after each answer or to accept a
default. Only the final password-file path can end with EOF after a nonempty
answer. Earlier EOF, a missing final answer, or a prompt or input read failure
exits with status 1 and preserves the existing configuration.

Both setup modes require SMTP and IMAP ports from 1 to 65535. An invalid port
flag exits with status 2 before any prompts or configuration writes. In
interactive setup, `--profile`, `--bridge-smtp-port`, and `--bridge-imap-port`
set the defaults displayed at their prompts. Press Enter to keep a default or
type a replacement. Port answers must be decimal integers in the same range.
An invalid answer exits with status 2 and preserves the existing configuration.

Interactive `auth login` also writes prompts to stderr. If a prompt cannot be
delivered, login exits with status 1 before reading an answer or saving login
state.

This prompts for profile + Bridge host/ports/username and writes config to:

- `~/.config/protonmailcli/config.toml`

### Agent non-interactive setup

```bash
./protonmailcli --json --no-input setup --non-interactive \
  --profile default \
  --bridge-host 127.0.0.1 \
  --bridge-smtp-port 1025 \
  --bridge-imap-port 1143 \
  --username you@proton.me \
  --smtp-password-file ~/.config/protonmailcli/bridge.pass
```

## Auth and diagnostics

Login for agent workflows (writes auth session into local state):

```bash
./protonmailcli --json --no-input auth login \
  --username you@proton.me \
  --password-file ~/.config/protonmailcli/bridge.pass
```

Check auth/session:

```bash
./protonmailcli --json auth status
```

List/select Bridge account profile:

```bash
./protonmailcli --json bridge account list
./protonmailcli --json bridge account use --username you@proton.me
```

Bridge health checks:

```bash
./protonmailcli --json doctor
```

`doctor` now returns grouped diagnostics in JSON:

- `summary.bridge`
- `summary.authPrereqs`
- `summary.config`
- `doctor.bridge.checks` (IMAP/SMTP TCP checks)

These details remain in `data` when a diagnostic fails, alongside the error and
nonzero exit code. Login requires a readable password file with nonempty content.

## Usage

### IMAP draft identity and diagnostics

Successful draft creation uses the server's single-message `APPENDUID`, never
the last message in the mailbox. If an accepted append lacks a valid UID or its
completion is uncertain, the command fails with inspection guidance and does not
try the SMTP fallback. Inspect Drafts before retrying: the draft may already
exist. Servers without a usable APPENDUID cannot provide a confirmed draft ID.
IMAP draft IDs contain the exact server mailbox name, for example
`imap-v2:Drafts:41` or `imap:Brouillons:41`. IMAP `draft create-many`
returns exit 4 when every item fails and exit 10 for mixed success/failure;
per-item failure details remain in the batch result.
The `imap-v2` prefix binds the literal `Drafts` name instead of treating it as the
legacy alias. Preserve returned IDs unchanged. A first draft operation fails if
its exact receipt no longer matches the selected Drafts mailbox; it never follows the role
to a different folder. Older `imap:Drafts:<uid>` receipts and bare draft UIDs keep
their unambiguous legacy-alias behavior. IDs do not include IMAP UIDVALIDITY;
after a mailbox is deleted and recreated under the same name, obtain fresh IDs.
Uncertain creation uses `imap_draft_create_uncertain`; single-command JSON errors
use category `uncertain` and `retryable: false`. Batch items carry that same error
code. Inspect Drafts before deciding whether any replacement is needed. If SMTP
fallback was attempted, inspect both INBOX and Drafts: accepted delivery can
remain in INBOX when its move to Drafts fails. Replaying a cached IMAP batch with
the same idempotency key preserves that exit status.

For IMAP `draft create` and `draft create-many`, keyed commands save a pending
record before dispatch and checkpoint the terminal response, including batches
with no confirmed successes. Reusing that key replays the saved result without
sending again. An interruption or failed final save leaves a pending record:
subsequent runs stop with `imap_draft_create_uncertain`, not an automatic retry.
Retain the key and state, and reconcile every batch item in Drafts and, if SMTP
fallback was attempted, INBOX. Reuse or reconcile an existing copy when found;
only after independently confirming absence from both locations should you
create missing drafts under a new key (a batch replacement must include only
missing items). An empty Drafts folder alone does not establish absence after
SMTP fallback. There is no automatic reset or resume of an unfinished batch.

When two commands try to update the same state file, the second returns
`state_busy` (exit 6) before making changes. Retry after the first writer finishes.
IMAP sends, send batches, and follow-ups without command or item keys need only
read access to local state. IMAP `tag create` also leaves local state untouched.
Use the same CLI version across writers so they honor the lock and pending records.

A command-level draft batch key covers the manifest and the body files read for
rows with recipients and a single body source. If a previously recorded file-backed key conflicts, inspect Drafts
before choosing a new key. Dry runs do not reserve keys, and unkeyed commands
have no durable replay protection.

`PMAIL_IMAP_DEBUG=1` omits the authentication exchange, including server text that
could echo credentials. Other protocol diagnostics remain available.

Create draft:

```bash
./protonmailcli --json draft create \
  --to alice@example.com \
  --subject "Weekly sync" \
  --body "status update" \
  --idempotency-key draft-weekly-sync-001
```

Create draft from stdin (agent-safe piping):

```bash
cat ./drafts/weekly-sync.md | ./protonmailcli --json --no-input draft create \
  --to alice@example.com \
  --subject "Weekly sync" \
  --stdin
```

List live drafts from Bridge IMAP:

```bash
./protonmailcli --json draft list
```

Pass the ID from `draft list` directly to `draft get`, `draft delete`, or `message send`.
If the CLI reports an ambiguous older receipt, use its `message get` command
for each candidate mailbox. Compare the recipients, subject, and body before
choosing the intended draft. The listing covers only the selected Drafts folder.

Create a threaded follow-up draft from an existing message:

```bash
./protonmailcli --json --no-input message follow-up \
  --message-id imap:Sent:845 \
  --body "Quick follow-up on this thread."
```

Send draft safely in non-interactive mode:

```bash
export PMAIL_SMTP_PASSWORD="your-bridge-password"
./protonmailcli --json --no-input message send \
  --draft-id d_123 \
  --confirm-send d_123 \
  --idempotency-key send-d123-001
```

Dry-run send:

```bash
./protonmailcli --json --no-input --dry-run message send \
  --draft-id d_123 \
  --confirm-send d_123
```

Search:

```bash
./protonmailcli --json search drafts --query sync
./protonmailcli --json search messages --query invoice
./protonmailcli --json search messages --from billing@example.com --after 2026-01-01 --limit 25
./protonmailcli --json search messages --query invoice --limit 50 --cursor 50
./protonmailcli --json search messages --mailbox "All Mail" --to info@example.com --before 2026-03-01
./protonmailcli --json search messages --subject "invoice" --has-tag invoices --unread --since-id 1000
```

Mailboxes:

```bash
./protonmailcli --json mailbox list
```

`mailbox list` returns stable mapping metadata per mailbox:

- `id`: canonical key (`inbox`, `drafts`, `sent`, `all_mail`, or sanitized custom ID)
- `name`: Bridge mailbox name
- `kind`: `system` or `custom`

Resolve a mailbox deterministically from either ID or name:

```bash
./protonmailcli --json mailbox resolve --name all_mail
./protonmailcli --json mailbox resolve --name "All Mail"
```

Tags:

```bash
./protonmailcli --json tag create --name finance
./protonmailcli --json tag add --message-id m_123 --tag finance
```

In IMAP mode, tags are IMAP keywords/flags on messages (not folder names).

Filters:

```bash
./protonmailcli --json filter create --name invoices --contains invoice --add-tag finance
./protonmailcli --json filter test --filter-id f_123
./protonmailcli --json filter apply --filter-id f_123
```

Generate shell completion:

```bash
./protonmailcli completion zsh
./protonmailcli completion bash
./protonmailcli completion fish
```

## Safety model

- `--no-input` disables prompts and forces explicit intent.
- Non-interactive `message send` requires `--confirm-send <draft-id>` unless `--force`.
- `--dry-run` returns planned behavior without state changes.
- Errors return stable exit codes and structured JSON when `--json` is set.

Put global options before the resource. Unexpected trailing arguments are
rejected before state or mailbox changes. Literal option values such as
`--body --json` remain text. `--dry-run setup` leaves config unchanged and reports
`configured: false`; previews do not initialize missing state directories.

Draft updates accept `--subject ''` and `--body ''` to clear those fields.
Batch manifest item keys prevent repeated successful items from creating or
sending again; reuse the same state file and keep each operation's keys distinct.
See [local command behavior](docs/local-command-behavior.md).

## Config and state

Configuration updates are written to a private temporary file beside the target,
then replace it only after writing, syncing, and closing succeed. A failed staging
write leaves the previous configuration readable. Saved configs use permissions
0600. Atomic replacement does not serialize concurrent writers or guarantee
recovery after power loss.

Defaults:

- Config: `~/.config/protonmailcli/config.toml`
- State: `~/.local/share/protonmailcli/state.json`

Selected env vars:

- `PMAIL_SMTP_PASSWORD`
- `PMAIL_PROFILE`
- `PMAIL_OUTPUT`
- `PMAIL_TIMEOUT`

Nonempty environment defaults override the config file; explicit output/profile
flags take precedence. TOML syntax, output modes, and durations are validated.
SMTP operations honor the timeout. IMAP verifies the server certificate and
hostname before login, including on loopback connections and send previews.
When upgrading from v0.2.2, IMAP now verifies the Bridge certificate before login.
For Bridge certificates outside system trust, set `bridge.tls_cert_file` to an
explicitly trusted public CA/certificate PEM for IMAP and SMTP. See
[configuration and Bridge transport behavior](docs/config-and-smtp-fixes.md).

## Output and exit codes

- `stdout`: result payload only
- `stderr`: hints, warnings, diagnostics
- `--json`: single envelope object
- Unset optional timestamps are omitted (for example unsent drafts omit `sentAt`)
- Path telemetry for agents:
  - `draft create` / `draft create-many[*]`: `createPath`
  - `message send` / `message send-many[*]`: `sendPath`
  - IMAP values: `imap_append`, `smtp_move_fallback`, `smtp`
  - local-state value: `local_state`

Exit codes:

- `0` success
- `1` runtime failure
- `2` usage/validation
- `3` auth/config/session failure
- `4` network/send failure
- `5` not found
- `6` conflict
- `7` safety confirmation required/block
- `8` rate limit
- `10` partial success

## Test

Run the complete local gate:

```bash
make verify
```

It checks the pinned helper bundle, module metadata, formatting, vet, tests,
executable contract fixtures, docs/help and the isolated local-state smoke.
The smoke exercises send with `--dry-run`; it sends no real email.

On Unix, the Go test suite requires `python3` on `PATH` to run the interactive
setup and login tests in a pseudo-terminal.

```bash
go test ./...
```

Run executable contract fixtures only:

```bash
go test ./internal/app -run TestContractFixtures -v
```

Run optional real-Bridge integration test (manual/gated):

```bash
PMAIL_E2E_BRIDGE=1 \
PMAIL_E2E_USERNAME=you@proton.me \
PMAIL_E2E_PASSWORD='bridge-password' \
PMAIL_E2E_TLS_CERT_FILE=/absolute/path/to/bridge-cert.pem \
go test -tags integration ./internal/app -run TestBridgeE2EDraftCreateSearchSend -v
```

`PMAIL_E2E_TLS_CERT_FILE` supplies the trusted public CA/certificate PEM to the
temporary E2E configuration for both IMAP and SMTP. It supports `~/` paths; omit
it when the Bridge certificate is already trusted by the operating system.
Certificate and hostname verification remain enabled.

By default the test creates its draft through IMAP and previews delivery.
If IMAP rejects draft creation, it stops before SMTP fallback. SMTP delivery,
including that fallback, requires the separate `PMAIL_E2E_REAL_SEND=1` opt-in. Its preview argument construction is also covered
without Bridge access by `TestBridgeE2ESafeSendArguments`, and temporary trust
configuration by `TestBridgeE2EExplicitCertificateConfiguration`, under the
integration build tag.

Validate CLI help contracts (recommended before release):

```bash
scripts/check-help.sh
```

Help snapshots are driven by `scripts/help-snapshots.txt`.
Snapshot generation runs against an isolated local-state setup for deterministic output.
The binary, build cache, config and state live in temporary storage that is
removed on success or failure. Snapshots include both stdout and stderr.
`make help-script-test` verifies stream capture, cleanup and docs failure cases.

If help output changed intentionally, refresh snapshots:

```bash
scripts/update-help.sh
```

Run agent smoke checks (no real sends):

```bash
scripts/smoke-agent.sh
```

Run full pre-release checks:

```bash
scripts/release-check.sh vX.Y.Z
```

Generate changelog section from commits:

```bash
scripts/generate-changelog.sh --version v0.3.0 --since v0.2.1
```

Validate changelog for current CLI version:

```bash
scripts/check-changelog.sh
```

Batch manifest schemas:

- `docs/schemas/draft-create-many.schema.json`
- `docs/schemas/message-send-many.schema.json`
- `docs/agent-manifest-schemas.md`

## Agent usage pattern

The CLI is designed for deterministic agent loops:

1. Start with `--json --no-input`.
2. Read `nextCursor` and continue until it is empty.
3. Use returned IDs directly (`imap-v2:Drafts:<uid>`, `imap:INBOX:<uid>`).
4. Use `--dry-run` before mutating commands in planning phases.

Example pagination loop:

```bash
./protonmailcli --json --no-input search messages --query invoice --limit 100 --cursor 0
# parse .data.nextCursor, then call again with --cursor <nextCursor> until empty
```

## For Coding Agents

If this repo is handed to an autonomous agent (Codex, Claude Code, GitHub agents), use this operating pattern:

1. Bootstrap and verify:
```bash
go test ./...
./protonmailcli --json doctor
./protonmailcli --json auth status
```
2. Run only machine mode in automations:
```bash
./protonmailcli --json --no-input <command> ...
```
3. Read IDs from output and pass them back exactly, including their server mailbox name.
4. Route by path telemetry when needed:
  use `data.createPath` / `data.sendPath` (or per-item `results[].createPath` / `results[].sendPath`) for deterministic branching.
5. Use safety-first execution for mutating actions:
  run with `--dry-run` first, then execute with explicit confirmation flags.
6. Page deterministically:
  use `--limit` and `--cursor`; stop when `nextCursor` is empty.
7. Validate behavior contracts before proposing changes:
```bash
go test ./internal/app -run TestContractFixtures -v
```

Recommended agent sequence for “find and send draft”. Set `BRIDGE_CONFIG` and `BRIDGE_STATE`
to the absolute paths for the intended Bridge instance, `BRIDGE_PROFILE` to
its profile, and `BRIDGE_ACCOUNT` to its exact username. Refine the search
until one draft matches, then review its recipients and body.

```bash
unset draft_id reviewed_context
reviewed_context=(--config "${BRIDGE_CONFIG:?Set the absolute config path}"
                 --state "${BRIDGE_STATE:?Set the absolute state path}"
                 --profile "${BRIDGE_PROFILE:?Set the Bridge profile}"
                 --expect-account "${BRIDGE_ACCOUNT:?Set the intended Bridge username}") &&
search_result=$(./protonmailcli --json --no-input "${reviewed_context[@]}" search drafts --query "KAOS") &&
candidate_id=$(python3 -c 'import json, sys
data = json.load(sys.stdin)["data"]
if len(data["drafts"]) != 1 or data.get("total", 1) != 1:
    sys.exit("Refine the search until exactly one draft matches")
print(data["drafts"][0]["id"])' <<< "$search_result") &&
  ./protonmailcli --json --no-input "${reviewed_context[@]}" draft get --draft-id "$candidate_id" &&
  ./protonmailcli --json --no-input "${reviewed_context[@]}" --dry-run message send --draft-id "$candidate_id" --confirm-send "$candidate_id" &&
  draft_id=$candidate_id
```

After reviewing the preview, send that same draft from the same shell. The send stops
if the effective Bridge username has changed. The saved options keep the same
config, state, and profile if the shell defaults change.

```bash
: "${draft_id:?Select and preview a draft first}" "${reviewed_context[0]:?Restore the reviewed context first}" &&
./protonmailcli --json --no-input "${reviewed_context[@]}" message send --draft-id "$draft_id" --confirm-send "$draft_id"
```

For retryable IMAP automation, generate a unique key for each intended send,
such as a UUID. Before the first attempt, save it with the active Bridge
username, draft ID, absolute config and state paths, and profile. Set `send_key` to that saved value and add
`--idempotency-key "$send_key"` to the send command. For retries, use the same
captured options, draft ID, and key. If delivery is uncertain, inspect it before retrying.

## Agent Playbooks

Below are practical patterns an agent can run reliably.

### 1. File-based draft creation (recommended)

When an LLM generates long email content, write it to a file and pass it with `--body-file`.

```bash
./protonmailcli --json --no-input draft create \
  --to info@example.com \
  --subject "Partnership follow-up" \
  --body-file ./drafts/partnership-followup.md
```

Why: avoids shell escaping issues and keeps prompt/content separation clean.

### 2. Batch draft ingestion from a manifest

Maintain a manifest (`drafts.json`) and create the drafts together. Each `to`
array can contain one or more recipient addresses.

Example manifest shape:

```json
[
  {"to":["a@example.com","second@example.com"],"subject":"Hello A","body_file":"./drafts/a.md","idempotency_key":"weekly-001-a"},
  {"to":["b@example.com"],"subject":"Hello B","body_file":"./drafts/b.md","idempotency_key":"weekly-001-b"}
]
```

Before the first Bridge batch, set `BATCH_CONFIG` and `BATCH_STATE` to its
absolute config and state paths, `BATCH_PROFILE` to its profile, and
`BATCH_ACCOUNT` to the exact Bridge username whose drafts you reviewed.
Record all four values with the manifest and restore them before a retry.

To check the username, read `data.active` with
`./protonmailcli --json --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" bridge account list`.
If the active account is empty, read `data.username` from
`./protonmailcli --json --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" auth status`.
Each Bridge batch command below checks the recorded `BATCH_ACCOUNT` again
before it reads drafts, replays a receipt, or dispatches a row.

Use a distinct item-level `idempotency_key` for each intended draft and omit
command-level `--idempotency-key` for this workflow. In Bridge mode, a
command-level key caches the whole result, including partial failures.
It cannot resume an already completed batch after input corrections. When failed rows report only local
input errors, correct them and retry with the same item keys and state file,
without a command-level key. Successful items replay their receipts. Preview the
manifest first.

```bash
./protonmailcli --json --no-input --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" --dry-run draft create-many --file drafts.json
```

After checking the preview, create the drafts.

```bash
./protonmailcli --json --no-input --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" draft create-many --file drafts.json
```

If Bridge reported a failure after a draft was dispatched, inspect Drafts before
retrying. When SMTP fallback was attempted, inspect both INBOX and Drafts: a
failed move can leave the accepted copy in INBOX. Use a fresh item key only after
confirming the draft is missing from both locations; otherwise reconcile the
existing copy without dispatching another.
Follow the [IMAP draft recovery procedure](#imap-draft-identity-and-diagnostics).

### 3. Search with filters and pagination

Use deterministic pagination for large inboxes.

```bash
./protonmailcli --json --no-input search messages \
  --mailbox "All Mail" \
  --from billing@example.com \
  --after 2026-01-01 \
  --limit 100 \
  --cursor 0
```

Then continue with returned `nextCursor` until it is empty.

### 4. Safe non-interactive send

Always send with explicit confirmation token in automation.

```bash
./protonmailcli --json --no-input message send \
  --draft-id imap-v2:Drafts:123 \
  --confirm-send imap-v2:Drafts:123
```

Preflight before bulk sends:

```bash
./protonmailcli --json --no-input --dry-run message send \
  --draft-id imap-v2:Drafts:123 \
  --confirm-send imap-v2:Drafts:123
```

### 5. Tag-driven triage

Attach tags to messages as an agent post-processing step.

```bash
./protonmailcli --json --no-input tag add \
  --message-id imap:INBOX:845 \
  --tag invoices
```

### 6. Contract-safe changes in CI

Before changing behavior, run executable contract fixtures:

```bash
go test ./internal/app -run TestContractFixtures -v
```

Treat fixture breaks as API contract changes and update fixtures intentionally.

### 7. Native batch operations

Use the four recorded `BATCH_` variables from the
[batch ingestion recipe](#2-batch-draft-ingestion-from-a-manifest) for each command.

The command-level keys below apply in Bridge mode. Matching input replays the
stored batch result, including failures. Changing a recipient or body-file path
in the manifest returns `idempotency_conflict` (exit 6). Local-state batches
accept item keys and reject the command-level flag (exit 2).
To retry a full manifest after correcting local input, reuse its original item keys.
Every previously successful row must already have its own receipt. See the
[batch ingestion recipe](#2-batch-draft-ingestion-from-a-manifest).
If the first send used only a command key, reconcile the outcome and submit
only rows confirmed unsent. Adding new item keys cannot identify earlier sends.
For a draft batch without item receipts, submit only drafts confirmed missing.

For an undispatched send row with no saved intent, reuse its original item key.
An attempted keyed send can remain pending even after an explicit SMTP rejection.
That key blocks every row in an item-only retry. Keep the original state and
receipt. Only after independently confirming non-delivery, assign a fresh item key
to that row. Leave accepted or unresolved sends out of the retry manifest.

Create many drafts from a single manifest:

```bash
./protonmailcli --json --no-input --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" draft create-many --file drafts.json --idempotency-key batch-drafts-001
```

Or pass manifest via stdin:

```bash
cat drafts.json | ./protonmailcli --json --no-input --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" draft create-many --stdin --idempotency-key batch-drafts-001
```

Preview the confirmed draft IDs in the reviewed account:

```bash
./protonmailcli --json --no-input --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" --dry-run message send-many --file sends.json --idempotency-key batch-send-001
```

After reviewing the preview, send the batch:

```bash
./protonmailcli --json --no-input --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" message send-many --file sends.json --idempotency-key batch-send-001
```

Or pass send manifest via stdin:

```bash
cat sends.json | ./protonmailcli --json --no-input --config "${BATCH_CONFIG:?}" --state "${BATCH_STATE:?}" --profile "${BATCH_PROFILE:?}" --expect-account "${BATCH_ACCOUNT:?}" message send-many --stdin --idempotency-key batch-send-001
```

### 8. Retry-aware errors for agents

In `--json` mode, all failures include machine-readable error fields:

- `error.code` (stable programmatic code)
- `error.category` (`usage`, `safety`, `config`, `auth`, `not_found`, `conflict`, `rate_limit`, `transient`, `uncertain`, `runtime`)
- `error.retryable` (`true` for transient/network-class failures)

Current automated tests cover:

- non-interactive setup flow
- draft creation flow
- non-interactive send confirmation enforcement (`exit 7`)
- auth login/status/logout flow
- doctor failure exit code (`exit 4`)
- completion output generation

## Release

Ask an agent to prepare the changelog from commit and PR evidence, review and commit it, then run:

```bash
make changelog-context VERSION=vX.Y.Z
make release-check VERSION=vX.Y.Z
make release-dry-run VERSION=vX.Y.Z
make release VERSION=vX.Y.Z
```

Every new changelog list item links to its pull request or direct commit. The approved changelog section becomes the GitHub Release notes. The dry run builds both macOS archives and checksums and renders and syntax-checks the Homebrew formula without remote writes.

See [RELEASING.md](RELEASING.md) for the full runbook and [release recovery](docs/release-recovery.md) for interrupted publication. Release scripts are `scripts/changelog-context.sh`, `scripts/release-check.sh`, and `scripts/release.sh`. Existing changelog helper scripts remain optional authoring tools.

## Docs

- CLI spec: `docs/cli-spec.md`
- Config/security: `docs/config-and-security.md`
- Test strategy: `docs/testing-strategy.md`
- Current implementation status: `docs/current-state.md`

## License

MIT (`LICENSE`)

### Local state save failures

State updates are staged beside the destination and atomically replace it only
after the complete file is written and closed. Failed staging leaves the previous
state readable. Existing regular-file permissions are preserved; new files use
0600. Symlink and directory targets are rejected without replacement. Use a
regular state file. The CLI holds an exclusive writer lock across reads,
checkpoints, and saves. Recovery after power loss is not guaranteed.

Message search and draft listing fail if any selected message cannot be fetched.
A failed collection returns an error rather than a successful partial or empty list;
retry the read after resolving the Bridge error.
Message lookup and follow-up preparation report retrieval failures separately
from a successful search with no matching message (`not_found`).

### Output delivery failures

A failed or short stdout write exits with status 1 when the command otherwise
succeeded. Existing command error statuses are preserved, with an additional
diagnostic on stderr. This applies on Unix when a pipe reader closes early,
including after the first line of a report. Output can be incomplete: a saved draft or completed
operation remains applied. Inspect state before retrying a mutation; the CLI
does not retry commands or failed writes to recover a missing receipt.

### Offline command help

Recognized leaf `--help`/`-h` requests (including setup and completion) work
before configuration, state, credentials or Bridge are loaded. Flags and defaults
come from the same definitions used for execution; the local-state test mode
shows its own supported options. Global flags still belong before the resource.

Offline help uses explicit `--json`/`--plain` and `--profile`, or human output
when omitted; it does not read saved output/profile preferences. Existing machine
help fields remain `help` and `usage`. Values such as `--subject --help` remain
data; help after `--` or after a positional argument does not bypass execution.
Examples containing `--dry-run` may still read Bridge; help itself does not.

### Bash completion updates

Regenerate the Bash completion script after upgrading, using the installation
command above. Completion now suggests child commands only at a group position,
respects global-option values, and stops at leaf arguments or `--`. It does not
yet suggest leaf flags or dynamic IDs; no CLI syntax or other shell changes are
introduced.
