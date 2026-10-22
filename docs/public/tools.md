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

### `read_attachment`

Arguments: `folder`, virtual message `uid`, and the attachment `attachment_id`
returned by `read_message`. This tool rechecks the message and attachment policy
before returning data. Images are returned as MCP image content for vision-capable
clients. PDFs return bounded embedded-text pages (up to eight pages and 64 KiB).
To inspect a scanned or visual PDF, pass `page` with `render_page: true`; the
tool returns that page as an MCP PNG image. Rendering requires `pdftoppm`.

For other allowed file types, the tool returns metadata and, when the operator
has configured `attachments.base_url`, a one-minute, single-use absolute download
URL. That URL is scoped to the exact attachment and must be treated as a secret.
It does not include the agent's bearer token.

### Attachment downloads

An allowed message read may include attachment descriptors with a relative
download URL. Fetch it through the authenticated MCP service with the same bearer
header; authorization and attachment policy are rechecked. Prefer
`read_attachment` when the client supports MCP image content or needs a scoped
absolute URL.

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
