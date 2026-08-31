// Package main implements a raw TCP client that connects to the TCP echo server
// and demonstrates the client side of TCP communication.
//
// Networking concepts demonstrated:
//
//   - net.Dial → initiates a TCP connection (performs the 3-way handshake from
//     the client's perspective: sends SYN, receives SYN-ACK, sends ACK)
//   - Full-duplex communication → reads and writes happen concurrently on the
//     same TCP connection (reading server responses in one goroutine, sending
//     user input in the main goroutine)
//   - Connection lifecycle → connect, exchange data, close (triggers FIN sequence)
//
// Usage:
//
//	go run ./cmd/tcpclient/ -host localhost -port 9000
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

func main() {
	host := flag.String("host", "localhost", "TCP server host")
	port := flag.String("port", "9000", "TCP server port")
	flag.Parse()

	addr := net.JoinHostPort(*host, *port)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger, addr); err != nil {
		logger.Error("client failed", "error", err)
		os.Exit(1)
	}
}

// run connects to the TCP server, reads user input from stdin, sends it to the
// server, and prints responses.
func run(ctx context.Context, logger *slog.Logger, addr string) error {
	// --- STEP 1: Initiate TCP connection ---
	// net.Dialer.DialContext performs the TCP 3-way handshake:
	//   Client → Server:  SYN          (synchronize sequence number)
	//   Server → Client:  SYN-ACK      (acknowledge + synchronize)
	//   Client → Server:  ACK          (acknowledge)
	//
	// After this completes, we have an established, full-duplex TCP connection.
	// The context allows us to cancel the dial if it takes too long or if
	// shutdown is requested.
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer conn.Close()

	logger.Info("connected to server",
		"local_addr", conn.LocalAddr().String(),
		"remote_addr", conn.RemoteAddr().String(),
	)

	// --- STEP 2: Full-duplex I/O ---
	// TCP connections are full-duplex: data can flow in both directions
	// simultaneously. We exploit this by reading server responses in a
	// background goroutine while sending user input in the main goroutine.
	var wg sync.WaitGroup

	// Background goroutine: read responses from server
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			// Only log unexpected errors (not EOF from server closing connection)
			if ctx.Err() == nil {
				logger.Error("read error", "error", err)
			}
		}
	}()

	// Main goroutine: read from stdin, send to server
	fmt.Println("Connected. Type messages and press Enter (Ctrl+C to quit):")
	fmt.Println("---")

	stdinScanner := bufio.NewScanner(os.Stdin)
	for stdinScanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		line := stdinScanner.Text()
		// Write to TCP connection — this puts bytes into the TCP send buffer.
		// The kernel handles segmentation into TCP segments, adds headers,
		// and transmits them.
		if _, err := fmt.Fprintf(conn, "%s\n", line); err != nil {
			logger.Error("send error", "error", err)
			break
		}
	}

	// --- STEP 3: Close connection ---
	// Closing the connection initiates TCP termination:
	//   Client → Server:  FIN
	//   Server → Client:  ACK
	//   Server → Client:  FIN
	//   Client → Server:  ACK
	//
	// This unblocks the reader goroutine (scanner.Scan returns false).
	conn.Close()
	wg.Wait()

	logger.Info("disconnected")
	return nil
}
