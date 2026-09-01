package main

import (
	"net/http"
	"runtime"
	"time"
)

// handleHealth returns a health check handler.
func handleHealth(serverName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"server":    serverName,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// handleInfo returns server metadata.
func handleInfo(serverName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"server":     serverName,
			"go_version": runtime.Version(),
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
			"goroutines": runtime.NumGoroutine(),
			"uptime":     time.Since(startTime).String(),
		})
	}
}

// handleGetUsers returns a list of users.
func handleGetUsers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// In a real app, this would query a database.
		// For our load balancer testing, serving static data is fine.
		response := APIResponse{
			Status: "success",
			Data:   staticUsers,
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// handleGetProducts returns a list of products.
func handleGetProducts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := APIResponse{
			Status: "success",
			Data:   staticProducts,
		}
		writeJSON(w, http.StatusOK, response)
	}
}
