# Config and Security

## Directory layout

Default paths used by the CLI:

- Config: `~/.config/protonmailcli/config.toml`
- State: `~/.local/share/protonmailcli/state.json`

If XDG variables are set:

- Config: `$XDG_CONFIG_HOME/protonmailcli/config.toml`
- State: `$XDG_DATA_HOME/protonmailcli/state.json`

## Config shape

```toml
[defaults]
profile = "default"
output = "human"
timeout = "30s"

[bridge]
host = "127.0.0.1"
imap_port = 1143
smtp_port = 1025
tls = true
username = ""
password_file = ""
tls_cert_file = "" # optional trusted public IMAP/SMTP CA or certificate PEM

[safety]
require_confirm_send_non_tty = true
allow_force_send = true
```

## Runtime credential sources

Bridge credentials are resolved in this order:

1. `PMAIL_SMTP_PASSWORD` environment variable
2. password file path from flag/auth/config (`--smtp-password-file`, auth state, or config)

For `message send` and `message send-many`, `--smtp-password-file` supplies the
password for both the initial IMAP draft read and the SMTP send. It can be the
only configured password source, including for `--dry-run`. A nonempty
`PMAIL_SMTP_PASSWORD` still takes precedence over password files.

Username is resolved from the selected Bridge account, then auth state, then
config. Doctor uses the same precedence.

## Environment variables in active use

- `PMAIL_SMTP_PASSWORD`
- `PMAIL_PROFILE`
- `PMAIL_OUTPUT`
- `PMAIL_TIMEOUT`
- `PMAIL_USE_LOCAL_STATE` (test/local backend mode)

Nonempty profile/output/timeout environment values override file defaults;
explicit CLI profile/output flags override those values. `PMAIL_TIMEOUT` must be
a positive duration and applies to SMTP as well as IMAP. Invalid TOML or defaults
return `config_error`, while a missing file returns `config_missing`. TOML inline
comments do not alter boolean safety settings. See
[configuration and Bridge transport behavior](config-and-smtp-fixes.md) for certificate trust
and accepted-send handling.

Local-state mode simulates single and bulk sends entirely in the local store.
Login validates that a password file is regular, readable, no larger than 64 KiB,
and contains nonempty content. Named pipes and devices are rejected without
waiting for their contents. Credential resolution and doctor use the same reader.

## Secrets policy

- Do not pass raw secrets directly on command lines.
- Use password files (`--password-file`, `--smtp-password-file`) or `PMAIL_SMTP_PASSWORD` in controlled environments.
- Never commit password files, private keys, or token material.

## Safety policy

- Redirecting stdin from a pipe, file, or `/dev/null` makes a send non-interactive.
- Non-interactive `message send` requires `--confirm-send` unless `--force`.
- `--force` is allowed only when `allow_force_send = true`.
- Use `--dry-run` in automations before mutating commands.
- Setup previews preserve existing config, and read/preview commands do not
  initialize absent state files or directories.

State writes require an operating system with a Unix or Windows lock backend. Commands that update the same state file take an exclusive writer lock before loading it. If another writer is active, the second command returns exit 6 with `state_busy` and can be retried. The adjacent `.lock` file may remain after the process releases the lock. Read-only commands and dry runs can run alongside a writer.

Use a regular state file at its own path. Unix writers reject hard-linked state
files because each alias could otherwise use a different lock for the same data.
Pass the actual file path to `--state` when a symbolic link points at it.

On Unix the writer matches the sidecar’s read/write permission bits to the
existing state file. When group and other read/write access differ, it also matches
the state's group before publishing a new sidecar. A failed group assignment
stops publication; an existing sidecar's owner may need to correct its group.
Equal group and other permissions need no group reassignment. Checkpoints apply
the same rule to the staged replacement: retain the existing state's group when
its group and other read/write access differ, before copying permissions or
publishing contents. If that group cannot be assigned, the checkpoint fails and
preserves the previous state. An already matching group needs no reassignment. New state starts with a private
`0600` sidecar. Group or permission changes through hard-linked sidecars are
rejected.
Read-access checks retry a state inode replaced by a concurrent checkpoint;
continued replacement returns `state_busy` rather than a permanent state error.
Shared writers on AIX and Solaris need write access to the sidecar for an exclusive
record lock. These platforms keep the compatible record lock in a subprocess of
the same executable, so reading a body file cannot release the writer lock. The
subprocess releases the lock when the command closes it or exits, including an
abrupt exit; mutations fail before dispatch if the lock owner cannot start.
Initial lock publication on AIX, Solaris, and illumos requires a filesystem that
supports hard links.

On Windows the sidecar inherits filesystem access controls. Before creating it,
the writer checks that it can open the existing regular state file for reading
and rejects a state file with multiple hard links before publishing a sidecar.
Use the registered state filename for writes. An alternate 8.3 filename is
rejected with the registered name in the error. The writer opens the sidecar
without following a symbolic link and requires an empty regular file.

Windows readers share the state file for replacement. On filesystems that
support POSIX replacement, such as modern NTFS, a reader can finish on the old
snapshot while a checkpoint publishes the next one. If the directory cannot
be opened for this operation or the filesystem only supports ordinary rename,
an open reader can cause the checkpoint to fail.

### Checkpoint durability

With a durably provisioned state directory, a Unix checkpoint syncs the staged
file, atomically renames it, then syncs the parent directory before reporting
success. On Unix, state paths resolve their raw parent through the filesystem
before staging, locking, replacement, or sync. A symlink followed by `..` keeps
its filesystem meaning. A writer uses the resolved path for its initial load and
all checkpoints until it releases the lock, even if the original symlink changes.
A writer keeps its directory open and checks its identity before loading or saving.
Replacing that directory while a command waits for input aborts its next state operation.
The final state filename still must satisfy the regular-file policy. Keyed SMTP and draft operations wait for this checkpoint before dispatch. The parent directory must
be openable for syncing, and its filesystem must support directory sync; an
unsupported or failed sync is an error rather than a successful checkpoint.
On macOS and Linux, missing state-directory components are created as siblings
with the same native `0755` creation mode, umask, inherited group, and inherited
ACLs as an ordinary directory. The CLI syncs a small initialization marker inside
the new directory before publishing it with atomic no-replace rename. Each
writer that encounters a recognized marker syncs the directory's actual parent
before removing the marker and using the directory, including when the path
passes through a symlink. This also recovers a creator interrupted after
publication. An unsupported atomic rename fails before the state directory is
used; pre-provision the directory on such filesystems. A competing destination is
inspected once: an existing directory can be used, while a dangling link,
non-directory, or vanished entry returns an error with staging cleaned up.
Existing unmarked shared
directories do not require access to their ancestors. Unrelated files and
symlinks at the marker name remain untouched. An interrupted unpublished
`.state-directory-*` sibling can remain and can be removed after confirming it
contains only its initialization marker.

Other Unix platforms sync each directory component they create, but directory
provisioning remains a separate durability boundary there. Complete first-use
setup in one process before starting concurrent writers: another process can
observe a new component before its creator syncs the parent, and an interrupted
creator can leave the same apparent state. After interrupted first-use setup,
ensure new components are durably provisioned before reuse. Do not remove
components containing state or other callers' files. Existing shared directories
do not require their ancestors to be opened or synced. Windows retains its
existing file-sync, directory creation, and native replacement behavior.

If directory sync fails after rename, the new state may already be visible.
The CLI reports that publication occurred but durability could not be confirmed;
it does not roll the checkpoint back. An intent-checkpoint failure dispatches
nothing, and a visible pending receipt continues to block automatic retries.
Keep the key and state when recovering from a checkpoint error. After a remote
operation has completed, failure to save its result still requires reconciliation
using the reported outcomes rather than repeating the operation.

## Idempotency

Mutating IMAP/send commands support `--idempotency-key`.

Behavior:

- Same key + same payload -> returns cached response.
- Same key + different payload -> conflict response (`exit 6`).

Batch manifests also honor item-level keys. An item conflict is reported in that
item's result; mixed batches exit 10. See [IMAP workflow behavior](imap-workflows.md)
for durable receipt handling and its limits, and
[local command behavior](local-command-behavior.md) for offline simulation.

## Release safety

Before tagging a release, run:

```bash
scripts/release-check.sh
```
