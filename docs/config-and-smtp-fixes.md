# Configuration and Bridge transport behavior

Configuration uses TOML parsing and writing. Inline comments do not change values,
quoted `#` characters and escaped strings are preserved, and malformed values
(including quoted or misspelled booleans) fail configuration loading. Keep safety
settings as TOML booleans, for example:

```toml
[safety]
require_confirm_send_non_tty = true # require explicit confirmation
allow_force_send = false
```

Runtime defaults use this precedence, from highest to lowest:

1. Explicit CLI output/profile flags.
2. Nonempty `PMAIL_OUTPUT`, `PMAIL_PROFILE`, and `PMAIL_TIMEOUT` environment values.
3. Values in the configuration file.
4. Built-in defaults (`human`, `default`, and `30s`).

`PMAIL_OUTPUT` accepts `human`, `json`, or `plain`. `PMAIL_TIMEOUT` requires a
positive Go duration such as `100ms` or `30s`. Invalid nonempty output modes and
durations fail configuration loading. An empty environment variable leaves its
configured value unchanged. Environment defaults are applied when configuration
is loaded; they do not alter defaults persisted by initial setup.

## Trusting a Bridge IMAP or SMTP certificate

IMAP verifies the server certificate and configured hostname before logging in,
including connections to localhost or a loopback IP address. IMAP and SMTP use
the same certificate trust policy. When a Bridge certificate is not trusted by
the operating system, export its public CA/certificate and configure its PEM file
explicitly:

```toml
[bridge]
host = "127.0.0.1"
imap_port = 1143
smtp_port = 1025
tls_cert_file = "/absolute/path/to/bridge-cert.pem"
```

Only use the public certificate obtained from the Bridge instance you intend to
trust. This adds its certificate to normal system trust. Certificate validity and
hostname verification remain enabled; the configured host must match a name or
IP address in the certificate. The PEM file must provide trust for both endpoints
if their certificates differ. An unreadable or invalid PEM file fails IMAP setup
before a connection is opened. The CLI reports these local trust configuration
failures as `config_error` (exit 3, `retryable: false`); correct the configuration
before retrying.

IMAP always requires STARTTLS, with TLS 1.2 or newer, before authentication.
For SMTP, setting `tls_cert_file` requires the server to advertise STARTTLS.
Without this setting, normal system certificate trust and automatic SMTP STARTTLS
remain in use. Neither protocol disables certificate verification for loopback
hosts.

SMTP uses the configured operation timeout for connection establishment, server
responses, TLS, authentication, and message delivery. The default is 30 seconds.

## Header validation and delivery acknowledgement

From, recipient, subject, and extra-header values cannot contain CR or LF.
Extra-header names must be nonempty printable ASCII without spaces or colons.
Message bodies may contain line breaks. SMTP recipients can differ from the
visible `To` header when an internal draft fallback sends only to the account's
own address; the intended draft recipients remain in its message headers.

SMTP's successful final DATA response acknowledges acceptance. A later QUIT
failure does not turn that accepted send into a retryable failure. Errors before
that acknowledgement still return failure; a lost acknowledgement can leave the
server's acceptance uncertain, so inspect the mailbox before resending.

Successful TLS workflow tests use explicitly trusted loopback IMAP and SMTP
fixtures that never relay messages. These tests do not establish live Bridge
certificate configuration or delivery behavior.
