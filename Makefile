VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN := sireno-relay
PREFIX ?= $(HOME)/.local

.PHONY: build fmt-check test vet check hooks install

build:
	cd cli && go build -ldflags '$(LDFLAGS)' -o bin/$(BIN) .

fmt-check:
	@test -z "$$(gofmt -l $$(find cli -name '*.go' -type f -not -path './vendor/*'))" || (echo "Run gofmt on the files above."; exit 1)

test:
	cd cli && go test ./...

vet:
	cd cli && go vet ./...

check: fmt-check vet test

hooks:
	git config core.hooksPath .githooks

install:
	$(MAKE) build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 cli/bin/$(BIN) "$(DESTDIR)$(PREFIX)/bin/$(BIN)"
