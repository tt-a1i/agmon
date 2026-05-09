.PHONY: build install test lint clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o tokenmeter ./cmd/tokenmeter/

install: build
	cp tokenmeter $(shell go env GOPATH)/bin/tokenmeter
	codesign --sign - --force $(shell go env GOPATH)/bin/tokenmeter 2>/dev/null || true

test:
	go test -cover ./...

lint:
	go vet ./...

clean:
	rm -f tokenmeter
