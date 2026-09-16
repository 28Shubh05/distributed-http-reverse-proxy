// Package admin implements the administrative control plane HTTP API
// for inspecting and managing backends, configs, routing policies, and health events.
package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/cache"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/circuitbreaker"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/health"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/storage"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Config holds settings for the Admin API server.
type Config struct {
	// Token is the secret token required for authentication.
	Token string

	// Store persists backend metadata and configurations.
	Store storage.Store

	// LoadBalancer is the active routing balancer.
	LoadBalancer loadbalancer.LoadBalancer

	// HealthChecker tracks live backend health status.
	HealthChecker *health.Checker

	// Cache tracks proxy response cache statistics.
	Cache cache.Cache

	// ReloadFunc is an optional callback executed on POST /admin/reload.
	ReloadFunc func(ctx context.Context) error

	// Logger for admin events.
	Logger *slog.Logger
}

// Server exposes administrative endpoints over HTTP.
type Server struct {
	cfg     Config
	mux     *http.ServeMux
	handler http.Handler
}

// NewServer initializes an Admin API Server with registered routes and authentication.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Store == nil {
		cfg.Store = storage.NewMemoryStore()
	}

	s := &Server{
		cfg: cfg,
		mux: http.NewServeMux(),
	}

	s.routes()
	s.handler = s.authMiddleware(s.mux)
	return s
}

// Handler returns the authenticated HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If no token is configured or path is /metrics, allow public scrape
		if s.cfg.Token == "" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		// Check Authorization: Bearer <token>
		authHeader := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else if customToken := r.Header.Get("X-Admin-Token"); customToken != "" {
			token = customToken
		}

		if token != s.cfg.Token {
			s.cfg.Logger.Warn("unauthorized admin access attempt",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			s.respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.Handle("GET /metrics", promhttp.Handler())

	s.mux.HandleFunc("GET /admin/backends", s.handleGetBackends)
	s.mux.HandleFunc("POST /admin/backends", s.handleCreateBackend)
	s.mux.HandleFunc("DELETE /admin/backends/{id}", s.handleDeleteBackend)

	s.mux.HandleFunc("GET /admin/stats", s.handleGetStats)
	s.mux.HandleFunc("GET /admin/health", s.handleGetHealth)

	s.mux.HandleFunc("GET /admin/config", s.handleGetConfig)
	s.mux.HandleFunc("POST /admin/config", s.handleSetConfig)
	s.mux.HandleFunc("POST /admin/reload", s.handleReload)

	s.mux.HandleFunc("GET /admin/events", s.handleGetEvents)
}

// GET /admin/backends
func (s *Server) handleGetBackends(w http.ResponseWriter, r *http.Request) {
	backends, err := s.cfg.Store.ListBackends(r.Context())
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.respondJSON(w, http.StatusOK, map[string]any{"backends": backends})
}

// POST /admin/backends
func (s *Server) handleCreateBackend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		URL    string `json:"url"`
		Weight int    `json:"weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid json body: "+err.Error())
		return
	}

	if req.ID == "" || req.URL == "" {
		s.respondError(w, http.StatusBadRequest, "id and url are required")
		return
	}
	if req.Weight <= 0 {
		req.Weight = 1
	}

	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid url: "+err.Error())
		return
	}

	record := storage.BackendRecord{
		ID:     req.ID,
		URL:    req.URL,
		Weight: req.Weight,
	}

	if err := s.cfg.Store.UpsertBackend(r.Context(), record); err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to save backend: "+err.Error())
		return
	}

	// Update in-memory load balancer and health checker if available
	if s.cfg.LoadBalancer != nil {
		bh := health.NewBackendHealth(health.DefaultConfig())
		cb := circuitbreaker.New(circuitbreaker.DefaultConfig())
		backend := &loadbalancer.Backend{
			ID:             req.ID,
			URL:            parsedURL,
			Weight:         req.Weight,
			Health:         bh,
			CircuitBreaker: cb,
		}
		s.cfg.LoadBalancer.AddBackend(backend)

		if s.cfg.HealthChecker != nil {
			s.cfg.HealthChecker.Register(req.ID, req.URL, bh)
		}
	}

	s.cfg.Logger.Info("admin added backend", "id", req.ID, "url", req.URL, "weight", req.Weight)
	s.respondJSON(w, http.StatusCreated, record)
}

// DELETE /admin/backends/{id}
func (s *Server) handleDeleteBackend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.respondError(w, http.StatusBadRequest, "missing backend id")
		return
	}

	if err := s.cfg.Store.DeleteBackend(r.Context(), id); err != nil {
		if err == storage.ErrNotFound {
			s.respondError(w, http.StatusNotFound, "backend not found")
			return
		}
		s.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if s.cfg.LoadBalancer != nil {
		s.cfg.LoadBalancer.RemoveBackend(id)
	}
	if s.cfg.HealthChecker != nil {
		s.cfg.HealthChecker.Deregister(id)
	}

	s.cfg.Logger.Info("admin removed backend", "id", id)
	s.respondJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// GET /admin/stats
func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	if s.cfg.Cache != nil {
		cStats := s.cfg.Cache.Stats()
		stats["cache"] = map[string]any{
			"hits":      cStats.Hits,
			"misses":    cStats.Misses,
			"size":      cStats.Size,
			"evictions": cStats.Evictions,
		}
	}

	s.respondJSON(w, http.StatusOK, stats)
}

// GET /admin/health
func (s *Server) handleGetHealth(w http.ResponseWriter, r *http.Request) {
	records, err := s.cfg.Store.ListBackends(r.Context())
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	healthStatus := make([]map[string]any, 0, len(records))
	for _, b := range records {
		entry := map[string]any{
			"id":     b.ID,
			"url":    b.URL,
			"weight": b.Weight,
			"status": "HEALTHY",
		}
		healthStatus = append(healthStatus, entry)
	}

	s.respondJSON(w, http.StatusOK, map[string]any{
		"status":   "UP",
		"backends": healthStatus,
	})
}

// GET /admin/config
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	configs, err := s.cfg.Store.GetAllConfig(r.Context())
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.respondJSON(w, http.StatusOK, map[string]any{"configs": configs})
}

// POST /admin/config
func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid json body: "+err.Error())
		return
	}

	if req.Key == "" {
		s.respondError(w, http.StatusBadRequest, "key is required")
		return
	}

	if err := s.cfg.Store.SetConfig(r.Context(), req.Key, req.Value); err != nil {
		s.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.cfg.Logger.Info("admin updated config", "key", req.Key, "value", req.Value)
	s.respondJSON(w, http.StatusOK, map[string]string{"status": "updated", "key": req.Key, "value": req.Value})
}

// POST /admin/reload
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ReloadFunc != nil {
		if err := s.cfg.ReloadFunc(r.Context()); err != nil {
			s.cfg.Logger.Error("config reload failed", "err", err)
			s.respondError(w, http.StatusInternalServerError, "reload failed: "+err.Error())
			return
		}
	}

	s.cfg.Logger.Info("admin reload completed successfully")
	s.respondJSON(w, http.StatusOK, map[string]string{
		"status":  "reloaded",
		"message": "configuration and backend routing pool reloaded",
	})
}

// GET /admin/events
func (s *Server) handleGetEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	events, err := s.cfg.Store.ListHealthEvents(r.Context(), limit)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) respondJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) respondError(w http.ResponseWriter, code int, message string) {
	s.respondJSON(w, code, map[string]string{"error": message})
}
