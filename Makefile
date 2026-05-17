.PHONY: build install test lint vet vuln ci coverage clean flaky race

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o tm ./cmd/tm/

install: build
	cp tm $(shell go env GOPATH)/bin/tm
	codesign --sign - --force $(shell go env GOPATH)/bin/tm 2>/dev/null || true

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
	rm -f tm tokenmeter

flaky:
	ROUNDS=20 bash scripts/find_flaky.sh

race:
	go test ./... -race -count=5 -timeout=120s
