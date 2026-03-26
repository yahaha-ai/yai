package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/yahaha-ai/yai/internal/auth"
	"github.com/yahaha-ai/yai/internal/config"
	"github.com/yahaha-ai/yai/internal/fallback"
	"github.com/yahaha-ai/yai/internal/health"
	"github.com/yahaha-ai/yai/internal/proxy"
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

	// Build token map for auth
	tokenMap := make(map[string]string)
	for _, tok := range cfg.Auth.Tokens {
		tokenMap[tok.Token] = tok.Name
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
		json.NewEncoder(w).Encode(statuses)
	})

	// Proxy routes (auth required)
	mux.Handle("/proxy/", auth.Middleware(tokenMap, handler))

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("yai listening on %s", addr)
	log.Printf("  providers: %d", len(cfg.Providers))
	log.Printf("  fallback groups: %d", len(cfg.Fallback.Groups))
	log.Printf("  auth tokens: %d", len(cfg.Auth.Tokens))

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
