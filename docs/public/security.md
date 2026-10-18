---
title: Security model
---

# Security model

Sireno is an authorization boundary, not a sandbox. Agents use a bearer token
for one provider and one policy, never upstream credentials. Policies are
default-deny and rechecked for every request. Hidden folders, upstream IDs,
credentials, and attachment bytes are not exposed through normal MCP results.

The MCP listener is loopback-only. Token rotation and policy changes invalidate
subsequent cursor, read, send, and attachment access.
