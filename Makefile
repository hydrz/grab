# Project configuration
MOD_NAME := github.com/hydrz/grab
BINARY_NAME := $(shell basename $(MOD_NAME))
CMD := ./cmd/grab

# Build variables
VERSION := $(shell git describe --tags --always --match='v*' 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +'%Y-%m-%d_%H:%M:%S')

# Go configuration
GO := go
LDFLAGS := -s -w -X $(MOD_NAME)/version.Version=$(VERSION)

# Colors
BLUE := \033[34m
GREEN := \033[32m
YELLOW := \033[33m
RED := \033[31m
RESET := \033[0m

.DEFAULT_GOAL := help

###############
##@ Initial

.PHONY: init
init: ## Initialize development environment
	@echo "$(BLUE)Installing development tools...$(RESET)"
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	$(GO) install golang.org/x/tools/cmd/goimports@latest
	$(GO) install github.com/goreleaser/goreleaser/v2@latest
	@echo "$(GREEN)Development environment initialized!$(RESET)"

.PHONY: deps
deps: ## Download and verify dependencies
	@$(GO) mod download && $(GO) mod tidy && $(GO) mod verify

.PHONY: rename
rename: ## Rename Go module (interactive)
	@echo "$(YELLOW)Current module: $(MOD_NAME)$(RESET)" \
		&& echo "Enter new go module name:" \
		&& read new_module_name \
		&& echo "New module name: '$$new_module_name'" \
		&& echo -n "Are you sure? [y/N] " \
		&& read ans && [ $${ans:-N} = y ] \
		&& echo "$(BLUE)Updating module name...$(RESET)" \
		&& find . -type f -not -path '*/\.git*' -exec sed -i "s|${MOD_NAME}|$$new_module_name|g" {} \; \
		&& echo "$(GREEN)Module renamed successfully!$(RESET)" \
		&& git add . && git commit -m "rename module to '$$new_module_name'"

###############
##@ Development

.PHONY: run
run: ## Run the application
	@CGO_ENABLED=0 $(GO) run -ldflags="$(LDFLAGS)" $(CMD)

fmt: ## Format code
	@$(GO) fmt ./...
	@if command -v goimports >/dev/null 2>&1; then goimports -w .; fi

.PHONY: lint
lint: ## Run linter
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=3m; \
	else \
		echo "$(RED)golangci-lint not found. Run 'make init'$(RESET)"; \
		exit 1; \
	fi

##@ Testing
.PHONY: test
test: ## Run tests with coverage report
	@$(GO) test -race -coverprofile=coverage.out ./...
	@$(GO) tool cover -func=coverage.out | tail -1

.PHONY: test-verbose
test-verbose: ## Run tests with verbose output
	@$(GO) test -v -race ./...

.PHONY: coverage
coverage: test ## Generate and open HTML coverage report
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)Coverage report generated: coverage.html$(RESET)"
	@echo "$(BLUE)Open coverage.html in your browser to view detailed coverage$(RESET)"

.PHONY: bench
bench: ## Run benchmarks
	@$(GO) test -bench=. -benchmem ./...

#########
##@ Build

.PHONY: build
build: ## Build binary
	@echo "$(BLUE)Building $(BINARY_NAME)...$(RESET)"
	@mkdir -p bin
	@CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o bin/$(BINARY_NAME) $(CMD)
	@echo "$(GREEN)Built: bin/$(BINARY_NAME)$(RESET)"

.PHONY: build-all
build-all: ## Build for multiple platforms
	@echo "$(BLUE)Building for multiple platforms...$(RESET)"
	@mkdir -p bin
	@for os in linux darwin windows; do \
		for arch in amd64 arm64; do \
			echo "Building $$os/$$arch..."; \
			GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build \
				-ldflags="$(LDFLAGS)" \
				-o bin/$(BINARY_NAME)-$$os-$$arch$$([ "$$os" = "windows" ] && echo ".exe") \
				$(CMD); \
		done; \
	done
	@echo "$(GREEN)Multi-platform build completed!$(RESET)"

.PHONY: install
install: ## Install binary to GOPATH/bin
	@CGO_ENABLED=0 $(GO) install -ldflags="$(LDFLAGS)" $(CMD)


##@ Release
.PHONY: release
release: ## Create release with goreleaser
	@if command -v goreleaser >/dev/null 2>&1; then \
		goreleaser release --clean; \
	else \
		echo "$(RED)goreleaser not found. Run 'make init'$(RESET)"; \
		exit 1; \
	fi


##@ Maintenance
.PHONY: clean
clean: ## Clean build artifacts and caches
	@$(GO) clean -cache -testcache -modcache
	@rm -rf bin/ dist/ coverage.out coverage.html
	@rm -rf downloads/
	@echo "$(GREEN)Cleaned!$(RESET)"

.PHONY: check
check: deps test lint ## Run all checks
	@echo "$(GREEN)All checks passed!$(RESET)"


##@ Information
.PHONY: info
info: ## Show build information
	@echo "$(BLUE)Project Information:$(RESET)"
	@echo "Module:     $(MOD_NAME)"
	@echo "Binary:     $(BINARY_NAME)"
	@echo "Version:    $(VERSION)"
	@echo "Commit:     $(GIT_COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Go Version: $$($(GO) version | cut -d' ' -f3)"

.PHONY: help
help: ## Show this help message
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make $(BLUE)<target>$(RESET)\n"} \
		/^[a-zA-Z_-]+:.*?##/ { printf "  $(BLUE)%-12s$(RESET) %s\n", $$1, $$2 } \
		/^##@/ { printf "\n$(YELLOW)%s$(RESET)\n", substr($$0, 5) }' $(MAKEFILE_LIST)
