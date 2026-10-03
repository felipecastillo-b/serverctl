# serverctl — build tooling
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN     := bin/serverctl
PKG     := ./cmd/serverctl
LDFLAGS := -X main.version=$(VERSION)

.DEFAULT_GOAL := help
.PHONY: help build run test vet lint fmt tidy clean

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-8s %s\n", $$1, $$2}'

build: ## Build the serverctl binary into bin/
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

run: build ## Build and run serverctl
	./$(BIN)

test: ## Run all tests
	go test ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format all Go sources
	gofmt -w .

tidy: ## Tidy module dependencies
	go mod tidy

clean: ## Remove build artifacts
	rm -rf bin/ dist/
