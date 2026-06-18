# go-owl-migrate Makefile

BINARY_NAME := owl-migrate
MAIN_PATH := ./cmd/migrate/main.go
BUILD_DIR := build
GO := go

COMMIT_ID := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date "+%Y-%m-%d %H:%M:%S")
LDFLAGS := -ldflags "-s -w -X 'github.com/cangyunye/go-owl-migrate/internal/cmd.version=0.1.0' -X 'github.com/cangyunye/go-owl-migrate/internal/cmd.commitID=$(COMMIT_ID)' -X 'github.com/cangyunye/go-owl-migrate/internal/cmd.buildTime=$(BUILD_TIME)'"

.PHONY: build test lint fmt deps clean run

# Build tags for optional dialects:
#   sqlite3    — include SQLite3 support (CGo, requires gcc)
#   duckdb     — include DuckDB support (CGo, requires libduckdb)
#
# Compound dialects (goldendb, oceanbase, panweidb, opengaussdb) are
# included by default. Exclude with:
#   go build -tags "nogoldendb,nooceanbase,nopanweidb,noopengaussdb"

# Default build: all dialects (core + compound + optional with tags)
build:
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)"

# Build with SQLite3 support (CGo required)
build/sqlite3:
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build -tags sqlite3 $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-sqlite3 $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-sqlite3"

# Build with DuckDB support (CGo + libduckdb required)
# Uses prebuilt libduckdb bundled with go-duckdb driver (static link, default).
# For custom/system libduckdb, add -tags duckdb_use_lib and set CGO_LDFLAGS.
build/duckdb:
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build -tags duckdb $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-duckdb $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-duckdb"

# Build with DuckDB using system libduckdb (-tags duckdb_use_lib)
# Requires libduckdb installed or downloaded:
#   make duckdb/download    — downloads prebuilt libduckdb v1.7.0
#   export CGO_LDFLAGS="-L./lib" && make build/duckdb-lib
build/duckdb-lib:
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build -tags "duckdb,duckdb_use_lib" $(LDFLAGS) \
	  -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-duckdb-lib $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-duckdb-lib (system libduckdb)"

# Download prebuilt libduckdb from GitHub releases
# Target: libduckdb v1.7.0 for the current OS/ARCH
# Extracts into ./lib/ directory for use with build/duckdb-lib
DUCKDB_VERSION := v1.7.0
DUCKDB_OS := $(shell uname -s | tr A-Z a-z)
DUCKDB_ARCH := $(shell uname -m)
duckdb/download:
	@mkdir -p ./lib
	@echo "Downloading libduckdb $(DUCKDB_VERSION) for $(DUCKDB_OS)-$(DUCKDB_ARCH)..."
	curl -sL "https://github.com/duckdb/duckdb/releases/download/$(DUCKDB_VERSION)/libduckdb-$(DUCKDB_OS)-$(DUCKDB_ARCH).zip" \
	  -o /tmp/libduckdb.zip
	unzip -o /tmp/libduckdb.zip -d ./lib/ > /dev/null 2>&1
	rm -f /tmp/libduckdb.zip
	@echo "libduckdb extracted to ./lib/"
	@echo "Build with: CGO_LDFLAGS="-L./lib" make build/duckdb-lib"

# Core-only: 3 dialects (oracle, postgres, mysql) + compound dialects
build/core:
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-core $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-core"

# Minimal: only oracle, postgres, mysql (no compound dialects)
build/minimal:
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build -tags "nogoldendb,nooceanbase,nopanweidb,noopengaussdb" $(LDFLAGS) \
	  -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-minimal $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-minimal"

# Oracle-only: single dialect build
build/oracle:
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build -tags "nogoldendb,nooceanbase,nopanweidb,noopengaussdb" $(LDFLAGS) \
	  -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-oracle $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-oracle"

build/linux:
	@mkdir -p $(BUILD_DIR)/linux-amd64
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/linux-amd64/$(BINARY_NAME) $(MAIN_PATH)

build/darwin-arm64:
	@mkdir -p $(BUILD_DIR)/darwin-arm64
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/darwin-arm64/$(BINARY_NAME) $(MAIN_PATH)

build/windows:
	@mkdir -p $(BUILD_DIR)/windows-amd64
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/windows-amd64/$(BINARY_NAME).exe $(MAIN_PATH)

build/all: build build/linux build/windows

test:
	$(GO) test -v ./...

# Run tests including optional dialects (SQLite3 + DuckDB)
test/full:
	$(GO) test -tags "sqlite3 duckdb" -v ./...

test-quick:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

lint:
	@which golangci-lint > /dev/null && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

vet:
	$(GO) vet ./...

deps:
	$(GO) mod download
	$(GO) mod tidy

clean:
	rm -rf $(BUILD_DIR)

run:
	$(GO) run $(MAIN_PATH) $(ARGS)
