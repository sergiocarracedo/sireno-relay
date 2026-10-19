---
title: Security model
description: What the MCP boundary protects, what it cannot protect, and how to deploy the relay safely.
---

# Security model

SirenoRELAY is an authorization and data-filtering boundary between an agent
and an upstream mailbox. It is not a sandbox for a compromised agent or a
replacement for operating-system isolation.

## Why use an MCP relay?

Without a relay, an agent integration may need the mailbox password or a
provider token. That credential can grant broad mailbox access and may be
copied into logs, prompts, tool configuration, or other processes. With
SirenoRELAY, the agent authenticates to the MCP endpoint using a separate,
revocable bearer token. SirenoRELAY owns the IMAP/SMTP credentials, performs the
upstream requests, enforces the selected policy, sanitizes the result, and
returns only permitted data.

The agent does not learn the real mailbox credentials from MCP calls. This
protection depends on deployment: an agent that can read the relay's config,
secret files, process environment, or memory can still steal upstream
credentials. Do not run the relay and an untrusted agent under the same
unisolated OS identity.

## Request path

1. The MCP client sends a request with a bearer token.
2. SirenoRELAY authenticates the token and binds it to one named user, provider,
   and profile.
3. The operation reloads current configuration and checks the relevant policy.
4. The upstream provider is called only for authorized data or an authorized
   send. Results are filtered and sanitized before they reach the MCP client.

This applies to folder listing, list/search, reads, attachment downloads,
message flags, and sends. The current tool surface is intentionally narrow; do
not assume that an operation not exposed as a tool is available.

## What is filtered

- **Folders:** only permitted folders are listed; hidden folders and their
  upstream identifiers are not exposed as ordinary MCP results.
- **Messages:** sender, subject, body, attachment, and scanner policies are
  applied before messages are returned.
- **Writes:** message sends are checked against recipient and content policies
  before upstream handoff. Writable folder permissions are required for
  supported mutations.
- **Identity and tokens:** each request is authenticated; token rotation and
  policy changes affect subsequent operations and invalidate dependent access
  such as cursors.

Policies are default-deny. Empty allow lists do not mean “allow all”. Deny
rules override allows. Review broad wildcards carefully.

## Recommended deployment

Prefer a dedicated service account that owns the configuration, secret files,
state, and audit logs. Keep the agent in a different OS account/container with
only the MCP endpoint and its individual bearer token. If using Docker, mount
configuration and secrets read-only into the relay container, keep them out of
the agent container, avoid privileged mode and unnecessary host mounts, and
bind/publish only the required loopback endpoint. A container is not a security
boundary if the agent has Docker socket or host administrator access.

For a service deployment, use a restricted systemd service account, set
`UMask=0077`, restrict filesystem access, and keep state/log directories
service-owned. The included unit example is a starting point and must be
reviewed for local paths and service hardening before installation.

The listener is loopback-only. Loopback limits network exposure; it does not
prevent other processes running as the same user from connecting or inspecting
files. If a separate user/container must reach the relay, use an explicitly
designed local transport or network boundary without making the endpoint
public.

## Limits and operational cautions

- SirenoRELAY cannot constrain an agent that already has upstream credentials,
  can access the relay's OS identity, or can bypass the MCP endpoint and reach
  the mail server directly.
- Mail content is untrusted input. Filtering and sanitization reduce exposure;
  they do not make email safe to follow as instructions.
- `serve --debug` emits debug-level operational metadata to stdout. MCP request
  and response content is never recorded.
- SMTP delivery can become uncertain if the connection fails after submission
  starts. Check the Sent folder before retrying manually.
- Use high-entropy tokens, one per agent. Store only their hashes in
  configuration, deliver raw tokens through a secret channel, and rotate a
  token immediately if exposed.

See [configuration](./configuration) and [MCP tools](./tools) for the full
policy and operation references. See the repository's operations guide for
service-specific runbook details.
