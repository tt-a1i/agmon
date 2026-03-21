.PHONY: build install test lint clean

build:
	go build -o agmon ./cmd/agmon/

install: build
	cp agmon $(shell go env GOPATH)/bin/agmon

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -f agmon
