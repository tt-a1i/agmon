.PHONY: build install clean

build:
	go build -o agmon ./cmd/agmon/

install: build
	cp agmon $(shell go env GOPATH)/bin/agmon

clean:
	rm -f agmon
