# Operations

Start a foreground relay:

```sh
sireno-relay serve --config /home/owner/.config/sireno/config.yml
```

Use Ctrl+C for a graceful shutdown. Run the binary under the same owner account that owns configuration and state. A systemd user-unit template is available at `docs/examples/relay.service`; adjust binary and config paths before installing it.

Policy changes take effect on the next operation. Active IMAP sessions whose selected virtual mailbox differs after a policy change are closed and must reconnect.

Before upgrades or policy changes, run `sireno-relay config-check`, then use `sireno-relay policy-check` against each agent/folder pairing. It reports counts only, so it is safe for owner review without printing messages.

When upstream SMTP disconnects after submission begins, delivery can be uncertain. Sireno does not retry automatically; inspect the provider's Sent folder before manually retrying.
