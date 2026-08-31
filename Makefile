.PHONY: all build install uninstall clean help test web-build dev

# Build variables
BINARY_NAME=lele
BUILD_DIR=build
CMD_DIR=cmd/$(BINARY_NAME)
MAIN_GO=$(CMD_DIR)/main.go

# Version
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse --short=8 HEAD 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date +%FT%T%z)
GO_VERSION=$(shell $(GO) version | awk '{print $$3}')
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.gitCommit=$(GIT_COMMIT) -X main.buildTime=$(BUILD_TIME) -X main.goVersion=$(GO_VERSION) -s -w"

# Go variables
GO?=go
GOFLAGS?=-v -tags stdjson

# Installation
INSTALL_PREFIX?=$(HOME)/.local
INSTALL_BIN_DIR=$(INSTALL_PREFIX)/bin
INSTALL_MAN_DIR=$(INSTALL_PREFIX)/share/man/man1

# Workspace and Skills
LELEm_HOME?=$(HOME)/.lele
WORKSPACE_DIR?=$(LELEm_HOME)/workspace
WORKSPACE_SKILLS_DIR=$(WORKSPACE_DIR)/skills
BUILTIN_SKILLS_DIR=$(CURDIR)/skills
WEB_DIR=web

# OS detection
UNAME_S:=$(shell uname -s)
UNAME_M:=$(shell uname -m)

# Platform-specific settings
ifeq ($(UNAME_S),Linux)
	PLATFORM=linux
	ifeq ($(UNAME_M),x86_64)
		ARCH=amd64
	else ifeq ($(UNAME_M),aarch64)
		ARCH=arm64
	else ifeq ($(UNAME_M),loongarch64)
		ARCH=loong64
	else ifeq ($(UNAME_M),riscv64)
		ARCH=riscv64
	else
		ARCH=$(UNAME_M)
	endif
else ifeq ($(UNAME_S),Darwin)
	PLATFORM=darwin
	ifeq ($(UNAME_M),x86_64)
		ARCH=amd64
	else ifeq ($(UNAME_M),arm64)
		ARCH=arm64
	else
		ARCH=$(UNAME_M)
	endif
else
	PLATFORM=$(UNAME_S)
	ARCH=$(UNAME_M)
endif

BINARY_PATH=$(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)-$(ARCH)

# Default target
all: build

## generate: Run generate
generate:
	@echo "Run generate..."
	@rm -r ./$(CMD_DIR)/workspace 2>/dev/null || true
	@$(GO) generate ./...
	@echo "Run generate complete"

## web-build: Build the web app for embedding
# Farm caches PostCSS/Tailwind output in node_modules/.farm keyed only on the
# CSS entry, so edits to .tsx files never invalidate it: Tailwind's JIT scans
# all sources but the bundler serves the stale cached sheet, silently dropping
# newly-introduced utility classes (e.g. translate-x-5) from the emitted CSS.
# Wipe cache + dist so every build sees the full current source.
web-build:
	@echo "Building web app..."
	@rm -rf $(WEB_DIR)/node_modules/.farm $(WEB_DIR)/dist
	@cd $(WEB_DIR) && bun run --bun build 
	@rm -rf ./cmd/lele/web
	@mkdir -p ./cmd/lele/web
	@cp -R ./web/dist ./cmd/lele/web/dist
	@echo "Web app build complete"

## build: Build the lele binary for current platform
build: web-build generate
	@echo "Building $(BINARY_NAME) for $(PLATFORM)/$(ARCH)..."
	@mkdir -p $(BUILD_DIR)
	@$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_PATH) ./$(CMD_DIR)
	@echo "Build complete: $(BINARY_PATH)"
	@ln -sf $(BINARY_NAME)-$(PLATFORM)-$(ARCH) $(BUILD_DIR)/$(BINARY_NAME)

## build-all: Build lele for all platforms
build-all: web-build generate
	@echo "Building for multiple platforms..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)
	GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./$(CMD_DIR)
	GOOS=linux GOARCH=loong64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-loong64 ./$(CMD_DIR)
	GOOS=linux GOARCH=riscv64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-riscv64 ./$(CMD_DIR)
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./$(CMD_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./$(CMD_DIR)
	@echo "All builds complete"

## install: Install lele binary and copy builtin skills
# Uses copy-to-temp + atomic rename so the binary can be replaced even while
# it is running: `cp` over an executing file fails with ETXTBSY ("Text file
# busy"), but rename() swaps the inode and the live process keeps the old one.
install: build
	@echo "Installing $(BINARY_NAME)..."
	@mkdir -p $(INSTALL_BIN_DIR)
	@tmp="$(INSTALL_BIN_DIR)/.$(BINARY_NAME).new.$$"; \
	cp $(BUILD_DIR)/$(BINARY_NAME) $$tmp \
		&& chmod +x $$tmp \
		&& mv -f $$tmp $(INSTALL_BIN_DIR)/$(BINARY_NAME) \
		|| { rm -f $$tmp; echo "install: failed to replace $(INSTALL_BIN_DIR)/$(BINARY_NAME)"; exit 1; }
	@echo "Installed binary to $(INSTALL_BIN_DIR)/$(BINARY_NAME)"
	@echo "Installation complete!"

## uninstall: Remove lele from system
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@rm -f $(INSTALL_BIN_DIR)/$(BINARY_NAME)
	@echo "Removed binary from $(INSTALL_BIN_DIR)/$(BINARY_NAME)"
	@echo "Note: Only the executable file has been deleted."
	@echo "If you need to delete all configurations (config.json, workspace, etc.), run 'make uninstall-all'"

## uninstall-all: Remove lele and all data
uninstall-all:
	@echo "Removing workspace and skills..."
	@rm -rf $(LELEm_HOME)
	@echo "Removed workspace: $(LELEm_HOME)"
	@echo "Complete uninstallation done!"

## clean: Remove build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

## vet: Run go vet for static analysis
vet:
	@$(GO) vet ./...

## fmt: Format Go code
test:
	@$(GO) test ./...

## fmt: Format Go code
fmt:
	@$(GO) fmt ./...

## deps: Download dependencies
deps:
	@$(GO) mod download
	@$(GO) mod verify

## update-deps: Update dependencies
update-deps:
	@$(GO) get -u ./...
	@$(GO) mod tidy

## check: Run vet, fmt, and verify dependencies
check: deps fmt vet test

## run: Build and run lele
run: build
	@$(BUILD_DIR)/$(BINARY_NAME) $(ARGS)

## dev: Run frontend and backend in development mode with hotreload
dev:
	@echo "Starting development environment..."
	@(cd $(WEB_DIR) && bun run dev) & \
	(go run github.com/air-verse/air@latest) & \
	wait

## help: Show this help message
help:
	@echo "lele Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
	@echo ""
	@echo "Examples:"
	@echo "  make build              # Build for current platform"
	@echo "  make install            # Install to ~/.local/bin"
	@echo "  make uninstall          # Remove from /usr/local/bin"
	@echo "  make install-skills     # Install skills to workspace"
	@echo "  make dev                # Run frontend and backend in dev mode with hotreload"
	@echo ""
	@echo "Environment Variables:"
	@echo "  INSTALL_PREFIX          # Installation prefix (default: ~/.local)"
	@echo "  WORKSPACE_DIR           # Workspace directory (default: ~/.lele/workspace)"
	@echo "  VERSION                 # Version string (default: git describe)"
	@echo ""
	@echo "Current Configuration:"
	@echo "  Platform: $(PLATFORM)/$(ARCH)"
	@echo "  Binary: $(BINARY_PATH)"
	@echo "  Install Prefix: $(INSTALL_PREFIX)"
	@echo "  Workspace: $(WORKSPACE_DIR)"
## Desktop app targets
.PHONY: desktop-sidecar desktop-build desktop-dev
desktop-sidecar: ## Build the lele sidecar binary for the desktop app
	bash desktop/scripts/build-sidecar.sh
desktop-build: desktop-sidecar ## Build the desktop app (requires Tauri deps)
	cd desktop && bun install && bun run tauri build
desktop-dev: ## Run the desktop app in dev mode (hot reload)
	cd desktop && PKG_CONFIG_PATH=/usr/lib64/pkgconfig:/usr/share/pkgconfig \
		LELE_SIDECAR_BIN=$(CURDIR)/desktop/src-tauri/binaries/lele-x86_64-unknown-linux-gnu \
		bun run dev
