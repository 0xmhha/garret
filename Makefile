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

.PHONY: help build build-go web web-audit e2e test lint vuln secrets generate generate-check check release

help:
	@echo "make build           build the web interface, then compile with it embedded"
	@echo "make build-go        compile without Node (embeds the placeholder page)"
	@echo "make web             install, test and build the web interface"
	@echo "make e2e             run the browser suite in Chrome and WebKit (after make build;"
	@echo "                     WebKit once: cd web && npx playwright install webkit)"
	@echo "make release         build bin/garret; fails unless the web interface is embedded"
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
