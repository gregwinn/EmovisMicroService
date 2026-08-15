# Transaction Ingest Service
#
# `make` on its own prints the available targets. Every target here is also what
# CI runs — if it passes locally it passes in the pipeline.

GO                     ?= go
GOBIN                  := $(shell $(GO) env GOPATH)/bin
GOLANGCI_LINT_VERSION  := v2.12.2
GOLANGCI_LINT          := $(GOBIN)/golangci-lint

BIN_DIR                := bin
COVERAGE_FILE          := coverage.out
COVERAGE_THRESHOLD     := 75

VERSION                ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS                := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Help
# ---------------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@echo "Transaction Ingest Service — $(VERSION)"
	@echo ""
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2} \
		/^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)
	@echo ""

##@ Development

.PHONY: tools
tools: ## Install pinned development tooling
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: tidy
tidy: ## Tidy and verify go.mod / go.sum
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: fmt
fmt: ## Format the code
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run the linters
	$(GOLANGCI_LINT) run ./...

$(GOLANGCI_LINT):
	@$(MAKE) tools

##@ Testing

.PHONY: test
test: ## Run all tests with the race detector
	$(GO) test -race -count=1 ./...

.PHONY: test-short
test-short: ## Run only fast tests (skips anything needing Docker)
	$(GO) test -short -count=1 ./...

.PHONY: cover
cover: ## Run tests and enforce the coverage threshold
	$(GO) test -race -count=1 -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	@$(GO) tool cover -func=$(COVERAGE_FILE) | tail -1
	@total=$$($(GO) tool cover -func=$(COVERAGE_FILE) | tail -1 | awk '{print $$3}' | tr -d '%'); \
	 awk -v t="$$total" -v min="$(COVERAGE_THRESHOLD)" 'BEGIN { \
	   if (t+0 < min+0) { printf "\033[31mcoverage %.1f%% is below the %s%% threshold\033[0m\n", t, min; exit 1 } \
	   printf "\033[32mcoverage %.1f%% meets the %s%% threshold\033[0m\n", t, min }'

.PHONY: cover-html
cover-html: cover ## Open the coverage report in a browser
	$(GO) tool cover -html=$(COVERAGE_FILE)

##@ Build

.PHONY: build
build: ## Build all binaries into ./bin
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/ ./cmd/...
	@ls -1 $(BIN_DIR)

.PHONY: run
run: ## Run the API server locally
	$(GO) run ./cmd/transaction-api

.PHONY: clean
clean: ## Remove build and coverage artifacts
	rm -rf $(BIN_DIR) $(COVERAGE_FILE)

##@ Meta

.PHONY: ci
ci: tidy lint cover build ## Everything CI runs, in order
	@echo ""
	@echo "\033[32mAll CI checks passed.\033[0m"
