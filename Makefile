.DEFAULT_GOAL := help
.PHONY: help dev build build-debug build-universal package run cli cli-run tui tui-run \
        tidy fmt vet test test-integration test-frontend test-all cover lint \
        frontend-install frontend-dev frontend-build vendor-mathjax bindings \
        clean clean-frontend clean-all doctor

BINARY     := margo
BUILD_DIR  := build/bin
CLI_BIN    := $(BUILD_DIR)/margo-cli
TUI_BIN    := $(BUILD_DIR)/margo-tui

WAILS      := wails

# Ubuntu 24.04+ ships only webkit2gtk-4.1; wails v2 links 4.0 unless
# built with this tag.
WAILS_TAGS := $(shell pkg-config --exists webkit2gtk-4.1 2>/dev/null && echo -tags webkit2_41)

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Targets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ---------- Wails app ----------

dev: ## Run Wails app in dev mode (live reload)
	$(WAILS) dev $(WAILS_TAGS)

build: ## Build production Wails app
	$(WAILS) build $(WAILS_TAGS)

build-debug: ## Build Wails app with debug symbols + devtools
	$(WAILS) build -debug -devtools $(WAILS_TAGS)

build-universal: ## Build macOS universal binary (arm64 + amd64)
	$(WAILS) build -platform darwin/universal

package: ## Build and package (e.g. .app bundle on macOS)
	$(WAILS) build -clean $(WAILS_TAGS)

run: build ## Build then launch the app
	@if [ "$$(uname)" = "Darwin" ]; then open $(BUILD_DIR)/$(BINARY).app; else $(BUILD_DIR)/$(BINARY); fi

bindings: ## Regenerate frontend/wailsjs Go<->JS bindings
	$(WAILS) generate module

# ---------- CLI ----------

cli: ## Build the headless margo CLI to build/bin/margo-cli
	@mkdir -p $(BUILD_DIR)
	go build -o $(CLI_BIN) ./cmd/margo-cli

cli-run: ## Run the CLI (override args with ARGS=...). Example: make cli-run ARGS="-provider openai -prompt hi"
	go run ./cmd/margo-cli $(ARGS)

# ---------- TUI ----------

tui: ## Build the Bubble Tea TUI to build/bin/margo-tui
	@mkdir -p $(BUILD_DIR)
	go build -o $(TUI_BIN) ./cmd/margo-tui

tui-run: ## Run the TUI directly
	go run ./cmd/margo-tui

# ---------- Go ----------

tidy: ## go mod tidy
	go mod tidy

fmt: ## gofmt -w on all Go files
	gofmt -w .

vet: ## go vet
	go vet ./...

test: ## Run all Go tests
	go test ./...

test-integration: ## Run MCP integration tests (requires npx; downloads npm packages on first run)
	go test -tags=integration -v -timeout=2m ./pkg/margo/mcp/

test-frontend: ## Run Vitest frontend tests
	cd frontend && npm run test

test-all: test test-frontend ## Run Go + frontend tests

cover: ## Run tests with coverage report
	go test -cover -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint (requires golangci-lint built against this Go version)
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not installed: https://golangci-lint.run/usage/install/"; exit 1; }
	@# golangci-lint typechecks with the stdlib it was compiled against.
	@# A binary older than the local toolchain reports every stdlib
	@# import as "could not load export data" — upgrade it, don't chase
	@# the errors. `go vet` and CI cover the same ground meanwhile.
	golangci-lint run ./...

# ---------- Frontend ----------

frontend-install: ## npm install in frontend/ and vendor mathjax
	cd frontend && npm install
	$(MAKE) vendor-mathjax

# MathJax 4 loads TeX extensions, glyph ranges and speech data on demand,
# from the CDN by default. Vendor every file it can request so the app
# renders offline; index.html points loader.paths.fonts at MJ_OUT/fonts.
MJ_SRC := frontend/node_modules/mathjax
MJ_FONTS := frontend/node_modules/@mathjax
MJ_OUT := frontend/public/mathjax

vendor-mathjax: ## Copy mathjax and its on-demand files from node_modules to frontend/public/mathjax
	rm -rf $(MJ_OUT)
	mkdir -p $(MJ_OUT)/input/tex $(MJ_OUT)/sre/mathmaps $(MJ_OUT)/fonts/mathjax-newcm-font/svg
	cp $(MJ_SRC)/tex-svg.js $(MJ_SRC)/LICENSE $(MJ_OUT)/
	cp -r $(MJ_SRC)/input/tex/extensions $(MJ_OUT)/input/tex/
	cp $(MJ_SRC)/sre/speech-worker.js $(MJ_OUT)/sre/
	cp $(MJ_SRC)/sre/mathmaps/base.json $(MJ_SRC)/sre/mathmaps/en.json $(MJ_SRC)/sre/mathmaps/nemeth.json $(MJ_OUT)/sre/mathmaps/
	cp -r $(MJ_FONTS)/mathjax-newcm-font/svg/dynamic $(MJ_OUT)/fonts/mathjax-newcm-font/svg/
	for e in bbm bboldx dsfont mhchem; do \
	  mkdir -p $(MJ_OUT)/fonts/mathjax-$$e-font-extension && \
	  cp $(MJ_FONTS)/mathjax-$$e-font-extension/svg.js $(MJ_OUT)/fonts/mathjax-$$e-font-extension/; \
	done
	@echo "vendored mathjax: $$(du -sh $(MJ_OUT) | cut -f1)"

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
	@echo "wails tags: $(or $(WAILS_TAGS),none)"
