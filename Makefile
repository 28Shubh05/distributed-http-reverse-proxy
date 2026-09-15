.PHONY: all build test vet fmt clean run-tcp-server run-tcp-client run-backend bench bench-nopool help

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
	$(GOBUILD) -o $(BINARY_DIR)/admin.exe ./cmd/admin/
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

## run-proxy: Run reverse proxy (with connection pooling)
run-proxy: build
	./$(BINARY_DIR)/proxy.exe -port 8080 -backend http://localhost:9001

## run-proxy-nopool: Run reverse proxy without connection pooling (for benchmarking)
run-proxy-nopool: build
	./$(BINARY_DIR)/proxy.exe -port 8080 -backend http://localhost:9001 -no-pool

## bench: Run connection pool benchmarks (pooled vs no-pool)
bench:
	$(GOTEST) -bench=. -benchtime=5s -benchmem ./internal/connectionpool/

## bench-parallel: Run parallel connection pool benchmarks
bench-parallel:
	$(GOTEST) -bench=Parallel -benchtime=5s -benchmem ./internal/connectionpool/

## bench-cache: Run cache benchmarks (cache disabled vs cache enabled)
bench-cache:
	$(GOTEST) -bench=BenchmarkCache -benchtime=3s -benchmem ./internal/cache/

## help: Show this help
help:
	@echo "Available targets:"
	@grep -E '^## ' Makefile | sed 's/## /  /'
