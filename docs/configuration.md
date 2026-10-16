# Configuration

Sireno has its own strict YAML configuration; it does **not** read Himalaya's TOML account file. Himalaya is nevertheless a required local prerequisite: Sireno checks that `himalaya` is in `PATH` for every command, so the owner has an installed, initialized email CLI alongside the relay. Install it from [Himalaya's installation guide](https://github.com/pimalaya/himalaya#installation), then run `himalaya` once to use its setup wizard.

## Create a configuration

1. Copy the shipped example into an owner-controlled directory.
2. Replace every example address and password command.
3. Restrict file access before validation.
4. Validate, then run the relay.

```sh
install -d -m 700 "$HOME/.config/sireno"
cp config.example.yml "$HOME/.config/sireno/config.yml"
chmod 600 "$HOME/.config/sireno/config.yml"

sireno-relay config-check --config "$HOME/.config/sireno/config.yml"
sireno-relay serve --config "$HOME/.config/sireno/config.yml"
```

Sireno rejects configurations that are not regular files, are readable by group/others, are not owned by the process user, contain duplicate/unknown keys, reference unset variables, or contain invalid policies. It creates `sireno-state.json` beside the configuration; keep that directory owner-only too.

## Update a configuration

Edit the same YAML file, preserve mode `0600`, and validate it before restarting or relying on the new rules:

```sh
${EDITOR:-vi} "$HOME/.config/sireno/config.yml"
chmod 600 "$HOME/.config/sireno/config.yml"
sireno-relay config-check --config "$HOME/.config/sireno/config.yml"
sireno-relay policy-check --config "$HOME/.config/sireno/config.yml" --user hermes-agent --folder INBOX/Invoices
```

The next operation reloads the file and uses the new policy immediately. If the replacement is invalid or unreadable, the operation fails closed until a valid owner-only configuration is restored. Keep a backup before changing a restrictive policy:

```sh
cp "$HOME/.config/sireno/config.yml" "$HOME/.config/sireno/config.yml.bak"
```

## Top-level fields

| Field | Required | Meaning |
| --- | --- | --- |
| `providers` | Yes | Named owner mail accounts that agents can use through profiles. |
| `profiles` | Yes | Named least-privilege policies, reusable by multiple agent users. |
| `users` | Yes | Agent relay logins; each binds one provider to one profile. |
| `listen.mcp` | No | Streamable HTTP MCP listener. Default while serving: `127.0.0.1:8080`; use a private container-network address for container deployment. |
| `logging` | No | JSON operational/audit logging configuration. |

All map keys are names you choose. Use stable, non-secret names such as `personal`, `invoice-bot`, and `invoices`.

## `logging`

Sireno writes JSON-lines audit logs. Omit this section (or use `target: stdout`) for safe operational events on standard output. Set `level` to `error`, `warn`, `info` (default), or `debug`.

```yaml
logging:
  level: debug
  target: /secure/log/sireno.jsonl
  rotation:
    retention_days: 7 # default
    max_file_size: 100MiB # default; also accepts a byte count
  mcp:
    capture_content: true
```

`mcp.capture_content` defaults to `false`. When enabled, Sireno records bounded MCP request and response JSON to the file target for owner-only debugging. It is rejected for `stdout`; bearer tokens, authorization headers, and attachment-download bytes are never logged. The target file is created with mode `0600`; Sireno refuses an existing target that is not an owner-only regular file. Rotated files are removed after the retention period.

## `providers.<name>`

| Field | Required | Meaning |
| --- | --- | --- |
| `address` | Yes | Account email address. The relay requires agent-submitted SMTP `From` to match it. |
| `imap.address` | Yes | TLS IMAP endpoint, usually `host:993`. |
| `imap.username` | Yes | Upstream IMAP login. |
| `imap.password` | Yes | Password value, usually `"{env:SIRENO_IMAP_PASSWORD}"`, or an argument array that prints exactly one password/app-password line. Arrays run without a shell. |
| `smtp.address` | Yes | TLS SMTP submission endpoint, usually `host:465`. |
| `smtp.username` | Yes | Upstream SMTP login. |
| `smtp.password` | Yes | Password value, usually `"{env:SIRENO_SMTP_PASSWORD}"`, or a no-shell command array. |

Use an environment variable for a direct value, for example `password: "{env:SIRENO_IMAP_PASSWORD}"`. Alternatively, `password: [pass, show, mail/personal-imap]` executes `pass show mail/personal-imap`. Command arrays never use a shell. V1 supports TLS IMAP/SMTP and password/app-password authentication only. It does not support OAuth, JMAP, POP3, or Gmail/Graph native APIs.

## `profiles.<name>`

| Field | Required | Meaning |
| --- | --- | --- |
| `folders.allow` / `folders.deny` | No | Folder access rules. Omitted means no folder is visible. |
| `addresses.allow` / `addresses.deny` | No | Incoming address visibility and outbound recipient rules. |
| `inbound` | No | Rules applied before mail is shown to an agent. |
| `outbound` | No | Rules applied before mail is submitted upstream. |

### Rule syntax

Every allow/deny item has `mode:pattern` syntax:

| Mode | Folder meaning | Address meaning |
| --- | --- | --- |
| `ro` | Read allowed mail | Allow reading incoming messages with this stored address. |
| `rw` | Read access; mutable folder operations are reserved for the supported safe surface | Allow incoming reads and outbound sends. |
| `wo` | Invalid | Allow outbound delivery only. |

Patterns are case-insensitive exact matches by default, accept `*` wildcards, or use `re:` plus a Go regular expression. Examples: `ro:INBOX/Invoices`, `ro:Sent/*`, `wo:accounting@example.org`, `ro:re:.*@trusted\\.example\\.org`.

Rules are default-deny. A matching deny always overrides allow. Folder permission is always needed for a read. If at least one read-capable address allow (`ro`/`rw`) is configured, at least one stored From/To/Cc/Bcc address must also match it. A stored denied address hides the whole message. Sireno warns on broad `ro:*` and `rw:*` folder permissions.

## Content rules: `inbound` and `outbound`

| Field | Required | Meaning |
| --- | --- | --- |
| `denied_subjects` | No | Exact/wildcard/`re:` subject patterns that hide inbound mail or reject outbound mail. |
| `denied_bodies` | No | Exact/wildcard/`re:` text-body patterns. |
| `remove_links` | No, default false | Replace visible `http`, `https`, and `www` links with `[link removed]`. |
| `scanner` | No | Fixed executable argument array for best-effort secret detection. |
| `attachments.allow` / `attachments.deny` | No | MIME types that may be accepted as attachments. Omitted `allow` means no attachment is allowed. |
| `attachments.mode` | No, default `remove` | Action for an attachment disallowed by the MIME policy: `reject`, `remove`, or `silent-remove`. |

A scanner reads bounded mail text on stdin and must emit exactly JSON `{"status":"clean"}` or `{"status":"finding"}` to stdout, then exit successfully. A finding, timeout, crash, malformed JSON, oversized output, or any other failure denies the mail. Gitleaks needs a wrapper that converts its result into this contract; do not configure Gitleaks directly. Define known recovery or secret subjects/bodies explicitly—scanners are not a completeness guarantee.

Sireno accepts bounded multipart mail with UTF-8/ASCII plain text or HTML converted to text. Attachments are default-deny in **both** directions: every attachment must match an `attachments.allow` pattern and no `attachments.deny` pattern. Patterns are MIME exact matches, `*` globs, or `re:` Go regular expressions; deny wins. Sireno requires the declared MIME type to match detected bytes and caps every attachment at 10 MiB (16 MiB total message size). It rejects malformed or uninspectable content fail-closed. Allowing a binary type permits an agent to inspect its bytes; Sireno does not OCR or semantically scan binary content.

`attachments.mode` controls a policy-denied attachment. `reject` hides the entire inbound email and rejects the entire outbound submission. `remove` removes the attachment and adds the fixed text `[One or more attachments were removed by Sireno security policy.]` to the visible or submitted body. `silent-remove` removes it without adding text. Omit the field to use `remove`.

Inbound message results include virtual attachment IDs and a relative `download_url`. Resolve it against the MCP endpoint and fetch it with the same `Authorization: Bearer <token>` header. Sireno rechecks the bearer token, message visibility, MIME policy, and virtual IDs for each download; it never places the bearer token in a URL. Attachment names and untrusted metadata are not exposed.

## `users.<name>`

| Field | Required | Meaning |
| --- | --- | --- |
| `provider` | Yes | Existing key in `providers`. |
| `profile` | Yes | Existing key in `profiles`. |
| `token_hash` | Yes | Lowercase SHA-256 hash of the opaque bearer token supplied to the agent. Sireno stores no plaintext agent token. |
| `tag_sent` | No, default `true` | Set `false` to opt out of Sireno adding trusted `X-Sireno-Agent: <name>` to outbound messages. Client-provided values are discarded. |

## Agent tokens

Create a high-entropy token outside the configuration, give it only to the agent, and write its SHA-256 hash under the user:

```sh
TOKEN="$(openssl rand -hex 32)"
printf %s "$TOKEN" | sha256sum
```

```yaml
users:
  hermes-agent:
    provider: personal
    profile: invoices
    token_hash: <lowercase-sha256-output>
```

The agent supplies the plaintext value as `Authorization: Bearer <token>`. Never put it in YAML, a command line, logs, or version control.

## Environment variables

String values can include `{env:NAME}`. Values are expanded before validation; an unset referenced variable is an error. Quote values beginning with `{` so YAML treats them as strings. An unset referenced variable is an error. Use this for deployment-specific names and endpoints, not for long-lived mail credentials:

```yaml
providers:
  personal:
    address: "{env:SIRENO_ACCOUNT_ADDRESS}"
    imap:
      address: "{env:SIRENO_IMAP_ENDPOINT}"
      username: "{env:SIRENO_ACCOUNT_ADDRESS}"
      password: "{env:SIRENO_IMAP_PASSWORD}"
```

Interpolation applies only to YAML string values. Keys, booleans, and numeric values are not interpolated.

## Complete example

`config.example.yml` is a runnable structural example. Copy it rather than editing it in place, and run `sireno-relay config-check` after every update.
