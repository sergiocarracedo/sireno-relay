# Policy reference

SirenoRELAY starts from deny-all. Folder rules use `ro:pattern` or `rw:pattern`.

| Target | `ro` | `rw` | `wo` |
| --- | --- | --- | --- |
| Folder | Read visible mail | Read plus safe folder-management surface | Invalid |
| Address | Not used | Not used | Not used |

`inbound.addresses` and `outbound.addresses` contain unprefixed address patterns. `re:` starts a Go regular expression; otherwise `*` is a case-insensitive wildcard. Deny rules override allows.

A message is readable only if its folder is readable and every stored `From` sender is allowed by `inbound.addresses`. A recipient never authorizes an unallowed sender. Any denied or unallowed sender hides the message.

For sends, every SMTP envelope recipient and every declared To/Cc/Bcc address must independently be allowed by `outbound.addresses`. `outbound.signature`, when set, is appended to the sanitized outbound body.

Content checks run separately for inbound and outbound mail. Subject and body patterns deny matching mail. `remove_links` produces a sanitized message. Attachments are default-deny in both directions; `inbound.attachments` and `outbound.attachments` explicitly allow MIME types using exact, glob, or `re:` patterns, with deny winning. `attachments.mode` defaults to `remove`; use `reject` to hide/reject the entire email, or `silent-remove` to remove the attachment without the standard removal notice. SirenoRELAY verifies declared MIME against detected bytes and fails closed for malformed or uninspectable MIME.
