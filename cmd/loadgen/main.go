// Package main implements a high-performance concurrent HTTP load generation tool
// for stress testing, throughput evaluation, and latency quantile benchmarking.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type result struct {
	statusCode int
	latency    time.Duration
	err        error
}

func main() {
	targetURL := flag.String("url", "http://localhost:8080/api/info", "Target URL to benchmark")
	concurrency := flag.Int("c", 100, "Number of concurrent client workers (e.g. 100, 500, 1000, 5000)")
	totalRequests := flag.Int("n", 10000, "Total number of requests to execute")
	duration := flag.Duration("d", 0, "Benchmark duration (if > 0, overrides -n and runs for specified time)")
	timeout := flag.Duration("timeout", 10*time.Second, "Individual HTTP request timeout")
	method := flag.String("method", "GET", "HTTP method (GET, POST, etc.)")
	flag.Parse()

	if *concurrency <= 0 {
		fmt.Fprintf(os.Stderr, "Error: concurrency must be > 0\n")
		os.Exit(1)
	}

	fmt.Printf("==================================================================\n")
	fmt.Printf("  DISTRIBUTED HTTP REVERSE PROXY — LOAD TESTING SUITE\n")
	fmt.Printf("==================================================================\n")
	fmt.Printf("  Target URL:       %s\n", *targetURL)
	fmt.Printf("  Concurrency:      %d workers\n", *concurrency)
	if *duration > 0 {
		fmt.Printf("  Mode:             Time-based (%v)\n", *duration)
	} else {
		fmt.Printf("  Mode:             Request-based (%d requests)\n", *totalRequests)
	}
	fmt.Printf("  Request Timeout:  %v\n", *timeout)
	fmt.Printf("==================================================================\n\n")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	transport := &http.Transport{
		MaxIdleConns:        *concurrency * 2,
		MaxIdleConnsPerHost: *concurrency,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   *timeout,
	}

	resultsCh := make(chan result, 10000)
	var wg sync.WaitGroup
	var completedCount atomic.Int64

	startTime := time.Now()

	// Launch workers
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					if *duration == 0 {
						curr := completedCount.Add(1)
						if curr > int64(*totalRequests) {
							return
						}
					}

					reqStart := time.Now()
					req, err := http.NewRequestWithContext(ctx, *method, *targetURL, nil)
					if err != nil {
						resultsCh <- result{err: err}
						continue
					}

					resp, err := client.Do(req)
					reqDuration := time.Since(reqStart)

					if err != nil {
						resultsCh <- result{latency: reqDuration, err: err}
						continue
					}

					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()

					resultsCh <- result{
						statusCode: resp.StatusCode,
						latency:    reqDuration,
					}
				}
			}
		}()
	}

	// Collector goroutine
	var latencies []float64
	statusCodes := make(map[int]int)
	var errorsCount int
	var collectorWg sync.WaitGroup
	collectorWg.Add(1)

	go func() {
		defer collectorWg.Done()
		for res := range resultsCh {
			if res.err != nil {
				errorsCount++
			} else {
				latencies = append(latencies, res.latency.Seconds()*1000) // ms
				statusCodes[res.statusCode]++
			}
		}
	}()

	wg.Wait()
	close(resultsCh)
	collectorWg.Wait()

	totalTime := time.Since(startTime)
	totalCompleted := len(latencies) + errorsCount

	// Print Results
	fmt.Printf("\n--- Benchmark Results ---\n")
	fmt.Printf("Elapsed Time:         %v\n", totalTime.Round(time.Millisecond))
	fmt.Printf("Total Requests:       %d\n", totalCompleted)

	if totalTime.Seconds() > 0 {
		rps := float64(totalCompleted) / totalTime.Seconds()
		fmt.Printf("Requests Per Second:  %.2f req/s\n", rps)
	}

	fmt.Printf("Successful Requests:  %d\n", len(latencies))
	fmt.Printf("Failed Requests:      %d (%.2f%%)\n", errorsCount, float64(errorsCount)/float64(totalCompleted+1)*100)

	fmt.Printf("\nStatus Code Distribution:\n")
	for code, count := range statusCodes {
		pct := float64(count) / float64(totalCompleted) * 100
		fmt.Printf("  HTTP %d: %d (%.1f%%)\n", code, count, pct)
	}

	if len(latencies) > 0 {
		sort.Float64s(latencies)
		p50 := quantile(latencies, 0.50)
		p90 := quantile(latencies, 0.90)
		p95 := quantile(latencies, 0.95)
		p99 := quantile(latencies, 0.99)
		minLat := latencies[0]
		maxLat := latencies[len(latencies)-1]

		var sum float64
		for _, l := range latencies {
			sum += l
		}
		avgLat := sum / float64(len(latencies))

		fmt.Printf("\nLatency Summary (milliseconds):\n")
		fmt.Printf("  Fastest (Min):      %.3f ms\n", minLat)
		fmt.Printf("  Average:            %.3f ms\n", avgLat)
		fmt.Printf("  P50 (Median):       %.3f ms\n", p50)
		fmt.Printf("  P90:                %.3f ms\n", p90)
		fmt.Printf("  P95:                %.3f ms\n", p95)
		fmt.Printf("  P99:                %.3f ms\n", p99)
		fmt.Printf("  Slowest (Max):      %.3f ms\n", maxLat)
	}
	fmt.Printf("==================================================================\n")
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(q * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
