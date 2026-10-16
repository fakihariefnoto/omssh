SHELL := /bin/bash
BINARY := omssh
PKG := ./...

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

.PHONY: build
build: ## Build the binary into ./omssh
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/omssh

.PHONY: run
run: ## Build and run the TUI
	go run ./cmd/omssh

.PHONY: test
test: ## Run unit tests (races enabled)
	go test -race -count=1 $(PKG)

.PHONY: test-short
test-short: ## Run unit tests without integration tags
	go test -race -count=1 -short $(PKG)

.PHONY: cover
cover: ## Write coverage.txt and coverage.html
	go test -race -covermode=atomic -coverprofile=coverage.txt $(PKG)
	go tool cover -func=coverage.txt | tail -1
	go tool cover -html=coverage.txt -o coverage.html

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: fmt
fmt: ## Format all Go code
	gofmt -s -w .

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: check
check: fmt vet lint test ## Format, vet, lint, test

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BINARY) dist coverage.txt coverage.html

.PHONY: release
release: ## Cross-compile release archives with goreleaser
	goreleaser release --clean

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
