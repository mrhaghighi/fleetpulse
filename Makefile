GO ?= go
GOIMPORTS ?= goimports
GOLANGCI_LINT ?= golangci-lint
VERSION ?= 0.1.0

.PHONY: help build test test-integration lint fmt tidy web up down logs reset smoke loadtest backfill-year dist clean

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z0-9_-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build every package
	$(GO) build ./...

test: ## Run unit tests with the race detector
	$(GO) test -race -count=1 ./...

test-integration: ## Run integration tests (needs Docker)
	$(GO) test -race -count=1 -tags=integration ./...

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run

fmt: ## Format Go code with gofmt and goimports
	gofmt -w .
	$(GOIMPORTS) -w .

tidy: ## Tidy go.mod and go.sum
	$(GO) mod tidy

web: ## Build the single-file web UI from web/index.src.html
	./scripts/build_web.sh

up: ## Start the stack with docker compose
	docker compose up -d --build

down: ## Stop the stack
	docker compose down

logs: ## Follow stack logs
	docker compose logs -f

reset: ## Stop the stack and delete its volumes
	docker compose down -v

smoke: ## Run the end-to-end smoke test (ARGS="--with-year --chaos")
	GO="$(GO)" ./scripts/smoke.sh $(ARGS)

loadtest: ## Run the load test (ARGS="--stages 100,200 --hold 1m"; needs a running stack)
	GO="$(GO)" ./scripts/loadtest.sh $(ARGS)

backfill-year: ## Backfill one year of history (forces a rebuild)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	docker compose run --rm seed \
		--devices "$${SIM_DEVICES:-20}" \
		--seed "$${SIM_SEED:-1}" \
		--fanless-ratio "$${SIM_FANLESS_RATIO:-0.1}" \
		--raw-days 3 \
		--rollup-days 365 \
		--force

dist: ## Build release binaries and checksums
	GO="$(GO)" VERSION="$(VERSION)" ./scripts/dist.sh

clean: ## Remove build artifacts
	rm -rf bin dist
