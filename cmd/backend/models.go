package main

// User represents a simple user record.
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Product represents an item in our catalog.
type Product struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Price    float64 `json:"price"`
	InStock  bool    `json:"in_stock"`
}

// APIResponse provides a standard wrapper for API responses.
type APIResponse struct {
	Status  string `json:"status"` // "success" or "error"
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// Hardcoded static data for the GET endpoints to simulate a database.

var staticUsers = []User{
	{ID: 1, Name: "Alice Smith", Email: "alice@example.com", Role: "admin"},
	{ID: 2, Name: "Bob Johnson", Email: "bob@example.com", Role: "user"},
	{ID: 3, Name: "Charlie Brown", Email: "charlie@example.com", Role: "user"},
}

var staticProducts = []Product{
	{ID: 101, Name: "Mechanical Keyboard", Category: "Electronics", Price: 120.50, InStock: true},
	{ID: 102, Name: "Wireless Mouse", Category: "Electronics", Price: 45.00, InStock: true},
	{ID: 103, Name: "Coffee Mug", Category: "Home", Price: 12.99, InStock: false},
	{ID: 104, Name: "Desk Lamp", Category: "Office", Price: 35.00, InStock: true},
}

// OrderRequest represents an incoming order payload.
type OrderRequest struct {
	ProductID int `json:"product_id"`
	Quantity  int `json:"quantity"`
}

// OrderResponse represents a processed order.
type OrderResponse struct {
	OrderID   string  `json:"order_id"`
	ProductID int     `json:"product_id"`
	Quantity  int     `json:"quantity"`
	Total     float64 `json:"total"`
	Status    string  `json:"status"`
}
