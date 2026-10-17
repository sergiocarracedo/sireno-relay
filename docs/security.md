# Security boundaries and limitations

Sireno is an authorization proxy, not a sandbox. Its guarantees apply only while an agent has no alternate path to the upstream account or owner filesystem.

- Agents use a loopback Streamable HTTP MCP endpoint and an opaque bearer token; there is no agent IMAP/SMTP or direct-mail CLI path.
- Provider passwords are read by bounded no-shell commands and never returned through agent interfaces or routine logs.
- The visible mailbox uses virtual IDs. Upstream identifiers, mailbox generations, hidden folders, hidden-message counts, and raw MIME are not exposed.
- Config reloads are checked on every operation. A changed provider generation or rotated bearer token closes the selected view rather than serving stale authorization.
- Content parsing, sanitization, and scanner errors fail closed.

V1 does not defend timing/resource side channels, mail fetched before policy change, compromised owner accounts, provider credentials exposed outside Sireno, or scanners missing unknown secrets. Heuristic scanners are a supplement to explicit deny rules, never a guarantee.

Avoid protocol debug logs: they can contain credentials and mail contents. Do not store raw email outside the upstream provider; current local state holds only virtual identifier metadata.
