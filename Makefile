.DEFAULT_GOAL := help
.PHONY: help dev build build-debug build-universal package run cli \
        tidy fmt vet test cover lint \
        frontend-install frontend-dev frontend-build bindings \
        clean clean-frontend clean-all doctor

BINARY     := margo
BUILD_DIR  := build/bin
CLI_BIN    := $(BUILD_DIR)/margo-cli

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Targets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ---------- Wails app ----------

dev: ## Run Wails app in dev mode (live reload)
	wails dev

build: ## Build production Wails app
	wails build

build-debug: ## Build Wails app with debug symbols + devtools
	wails build -debug -devtools

build-universal: ## Build macOS universal binary (arm64 + amd64)
	wails build -platform darwin/universal

package: ## Build and package (e.g. .app bundle on macOS)
	wails build -clean

run: build ## Build then launch the app
	@if [ "$$(uname)" = "Darwin" ]; then open $(BUILD_DIR)/$(BINARY).app; else $(BUILD_DIR)/$(BINARY); fi

bindings: ## Regenerate frontend/wailsjs Go<->JS bindings
	wails generate module

# ---------- CLI ----------

cli: ## Build the headless margo CLI to build/bin/margo-cli
	@mkdir -p $(BUILD_DIR)
	go build -o $(CLI_BIN) ./cmd/margo-cli

cli-run: ## Run the CLI (override args with ARGS=...). Example: make cli-run ARGS="-provider openai -prompt hi"
	go run ./cmd/margo-cli $(ARGS)

# ---------- Go ----------

tidy: ## go mod tidy
	go mod tidy

fmt: ## gofmt -w on all Go files
	gofmt -w .

vet: ## go vet
	go vet ./...

test: ## Run all Go tests
	go test ./...

cover: ## Run tests with coverage report
	go test -cover -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint (requires golangci-lint installed)
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not installed: https://golangci-lint.run/usage/install/"; exit 1; }
	golangci-lint run ./...

# ---------- Frontend ----------

frontend-install: ## npm install in frontend/
	cd frontend && npm install

frontend-dev: ## Run Vite dev server standalone (no Wails)
	cd frontend && npm run dev

frontend-build: ## Build frontend assets to frontend/dist
	cd frontend && npm run build

# ---------- Cleanup ----------

clean: ## Remove Go and Wails build artifacts
	rm -rf $(BUILD_DIR) coverage.out

clean-frontend: ## Remove frontend build output and node_modules
	rm -rf frontend/dist frontend/node_modules

clean-all: clean clean-frontend ## Remove all build artifacts and dependencies

# ---------- Diagnostics ----------

doctor: ## Verify required toolchain (go, wails, npm)
	@echo "go:    $$(go version 2>/dev/null || echo MISSING)"
	@echo "wails: $$(wails version 2>/dev/null || echo MISSING)"
	@echo "node:  $$(node --version 2>/dev/null || echo MISSING)"
	@echo "npm:   $$(npm --version 2>/dev/null || echo MISSING)"
