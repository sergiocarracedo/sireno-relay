---
title: CLI reference
description: Commands, flags, expected output, and safe operational use.
---

# CLI reference

The executable is `sirenorelay`. Unless stated otherwise, commands that load
configuration accept `--config PATH`; without it they use
`~/.config/sirenorelay/config.yml`. Commands require the Himalaya executable in
`PATH`, except version and shell-completion generation.

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

## `sirenorelay policy-check --user NAME --folder NAME [--config PATH]`

Evaluates one configured user's access to a folder and, if allowed, reports the
number of visible messages. It never prints message contents. Both `--user`
and `--folder` are required. Use the exact upstream folder name and a configured
user name. This is a read-only check, not a substitute for reviewing the full
policy or testing sender/recipient filtering.

## `sirenorelay serve [--config PATH] [--debug]`

Starts the Streamable HTTP MCP endpoint and remains in the foreground until
interrupted or terminated. The default bind address is `127.0.0.1:8080`; the
configuration may select another loopback address. Run it under a service
manager for production. It watches the config file and reports valid reloads;
subsequent operations use current policy.

`--debug` is for controlled troubleshooting only: it enables debug logging and
MCP content capture to stdout. Treat this output as sensitive mail data and do
not enable it in normal service operation.

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
