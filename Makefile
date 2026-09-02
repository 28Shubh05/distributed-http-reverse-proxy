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
	@if not exist $(BINARY_DIR) mkdir $(BINARY_DIR)
	$(GOBUILD) -o $(BINARY_DIR)/$(TCP_SERVER).exe ./cmd/tcpserver/
	$(GOBUILD) -o $(BINARY_DIR)/$(TCP_CLIENT).exe ./cmd/tcpclient/
	$(GOBUILD) -o $(BINARY_DIR)/$(BACKEND).exe ./cmd/backend/
	$(GOBUILD) -o $(BINARY_DIR)/proxy.exe ./cmd/proxy/
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

## run-backend-1: Run HTTP backend server 1 (port 9001)
run-backend-1: build
	./$(BINARY_DIR)/$(BACKEND).exe -port 9001 -name backend-1

## run-backend-2: Run HTTP backend server 2 (port 9002)
run-backend-2: build
	./$(BINARY_DIR)/$(BACKEND).exe -port 9002 -name backend-2

## run-backend-3: Run HTTP backend server 3 (port 9003)
run-backend-3: build
	./$(BINARY_DIR)/$(BACKEND).exe -port 9003 -name backend-3

## run-proxy: Run reverse proxy
run-proxy: build
	./$(BINARY_DIR)/proxy.exe -port 8080 -backend http://localhost:9001

## help: Show this help
help:
	@echo "Available targets:"
	@grep -E '^## ' Makefile | sed 's/## /  /'
