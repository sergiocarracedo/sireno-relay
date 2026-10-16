VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN := sireno-relay
PREFIX ?= $(HOME)/.local

.PHONY: build test vet check install

build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/sireno

test:
	go test ./...

vet:
	go vet ./...

check: test vet

install:
	$(MAKE) build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 bin/$(BIN) "$(DESTDIR)$(PREFIX)/bin/$(BIN)"
