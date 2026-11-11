# garret development entry point.
#
# Only basic make syntax is used so that the make 3.81 shipped with macOS
# behaves the same as GNU make on Linux.

GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOLANGCI_LINT_VERSION := 2.15.0
BETTERLEAKS ?= betterleaks
QUERIES_DIR := internal/store/queries

.PHONY: help build test lint vuln secrets generate generate-check check

help:
	@echo "make build           compile every package"
	@echo "make test            run tests with the race detector"
	@echo "make lint            run golangci-lint (v$(GOLANGCI_LINT_VERSION) in CI)"
	@echo "make vuln            run govulncheck"
	@echo "make secrets         scan the working tree with betterleaks"
	@echo "make generate        regenerate sqlc code when queries exist"
	@echo "make generate-check  fail if generated code is out of date"
	@echo "make check           everything above, as CI does, plus secrets"

build:
	$(GO) build ./...

test:
	$(GO) test -race -count=1 ./...

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

check: build test lint vuln generate-check secrets
