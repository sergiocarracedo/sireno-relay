# Security boundaries and limitations

Sireno is an authorization proxy, not a sandbox. Its guarantees apply only while an agent has no alternate path to the upstream account or owner filesystem.

- Agent IMAP/SMTP/CLI access is local-only and authenticated separately from the provider.
- Provider passwords are read by bounded no-shell commands and never returned through agent interfaces or routine logs.
- The visible mailbox uses virtual IDs and counts. Upstream UIDs, `UIDNEXT`, hidden folders, hidden-message counts, and raw MIME are not exposed.
- Config reloads are checked on every operation. A changed selected IMAP view is closed rather than served from stale authorization.
- Content parsing, sanitization, and scanner errors fail closed.

V1 does not defend timing/resource side channels, mail fetched before policy change, compromised owner accounts, provider credentials exposed outside Sireno, or scanners missing unknown secrets. Heuristic scanners are a supplement to explicit deny rules, never a guarantee.

Avoid protocol debug logs: they can contain credentials and mail contents. Do not store raw email outside the upstream provider; current local state holds only virtual identifier metadata.
