package main

import (
	"encoding/json"
	"fmt"
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

// handleCreateOrder processes a new order.
// Simulates processing latency to provide a realistic target for load balancing.
func handleCreateOrder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req OrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, APIResponse{
				Status:  "error",
				Message: "invalid request payload",
			})
			return
		}

		if req.Quantity <= 0 {
			writeJSON(w, http.StatusBadRequest, APIResponse{
				Status:  "error",
				Message: "quantity must be greater than 0",
			})
			return
		}

		// Find the product to calculate total
		var product *Product
		for _, p := range staticProducts {
			if p.ID == req.ProductID {
				// We need a copy of the struct, but this is simple enough
				productVal := p
				product = &productVal
				break
			}
		}

		if product == nil {
			writeJSON(w, http.StatusNotFound, APIResponse{
				Status:  "error",
				Message: "product not found",
			})
			return
		}

		if !product.InStock {
			writeJSON(w, http.StatusConflict, APIResponse{
				Status:  "error",
				Message: "product out of stock",
			})
			return
		}

		// Simulate database/processing latency (10ms to 110ms)
		// This is CRUCIAL for our load balancer later (testing EWMA etc.)
		time.Sleep(50 * time.Millisecond)

		orderID := fmt.Sprintf("ORD-%d", time.Now().UnixNano())

		resp := OrderResponse{
			OrderID:   orderID,
			ProductID: req.ProductID,
			Quantity:  req.Quantity,
			Total:     product.Price * float64(req.Quantity),
			Status:    "confirmed",
		}

		writeJSON(w, http.StatusCreated, APIResponse{
			Status: "success",
			Data:   resp,
		})
	}
}
