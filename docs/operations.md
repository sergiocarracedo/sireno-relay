# Operations

For persistent owner-side operation, install and start the built-in service:

```sh
sirenorelay service install
sirenorelay service start
sirenorelay service status
```

The service runs under the invoking owner's account. Linux uses a systemd user
unit and macOS uses an owner-scoped launchd service. No `sudo` is required.
Use `--config /absolute/path` consistently when the relay config is outside the
default location. Manage it with `restart`, `stop`, and `uninstall`; use
`journalctl --user -u sirenorelay.service` to view Linux service logs.

Start a foreground relay for development:

```sh
sirenorelay serve --config /home/owner/.config/sirenorelay/config.yml
```

Use Ctrl+C for a graceful shutdown. Run the binary under the same owner account
that owns configuration and state.

Policy changes take effect on the next operation. Active IMAP sessions whose selected virtual mailbox differs after a policy change are closed and must reconnect.

Before upgrades or policy changes, run `sirenorelay config-check`, then use `sirenorelay policy-check` against each agent/folder pairing. It reports counts only, so it is safe for owner review without printing messages.

When upstream SMTP disconnects after submission begins, delivery can be uncertain. SirenoRELAY does not retry automatically; inspect the provider's Sent folder before manually retrying.
