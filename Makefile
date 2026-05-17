.PHONY: build install test lint vet vuln ci coverage clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o tokenmeter ./cmd/tokenmeter/

install: build
	cp tokenmeter $(shell go env GOPATH)/bin/tokenmeter
	codesign --sign - --force $(shell go env GOPATH)/bin/tokenmeter 2>/dev/null || true

test:
	go test -cover ./...

vet:
	go vet ./...

lint:
	golangci-lint run --timeout=5m

vuln:
	govulncheck ./...

ci: vet lint test
	@echo "CI checks passed"

coverage:
	go test ./... -coverprofile=coverage.out -covermode=atomic -timeout=180s
	go tool cover -func=coverage.out | tail -20

clean:
	rm -f tokenmeter
