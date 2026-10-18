---
title: Configuration
---

# Configuration

Define reusable `providers`, `profiles`, and bearer-token-authenticated `users`
in an owner-only YAML file. Store only a lowercase SHA-256 token hash:

```yaml
users:
  agent:
    provider: personal
    profile: invoices
    token_hash: <lowercase-sha256>
```

Use mode `0600`; validate with `sireno-relay config-check --config config.yml`.
