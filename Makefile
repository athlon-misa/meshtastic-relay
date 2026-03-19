MODULE := github.com/meshtastic-relay/relay
BINARY := relayd
BUILD_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOVET := $(GOCMD) vet
GOMOD := $(GOCMD) mod
GOFMT := gofmt

.PHONY: all build build-cli build-arm test test-verbose test-cover lint fmt vet tidy clean run poc-broker poc-node-a poc-node-b poc-clean

all: lint test build

## build: Build the relay daemon for the current platform
build:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/relayd

## build-cli: Build the CLI client
build-cli:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/relay-cli ./cmd/relay-cli

## build-arm: Cross-compile for Raspberry Pi (ARMv7 Linux)
build-arm:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-armv7 ./cmd/relayd

## build-arm64: Cross-compile for Raspberry Pi 4+ (ARM64 Linux)
build-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/relayd

## test: Run all tests
test:
	$(GOTEST) ./...

## test-verbose: Run all tests with verbose output
test-verbose:
	$(GOTEST) -v ./...

## test-cover: Run tests with coverage report
test-cover:
	$(GOTEST) -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report written to coverage.html"

## lint: Run linting checks
lint: fmt vet
	@echo "Lint checks passed"

## fmt: Check formatting
fmt:
	@test -z "$$($(GOFMT) -l .)" || (echo "Files need formatting:" && $(GOFMT) -l . && exit 1)

## vet: Run go vet
vet:
	$(GOVET) ./...

## tidy: Tidy and verify module dependencies
tidy:
	$(GOMOD) tidy
	$(GOMOD) verify

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

## run: Build and run the daemon
run: build
	./$(BUILD_DIR)/$(BINARY)

## --- POC targets ---

## poc-broker: Start the MQTT broker for POC testing
poc-broker:
	cd poc && docker compose up -d
	@echo "MQTT broker running on localhost:1883"

## poc-node-a: Run relay node A (home) on port 9370
poc-node-a: build
	./$(BUILD_DIR)/$(BINARY) -config poc/node-a.yaml -debug

## poc-node-b: Run relay node B (office) on port 9371
poc-node-b: build
	./$(BUILD_DIR)/$(BINARY) -config poc/node-b.yaml -debug

## poc-test: Run E2E tests (requires broker + both nodes running)
poc-test:
	POC_E2E=1 $(GOTEST) -v -count=1 ./poc/

## poc-clean: Stop MQTT broker and remove POC data files
poc-clean:
	cd poc && docker compose down -v
	rm -f poc/node-a.key poc/node-b.key poc/node-a.db poc/node-b.db

## help: Show this help message
help:
	@echo "Available targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
