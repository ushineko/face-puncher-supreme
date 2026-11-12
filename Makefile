.PHONY: build build-ui build-go copy-readme test lint coverage clean setup install-lint install uninstall

BINARY := fpsd
VERSION := 1.5.3
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/ushineko/face-puncher-supreme/internal/version
LDFLAGS := -ldflags "-X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(DATE)"

BINDIR := $(shell go env GOPATH)
LINT_NAME ?= golangci-lint
LINT_VERSION ?= v2.12.2
LINT_PROGRAM = $(LINT_NAME)-$(LINT_VERSION)

# Release asset coordinates for the pinned linter version. The upstream
# install.sh is not used: its checksum extraction matches the .sbom.json asset
# line and fails verification on recent releases. The checksums file is pinned
# here, so a release replaced after this line was written fails. A new
# LINT_VERSION needs a new hash.
LINT_CHECKSUMS_SHA256 = 9accc7943a5b4be44416a7d4efa7efb3d18c7f1919d6581cc3536e185301a2d4
LINT_VERSION_NUM = $(LINT_VERSION:v%=%)
LINT_BASE_URL = https://github.com/golangci/golangci-lint/releases/download/$(LINT_VERSION)

# golangci-lint type-checks against the standard library of whichever Go it
# finds, using a go/types built into the linter binary. A linter built with Go
# 1.26 panics ("file requires newer Go version go1.27") on a machine whose
# GOROOT is 1.27. go.mod carries no `toolchain` line, so the pin lives here,
# matching the Go this linter release was built with. Bump it together with
# LINT_VERSION.
LINT_GO_TOOLCHAIN ?= go1.26.0

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
	@echo "Setting up $(LINT_PROGRAM) ..."
	@set -e; \
	os=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	arch=$$(uname -m); \
	case "$$arch" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; esac; \
	dist="$(LINT_NAME)-$(LINT_VERSION_NUM)-$$os-$$arch"; \
	tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	curl -fsSL "$(LINT_BASE_URL)/$$dist.tar.gz" -o "$$tmp/$$dist.tar.gz"; \
	curl -fsSL "$(LINT_BASE_URL)/$(LINT_NAME)-$(LINT_VERSION_NUM)-checksums.txt" -o "$$tmp/checksums.txt"; \
	sums=$$( (sha256sum "$$tmp/checksums.txt" 2>/dev/null || shasum -a 256 "$$tmp/checksums.txt") | awk '{print $$1}'); \
	if [ "$$sums" != "$(LINT_CHECKSUMS_SHA256)" ]; then echo "checksums file mismatch: want '$(LINT_CHECKSUMS_SHA256)' got '$$sums'"; exit 1; fi; \
	want=$$(awk -v f="$$dist.tar.gz" '$$2 == f {print $$1}' "$$tmp/checksums.txt"); \
	got=$$( (sha256sum "$$tmp/$$dist.tar.gz" 2>/dev/null || shasum -a 256 "$$tmp/$$dist.tar.gz") | awk '{print $$1}'); \
	if [ -z "$$want" ] || [ "$$want" != "$$got" ]; then echo "checksum mismatch for $$dist.tar.gz: want '$$want' got '$$got'"; exit 1; fi; \
	tar -C "$$tmp" -xzf "$$tmp/$$dist.tar.gz"; \
	mkdir -p "$(BINDIR)/bin"; \
	mv -v "$$tmp/$$dist/$(LINT_NAME)" "$(BINDIR)/bin/$(LINT_PROGRAM)"

# The linter's cache records file paths relative to the checkout that filled
# it. Shared between git worktrees, it reports findings against files in a
# worktree that has since been removed. One cache per checkout, ignored.
lint: export GOTOOLCHAIN = $(LINT_GO_TOOLCHAIN)
lint: export GOLANGCI_LINT_CACHE = $(CURDIR)/.cache/golangci-lint
lint: install-lint
	@go version
	$(BINDIR)/bin/$(LINT_PROGRAM) run --timeout 5m0s --config config/.golangci-$(LINT_VERSION).yml ./...

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
