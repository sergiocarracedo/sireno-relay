---
date: 2026-10-16
topic: public-documentation-and-release
---

# Public Documentation and Release

## Summary

Prepare Sireno for a public release after full MCP mail parity. Ship a concise, shared-Markdown documentation set, a feature-led Astro site on GitHub Pages, secure owner token-management commands, and Linux/macOS releases with Homebrew distribution.

---

## Problem Frame

The existing user-facing documentation describes a local IMAP/SMTP relay even though Sireno is moving to a token-authenticated MCP boundary. It is inconsistent, omits current configuration options, and cannot safely guide an owner through provisioning an agent. Distribution and a public product surface are also missing.

---

## Actors

- A1. Owner: configures providers and policy, provisions agent tokens, operates the relay, and reviews audit logs.
- A2. Agent operator: gives an agent its MCP endpoint and bearer token without access to owner configuration or provider credentials.
- A3. Agent: accesses only the mail resources and operations the owner policy permits.
- A4. Maintainer: publishes verified releases, documentation, and website updates.

---

## Key Flows

- F1. **Provision an agent**
  - **Trigger:** An owner needs to authorize a new agent.
  - **Actors:** A1, A2
  - **Steps:** The owner adds or edits the user binding, generates or rotates its token, receives the raw value once on an interactive terminal, and gives it to the agent operator through a secret channel.
  - **Outcome:** YAML stores only the token verifier; the agent receives no provider credential.
  - **Covered by:** R4, R5, R6

- F2. **Choose an installation path**
  - **Trigger:** An owner adopts Sireno.
  - **Actors:** A1, A4
  - **Steps:** The owner follows either a local or private-Docker quick start, validates configuration, starts the MCP relay, and connects an agent.
  - **Outcome:** The agent can use the documented MCP tools under the configured policy.
  - **Covered by:** R1, R2, R7, R8

- F3. **Publish a release**
  - **Trigger:** A maintainer creates a release tag after the release gate passes.
  - **Actors:** A4
  - **Steps:** Automation builds supported binaries, publishes checksums and release notes, updates Homebrew, and deploys the website.
  - **Outcome:** Owners can install a verified release on Linux or macOS and reach matching documentation.
  - **Covered by:** R9, R10

---

## Requirements

**Release gate and product claims**
- R1. Public documentation and the website must describe only verified, shipped behavior.
- R2. The first public release requires full MCP mail parity: policy-filtered reads and search, attachments, sending, and safe mailbox/folder write operations.
- R3. Documentation must state Sireno's security boundary accurately: it constrains access through its MCP interface but cannot protect mail if an agent also has provider credentials or a direct upstream path.

**Documentation**
- R4. Replace stale user-facing documentation with concise documentation that explains Sireno's goal, threat model, MCP isolation model, provider/profile/user bindings, bearer-token authentication, and default-deny policy.
- R5. Provide a complete configuration reference covering every supported field, default, accepted value, and policy behavior, including `ro`, `rw`, `wo`, exact/glob/regex matching, allow/deny precedence, attachments, listener, logging, and environment references.
- R6. Provide a concise CLI reference covering owner operations, user/token lifecycle commands, configuration validation, serving, audit logging, and shell completion.
- R7. Provide equally complete local and private-Docker getting-started paths.
- R8. Maintain documentation in shared Markdown used by the repository and Astro site; keep the README as a short overview and entry point.

**User and token lifecycle**
- R9. Provide owner-only add, edit, and token-rotation user commands. Token generation must use high entropy, persist only a verifier through an atomic configuration replacement, and reveal the raw token once on an interactive owner terminal.
- R10. Editing a user’s provider/profile binding must not rotate its token implicitly.

**Distribution and website**
- R11. Publish Linux and macOS binaries for amd64 and arm64, release checksums, and a Homebrew formula through tag-driven GitHub release automation.
- R12. Publish an Astro feature-led landing page and the shared documentation on GitHub Pages.

---

## Acceptance Examples

- AE1. **Covers R3, R4.** Given an owner reading the security overview, when they configure an agent, the guide makes clear that the token authorizes only Sireno and that provider credentials must remain unavailable to the agent.
- AE2. **Covers R5.** Given a policy author deciding whether to permit a folder or attachment, when they consult the configuration reference, they can determine the default behavior, valid pattern syntax, and deny precedence without reading source code.
- AE3. **Covers R9, R10.** Given an owner running a user-token command in an interactive terminal, when Sireno generates or rotates a token, it displays the raw token once and saves only its hash; editing the same user does not invalidate the token.
- AE4. **Covers R7.** Given a new owner, when they follow either quick start, they can validate configuration and connect an MCP client without adapting an unrelated setup path.

---

## Success Criteria

- A new owner can install, configure, provision, and connect an agent using only published documentation.
- The public site and README make no unsupported protocol, tool, or security claim.
- A tagged release produces installable Linux/macOS artifacts, checksums, and a working Homebrew formula.

---

## Scope Boundaries

### Deferred for later

- Windows release artifacts and package-manager support beyond Homebrew.
- Documentation for features not yet released, including future search predicates or provider backends.

### Outside this product's identity

- Giving agents provider credentials, direct upstream networking, or unrestricted mail-client access.
- Treating audit logs as a safe place for provider credentials, bearer tokens, or attachment bytes.

---

## Key Decisions

- Full MCP parity before public launch: documentation must not market unfinished capabilities.
- Shared Markdown is canonical: repository and website docs evolve together.
- Generated, terminal-only tokens: minimizes token exposure while retaining a usable owner workflow.
- GitHub Pages and Homebrew: low-friction public distribution using a familiar release pattern.

---

## Dependencies / Assumptions

- Safe mailbox/folder mutation and paginated Himalaya-backed search are completed before the release gate.
- The Homebrew tap and GitHub Pages repository settings are available to the maintainer.
- Local and Docker deployment guidance can accurately describe their different isolation guarantees.
