# Sireno AI Relay

Sireno gives an agent a restricted, local IMAP/SMTP mailbox and a matching CLI. It is a deterministic access boundary: folders and messages denied by policy are absent from the agent's view, including lists, counts, searches, fetches, and virtual message IDs.

> This is an early v1 implementation. It supports loopback-only IMAP/SMTP upstream accounts using password/app-password credentials. POP3, OAuth, remote clients, and non-email relays are intentionally out of scope.

## Security model

Run Sireno under an owner OS account. Agent processes must not be able to read the owner configuration, local state, upstream credentials, or directly reach the upstream mail provider. Sireno cannot constrain an agent that already holds provider credentials.

The relay fails closed: unsupported MIME, malformed mail, oversized mail, failed scanners, and policy reloads that make a selected mailbox ambiguous do not expose the original message. Logical invisibility does not prevent timing side channels or revoke mail an agent already retrieved.

## Quick start

```sh
cp config.example.yml config.yml
chmod 600 config.yml
# Replace all example addresses and pass commands.

go run ./cmd/sireno config-check --config config.yml
go run ./cmd/sireno serve --config config.yml
```

In another terminal, give the agent only its relay password:

```sh
export SIRENO_AGENT_PASSWORD="$(pass show agents/hermes)"
go run ./cmd/sireno mail folders --user hermes-agent
go run ./cmd/sireno mail list --user hermes-agent --folder INBOX/Invoices
go run ./cmd/sireno mail read --user hermes-agent --folder INBOX/Invoices --uid 1
printf 'From: owner@example.org\r\nTo: accounting@example.org\r\nSubject: Invoice\r\n\r\nProcessed.' \
  | go run ./cmd/sireno mail send --user hermes-agent --from owner@example.org --to accounting@example.org
```

Mail clients can instead connect to `127.0.0.1:1143` (IMAP) and `127.0.0.1:1025` (SMTP) using the configured agent username and password.

## Policy

Policy is explicit and default-deny. Rules use `mode:pattern`:

- `ro` grants read; `rw` grants read and folder management; `wo` grants send-only address access.
- Exact names, `*` wildcards, and `re:<regular expression>` are supported.
- A matching deny wins over every allow.
- Folder read is always required. Address read is additionally required only when the profile configures address allow rules.
- A send checks the SMTP envelope **and** message `To`, `Cc`, and `Bcc`; one denied recipient rejects the whole message before upstream submission.

`ro:*` and `rw:*` trigger an owner warning. Folder `wo` rules are invalid. Bcc is evaluated on received messages only if it exists in stored headers.

Inbound and outbound content rules are separate. Known recovery/secret patterns should be declared explicitly. An optional executable scanner adds best-effort detection; it must emit JSON `{"status":"clean"}` or `{"status":"finding"}` on stdout and exit successfully. A scanner failure, malformed output, or timeout denies the operation. A Gitleaks wrapper must translate its ambiguous nonzero status into that contract; do not configure Gitleaks directly as the scanner command.

Link removal replaces links with `[link removed]` and does not retain their targets. V1 accepts only plain-text UTF-8/ASCII MIME for policy-checked mail. HTML, multipart, attachments, encoded transfer bodies, encrypted/signed mail, and unsupported MIME are hidden or rejected rather than forwarded unsafely.

## Owner commands

```sh
sireno config-check --config config.yml
sireno policy-check --config config.yml --user hermes-agent --folder INBOX/Invoices
sireno serve --config config.yml
```

`policy-check` evaluates real upstream mail but prints only a visible count—not subjects or bodies. Configuration/state files are owner-only; do not run these owner commands in an agent environment.

## Operations

Run `sireno serve` in the foreground for development; Ctrl+C closes both listeners. For a persistent installation, use the included user service example after installing a compiled binary. Do not bind to non-loopback addresses in v1.

See `docs/configuration.md`, `docs/policy.md`, `docs/security.md`, and `docs/operations.md` for details.
