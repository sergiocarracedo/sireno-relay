# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS relay-build
WORKDIR /src/cli

COPY cli/go.mod cli/go.sum ./
RUN go mod download

COPY cli/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/sirenorelay .

FROM rust:1.92-bookworm AS himalaya-build
ARG HIMALAYA_REF=v2.2.1
RUN cargo install --git https://github.com/pimalaya/himalaya.git --tag "$HIMALAYA_REF" --locked \
    --no-default-features --features imap,smtp,rustls-ring --root /out

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install --no-install-recommends -y ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 sirenorelay \
    && useradd --uid 10001 --gid sirenorelay --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin sirenorelay

COPY --from=relay-build /out/sirenorelay /usr/local/bin/sirenorelay
COPY --from=himalaya-build /out/bin/himalaya /usr/local/bin/himalaya

USER 10001:10001
ENTRYPOINT ["/usr/local/bin/sirenorelay"]
CMD ["serve", "--config", "/var/lib/sirenorelay/config.yml"]
