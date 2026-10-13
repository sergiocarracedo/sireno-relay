# Configuration

Sireno reads strict YAML; unknown and duplicate keys fail validation. The owner config file and generated `sireno-state.json` must be readable only by the owner account.

Use `config.example.yml` as the complete reference. A provider has one IMAP and SMTP endpoint, each with an address, login, and an argument-array password command. Commands run without a shell and their output must be one line. V1 uses TLS endpoints such as port 993/465; password/app-password authentication is supported, not OAuth.

Each user maps exactly one provider and one profile. Its `password_command` is the credential presented by the agent to Sireno—not the provider password. Users may opt out of the trusted `X-Sireno-Agent` header with `tag_sent: false`.

Listener addresses must be loopback (`127.0.0.1`, `::1`, or `localhost`). If omitted, they default to `127.0.0.1:1143` and `127.0.0.1:1025` when serving.

Validate before starting:

```sh
sireno config-check --config config.yml
```

Fix every validation error and review warnings before granting an agent access.
