# Configuration

SirenoRELAY has its own strict YAML configuration; it does **not** read Himalaya's TOML account file. Himalaya is nevertheless required locally. Install it from [Himalaya's installation guide](https://github.com/pimalaya/himalaya#installation).

## Create a configuration

For an interactive IMAP/SMTP setup, run:

```sh
sirenorelay onboard
```

It uses the provider name `default`, connects to the account, lists folders for keyboard multiple selection (↑/↓ to move, Space to select, Enter to continue), creates a read-only default profile, asks separately for allowed inbound senders and outbound recipients, and displays a bearer token once. If listing folders fails, it lets you correct the provider settings and try again. It stores its completion marker at `~/.config/sirenorelay/onboard`, config at `~/.config/sirenorelay/config.yml`, and owner-only upstream secret files below that directory. It will not overwrite an existing configuration. The wizard supports only IMAP/SMTP; use manual YAML for another Himalaya backend.

Without `--config`, SirenoRELAY uses `~/.config/sirenorelay/config.yml`. If
that file does not exist, it uses an existing legacy
`~/.config/sireno-relay/config.yml`. An explicit `--config` always wins.

For a manual setup:

```sh
   install -d -m 700 "$HOME/.config/sirenorelay"
   cp config.example.yml "$HOME/.config/sirenorelay/config.yml"
   chmod 600 "$HOME/.config/sirenorelay/config.yml"

   sirenorelay config-check --config "$HOME/.config/sirenorelay/config.yml"
   sirenorelay serve --config "$HOME/.config/sirenorelay/config.yml"
```

SirenoRELAY rejects configurations that are not regular files, are readable by group/others, are not owned by the process user, contain duplicate/unknown keys, reference unset variables, or contain invalid policies. It creates `sireno-state.json` beside the configuration; keep that directory owner-only too.

## Update a configuration

Edit the same YAML file, preserve mode `0600`, and validate it before restarting or relying on the new rules:

```sh
${EDITOR:-vi} "$HOME/.config/sirenorelay/config.yml"
chmod 600 "$HOME/.config/sirenorelay/config.yml"
sirenorelay config-check --config "$HOME/.config/sirenorelay/config.yml"
sirenorelay policy-check --config "$HOME/.config/sirenorelay/config.yml" --user hermes-agent --live --folder INBOX/Invoices
sirenorelay policy-check --config "$HOME/.config/sirenorelay/config.yml" --user hermes-agent --inbound --folder INBOX/Invoices --sender invoices@example.org --body "Invoice attached"
```

The next operation reloads the file and uses the new policy immediately. If the replacement is invalid or unreadable, the operation fails closed until a valid owner-only configuration is restored. Keep a backup before changing a restrictive policy:

```sh
cp "$HOME/.config/sirenorelay/config.yml" "$HOME/.config/sirenorelay/config.yml.bak"
```

## Top-level fields

| Field | Required | Meaning |
| --- | --- | --- |
| `providers` | Yes | Named owner mail accounts that agents can use through profiles. |
| `profiles` | Yes | Named least-privilege policies, reusable by multiple agent users. |
| `users` | Yes | Agent bearer-token bindings; each binds one provider to one profile. |
| `listen.mcp` | No | Streamable HTTP MCP listener. Default while serving: `127.0.0.1:8080`; SirenoRELAY accepts loopback bindings only. |
| `logging` | No | JSON operational/audit logging configuration. |

All map keys are names you choose. Use stable, non-secret names such as `personal`, `invoice-bot`, and `invoices`.

## `logging`

SirenoRELAY writes JSON-lines audit logs. Omit this section (or use `target: stdout`) for safe operational events on standard output. Set `level` to `error`, `warn`, `info` (default), or `debug`.

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

`mcp.capture_content` is retained for configuration compatibility but never records request or response content. Audit output contains bounded operational metadata such as the MCP method, tool, status, duration, and safe response summaries: visible folder names/counts, or message and attachment counts. For upstream failures, it records a sanitized `provider_failed` outcome with stable `provider_operation` and `failure_reason` fields (for example, `envelope_listing` and `envelope_listing_failed`), never the provider error text. It never records bearer tokens, credentials, mail subjects, addresses, bodies, attachment names, or attachment bytes. The target file is created with mode `0600`; SirenoRELAY refuses an existing target that is not an owner-only regular file. Rotated files are removed after the retention period.

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

Use an environment variable for a direct value, for example `password: "{env:SIRENO_IMAP_PASSWORD}"`. Alternatively, `password: [pass, show, mail/personal-imap]` executes `pass show mail/personal-imap`. Command arrays never use a shell and run with the configuration file's directory as their working directory, so `password: [cat, ./secrets/imap]` is relative to `config.yml`. V1 supports TLS IMAP/SMTP and password/app-password authentication only. It does not support OAuth, JMAP, POP3, or Gmail/Graph native APIs.

## `profiles.<name>`

| Field | Required | Meaning |
| --- | --- | --- |
| `folders.allow` / `folders.deny` | No | Folder access rules. Omitted means no folder is visible. |
| `inbound.addresses.allow` / `.deny` | No | Allowed and denied incoming `From` senders. |
| `outbound.addresses.allow` / `.deny` | No | Allowed and denied outbound recipients. |
| `outbound.signature` | No | Text appended to sanitized outbound mail. |

### Rule syntax

Folder allow/deny items have `mode:pattern` syntax:

| Mode | Folder meaning |
| --- | --- |
| `ro` | Read allowed mail. |
| `rw` | Read access; mutable folder operations are reserved for the supported safe surface. |
| `wo` | Invalid. |

Patterns are case-insensitive exact matches by default, accept `*` wildcards, or use `re:` plus a Go regular expression. Folder examples: `ro:INBOX/Invoices`, `ro:Sent/*`. Address patterns under `inbound.addresses` and `outbound.addresses` have no mode prefix.

Rules are default-deny. A matching deny always overrides allow. Folder permission is always needed for a read. When inbound allow rules exist, every stored `From` sender must match; an unallowed or denied sender hides the message. Outbound rules apply to every envelope and declared `To`/`Cc`/`Bcc` recipient. `*` is an explicit broad grant. SirenoRELAY warns on broad `ro:*` and `rw:*` folder permissions.

## Content rules: `inbound` and `outbound`

| Field | Required | Meaning |
| --- | --- | --- |
| `denied_subjects` | No | Exact/wildcard/`re:` subject patterns that hide inbound mail or reject outbound mail. |
| `denied_bodies` | No | Exact/wildcard/`re:` text-body patterns. |
| `remove_links` | No, default false | Replace visible `http`, `https`, and `www` links with `[link removed]`. |
| `scanner` | No | Fixed executable argument array for best-effort secret detection. |
| `attachments.allow` / `attachments.deny` | No | MIME types that may be accepted as attachments. Omitted `allow` means no attachment is allowed. |
| `attachments.mode` | No, default `remove` | Action for an attachment disallowed by the MIME policy: `reject`, `remove`, or `silent-remove`. |
| `signature` | Outbound only | Optional text appended after sanitized message text. |

A scanner reads bounded mail text on stdin and must emit exactly JSON `{"status":"clean"}` or `{"status":"finding"}` to stdout, then exit successfully. A finding, timeout, crash, malformed JSON, oversized output, or any other failure denies the mail. Gitleaks needs a wrapper that converts its result into this contract; do not configure Gitleaks directly. Define known recovery or secret subjects/bodies explicitly—scanners are not a completeness guarantee.

SirenoRELAY accepts bounded multipart mail with UTF-8/ASCII plain text or HTML converted to text. Attachments are default-deny in **both** directions: every attachment must match an `attachments.allow` pattern and no `attachments.deny` pattern. Patterns are MIME exact matches, `*` globs, or `re:` Go regular expressions; deny wins. SirenoRELAY requires the declared MIME type to match detected bytes and caps every attachment at 10 MiB (16 MiB total message size). It rejects malformed or uninspectable content fail-closed. Allowing a binary type permits an agent to inspect its bytes; SirenoRELAY does not OCR or semantically scan binary content.

`attachments.mode` controls a policy-denied attachment. `reject` hides the entire inbound email and rejects the entire outbound submission. `remove` removes the attachment and adds the fixed text `[One or more attachments were removed by Sireno security policy.]` to the visible or submitted body. `silent-remove` removes it without adding text. Omit the field to use `remove`.

Inbound message results include virtual attachment IDs and a relative `download_url`. Resolve it against the MCP endpoint and fetch it with the same `Authorization: Bearer <token>` header. SirenoRELAY rechecks the bearer token, message visibility, MIME policy, and virtual IDs for each download; it never places the bearer token in a URL.

`attachments.base_url` is an optional HTTP(S) origin used by `read_attachment` for allowed file formats that cannot be returned directly through MCP. It must contain only a scheme, host, and optional port—for example `http://local-sirenorelay:8081`. The tool returns a one-minute, single-use URL scoped to exactly one attachment. Treat it as a secret: it authorizes that attachment without the normal bearer header during its brief lifetime. The Home Assistant App automatically supplies its internal App hostname and MCP bridge port when this setting is absent; an explicit config value takes precedence.

## `users.<name>`

| Field | Required | Meaning |
| --- | --- | --- |
| `provider` | Yes | Existing key in `providers`. |
| `profile` | Yes | Existing key in `profiles`. |
| `token_hash` | Yes | Lowercase SHA-256 hash of the opaque bearer token supplied to the agent. SirenoRELAY stores no plaintext agent token. |
| `tag_sent` | No, default `true` | Set `false` to opt out of SirenoRELAY adding trusted `X-Sireno-Agent: <name>` to outbound messages. Client-provided values are discarded. |

## Agent tokens

`onboard` creates and reveals a high-entropy token once. To replace a token later without changing its provider or profile binding, use:

```sh
sirenorelay user token rotate --user hermes-agent --config config.yml
```

For manual setup, create a high-entropy token outside the configuration, give it only to the agent, and write its SHA-256 hash under the user:

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

`config.example.yml` is a runnable structural example. Copy it rather than editing it in place, and run `sirenorelay config-check` after every update.
