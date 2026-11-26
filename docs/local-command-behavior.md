# Local command behavior

Global flags belong before the resource. Every known leaf command validates all
arguments before reading config/state or contacting Bridge. Unexpected positional
arguments and unknown flags fail with exit 2. Values beginning with dashes remain
values, for example `draft create --to user@example.invalid --body --json`.

`--dry-run setup` reports `configured: false` and `dryRun: true` without writing
config. Reads and previews leave missing state files and directories absent.
A writer may create the parent directory and its lock file before validation
finishes. The state file itself appears on the first save.

Draft updates distinguish an omitted field from an explicitly empty value.
Use `--subject ''` or `--body ''` to clear the corresponding field. A body update
still accepts only one source: `--body`, `--body-file`, or `--stdin`.

With `PMAIL_USE_LOCAL_STATE=1`, both single and bulk sends simulate delivery in
the local state file without credentials or an SMTP connection. Confirmation
and force policy still apply. Successful sends report `sendPath: local_state`.

Local batch manifests honor item `idempotency_key` values. Repeating a key with
the same recipient/content payload (and draft ID for sends) replays its receipt.
A changed payload conflicts. Keep the same state file between invocations;
previews do not reserve keys. Concurrent writers for the same state file
receive a retryable `state_busy` conflict while another command holds the lock.
Commands release it after their last state change and before printing the result.
When writer access is unavailable but state remains readable, commands can still
validate input and replay matching local receipts without saving. This includes
local follow-up drafts and item-keyed draft/send batches. Replayed successes do
not count as new state changes; mixed local results retain exit 10, and an
all-invalid local batch retains exit 1. Fresh local mutations still require
write access, and changed keyed payloads remain conflicts.
For an item-only remote batch containing both unkeyed rows and fresh item keys,
the CLI checkpoints its unchanged state before dispatching any row. This checks
receipt storage even when an existing sidecar remains lockable in a read-only
directory. A failed preflight dispatches no rows; successful local receipt and
validation results can still be reported. Entirely unkeyed batches, batches with
only existing terminal keys, and dry-runs do not perform this checkpoint.
On macOS and Linux, mutations reject hardlinked state files before loading
receipts or dispatching operations. Keep one regular state path for each receipt
store because two hardlink names would acquire independent writer locks.

The persistent `<state>.lock` file uses 0600 for new private
state, and matches an existing state file's read/write permission bits regardless
of the writer's umask. A new lock is published only after its final permissions
are set; interrupted staging does not leave a private lock blocking shared writers.
An existing state file must be readable before a writer publishes its first
lock; a failed read-access check leaves no lock owned by the failed caller.
On macOS and Linux, publication falls back to native atomic no-replace rename
when the filesystem rejects hard links. Other Unix platforms require hard-link
publication. If neither supported primitive is available, lock creation fails;
ordinary replacement rename is never used because it could replace another
writer's active lock.
Existing locks are opened read/write where possible, with a read-only fallback
for filesystems that permit locking that descriptor. If a filesystem requires
write access for the exclusive lock, the failure retains the original permission
cause so readable pending-receipt recovery remains available.
For shared state, keep the directory, state, and lock in the same shared group
(use a setgid directory where group inheritance requires it). When changing state
permissions or upgrading from an owner-only lock, have the lock's owner run
a mutating command once to update its permissions before another group user writes.
Lock files must be empty regular files without symbolic links. An abandoned
`.state-lock-*` staging name can remain after interruption; it does not prevent
locking the published file. Multiply linked locks are accepted only when their
permissions already match, so permission updates cannot change another alias.
Remove abandoned staging aliases before the lock owner changes shared permissions.
Leave published lock files in place: deleting or replacing one while a writer is active can
allow two writers to use different locks. Process exit releases the advisory
lock; an existing lock file alone does not mean a writer is still active.

Auth login requires a regular, readable, nonempty password file of at most 64 KiB.
Named pipes and devices are rejected without waiting. This validates local
credential availability; it does not authenticate with the remote server.
Failed `doctor` JSON includes the computed `data.summary`, `data.checks`, and
`data.doctor` sections alongside its error so callers can inspect failed checks.
Doctor recognizes the selected Bridge account before auth/config usernames.

The integration suite's default send phase places `--dry-run` before the
resource. Its argument helper is exercised with isolated local state, without
requiring the opt-in live Bridge test.

On Windows, a write-protected volume permits the same read-only receipt checks
as a state directory with insufficient write permissions.
