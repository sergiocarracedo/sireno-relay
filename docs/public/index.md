---
title: Sireno
description: Token-authenticated MCP access to policy-filtered email.
---

# Sireno

Give agents narrowly scoped email access without giving them upstream credentials.

Sireno exposes a loopback Streamable HTTP MCP endpoint. A bearer token selects one reusable provider and one reusable policy. Every list, search, read, attachment download, and send is rechecked against the current configuration.

## Capability status

- Available: folders, paginated list/search, read, attachment download, and plain-text send.
- Unavailable until provider behavior is proven target-scoped: flag updates, copy, move, delete, append, and folder mutations.

## Start here

1. Copy `config.example.yml` into an owner-only location.
2. Generate a high-entropy bearer token and store only its SHA-256 hash in the user configuration.
3. Validate with `sireno-relay config-check` and start `sireno-relay serve`.
4. Give the agent the loopback MCP endpoint and bearer token, never upstream credentials.

See the repository configuration and security guides for the current reference.
