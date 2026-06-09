# go-owl-migrate Makefile

BINARY_NAME := owl-migrate
MAIN_PATH := ./cmd/migrate/main.go
BUILD_DIR := build
GO := go

COMMIT_ID := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date "+%Y-%m-%d %H:%M:%S")
LDFLAGS := -ldflags "-s -w -X 'github.com/cangyunye/go-owl-migrate/internal/cmd.version=0.1.0' -X 'github.com/cangyunye/go-owl-migrate/internal/cmd.commitID=$(COMMIT_ID)' -X 'github.com/cangyunye/go-owl-migrate/internal/cmd.buildTime=$(BUILD_TIME)'"

.PHONY: build test lint fmt deps clean run

build:
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)"

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
