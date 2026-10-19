---
title: Onboarding
description: Install prerequisites, create an owner-only mailbox configuration, and connect an agent safely.
---

# Onboarding

SirenoRELAY uses the Himalaya CLI as its upstream mail provider. Install
Himalaya first and ensure it is in the `PATH` of the account that will run
SirenoRELAY. SirenoRELAY uses its own YAML configuration; it does not reuse
Himalaya account configuration.

## Install

After the first GitHub release publishes the Homebrew formula, install on macOS
or Linux with:

```sh
brew install sergiocarracedo/tap/sirenorelay
sirenorelay --version
```

The formula installs Himalaya as a dependency. Until the first release and
formula are published, build from the repository using `make build` or use a
verified release artifact; do not run the Homebrew command before it exists.
For an agent-guided setup, install the repository skill in your agent environment:

```sh
npx skills add sergiocarracedo/sireno-relay --skill sirenorelay
```

Ask the agent to read the skill and guide you through installing and configuring
the relay **on an owner-controlled account**, not in the agent's account. Never
paste upstream credentials or bearer tokens into an agent prompt.

## Interactive setup

Run onboarding in an owner-controlled interactive terminal:

```sh
sirenorelay onboard
```

The wizard asks for IMAP/SMTP account details and credentials, verifies the
connection by listing folders, then lets you select multiple folders with the
arrow keys, Space, and Enter. It creates the `default` provider and a default
profile, then asks which sender addresses may be read and which recipients may
be sent to. Leaving either allow list blank denies that direction. Selecting
`*` grants broad access and requires confirmation.

On success it creates `~/.config/sirenorelay/config.yml`, secret files, and a
completion marker at `~/.config/sirenorelay/onboard`, all under the owner
configuration directory. The generated MCP bearer token is shown once. Copy it
to a secret manager or the agent's protected configuration; do not put it in a
prompt, source file, shared log, or public MCP URL.

Check the resulting setup:

```sh
sirenorelay config-check
sirenorelay policy-check --user default --folder INBOX
```

Use the actual user and folder names shown by your generated configuration.
Review the policy before providing the token to an agent.

## Run as a service

After onboarding and validation, install and start the owner-scoped service:

```sh
sirenorelay service install
sirenorelay service start
sirenorelay service status
```

Pass `--config /absolute/path` to every service command when the configuration
is not in the default location. The Linux installer creates and enables a
systemd **user** service; it does not need `sudo` and runs as the owner account.
On macOS it creates an owner-scoped launchd service. Both use the resolved
binary and configuration paths, restart after a failure, and shut down cleanly
when stopped.

Manage its lifecycle with `sirenorelay service restart`,
`sirenorelay service stop`, or `sirenorelay service uninstall`. Keep the relay
under the owner/service account, not the agent account. For short-lived local
development, run `sirenorelay serve` in the foreground instead.

The default MCP listener is `127.0.0.1:8080`. The agent's MCP client needs that
endpoint and its bearer token, but must not be able to read the configuration,
secret files, service environment, or relay state. See [security model](./security)
for dedicated-user and container guidance.

## Connect an agent

Configure its MCP client for the Streamable HTTP endpoint at
`http://127.0.0.1:8080/mcp` and send the assigned bearer token in the
`Authorization: Bearer …` header using the client's protected secret mechanism.
Do not send the upstream IMAP/SMTP username or password to the agent. The agent
can use `list_folders`, `list_messages`, `search_messages`, `read_message`,
`read_messages`, `send_message`, `update_message_flags`, and attachment
download where policy allows.

If the agent runs in a separate container or host, `127.0.0.1` refers to that
client itself, not the relay. Configure a private, access-controlled route
appropriate to the environment; do not solve this by exposing the listener
publicly or binding it to `0.0.0.0`.

## Rotation and recovery

Rotate a token from an interactive owner terminal:

```sh
sirenorelay user token rotate --user default
```

The new token is displayed once, replaces the old token immediately, and must
be updated in the agent's secret configuration. If the token is lost, rotate
it; SirenoRELAY cannot reveal the original token. If upstream credentials are
exposed, rotate them at the mail provider and update the protected relay secret.

For non-interactive or more advanced setups, use the [configuration
reference](./configuration), then validate before serving.
