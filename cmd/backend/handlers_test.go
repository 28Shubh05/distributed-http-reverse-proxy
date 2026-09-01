package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleHealth(t *testing.T) {
	req, err := http.NewRequest("GET", "/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := handleHealth("test-server")
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var response map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}

	if response["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", response["status"])
	}
	if response["server"] != "test-server" {
		t.Errorf("expected server 'test-server', got %v", response["server"])
	}
}

func TestHandleGetProducts(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/products", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := handleGetProducts()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var response APIResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}

	if response.Status != "success" {
		t.Errorf("expected status 'success', got %v", response.Status)
	}
}

func TestHandleCreateOrder(t *testing.T) {
	tests := []struct {
		name           string
		payload        OrderRequest
		expectedStatus int
	}{
		{
			name:           "Valid Order",
			payload:        OrderRequest{ProductID: 101, Quantity: 1},
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "Invalid Quantity",
			payload:        OrderRequest{ProductID: 101, Quantity: 0},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Non-existent Product",
			payload:        OrderRequest{ProductID: 999, Quantity: 1},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "Out of Stock Product",
			payload:        OrderRequest{ProductID: 103, Quantity: 1},
			expectedStatus: http.StatusConflict,
		},
	}

	handler := handleCreateOrder()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.payload)
			req, err := http.NewRequest("POST", "/api/orders", bytes.NewBuffer(body))
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectedStatus {
				t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, tc.expectedStatus)
			}
		})
	}
}
