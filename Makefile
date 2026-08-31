.PHONY: all build test vet fmt clean run-tcp-server run-tcp-client run-backend help

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOFMT=gofmt
BINARY_DIR=bin

# Binary names
TCP_SERVER=tcpserver
TCP_CLIENT=tcpclient
BACKEND=backend

# Default target
all: fmt vet build

## build: Build all binaries
build:
	@echo "Building all binaries..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -o $(BINARY_DIR)/$(TCP_SERVER).exe ./cmd/tcpserver/
	$(GOBUILD) -o $(BINARY_DIR)/$(TCP_CLIENT).exe ./cmd/tcpclient/
	$(GOBUILD) -o $(BINARY_DIR)/$(BACKEND).exe ./cmd/backend/
	@echo "Build complete. Binaries in $(BINARY_DIR)/"

## test: Run all tests
test:
	$(GOTEST) -v ./...

## test-race: Run all tests with race detector
test-race:
	$(GOTEST) -race -v ./...

## vet: Run go vet
vet:
	$(GOVET) ./...

## fmt: Format all Go files
fmt:
	$(GOFMT) -w -s .

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BINARY_DIR)
	@echo "Clean complete."

## run-tcp-server: Run TCP echo server
run-tcp-server: build
	./$(BINARY_DIR)/$(TCP_SERVER).exe

## run-tcp-client: Run TCP client
run-tcp-client: build
	./$(BINARY_DIR)/$(TCP_CLIENT).exe

## run-backend: Run HTTP backend server
run-backend: build
	./$(BINARY_DIR)/$(BACKEND).exe

## help: Show this help
help:
	@echo "Available targets:"
	@grep -E '^## ' Makefile | sed 's/## /  /'
