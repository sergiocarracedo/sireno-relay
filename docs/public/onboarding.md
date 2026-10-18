---
title: Onboarding
---

# Onboarding

Run `sireno-relay onboard` from an owner-controlled interactive terminal.
It supports IMAP/SMTP accounts, uses the provider name `default`, asks for the
account and upstream credentials, verifies them by listing live folders, and
lets you select multiple folders for the agent's read-only view with ↑/↓,
Space, and Enter. A failed folder listing returns to the provider settings so
you can correct them.

The wizard then asks for permitted inbound `From` senders and outbound
recipients. Leaving either list blank denies that direction; selecting `*`
requires confirmation because it authorizes every sender or recipient.

On success it stores an owner-only configuration at
`~/.config/sireno-relay/config.yml`, upstream secret files under that directory,
and a non-secret completion marker at `~/.config/sireno-relay/onboard`. It
prints the generated bearer token once. Rotate it later with:

```sh
sireno-relay user token rotate --user agent
```

For other Himalaya backends or non-interactive deployment, use the manual
configuration reference instead.
