---
title: MCP tools
description: What each exposed mail tool accepts, returns, and checks before operating upstream.
---

# MCP tool reference

The Streamable HTTP endpoint is `/mcp`. Every request must include the
`Authorization: Bearer TOKEN` header. A token resolves to one configured user,
provider, and profile; the client cannot select another identity in a tool
argument. The response contains only data allowed by the current policy.

## Read and search

### `list_folders`

Takes no arguments. Returns the names of folders visible under the user's
folder rules. Hidden folders are omitted. Call this first and use a returned
folder name for later operations.

### `list_messages`

Arguments: `folder` (visible folder), optional `query`, and optional
`cursor`. Returns at most 20 sanitized message envelopes plus `next_cursor`
when more results are available. The cursor is opaque and bound to the current
identity, folder, query, policy, and configuration. It can expire after a
configuration or policy change. Do not manufacture or share cursors.

### `search_messages`

Uses the same folder, query, and cursor shape as `list_messages`, but requires a
non-empty query. Query syntax follows the configured Himalaya backend. Search
results still pass the same visibility and content policy filters; search is
not a way to enumerate hidden messages.

### `read_message`

Arguments: `folder` and one virtual `uid` returned by a list/search operation.
Returns sanitized message content and attachment descriptors, if allowed.
Upstream message identifiers are not a substitute for the returned virtual UID.

### `read_messages`

Arguments: `folder` and one to 20 virtual `uids`. Returns the permitted
sanitized messages. A UID must come from the current visible mailbox view.

### Attachment downloads

An allowed read may include attachment descriptors with a download URL. Fetch
that URL through the authenticated MCP service; authorization and attachment
policy are rechecked. Attachment bytes are not included directly in the normal
message result.

## Writes

### `send_message`

Arguments: `from`, one or more `to` addresses, `subject`, and plain-text `body`.
Attachments are not supported. `from` must be the configured provider address.
Every recipient and the message content are checked against outbound policy
before SMTP handoff. A denied recipient returns an error and is not delivered;
correct the recipient and submit a new request. The tool reports success only
after the provider operation succeeds, though a network failure during SMTP
submission can make delivery uncertain.

### `update_message_flags`

Arguments: `folder`, virtual `uid`, `flags`, and `action`. The only flags are
`seen`, `flagged`, and `answered`; action is `add` or `remove`. The folder must
have writable access. This is the only currently exposed message mutation.

## Important limits

SirenoRELAY does not currently expose move, copy, delete, append, or folder
mutation tools. Do not infer that upstream support makes an operation available
through MCP. All tools authenticate each HTTP request and re-check current
identity and policy. For the broader boundary and deployment requirements, see
the [security model](./security) and [configuration reference](./configuration).
