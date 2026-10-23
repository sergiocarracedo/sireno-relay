---
title: CLI reference
description: Commands, flags, expected output, and safe operational use.
---

# CLI reference

The executable is `sirenorelay`. Unless stated otherwise, commands that load
configuration accept `--config PATH`; without it they use
`~/.config/sirenorelay/config.yml`. For compatibility, if that file does not
exist, SirenoRELAY uses an existing `~/.config/sireno-relay/config.yml`.
Commands require the Himalaya executable in
`PATH`, except version, shell-completion generation, and non-starting service
commands such as `service status` and `service uninstall`.

## `sirenorelay onboard`

Starts an interactive first-run wizard. It requires a real terminal for stdin
and stdout. It prompts for IMAP/SMTP settings and secrets, checks connectivity
by listing folders, and writes the owner config and completion marker. It
prints the new bearer token once. It does not accept a config-path flag; the
wizard writes to the default user config directory.

## `sirenorelay config-check [--config PATH]`

Loads and validates configuration without starting the service. Checks file
ownership and permissions, YAML syntax and keys, required provider/user
references, token hash shape, rule syntax, listener loopback binding, and
logging options. Use it after every manual edit and before starting the relay.
Success prints `configuration valid`; configuration errors exit non-zero.

## `sirenorelay policy-check --user NAME MODE [--config PATH]`

Checks one configured user's policy without sending mail. Exactly one mode is
required:

```sh
# Count real messages currently visible in an upstream folder.
sirenorelay policy-check --user agent --live --folder INBOX

# Simulate inbound and outbound messages with the same policy evaluator used at runtime.
sirenorelay policy-check --user agent --inbound --folder INBOX \
  --sender invoices@example.org --subject "Invoice" --body "See attachment" \
  --attachment ./invoice.pdf
sirenorelay policy-check --user agent --outbound --recipient vendor@example.org \
  --subject "Reply" --body "Thanks"

# Evaluate a complete RFC 822 fixture. Outbound --recipient adds envelope recipients.
sirenorelay policy-check --user agent --inbound --folder INBOX --message sample.eml
sirenorelay policy-check --user agent --outbound --message draft.eml --recipient vendor@example.org
```

`--live` contacts the provider, evaluates every real inbound message, and prints
only the visible count. `--inbound` evaluates folders, senders, subject/body
rules, scanners, link removal, and attachments. `--outbound` evaluates the
configured provider `From` address, all envelope and declared recipients, and
outbound content rules. Simulation attachments are repeatable file paths and
use their extension as the declared MIME type; the evaluator still verifies the
bytes' detected MIME type. The command never prints message or attachment
contents, scanner output, or secrets. When an allowed message has attachments
removed by the configured mode, it reports that removal separately.

## `sirenorelay serve [--config PATH] [--debug]`

Starts the Streamable HTTP MCP endpoint and remains in the foreground until
interrupted or terminated. The default bind address is `127.0.0.1:8080`; the
configuration may select another loopback address. Run it under a service
manager for production. It watches the config file and reports valid reloads;
subsequent operations use current policy.

`--debug` is for controlled troubleshooting only: it enables debug-level
operational metadata on stdout. SirenoRELAY does not record MCP request or
response content, including when `logging.mcp.capture_content` is enabled.

## `sirenorelay service ACTION [--config PATH]`

Manages the persistent, owner-scoped MCP relay. `ACTION` is one of `install`,
`start`, `stop`, `restart`, `status`, or `uninstall`. Run `install`, then
`start`, after validating owner configuration. Linux uses a systemd user unit;
macOS uses an owner-scoped launchd service. These do not require `sudo` and
must be run as the OS account that owns the configuration and secrets.

`install`, `start`, and `restart` validate configuration and require Himalaya.
`status`, `stop`, and `uninstall` remain available if Himalaya has been removed,
so a broken installation can still be inspected or cleaned up. Add
`--config /absolute/path` when the service should use a non-default config; use
the same path for every lifecycle command.

## `sirenorelay user token rotate --user NAME [--config PATH]`

Interactively generates a replacement token for one configured user, updates
the stored hash, and displays the raw token once. It requires a terminal and
`--user`. The old token becomes invalid immediately. Deliver the replacement
through the agent's protected secret store and remove stale copies.

## `sirenorelay version` / `sirenorelay --version`

Both print the version embedded by the release build. Local builds may print a
development identifier. Neither requires Himalaya or a configuration file.

## `sirenorelay completion SHELL`

Writes shell-completion instructions to stdout. `SHELL` must be `bash`, `zsh`,
`fish`, or `powershell`; redirect the output according to that shell's
completion setup.

## Exit behavior

Invalid arguments and flag syntax return a usage error (exit status 2). Runtime,
configuration, provider, and service errors return non-zero (normally status 1).
Do not interpret a successful `policy-check` as proof that an email send will
succeed; SMTP availability and delivery outcome are separate concerns.
