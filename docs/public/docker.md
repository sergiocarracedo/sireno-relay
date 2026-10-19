---
title: Docker deployment
description: Run the relay and Himalaya in a non-root Linux container without exposing the MCP listener publicly.
---

# Docker deployment

The supplied `Dockerfile` builds a Linux image with SirenoRELAY and a pinned
Himalaya release. It runs `sirenorelay serve` as a non-root user by default.
Docker replaces the built-in `sirenorelay service` command; do not run that
command in the container.

## Build

From the repository root:

```sh
docker build --tag sirenorelay:local .
```

The image pins Himalaya to `v2.2.1`. To evaluate a newer compatible release,
build explicitly with `--build-arg HIMALAYA_REF=vX.Y.Z`, then validate the image
with `sirenorelay config-check` and your normal policy tests before deployment.

## Prepare owner files

The relay requires an owner-owned `0600` configuration file and an owner-only
parent directory. It writes `sireno-state.json` beside the configuration, so
the directory must remain writable. Mount the configuration file a second time
as read-only to prevent the relay process from changing it.

```sh
export SIRENO_CONFIG_DIR="$HOME/.config/sirenorelay"
export SIRENO_UID="$(id -u)"
export SIRENO_GID="$(id -g)"

chmod 700 "$SIRENO_CONFIG_DIR"
chmod 600 "$SIRENO_CONFIG_DIR/config.yml"

umask 077
cat > "$SIRENO_CONFIG_DIR/relay.env" <<'EOF'
SIRENO_IMAP_PASSWORD=replace-with-an-app-password
SIRENO_SMTP_PASSWORD=replace-with-an-app-password
EOF
```

`relay.env` is a host-side secret file: keep it owner-readable only, never add
it to version control, and never mount it into an agent container. Instead of
environment values, the configuration may use a fixed no-shell command that
reads a container secret; see the [configuration reference](./configuration).

## Run with an agent on the Linux host

On Linux, host networking preserves the relay's required loopback listener.
The host agent can connect to `http://127.0.0.1:8080/mcp`; Docker does not
publish a port.

```sh
docker run --detach --name sirenorelay \
  --network host \
  --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --user "$SIRENO_UID:$SIRENO_GID" \
  --env-file "$SIRENO_CONFIG_DIR/relay.env" \
  --mount "type=bind,src=$SIRENO_CONFIG_DIR,dst=/var/lib/sirenorelay" \
  --mount "type=bind,src=$SIRENO_CONFIG_DIR/config.yml,dst=/var/lib/sirenorelay/config.yml,readonly" \
  sirenorelay:local
```

`--user` is required when bind-mounting files created by the host owner:
SirenoRELAY rejects a configuration owned by a different UID. Check startup and
stop the relay with:

```sh
docker logs sirenorelay
docker stop sirenorelay
```

Host networking is a Linux deployment pattern. Do not use `-p` as a substitute:
the relay deliberately binds only its container's loopback interface, so a
published bridge-network port cannot reach it.

## Run with an agent container

Keep the relay on Docker's default network and run the agent in the relay's
network namespace. Both containers then share loopback, while only the relay
receives configuration and credential mounts:

```sh
docker run --detach --name sirenorelay \
  --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --user "$SIRENO_UID:$SIRENO_GID" \
  --env-file "$SIRENO_CONFIG_DIR/relay.env" \
  --mount "type=bind,src=$SIRENO_CONFIG_DIR,dst=/var/lib/sirenorelay" \
  --mount "type=bind,src=$SIRENO_CONFIG_DIR/config.yml,dst=/var/lib/sirenorelay/config.yml,readonly" \
  sirenorelay:local

docker run --rm --network container:sirenorelay your-agent-image
```

Configure `your-agent-image` with the MCP URL
`http://127.0.0.1:8080/mcp` and only its assigned bearer token. Do not mount the
owner directory, `relay.env`, or `/var/run/docker.sock` into that container.

## Operational limits

The container image supports serving an existing configuration; interactive
`onboard` needs a terminal and is best run on the owner host before deployment.
The image has no HTTP health endpoint, so monitor the container process and
operational logs rather than inventing a public listener. See the
[security model](./security) before putting the relay beside an untrusted agent.
