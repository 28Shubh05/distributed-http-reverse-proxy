// Package main implements a raw TCP echo server that demonstrates fundamental
// networking concepts: socket creation, connection acceptance, concurrent client
// handling via goroutines, and graceful shutdown.
//
// Networking concepts demonstrated:
//
//   - net.Listen → creates a TCP socket, binds to address, starts listening
//     (equivalent to the C syscalls: socket() → bind() → listen())
//   - listener.Accept → blocks until a client completes the TCP 3-way handshake
//     (SYN → SYN-ACK → ACK), then returns the established connection
//   - net.Conn → represents an established TCP connection (a bidirectional byte stream)
//   - One goroutine per connection → the standard Go pattern for concurrent I/O
//   - Graceful shutdown → stop accepting new connections, drain active ones, then exit
//
// Usage:
//
//	go run ./cmd/tcpserver/ -port 9000
//	# Then connect with: go run ./cmd/tcpclient/ -port 9000
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	port := flag.String("port", "9000", "TCP server listen port")
	flag.Parse()

	addr := net.JoinHostPort("", *port)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// signal.NotifyContext returns a context that is cancelled when the process
	// receives SIGINT (Ctrl+C) or SIGTERM (container shutdown signal).
	// This is the idiomatic Go pattern for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger, addr); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// run starts the TCP echo server and blocks until shutdown is requested.
func run(ctx context.Context, logger *slog.Logger, addr string) error {
	// --- STEP 1: Create a listening socket ---
	// net.Listen("tcp", addr) performs three OS-level operations:
	//   1. socket()  — create a TCP socket file descriptor
	//   2. bind()    — associate the socket with the given address (IP:port)
	//   3. listen()  — mark the socket as passive (ready to accept connections)
	//
	// After this call, the OS kernel maintains a backlog queue of pending
	// connections from clients that have completed the TCP handshake but
	// haven't been accept()'d yet.
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	defer listener.Close()

	logger.Info("tcp echo server started",
		"addr", addr,
		"pid", os.Getpid(),
	)

	// wg tracks active connection goroutines so we can wait for them during shutdown.
	var wg sync.WaitGroup

	// connID is an atomic counter for assigning unique IDs to each connection.
	// atomic.Uint64 is safe for concurrent access without a mutex.
	var connID atomic.Uint64

	// --- STEP 2: Accept loop ---
	// Run the accept loop in a goroutine so the main goroutine can wait
	// for the shutdown signal.
	go func() {
		for {
			// Accept() blocks until:
			//   a) A client connects (TCP handshake completes) → returns net.Conn
			//   b) The listener is closed → returns error
			//
			// Each successful Accept() dequeues one connection from the kernel's
			// backlog queue. The returned net.Conn represents an established,
			// bidirectional TCP byte stream.
			conn, err := listener.Accept()
			if err != nil {
				// When we close the listener during shutdown, Accept returns an error.
				// This is expected — don't log it as a real error.
				if ctx.Err() != nil {
					return
				}
				logger.Error("accept error", "error", err)
				continue
			}

			id := connID.Add(1)
			wg.Add(1)
			// --- STEP 3: One goroutine per connection ---
			// This is the standard Go concurrency model for network servers.
			// Goroutines are lightweight (~2KB initial stack) so spawning thousands
			// is practical. This is fundamentally different from thread-per-connection
			// models in Java/C++ where threads cost ~1MB each.
			go func() {
				defer wg.Done()
				handleConnection(ctx, logger, conn, id)
			}()
		}
	}()

	// --- STEP 4: Wait for shutdown signal ---
	<-ctx.Done()
	logger.Info("shutdown signal received, stopping accept loop...")

	// Close listener immediately. This causes Accept() to return an error,
	// breaking the accept loop. No new connections will be accepted.
	listener.Close()

	// --- STEP 5: Drain active connections ---
	// Wait for all active connection handlers to finish, with a timeout.
	// In a production proxy, this timeout allows in-flight requests to complete
	// before we force-exit.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("all connections closed gracefully")
	case <-time.After(10 * time.Second):
		logger.Warn("shutdown timeout exceeded, some connections may be dropped")
	}

	return nil
}

// handleConnection processes a single client connection.
// It reads lines from the client and echoes them back, prefixed with "echo: ".
func handleConnection(ctx context.Context, logger *slog.Logger, conn net.Conn, id uint64) {
	// Always close the connection when done. This triggers TCP connection
	// termination (FIN → ACK → FIN → ACK). The OS handles the TIME_WAIT state.
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	logger.Info("client connected",
		"conn_id", id,
		"remote_addr", remote,
	)
	defer func() {
		logger.Info("client disconnected",
			"conn_id", id,
			"remote_addr", remote,
		)
	}()

	// bufio.Scanner reads the TCP byte stream line by line.
	// TCP is a byte stream protocol — it has no concept of "messages".
	// We use newline (\n) as a message delimiter, which is an application-level
	// convention (same concept HTTP uses with \r\n).
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		if ctx.Err() != nil {
			// Shutdown requested — stop processing this connection.
			return
		}

		line := scanner.Text()
		logger.Info("received",
			"conn_id", id,
			"data", line,
			"bytes", len(line),
		)

		// Echo back. conn.Write sends bytes into the TCP send buffer.
		// The OS TCP stack handles segmentation, retransmission, and flow control.
		response := fmt.Sprintf("echo: %s\n", line)
		if _, err := conn.Write([]byte(response)); err != nil {
			logger.Error("write failed",
				"conn_id", id,
				"error", err,
			)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		// Don't log EOF or closed-connection errors — they're normal when
		// the client disconnects.
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			logger.Error("read error",
				"conn_id", id,
				"error", err,
			)
		}
	}
}
