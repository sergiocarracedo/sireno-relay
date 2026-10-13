# Policy reference

Sireno starts from deny-all. A rule is `ro:pattern`, `rw:pattern`, or `wo:pattern`.

| Target | `ro` | `rw` | `wo` |
| --- | --- | --- | --- |
| Folder | Read visible mail | Read plus safe folder-management surface | Invalid |
| Address | Read a received message containing it | Read a received message containing it | Send only |

`re:` starts a Go regular expression; otherwise `*` is a case-insensitive wildcard. Deny rules override allows. Use specific patterns; broad `ro:*`/`rw:*` grants are warned about.

A message is readable only if its folder is readable and—when address allow rules exist—at least one stored From/To/Cc/Bcc address is readable. Any denied stored address hides the message. A missing Bcc header cannot be treated as a match.

For sends, every SMTP envelope recipient and every declared To/Cc/Bcc address must independently have `wo` or `rw` permission. An empty or mixed-permission recipient list is rejected without upstream delivery.

Content checks run separately for inbound and outbound mail. Subject and body patterns deny matching mail. `remove_links` produces a sanitized message. V1 fails closed for unsupported MIME and attachments, so attachment extension/size rules are reserved for the next supported MIME surface.
