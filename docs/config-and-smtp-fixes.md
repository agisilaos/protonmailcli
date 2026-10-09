# Configuration and SMTP behavior

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

## Trusting a local Bridge SMTP certificate

When the Bridge SMTP certificate is not trusted by the operating system, export
its public certificate and configure its PEM file explicitly:

```toml
[bridge]
host = "127.0.0.1"
smtp_port = 1025
tls_cert_file = "/absolute/path/to/bridge-cert.pem"
```

Only use the public certificate obtained from the Bridge instance you intend to
trust. This adds its certificate to normal system trust. Certificate validity and
hostname verification remain enabled; the configured host must match a name or
IP address in the certificate. With `tls_cert_file` set, a server that does not
advertise STARTTLS is rejected. Without this setting, normal system certificate
trust and automatic SMTP STARTTLS remain in use.

SMTP uses the configured operation timeout for connection establishment, server
responses, TLS, authentication, and message delivery. The default is 30 seconds.

## Header validation and delivery acknowledgement

From, recipient, subject, and extra-header values cannot contain CR or LF.
Extra-header names must be nonempty printable ASCII without spaces or colons.
Message bodies may contain line breaks.

SMTP's successful final DATA response acknowledges acceptance. A later QUIT
failure does not turn that accepted send into a retryable failure. Errors before
that acknowledgement still return failure; a lost acknowledgement can leave the
server's acceptance uncertain, so inspect the mailbox before resending.

Tests use loopback SMTP servers that never relay messages. These tests do not
establish live Bridge certificate configuration or delivery behavior.
