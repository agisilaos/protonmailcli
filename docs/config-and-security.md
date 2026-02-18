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

[safety]
require_confirm_send_non_tty = true
allow_force_send = true
```

## Runtime credential sources

Bridge credentials are resolved in this order:

1. `PMAIL_SMTP_PASSWORD` environment variable
2. password file path from flag/auth/config (`--smtp-password-file`, auth state, or config)

Username is resolved from auth state then config.

## Environment variables in active use

- `PMAIL_SMTP_PASSWORD`
- `PMAIL_PROFILE`
- `PMAIL_OUTPUT`
- `PMAIL_TIMEOUT`
- `PMAIL_USE_LOCAL_STATE` (test/local backend mode)

## Secrets policy

- Do not pass raw secrets directly on command lines.
- Use password files (`--password-file`, `--smtp-password-file`) or `PMAIL_SMTP_PASSWORD` in controlled environments.
- Never commit password files, private keys, or token material.

## Safety policy

- Non-interactive `message send` requires `--confirm-send` unless `--force`.
- `--force` is allowed only when `allow_force_send = true`.
- Use `--dry-run` in automations before mutating commands.

## Idempotency

Mutating IMAP/send commands support `--idempotency-key`.

Behavior:

- Same key + same payload -> returns cached response.
- Same key + different payload -> conflict response (`exit 6`).

## Release safety

Before tagging a release, run:

```bash
scripts/release-check.sh
```
