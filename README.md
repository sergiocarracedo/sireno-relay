# Sireno AI Relay

Sireno gives an agent a restricted, token-authenticated MCP mail view. It is a deterministic access boundary: folders and messages denied by policy are absent from the agent's view, including virtual message IDs.

> This is an early v1 implementation. Himalaya is the only upstream provider and Sireno binds its MCP listener to loopback. POP3, OAuth, remote clients, and non-email relays are intentionally out of scope.

## Prerequisite: Himalaya

Install the [Himalaya CLI](https://github.com/pimalaya/himalaya#installation). Sireno verifies it is available in `PATH`. Sireno uses its own YAML configuration; it does not read Himalaya's TOML accounts.

## Security model

Run Sireno under an owner OS account. Agent processes must not be able to read the owner configuration, local state, upstream credentials, or directly reach the upstream mail provider. Sireno cannot constrain an agent that already holds provider credentials.

The relay fails closed: unsupported MIME, malformed mail, oversized mail, failed scanners, and policy reloads that make a selected mailbox ambiguous do not expose the original message. Logical invisibility does not prevent timing side channels or revoke mail an agent already retrieved.

## Quick start

```sh
sireno-relay onboard
sireno-relay serve
```

`onboard` supports IMAP/SMTP accounts and verifies them by listing live folders. Use ↑/↓ to move, Space to select folders, and Enter to continue. A failed connection lets you correct the provider settings. It writes an owner-only configuration at `~/.config/sireno-relay/config.yml`, owner-only upstream secret files, and an onboarding marker at `~/.config/sireno-relay/onboard`. For another Himalaya backend or a non-interactive setup, use `config.example.yml` and the manual configuration reference.

Give the agent only the MCP endpoint and the bearer token whose SHA-256 hash is
stored in its `users` entry. The agent calls the discovered MCP tools with
`Authorization: Bearer <token>`; it never receives upstream credentials or an
IMAP/SMTP endpoint.

`list_messages` and `search_messages` return no more than 20 filtered
messages and an opaque `next_cursor` when another candidate page may exist.
`send_message` accepts only composed plain text; attachment uploads, moving,
deleting, and folder changes remain unavailable until their provider behavior
is proven target-scoped.

## Policy

Policy is explicit and default-deny. Folder rules use `mode:pattern`:

- `ro` grants read; `rw` grants read and reserved folder-management access; `wo` is invalid for folders.
- Exact names, `*` wildcards, and `re:<regular expression>` are supported.
- A matching deny wins over every allow.
- `inbound.addresses` contains unprefixed sender patterns. A visible message must have an allowed `From` sender; its recipients never authorize it.
- `outbound.addresses` contains unprefixed recipient patterns. Every envelope and declared recipient must be allowed.
- A send checks the supplied envelope **and** message `To`, `Cc`, and `Bcc`; one denied recipient rejects the whole message before upstream submission.

`ro:*` and `rw:*` trigger an owner warning. `*` under inbound or outbound addresses grants every sender or recipient, respectively, and should be used only deliberately. `outbound.signature` optionally appends a signature to sanitized sent text.

Inbound and outbound content rules are separate. Known recovery/secret patterns should be declared explicitly. An optional executable scanner adds best-effort detection; it must emit JSON `{"status":"clean"}` or `{"status":"finding"}` on stdout and exit successfully. A scanner failure, malformed output, or timeout denies the operation. A Gitleaks wrapper must translate its ambiguous nonzero status into that contract; do not configure Gitleaks directly as the scanner command.

Link removal replaces links with `[link removed]` and does not retain their targets. V1 accepts bounded UTF-8/ASCII plain-text and HTML-to-text multipart mail. Attachments remain default-deny: profiles must explicitly allow detected MIME types, and each download requires the agent bearer token again. Unsupported or malformed MIME is hidden or rejected fail-closed.

## Owner commands

```sh
sireno-relay config-check --config config.yml
sireno-relay policy-check --config config.yml --user hermes-agent --folder INBOX/Invoices
sireno-relay serve --config config.yml
sireno-relay user token rotate --user hermes-agent --config config.yml
```

`policy-check` evaluates real upstream mail but prints only a visible count—not subjects or bodies. Configuration/state files are owner-only; do not run these owner commands in an agent environment.

## CLI tooling

Sireno provides structured help and shell completion:

```sh
sireno-relay --help
sireno-relay completion zsh > "${fpath[1]}/_sireno-relay"
sireno-relay completion bash > "$(brew --prefix)/etc/bash_completion.d/sireno-relay"
sireno-relay version
```

For source builds, use `make build`, `make test`, `make vet`, `make check`, or `make install`. Run `make hooks` once after cloning to enable the repository's pre-commit quality gate. The build embeds the current Git version in `sireno-relay version`; release builds can override it with `make build VERSION=v1.2.3`.

CLI flags can also be supplied as `SIRENO_*` environment variables when a command defines the matching flag: for example, `SIRENO_CONFIG=/secure/sireno.yml sireno-relay config-check`. Relay policy and upstream credentials always come only from the strictly validated YAML file.

## Operations

Run `sireno-relay serve` in the foreground for development; Ctrl+C closes the MCP listener. For a persistent installation, use the included user service example after installing a compiled binary. Do not bind to non-loopback addresses in v1.

See `docs/configuration.md`, `docs/policy.md`, `docs/security.md`, and `docs/operations.md` for details.
