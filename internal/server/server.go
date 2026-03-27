// Package server provides the yai HTTP server with config hot-reload support.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

	"github.com/yahaha-ai/yai/internal/auth"
	"github.com/yahaha-ai/yai/internal/config"
	"github.com/yahaha-ai/yai/internal/fallback"
	"github.com/yahaha-ai/yai/internal/health"
	"github.com/yahaha-ai/yai/internal/proxy"
	"github.com/yahaha-ai/yai/internal/ratelimit"
)

// Server wraps the HTTP mux with hot-reloadable components.
type Server struct {
	configPath string
	handler    atomic.Value // stores *liveHandler
	checker    *health.Checker
	mu         sync.Mutex // serializes reloads
}

// liveHandler holds a snapshot of all hot-reloadable components.
type liveHandler struct {
	authHandler http.Handler
	checker     *health.Checker
}

// New creates a Server. The initial config must be valid (caller handles errors).
func New(configPath string, cfg *config.Config) (*Server, error) {
	s := &Server{configPath: configPath}
	if err := s.load(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// load builds components from config and stores them atomically.
func (s *Server) load(cfg *config.Config) error {
	tokenMap := buildTokenMap(cfg)
	p := proxy.New(cfg.Providers)

	// Stop old health checker if any
	if s.checker != nil {
		s.checker.Stop()
	}
	checker := health.New(cfg.Providers)
	checker.Start()
	s.checker = checker

	handler := fallback.New(p, checker, cfg.Fallback.Groups)
	authHandler := auth.Middleware(tokenMap, handler)

	s.handler.Store(&liveHandler{
		authHandler: authHandler,
		checker:     checker,
	})
	return nil
}

// Reload re-reads the config file and swaps components atomically.
// Returns an error if the new config is invalid (old config stays active).
func (s *Server) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	cfg, err := config.Parse(f)
	if err != nil {
		return err
	}

	return s.load(cfg)
}

// Handler returns an http.Handler that routes to the live components.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		live := s.handler.Load().(*liveHandler)
		statuses := live.checker.AllStatuses()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(statuses)
	})

	mux.HandleFunc("/proxy/", func(w http.ResponseWriter, r *http.Request) {
		live := s.handler.Load().(*liveHandler)
		live.authHandler.ServeHTTP(w, r)
	})

	return mux
}

// Stop cleans up background resources (health checker).
func (s *Server) Stop() {
	if s.checker != nil {
		s.checker.Stop()
	}
}

func buildTokenMap(cfg *config.Config) map[string]auth.TokenInfo {
	tokenMap := make(map[string]auth.TokenInfo)
	for _, tok := range cfg.Auth.Tokens {
		info := auth.TokenInfo{Name: tok.Name}
		if tok.RateLimit != "" {
			limit, err := ratelimit.ParseLimit(tok.RateLimit)
			if err != nil {
				log.Printf("WARN: auth token %q: invalid rate_limit: %v", tok.Name, err)
				continue
			}
			info.Limiter = ratelimit.NewLimiter(limit)
		}
		tokenMap[tok.Token] = info
	}
	return tokenMap
}
