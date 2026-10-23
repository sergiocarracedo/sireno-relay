---
title: Getting started
description: Install, configure, validate, and run SirenoRELAY locally, as a service, or in a container.
---

# Getting started

Run SirenoRELAY in an owner-controlled environment. The relay owns upstream
IMAP/SMTP credentials and configuration; the agent receives only the MCP
endpoint and its own bearer token.

## 1. Install the relay and Himalaya

After the first release, Homebrew installs both dependencies on macOS and
Linux:

```sh
brew install sergiocarracedo/tap/sirenorelay
sirenorelay --version
himalaya --version
```

Before the formula exists, clone this repository and build from source:

```sh
make build
export PATH="$PWD/bin:$PATH"
sirenorelay --version
```

Install [Himalaya](https://github.com/pimalaya/himalaya#installation) separately
for source builds. `sirenorelay config-check`, `serve`, onboarding, and service
start commands require it in `PATH`.

## 2. Create and validate owner configuration

For an IMAP/SMTP account, use the interactive wizard in an owner terminal:

```sh
sirenorelay onboard
sirenorelay config-check
sirenorelay policy-check --user default --live --folder INBOX
```

The wizard writes `~/.config/sirenorelay/config.yml` and prints the bearer token
once. For a manual or non-interactive setup, copy `config.example.yml` into an
owner-only directory, set the required credential variables, and run the same
validation commands. See [onboarding](./onboarding) and
[configuration](./configuration) for the complete setup.

## 3. Choose how to run it

### Foreground CLI

Use the CLI while developing, testing policy, or running the relay temporarily:

```sh
sirenorelay serve --config "$HOME/.config/sirenorelay/config.yml"
```

It serves MCP at `http://127.0.0.1:8080/mcp` by default. Leave this terminal
running; Ctrl+C shuts it down gracefully.

### Owner-scoped service

Use the built-in service manager for a persistent macOS or Linux installation:

```sh
sirenorelay service install --config "$HOME/.config/sirenorelay/config.yml"
sirenorelay service start --config "$HOME/.config/sirenorelay/config.yml"
sirenorelay service status --config "$HOME/.config/sirenorelay/config.yml"
```

It creates a systemd **user** service on Linux or an owner-scoped launchd
service on macOS; no `sudo` is needed. Use the same absolute `--config` path for
every lifecycle command. See [CLI reference](./cli) for restart, stop,
uninstall, and log commands.

### Docker container

The repository includes a Linux image containing both SirenoRELAY and Himalaya.
Build it and follow the [Docker deployment guide](./docker). Containers do not
use `sirenorelay service`; Docker is the service manager and runs `serve` as the
container process.

## 4. Connect the agent

Give the agent its bearer token through its protected secret configuration and
configure the Streamable HTTP MCP URL:

```text
http://127.0.0.1:8080/mcp
```

The agent must not receive upstream credentials or be able to read the relay
configuration, state, logs, process environment, or Docker socket. If it runs
in another container or host, do not expose the relay publicly to make this URL
work; use the deployment-specific local transport described in the
[security model](./security).
