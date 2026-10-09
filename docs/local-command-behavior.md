# Local command behavior

Global flags belong before the resource. Every known leaf command validates all
arguments before reading config/state or contacting Bridge. Unexpected positional
arguments and unknown flags fail with exit 2. Values beginning with dashes remain
values, for example `draft create --to user@example.invalid --body --json`.

`--dry-run setup` reports `configured: false` and `dryRun: true` without writing
config. Reads and previews leave missing state files and directories absent.
The first successful state mutation creates them.

Draft updates distinguish an omitted field from an explicitly empty value.
Use `--subject ''` or `--body ''` to clear the corresponding field. A body update
still accepts only one source: `--body`, `--body-file`, or `--stdin`.

With `PMAIL_USE_LOCAL_STATE=1`, both single and bulk sends simulate delivery in
the local state file without credentials or an SMTP connection. Confirmation
and force policy still apply. Successful sends report `sendPath: local_state`.

Local batch manifests honor item `idempotency_key` values. Repeating a key with
the same recipient/content payload (and draft ID for sends) replays its receipt.
A changed payload conflicts. Keep the same state file between invocations;
previews do not reserve keys. This does not provide concurrent-writer locking.

Auth login requires a regular, readable, nonempty password file of at most 64 KiB.
Named pipes and devices are rejected without waiting. This validates local
credential availability; it does not authenticate with the remote server.
Failed `doctor` JSON includes the computed `data.summary`, `data.checks`, and
`data.doctor` sections alongside its error so callers can inspect failed checks.
Doctor recognizes the selected Bridge account before auth/config usernames.
