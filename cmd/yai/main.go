package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yahaha-ai/yai/internal/auth"
	"github.com/yahaha-ai/yai/internal/config"
	"github.com/yahaha-ai/yai/internal/fallback"
	"github.com/yahaha-ai/yai/internal/health"
	"github.com/yahaha-ai/yai/internal/proxy"
	"github.com/yahaha-ai/yai/internal/ratelimit"
)

func main() {
	configPath := flag.String("config", "yai.yaml", "path to config file")
	flag.Parse()

	// Load config
	f, err := os.Open(*configPath)
	if err != nil {
		log.Fatalf("failed to open config %s: %v", *configPath, err)
	}
	cfg, err := config.Parse(f)
	f.Close()
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	// Build token map for auth (with optional rate limiters)
	tokenMap := make(map[string]auth.TokenInfo)
	for _, tok := range cfg.Auth.Tokens {
		info := auth.TokenInfo{Name: tok.Name}
		if tok.RateLimit != "" {
			limit, err := ratelimit.ParseLimit(tok.RateLimit)
			if err != nil {
				log.Fatalf("auth token %q: invalid rate_limit: %v", tok.Name, err)
			}
			info.Limiter = ratelimit.NewLimiter(limit)
		}
		tokenMap[tok.Token] = info
	}

	// Initialize components
	p := proxy.New(cfg.Providers)
	checker := health.New(cfg.Providers)
	checker.Start()
	defer checker.Stop()

	handler := fallback.New(p, checker, cfg.Fallback.Groups)

	// Build router
	mux := http.NewServeMux()

	// Health endpoint (no auth)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		statuses := checker.AllStatuses()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(statuses)
	})

	// Proxy routes (auth required)
	mux.Handle("/proxy/", auth.Middleware(tokenMap, handler))

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("yai listening on %s", addr)
	log.Printf("  providers: %d", len(cfg.Providers))
	log.Printf("  fallback groups: %d", len(cfg.Fallback.Groups))
	log.Printf("  auth tokens: %d", len(cfg.Auth.Tokens))

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Graceful shutdown on SIGINT/SIGTERM
	done := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("received %v, shutting down gracefully...", sig)

		timeout := 30 * time.Second
		if cfg.Server.ShutdownTimeout.Duration > 0 {
			timeout = cfg.Server.ShutdownTimeout.Duration
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
		close(done)
	}()

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
	<-done
	log.Printf("yai stopped")
}
