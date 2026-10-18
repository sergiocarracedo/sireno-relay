---
title: Configuration
---

# Configuration

Start with `sireno-relay onboard` for an interactive IMAP/SMTP setup. It lists
live folders for multiple selection, creates default-deny inbound sender and
outbound recipient rules, and shows the bearer token once. It writes
`~/.config/sireno-relay/config.yml` and an owner-only completion marker at
`~/.config/sireno-relay/onboard`.

For manual configuration, define reusable `providers`, `profiles`, and
bearer-token-authenticated `users` in an owner-only YAML file. Store only a
lowercase SHA-256 token hash:

```yaml
users:
  agent:
    provider: personal
    profile: invoices
    token_hash: <lowercase-sha256>
```

Use mode `0600`; validate with `sireno-relay config-check --config config.yml`.
Folder rules use `ro:` or `rw:` prefixes. Address rules are unprefixed:
`inbound.addresses` authorizes `From` senders and `outbound.addresses`
authorizes recipients. `*` is a deliberate broad grant. Add optional
`outbound.signature` to append text to outgoing mail. Rotate a bearer token
with `sireno-relay user token rotate --user agent --config config.yml`.
