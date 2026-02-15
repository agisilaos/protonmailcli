# Config and Security

## Directory layout

Default paths:

- `~/.config/protonmailcli/config.toml`
- `~/.config/protonmailcli/profiles/default.toml`
- `~/.local/share/protonmailcli/cache/`
- `~/.local/state/protonmailcli/logs/`

If `XDG_CONFIG_HOME` is set, use:

- `$XDG_CONFIG_HOME/protonmailcli/config.toml`

## Config shape (initial)

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

[safety]
require_confirm_send_non_tty = true
allow_force_send = true
```

## Environment variables

- `PMAIL_PROFILE`
- `PMAIL_OUTPUT`
- `PMAIL_TIMEOUT`
- `PMAIL_BRIDGE_HOST`
- `PMAIL_BRIDGE_IMAP_PORT`
- `PMAIL_BRIDGE_SMTP_PORT`
- `PMAIL_NO_COLOR`

## Secrets policy

- Never accept secrets via command-line flags.
- Accept secret input via:
  - `--password-file <path|->`
  - stdin (non-echo input when interactive)
  - platform credential store for persistent credentials
- Redact secrets from logs and error output.

## Logging and privacy

- Diagnostics to stderr by default.
- Optional log file in state dir with rotation.
- Do not log message bodies unless explicit debug mode is enabled.
- Attachment paths may be logged; attachment content must never be logged.

## Confirmation policy

- Interactive send: prompt with explicit summary.
- Non-interactive send: require `--confirm-send <token>`.
- `--force` is allowed only when policy `allow_force_send=true`.

## Idempotency

Mutating commands may accept `--idempotency-key`.

Server/client behavior:

- Same key + same payload: return existing result.
- Same key + different payload: return conflict (`exit 6`).

## Failure behavior

- Timeouts are bounded by `--timeout`.
- Retry only safe/idempotent operations automatically.
- Send operation retries only with explicit idempotency key.
