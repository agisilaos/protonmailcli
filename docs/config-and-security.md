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
tls_cert_file = "" # optional trusted public SMTP certificate in PEM format

[safety]
require_confirm_send_non_tty = true
allow_force_send = true
```

## Runtime credential sources

Bridge credentials are resolved in this order:

1. `PMAIL_SMTP_PASSWORD` environment variable
2. password file path from flag/auth/config (`--smtp-password-file`, auth state, or config)

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
[configuration and SMTP behavior](config-and-smtp-fixes.md) for certificate trust
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

- Non-interactive `message send` requires `--confirm-send` unless `--force`.
- `--force` is allowed only when `allow_force_send = true`.
- Use `--dry-run` in automations before mutating commands.
- Setup previews preserve existing config, and read/preview commands do not
  initialize absent state files or directories.

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
