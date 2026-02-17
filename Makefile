.PHONY: build build-ui build-go copy-readme test lint coverage clean setup install-lint install uninstall

BINARY := fpsd
VERSION := 1.3.3
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/ushineko/face-puncher-supreme/internal/version
LDFLAGS := -ldflags "-X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(DATE)"

BINDIR := $(shell go env GOPATH)
LINT_NAME := golangci-lint
LINT_VERSION := v2.9.0
LINT_PROGRAM := $(LINT_NAME)-$(LINT_VERSION)

copy-readme:
	cp README.md web/readme.md

build-ui: copy-readme
	cd web/ui && npm ci && npx vite build

build: build-ui
	go build $(LDFLAGS) -o $(BINARY) ./cmd/fpsd

build-go: copy-readme
	go build $(LDFLAGS) -o $(BINARY) ./cmd/fpsd

test:
	go test -race -short -v ./...

install-lint: $(BINDIR)/bin/$(LINT_PROGRAM)

$(BINDIR)/bin/$(LINT_PROGRAM):
	@echo "Installing $(LINT_PROGRAM) ..."
	@curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(BINDIR)/bin" $(LINT_VERSION)
	@mv -v $(BINDIR)/bin/$(LINT_NAME) $(BINDIR)/bin/$(LINT_PROGRAM)

lint: install-lint
	$(BINDIR)/bin/$(LINT_PROGRAM) run --timeout 5m0s ./...

coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

clean:
	rm -f $(BINARY) coverage.out coverage.html
	rm -rf web/ui/dist web/ui/node_modules

install: build
	./scripts/fps-ctl install

uninstall:
	./scripts/fps-ctl uninstall

setup: install-lint
	go mod download
	@echo "Dependencies downloaded"
