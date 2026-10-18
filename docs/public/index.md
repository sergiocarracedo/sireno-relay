---
title: Overview
description: Token-authenticated MCP access to policy-filtered email.
---

# SirenoRELAY

Give agents narrowly scoped email access without giving them upstream credentials.

SirenoRELAY exposes a loopback Streamable HTTP MCP endpoint. The agent receives a separate bearer token; the relay keeps IMAP/SMTP credentials and performs upstream requests on the agent's behalf. A token selects one reusable provider and policy. Every list, search, read, attachment download, flag update, and send is rechecked against current configuration.

## Capability status

- Available: folder listing, paginated list/search, single and batch reads, attachment download, plain-text send, and safe message-flag updates.
- Not exposed: copy, move, delete, append, and folder mutations.

## Start here

1. Copy `config.example.yml` into an owner-only location.
2. Generate a high-entropy bearer token and store only its SHA-256 hash in the user configuration.
3. Validate with `sirenorelay config-check` and start `sirenorelay serve`.
4. Give the agent the MCP endpoint and its bearer token, never upstream credentials. Run the relay outside the agent's OS identity/container.

See the [onboarding](./onboarding), [configuration](./configuration), [MCP tools](./tools), [CLI reference](./cli), and [security model](./security) for detailed guidance.
