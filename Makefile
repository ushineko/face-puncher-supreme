.PHONY: build test lint coverage clean setup

BINARY := fpsd
VERSION := 0.1.0
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/ushineko/face-puncher-supreme/internal/version
LDFLAGS := -ldflags "-X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(DATE)"

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/fpsd

test:
	go test -race -v ./...

lint:
	golangci-lint run ./...

coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

clean:
	rm -f $(BINARY) coverage.out coverage.html

setup:
	go mod download
	@echo "Dependencies downloaded"
