package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yahaha-ai/yai/internal/config"
	"github.com/yahaha-ai/yai/internal/server"
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

	// Initialize server with hot-reloadable components
	srv, err := server.New(*configPath, cfg)
	if err != nil {
		log.Fatalf("failed to initialize server: %v", err)
	}
	defer srv.Stop()

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("yai listening on %s", addr)
	log.Printf("  providers: %d", len(cfg.Providers))
	log.Printf("  fallback groups: %d", len(cfg.Fallback.Groups))
	log.Printf("  auth tokens: %d", len(cfg.Auth.Tokens))

	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv.Handler(),
	}

	// Signal handling: SIGHUP for reload, SIGINT/SIGTERM for shutdown
	done := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

		for sig := range sigCh {
			switch sig {
			case syscall.SIGHUP:
				log.Printf("received SIGHUP, reloading config...")
				if err := srv.Reload(); err != nil {
					log.Printf("config reload FAILED: %v (keeping previous config)", err)
				} else {
					log.Printf("config reloaded successfully")
				}

			case syscall.SIGINT, syscall.SIGTERM:
				log.Printf("received %v, shutting down gracefully...", sig)

				timeout := 30 * time.Second
				if cfg.Server.ShutdownTimeout.Duration > 0 {
					timeout = cfg.Server.ShutdownTimeout.Duration
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()

				if err := httpServer.Shutdown(ctx); err != nil {
					log.Printf("shutdown error: %v", err)
				}
				close(done)
				return
			}
		}
	}()

	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
	<-done
	log.Printf("yai stopped")
}
