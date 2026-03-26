.PHONY: build install test lint clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o agmon ./cmd/agmon/

install: build
	cp agmon $(shell go env GOPATH)/bin/agmon
	codesign --sign - --force $(shell go env GOPATH)/bin/agmon 2>/dev/null || true

test:
	go test -cover ./...

lint:
	go vet ./...

clean:
	rm -f agmon
