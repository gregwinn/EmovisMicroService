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
COVERAGE_THRESHOLD     := 85

# Two things are excluded from the coverage figure, each for a stated reason:
#
#   internal/httpapi/gen  generated from the OpenAPI spec and verified by
#                         `make generate-check`, not by tests. Measuring it
#                         would let a large generated file mask thin coverage
#                         of hand-written code.
#   cmd/                  process wiring — flag parsing, signal handling,
#                         dependency construction. It is exercised end to end
#                         by `make demo` and by running the binaries, which is
#                         a more honest check than a unit test that asserts
#                         main() called New().
#
# Everything else counts, and the threshold is set high enough that it bites.
COVERAGE_EXCLUDE       := -e /internal/httpapi/gen/ -e /cmd/

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

##@ Contract

.PHONY: generate
generate: ## Regenerate Go code from api/openapi.yaml
	$(GO) tool oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
	@echo "regenerated internal/httpapi/gen from api/openapi.yaml"

.PHONY: generate-check
generate-check: generate ## Fail if committed generated code has drifted from the spec
	@git diff --exit-code -- internal/httpapi/gen > /dev/null \
	  || { printf "\033[31mgenerated code is stale — run 'make generate' and commit the result\033[0m\n"; \
	       git --no-pager diff --stat -- internal/httpapi/gen; exit 1; }
	@echo "generated code matches the contract"

##@ Testing

.PHONY: test
test: ## Run all tests with the race detector (integration tests need Docker)
	$(GO) test -race -count=1 ./...

.PHONY: test-short
test-short: ## Run only unit tests (skips anything needing Docker)
	$(GO) test -short -count=1 ./...

.PHONY: test-integration
test-integration: ## Run only the integration tests (requires Docker)
	$(GO) test -race -count=1 -run 'Test' ./internal/store/postgres/...

.PHONY: cover
cover: ## Run tests and enforce the coverage threshold
	# -coverpkg=./... attributes coverage across package boundaries. Without it
	# the outbox relay reads as untested, because the tests that exercise it
	# live in the postgres package alongside the database it talks to.
	$(GO) test -race -count=1 -coverpkg=./... -coverprofile=$(COVERAGE_FILE).raw -covermode=atomic ./...
	@grep -v $(COVERAGE_EXCLUDE) $(COVERAGE_FILE).raw > $(COVERAGE_FILE)
	@rm -f $(COVERAGE_FILE).raw
	@$(GO) tool cover -func=$(COVERAGE_FILE) | tail -1
	@total=$$($(GO) tool cover -func=$(COVERAGE_FILE) | tail -1 | awk '{print $$3}' | tr -d '%'); \
	 awk -v t="$$total" -v min="$(COVERAGE_THRESHOLD)" 'BEGIN { \
	   if (t+0 < min+0) { printf "\033[31mcoverage %.1f%% is below the %s%% threshold\033[0m\n", t, min; exit 1 } \
	   printf "\033[32mcoverage %.1f%% meets the %s%% threshold\033[0m\n", t, min }'

.PHONY: cover-html
cover-html: cover ## Open the coverage report in a browser
	$(GO) tool cover -html=$(COVERAGE_FILE)

##@ Infrastructure

.PHONY: tf-fmt
tf-fmt: ## Format the Terraform
	terraform -chdir=deploy/terraform fmt -recursive

.PHONY: tf-validate
tf-validate: ## Validate the Terraform (no credentials needed)
	terraform -chdir=deploy/terraform init -backend=false -input=false > /dev/null
	terraform -chdir=deploy/terraform fmt -check -recursive -diff
	terraform -chdir=deploy/terraform validate

##@ Security

.PHONY: vuln
vuln: ## Check dependencies and the toolchain for known vulnerabilities
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

##@ Database

.PHONY: migrate
migrate: ## Apply pending database migrations (needs DATABASE_URL)
	$(GO) run ./cmd/migrate up

.PHONY: migrate-status
migrate-status: ## Show applied and pending migrations
	$(GO) run ./cmd/migrate status

##@ Local stack

.PHONY: compose-up
compose-up: ## Build and start the full stack (postgres, migrate, api, relay)
	docker compose up --build -d
	@echo "api on http://localhost:8080 — postgres on localhost:55432"

.PHONY: compose-down
compose-down: ## Stop the stack and remove its volumes
	docker compose down -v

.PHONY: compose-logs
compose-logs: ## Follow logs from the stack
	docker compose logs -f

.PHONY: demo
demo: compose-up ## Start the stack and walk the service end to end
	@./scripts/demo.sh

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
	rm -rf $(BIN_DIR) $(COVERAGE_FILE) $(COVERAGE_FILE).raw

##@ Meta

.PHONY: ci
ci: tidy generate-check lint cover build vuln ## Everything CI runs, in order
	@echo ""
	@echo "\033[32mAll CI checks passed.\033[0m"
