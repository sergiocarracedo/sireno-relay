---
title: Configuration
description: Complete reference for provider credentials, policy profiles, agent tokens, listener, and audit logging.
---

# Configuration reference

SirenoRELAY reads an owner-managed YAML file. The default path is
`~/.config/sirenorelay/config.yml`; `--config /absolute/path` selects another
file for commands that support it. The file must be a regular file owned by the
relay process and have no group/other permissions (normally `chmod 600`).
Unknown and duplicate YAML keys are errors. Environment interpolation uses
`{env:NAME}` in string values; a missing variable is an error.

## Configuration model

The file has five top-level sections: `providers` contain upstream account
settings, `profiles` contain reusable policy, `users` bind bearer tokens to one
provider and profile, `listen` controls the loopback MCP address, and `logging`
controls audit output.

```yaml
providers:
  personal:
    address: owner@example.org
    imap:
      address: imap.example.org:993
      username: owner@example.org
      password: "{env:SIRENO_IMAP_PASSWORD}"
    smtp:
      address: smtp.example.org:465
      username: owner@example.org
      password: "{env:SIRENO_SMTP_PASSWORD}"

profiles:
  invoices:
    folders:
      allow: ["ro:INBOX/Invoices"]
      deny: ["ro:INBOX/Private"]
    inbound:
      addresses:
        allow: ["billing@example.org"]
    outbound:
      addresses:
        allow: ["assistant@example.org"]

users:
  finance-agent:
    provider: personal
    profile: invoices
    token_hash: <64-character-lowercase-sha256>

listen:
  mcp: 127.0.0.1:8080

logging:
  level: info
  target: /var/log/sirenorelay/audit.jsonl
  rotation:
    retention_days: 7
    max_file_size: 100MiB
  mcp:
    capture_content: false
```

## `providers`

Each named provider is a reusable upstream mailbox. A provider requires
`address`, plus both `imap` and `smtp` objects. Each server object requires
`address`, `username`, and `password`.

- `address`: mailbox email address used for the upstream identity.
- `imap.address` / `smtp.address`: host and port understood by the configured
  Himalaya backend, such as `imap.example.org:993` and `smtp.example.org:465`.
- `username`: upstream account login.
- `password`: a literal string, `{env:VARIABLE}`, or a command array. Prefer an
  environment reference or secret manager. Command arrays run directly without
  a shell, and stdout supplies the secret; never put secrets in command-line
  arguments.

These credentials belong to the relay operator. The agent receives only its
SirenoRELAY bearer token and must run under an OS identity that cannot read the
provider secret or configuration.

## `profiles`

Profiles define a reusable policy. If a rule is absent or its allow list is
empty, access is denied. Deny rules take precedence over allow rules. Text
patterns support exact values, `*` wildcards, and `re:` followed by a Go regular
expression.

### `folders.allow` and `folders.deny`

Folder entries require an access prefix: `ro:NAME` for read-only or `rw:NAME`
for writable access. `wo:` is not supported. Folder names are matched against
the upstream folder list; use `re:` when names vary by account. An allowed
folder appears in `list_folders`; a denied folder is absent rather than shown as
a forbidden resource. Writable access is needed for operations such as changing
flags; grant it only where needed. A broad `ro:*` or `rw:*` allow is accepted
with a warning and should be treated as a deliberate broad grant.

### `inbound`

Inbound rules filter mail returned to the agent. `addresses.allow` and
`addresses.deny` match the sender's email address. `denied_subjects` and
`denied_bodies` hide messages whose subject or body matches a listed pattern.
`remove_links: true` removes links from sanitized content. `scanner` is an
optional executable command: SirenoRELAY passes bounded message text on stdin
and accepts only the documented clean/finding JSON response; errors or an
unexpected response fail closed and hide the mail.

`attachments` applies an independent default-deny filter. A MIME pattern may be
an exact type, a `*` wildcard, or `re:` expression. An attachment must match an
allow pattern and must not match deny. `mode` controls how a disallowed
attachment is handled: `reject` hides/rejects the message, `remove` removes the
attachment and adds a fixed notice, and `silent-remove` removes it without the
notice. The default is `remove`.

### `outbound`

Outbound rules are checked before the upstream SMTP handoff.
`addresses.allow` and `addresses.deny` match recipients; each recipient must be
allowed. `denied_subjects` and `denied_bodies` reject messages matching those
patterns. `signature` optionally appends operator-controlled text. `attachments`
uses the same default-deny MIME allow/deny patterns and modes as inbound mail;
for outbound, `reject` rejects the send while removal modes strip disallowed
attachments before handoff.

### `users`

Each user is an agent identity, not an operating-system account. `provider`
and `profile` name existing entries. `token_hash` is the lowercase 64-character
SHA-256 hash of a high-entropy bearer token; never store the raw token here.
Tokens must not be shared between users. `tag_sent` defaults to `true` and adds
the `X-Sireno-Agent` marker to mail sent by that user; set it to `false` to opt
out.

Generate or rotate tokens with `sirenorelay user token rotate --user NAME`.
Rotation prints the new raw token once and immediately invalidates the old one.

## `listen`

`listen.mcp` is a `host:port` address and must resolve to a loopback interface.
The default is `127.0.0.1:8080`. Do not expose the listener to a network or
reverse proxy without a separately reviewed authentication and transport
design. A loopback listener is not isolation from another process running as
the same OS user.

## `logging`

- `level`: `error`, `warn`, `info`, or `debug`.
- `target`: `stdout` or an absolute file path. File targets must be writable by
  the relay service account. Keep audit files owner-only.
- `rotation.retention_days`: non-negative retention period.
- `rotation.max_file_size`: non-negative byte count or `KiB`, `MiB`, or `GiB`
  size (for example `100MiB`).
- `mcp.capture_content`: defaults to false. When enabled, request/response
  content may be recorded and requires a file target; use only when necessary,
  with strict access and retention controls. Operational audit metadata does
  not include credentials or bearer tokens.

The `serve --debug` flag enables content capture and sends debug output to
stdout. Avoid it with real mail unless you have explicitly accepted the data
exposure risk.

## Validate and check policy

```sh
sirenorelay config-check
sirenorelay policy-check --user finance-agent --folder INBOX/Invoices
```

`policy-check` reports whether the folder is allowed and the number of visible
messages; it does not print message contents. Run it after policy edits and
before granting an agent a token.
