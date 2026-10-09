# Local command behavior

Auth login requires a regular, readable, nonempty password file of at most 64 KiB.
Named pipes and devices are rejected without waiting. This validates local
credential availability; it does not authenticate with the remote server.
Failed `doctor` JSON includes the computed `data.summary`, `data.checks`, and
`data.doctor` sections alongside its error so callers can inspect failed checks.
Doctor recognizes the selected Bridge account before auth/config usernames.
