// Package connectionpool_test benchmarks pooled vs non-pooled HTTP connections.
//
// # What we're measuring
//
// Each benchmark makes N sequential requests to an in-process test HTTP server.
//
//   - BenchmarkPooled   — reuses keep-alive connections (no TCP handshake per req)
//   - BenchmarkNoPool   — creates a new TCP connection for every request
//
// # Expected result
//
// On a local machine the difference between pooled and non-pooled is small
// because the loopback (127.0.0.1) RTT is ~0.05ms vs ~1-5ms over a real LAN.
// The real benefit of pooling shows up in production:
//
//   - eliminates ~1 RTT per request (TCP handshake)
//   - avoids kernel-side socket setup/teardown overhead
//   - drastically reduces TIME_WAIT sockets under load
//
// Run with:
//
//	go test -bench=. -benchtime=5s ./internal/connectionpool/
//	go test -bench=. -benchtime=5s -benchmem ./internal/connectionpool/
package connectionpool_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/connectionpool"
)

// newEchoServer returns a test HTTP server that returns 200 for every request.
func newEchoServer(t testing.TB) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// makeRequest performs one GET and discards the response body.
// It is important to drain and close the body so the connection can be reused.
func makeRequest(t testing.TB, client *http.Client, url string) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// BenchmarkPooled measures throughput when TCP connections are reused.
func BenchmarkPooled(b *testing.B) {
	srv := newEchoServer(b)

	transport := connectionpool.New(connectionpool.Default())
	client := &http.Client{Transport: transport}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		makeRequest(b, client, srv.URL)
	}
	b.StopTimer()

	transport.CloseIdleConnections()
}

// BenchmarkNoPool measures throughput when every request opens a fresh TCP connection.
func BenchmarkNoPool(b *testing.B) {
	srv := newEchoServer(b)

	transport := connectionpool.New(connectionpool.NoPool())
	client := &http.Client{Transport: transport}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		makeRequest(b, client, srv.URL)
	}
	b.StopTimer()

	transport.CloseIdleConnections()
}

// BenchmarkPooledParallel measures concurrent throughput with a shared pool.
// This is closer to real-world proxy behaviour where many goroutines
// share the same transport.
func BenchmarkPooledParallel(b *testing.B) {
	srv := newEchoServer(b)

	transport := connectionpool.New(connectionpool.Default())
	client := &http.Client{Transport: transport}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			makeRequest(b, client, srv.URL)
		}
	})
	b.StopTimer()

	transport.CloseIdleConnections()
}

// BenchmarkNoPoolParallel measures concurrent throughput without connection reuse.
func BenchmarkNoPoolParallel(b *testing.B) {
	srv := newEchoServer(b)

	transport := connectionpool.New(connectionpool.NoPool())
	client := &http.Client{Transport: transport}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			makeRequest(b, client, srv.URL)
		}
	})
	b.StopTimer()

	transport.CloseIdleConnections()
}
