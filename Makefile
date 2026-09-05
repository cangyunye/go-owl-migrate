# go-owl-migrate Makefile

BINARY_NAME := owl-migrate
MAIN_PATH := ./cmd/migrate/main.go
BUILD_DIR := build
GO := go

COMMIT_ID := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date "+%Y-%m-%d %H:%M:%S")
LDFLAGS := -ldflags "-s -w -X 'github.com/cangyunye/go-owl-migrate/internal/cmd.version=0.4.0' -X 'github.com/cangyunye/go-owl-migrate/internal/cmd.commitID=$(COMMIT_ID)' -X 'github.com/cangyunye/go-owl-migrate/internal/cmd.buildTime=$(BUILD_TIME)'"

.PHONY: build test lint fmt deps clean run web/docsite

# Build tags for optional dialects:
#   ob         — OceanBase (MySQL/Oracle mode)
#   og         — OpenGaussDB, PanWeiDB
#   gdb        — GoldenDB (MySQL/Oracle mode)
#   sqlite3    — SQLite3 support (CGo, requires gcc)
#   duckdb     — DuckDB support (CGo, requires libduckdb)
#
# Oracle, PostgreSQL and MySQL are always compiled in. Product dialects are
# opt-in, e.g.:
#   go build -tags ob   ./cmd/migrate/main.go   # base + OceanBase
#   go build -tags og   ./cmd/migrate/main.go   # base + OpenGaussDB/PanWeiDB
#   go build -tags gdb  ./cmd/migrate/main.go   # base + GoldenDB
#   go build -tags "ob og gdb" ./cmd/migrate/main.go
# See the build/<flavor> targets below.

# Stage the docs portal + markdown into web/docsite/ so go:embed can bundle
# them (go:embed cannot reference parent directories). Regenerated on build.
web/docsite:
	@rm -rf web/docsite
	@mkdir -p web/docsite/docs
	@cp docs-site/index.html web/docsite/
	@cp -R docs-site/vendor web/docsite/vendor
	@cp docs/*.md web/docsite/docs/
	@echo "Docs staged into web/docsite/ (placeholders overwritten; git restore web/docsite/ before committing)"

# Default build: base dialects only (oracle, postgres, mysql). Product
# dialects are opt-in — see the build/ob, build/og, build/gdb flavors below.
build: web/docsite
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)"

# Build with SQLite3 support (CGo required)
build/sqlite3: web/docsite
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build -tags sqlite3 $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-sqlite3 $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-sqlite3"

# Build with DuckDB support (CGo + libduckdb required)
# Uses prebuilt libduckdb bundled with go-duckdb driver (static link, default).
# For custom/system libduckdb, add -tags duckdb_use_lib and set CGO_LDFLAGS.
build/duckdb: web/docsite
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build -tags duckdb $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-duckdb $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-duckdb"

# Build with DuckDB using system libduckdb (-tags duckdb_use_lib)
# Requires libduckdb installed or downloaded:
#   make duckdb/download    — downloads prebuilt libduckdb v1.7.0
#   export CGO_LDFLAGS="-L./lib" && make build/duckdb-lib
build/duckdb-lib: web/docsite
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

# Product flavors: base + one dialect group (suffixes on the output binary).
#   make build/ob   — base + OceanBase
#   make build/og   — base + OpenGaussDB/PanWeiDB
#   make build/gdb  — base + GoldenDB
#   make build/full — base + ob + og + gdb
build/ob: web/docsite
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	CGO_ENABLED=0 $(GO) build -tags ob $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-ob $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-ob (base + OceanBase)"

build/og: web/docsite
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	CGO_ENABLED=0 $(GO) build -tags og $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-og $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-og (base + OpenGaussDB/PanWeiDB)"

build/gdb: web/docsite
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	CGO_ENABLED=0 $(GO) build -tags gdb $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-gdb $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-gdb (base + GoldenDB)"

build/full: web/docsite
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	CGO_ENABLED=0 $(GO) build -tags "ob og gdb" $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-full $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)-full (base + ob + og + gdb)"

build/linux: web/docsite
	@mkdir -p $(BUILD_DIR)/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/linux-amd64/$(BINARY_NAME) $(MAIN_PATH)

build/darwin-arm64: web/docsite
	@mkdir -p $(BUILD_DIR)/darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/darwin-arm64/$(BINARY_NAME) $(MAIN_PATH)

build/windows: web/docsite
	@mkdir -p $(BUILD_DIR)/windows-amd64
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/windows-amd64/$(BINARY_NAME).exe $(MAIN_PATH)

build/all: build build/linux build/windows

test:
	$(GO) test -v ./...

# Run tests including optional dialects (SQLite3 + DuckDB)
test/full:
	$(GO) test -tags "sqlite3 duckdb" -v ./...

# Run tests including product dialects (OceanBase / OpenGaussDB+PanWeiDB / GoldenDB)
test/products:
	$(GO) test -tags "ob og gdb" -v ./...

# Run E2E tests against docker-compose databases (requires: docker compose up)
# Product dialect e2e needs the ob/og registration tags compiled in.
test/e2e:
	$(GO) test -tags "e2e ob og" -v -count=1 ./internal/cmd/ ./internal/transfer/importer/ ./internal/transfer/exporter/ ./internal/metadata/extractor/ ./internal/e2eob/

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
