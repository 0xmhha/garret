# garret development entry point.
#
# Only basic make syntax is used so that the make 3.81 shipped with macOS
# behaves the same as GNU make on Linux.

GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOLANGCI_LINT_VERSION := 2.15.0
BETTERLEAKS ?= betterleaks
QUERIES_DIR := internal/store/queries
WEB_DIR := web
WEB_DIST := internal/webui/dist/index.html
# Go tests and builds embed the real interface once `make web` has built it
# (docs/specs/web.md 3); without it they use the committed placeholder.
GOTAGS := $(shell test -f $(WEB_DIST) && echo webui)
# Release archives (docs/specs/release.md). Archives are named by version and
# overwritten on a rebuild; SHA256SUMS lists only this version's.
VERSION ?= $(shell git describe --tags --always --dirty)
DIST_PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

.PHONY: help build build-go web web-audit e2e dist test lint vuln secrets generate generate-check check release

help:
	@echo "make build           build the web interface, then compile with it embedded"
	@echo "make build-go        compile without Node (embeds the placeholder page)"
	@echo "make web             install, test and build the web interface"
	@echo "make e2e             run the browser suite in Chrome and WebKit (after make build;"
	@echo "                     WebKit once: cd web && npx playwright install webkit)"
	@echo "make release         build bin/garret; fails unless the web interface is embedded"
	@echo "make dist            release archives for every platform, with SHA256SUMS"
	@echo "make test            run tests with the race detector"
	@echo "make lint            run golangci-lint (v$(GOLANGCI_LINT_VERSION) in CI)"
	@echo "make vuln            run govulncheck"
	@echo "make secrets         scan the working tree with betterleaks"
	@echo "make generate        regenerate sqlc code when queries exist"
	@echo "make generate-check  fail if generated code is out of date"
	@echo "make check           everything above, as CI does, plus secrets"

build: web
	$(GO) build -tags webui ./...

build-go:
	$(GO) build ./...

web:
	cd $(WEB_DIR) && npm ci && npm test && npm run build

web-audit:
	cd $(WEB_DIR) && npm audit --audit-level=high

# The browser suite builds its Go fixture with the embedded interface, so
# the interface must already be built (docs/design/testing.md).
e2e:
	@test -f $(WEB_DIST) || { echo "the web interface is not built; run make build first"; exit 1; }
	cd $(WEB_DIR) && npm run typecheck && npx playwright test

test:
	$(GO) test -race -count=1 -tags "$(GOTAGS)" ./...

release: web
	$(GO) build -tags webui -trimpath -o bin/garret ./cmd/garret
	@./bin/garret version | grep -q "web: built" || { echo "release refused: bin/garret does not embed the web interface"; exit 1; }
	@./bin/garret version

# One archive per platform, each holding the binary with the real web
# interface, LICENSE and README.md. Staging directories stay under dist/,
# which Git ignores.
dist: web
	@test -f $(WEB_DIST) || { echo "dist refused: the web interface is not built"; exit 1; }
	@for p in $(DIST_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; name=garret_$(VERSION)_$${os}_$${arch}; \
		echo "building $$name"; \
		mkdir -p dist/$$name || exit 1; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -tags webui -trimpath -o dist/$$name/garret ./cmd/garret || exit 1; \
		cp LICENSE README.md dist/$$name/ || exit 1; \
		tar -C dist -czf dist/$$name.tar.gz $$name || exit 1; \
	done
	cd dist && shasum -a 256 garret_$(VERSION)_*.tar.gz > SHA256SUMS
	@cat dist/SHA256SUMS

lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "golangci-lint not found; install v$(GOLANGCI_LINT_VERSION) from https://golangci-lint.run"; exit 1; }
	@$(GOLANGCI_LINT) version 2>/dev/null | grep -q "version $(GOLANGCI_LINT_VERSION) " || echo "warning: CI uses golangci-lint v$(GOLANGCI_LINT_VERSION); results may differ"
	$(GOLANGCI_LINT) run ./...

vuln:
	$(GO) tool govulncheck ./...

secrets:
	@command -v $(BETTERLEAKS) >/dev/null 2>&1 || { echo "betterleaks not found; see https://github.com/betterleaks/betterleaks"; exit 1; }
	$(BETTERLEAKS) git . --no-banner

generate:
	@if ls $(QUERIES_DIR)/*.sql >/dev/null 2>&1; then $(GO) tool sqlc generate; else echo "no queries yet; skipping sqlc"; fi

generate-check: generate
	@git diff --exit-code -- internal/store/sqlcgen || { echo "generated code is out of date; run make generate and commit"; exit 1; }

check: build test lint vuln generate-check web-audit secrets
