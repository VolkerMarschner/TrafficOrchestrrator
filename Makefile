BINARY  := trafficorch
VERSION := 0.4.5
PKG     := ./cmd

# Embed version at link-time
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.DEFAULT_GOAL := build

# ─── Build ────────────────────────────────────────────────────────────────────

.PHONY: build
build:                          ## Build for the current platform
	go build $(LDFLAGS) -o $(BINARY) $(PKG)

.PHONY: build-linux
build-linux:                    ## Cross-compile for Linux amd64
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 $(PKG)

.PHONY: build-linux-arm64
build-linux-arm64:              ## Cross-compile for Linux arm64
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-linux-arm64 $(PKG)

.PHONY: build-windows
build-windows:                  ## Cross-compile for Windows amd64
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-windows-amd64.exe $(PKG)

.PHONY: build-all
build-all: build-linux build-linux-arm64 build-windows  ## Build for all platforms

# ─── Test & Quality ───────────────────────────────────────────────────────────

.PHONY: test
test:                           ## Run all unit tests
	go test ./...

.PHONY: test-verbose
test-verbose:                   ## Run tests with verbose output
	go test -v ./...

.PHONY: test-cover
test-cover:                     ## Run tests and open HTML coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: vet
vet:                            ## Run go vet (static analysis)
	go vet ./...

.PHONY: check
check: vet test                 ## Run vet + tests (CI gate)

# ─── Install / Clean ──────────────────────────────────────────────────────────

.PHONY: install
install:                        ## Install binary into GOPATH/bin
	go install $(LDFLAGS) $(PKG)

.PHONY: clean
clean:                          ## Remove build artefacts
	rm -f $(BINARY) $(BINARY)-* coverage.out coverage.html

# ─── Help ─────────────────────────────────────────────────────────────────────

.PHONY: help
help:                           ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*##"}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
