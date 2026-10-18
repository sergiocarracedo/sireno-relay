---
name: sirenorelay
description: Install, configure, deploy, and connect SirenoRELAY as a policy-enforced MCP email boundary. Use when setting up SirenoRELAY, onboarding an email account, writing its YAML config, connecting an MCP client, rotating agent tokens, or deploying the relay for an agent. Always isolate the relay's upstream credentials from the agent process.
---

# SirenoRELAY setup

Help an owner deploy SirenoRELAY so an agent receives narrowly scoped MCP mail
tools without receiving the mailbox's IMAP/SMTP credentials.

## Security rules

- Prefer a dedicated OS service account or isolated relay container, separate
  from the agent's OS identity/container. The relay identity alone may read its
  config, upstream secrets, state, and logs.
- Never put upstream credentials in agent configuration, prompts, tool
  arguments, source control, or shared logs. Give the agent only its own
  high-entropy SirenoRELAY bearer token through a protected secret mechanism.
- Store only the token's lowercase SHA-256 hash in `users.*.token_hash`. Never
  ask the agent to create, read, or edit the owner configuration.
- Keep configuration and secret files owner-only (`0600` files, private parent
  directory). Do not expose the MCP listener publicly or bind it to
  `0.0.0.0`; configured listener addresses must be loopback.
- A loopback listener is not isolation from same-user processes. Do not run an
  untrusted agent as the relay service account. Do not grant an agent Docker
  socket, host administration, process inspection, or access to relay mounts.
- Treat content-capturing debug logs as sensitive. `serve --debug` captures MCP
  content to stdout; keep it off for normal operation.

## Workflow

### 1. Check prerequisites and installation source

SirenoRELAY requires the Himalaya CLI in the relay process's `PATH`; it uses
its own YAML file rather than Himalaya's account configuration. Once the
`sergiocarracedo/homebrew-tap` formula is published and verified, an owner on
macOS or Linux can run `brew install sergiocarracedo/tap/sirenorelay`, which
also installs Himalaya. Until then, prefer a verified project release artifact
with checksum/provenance, or build from the repository's current instructions.
Do not invent package versions or install commands; ask the owner to
choose/confirm the source if unclear.

### 2. Create the mailbox configuration

For a human-operated machine, run `sirenorelay onboard` from the relay owner's
interactive terminal. It verifies IMAP/SMTP by listing folders, prompts for
allowed folders/senders/recipients, creates the owner configuration, and shows
the bearer token once. Empty sender or recipient allow lists deny that
direction. Review wildcard grants (`*`) carefully.

For managed deployment, create YAML using `docs/public/configuration.md` and
`config.example.yml` as references. Include:

- named `providers` with IMAP and SMTP endpoints, usernames, and protected
  password references;
- `profiles` with the minimum folder, inbound sender/content/attachment, and
  outbound recipient/content/attachment rules needed for the task;
- one `users` entry per agent, bound to exactly one provider and profile;
- a unique, high-entropy bearer token per agent, storing only its SHA-256 hash;
- loopback `listen.mcp` and owner-controlled audit logging.

Prefer `{env:VARIABLE}` references or a platform secret manager for passwords;
do not put literal credentials in an example file or commit them. Scanner
commands are executed without a shell and fail closed. Document any command and
its operational trust assumptions before adding it.

### 3. Validate least privilege

Before starting or changing access, run:

```sh
sirenorelay config-check --config /path/to/config.yml
sirenorelay policy-check --config /path/to/config.yml --user AGENT --folder FOLDER
```

`policy-check` reports allow/deny and visible message counts, not message text.
Test with representative folders and policies. Confirm the agent cannot see a
private folder, messages from denied senders, disallowed attachments, or send
to an unauthorized recipient. Do not use a real send to test denial.

### 4. Deploy outside agent control

Run `sirenorelay serve --config /path/to/config.yml` as the dedicated relay
owner. For long-running use, prefer a system service (the repository includes
an example systemd user unit that requires path/identity review) or a hardened
container. In containers:

- keep agent and relay in separate containers with no shared secret volume;
- mount relay config/secrets read-only, and keep state/log volumes private;
- avoid privileged mode, host PID/network modes, broad host mounts, and Docker
  socket access;
- publish only a private/local MCP route needed by the agent and preserve
  authentication; never publish it to the public internet.

Containerization is deployment guidance, not a claim that the project ships an
official image. If no secure private path between agent and loopback-bound
relay is available, stop and ask the owner to choose an architecture rather
than weakening the listener to all interfaces.

### 5. Connect the MCP client

Configure the client for Streamable HTTP at `http://127.0.0.1:8080/mcp` when
the client and relay share the appropriate network namespace. Configure its
protected secret/header mechanism to send `Authorization: Bearer TOKEN`. The
token is not the upstream password. If the client is in another container,
`127.0.0.1` points to that container; use an explicitly private route and do
not expose the endpoint publicly.

Explain the available tools accurately: `list_folders`, `list_messages`,
`search_messages`, `read_message`, `read_messages`, `send_message`,
`update_message_flags`, and policy-checked attachment downloads. The policy
filters results and writes; do not promise unsupported operations such as
delete, move, or arbitrary folder mutation.

### 6. Operate and rotate

- Keep the foreground process under a service manager and monitor its exit and
  audit output without capturing message content.
- After policy/config edits, rerun `config-check` and the relevant
  `policy-check`; valid edits are reloaded for subsequent operations.
- Rotate an exposed or stale token with
  `sirenorelay user token rotate --user AGENT --config /path/to/config.yml`.
  Rotation is interactive and prints the replacement once; update the agent's
  secret store and invalidate/remove the old copy.
- On uncertain SMTP failure after submission begins, inspect the Sent folder
  before retrying to avoid duplicates.
- If upstream credentials are exposed, rotate them at the mail provider and
  replace the relay's protected secret.

## CLI reference

- `sirenorelay onboard`: interactive first-time config and token creation.
- `sirenorelay config-check [--config PATH]`: validate permissions, YAML,
  references, rules, and provider/user configuration.
- `sirenorelay policy-check --user NAME --folder NAME [--config PATH]`: show
  whether that folder is allowed and how many messages are visible.
- `sirenorelay serve [--config PATH]`: run the MCP endpoint. Avoid `--debug`
  with real mail because it enables content capture to stdout.
- `sirenorelay user token rotate --user NAME [--config PATH]`: replace an
  agent's bearer token interactively; the raw value is shown once.
- `sirenorelay version`: print the build version.
- `sirenorelay completion bash|zsh|fish|powershell`: generate shell completion.

If a requested option, tool, install source, or behavior is not confirmed by
the repository's current docs/configuration, inspect the project source or ask
the owner rather than guessing.
