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
	"github.com/yahaha-ai/yai/internal/telemetry"
)

// Set by goreleaser ldflags
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", "yai.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("yai %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

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

	// Initialize OpenTelemetry (nil if disabled)
	otelProvider, err := telemetry.New(context.Background(), cfg.Telemetry)
	if err != nil {
		log.Fatalf("failed to initialize telemetry: %v", err)
	}

	// Initialize server with hot-reloadable components
	srv, err := server.New(*configPath, cfg, otelProvider)
	if err != nil {
		log.Fatalf("failed to initialize server: %v", err)
	}
	defer srv.Stop()

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("yai %s listening on %s", version, addr)
	log.Printf("  providers: %d", len(cfg.Providers))
	log.Printf("  fallback groups: %d", len(cfg.Fallback.Groups))
	log.Printf("  auth tokens: %d", len(cfg.Auth.Tokens))
	if cfg.Telemetry.Enabled {
		log.Printf("  telemetry: %s → %s", cfg.Telemetry.ServiceName, cfg.Telemetry.Endpoint)
	}

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

				// Shutdown OTel (flush pending data)
				if otelProvider != nil {
					otelProvider.Shutdown(ctx)
				}

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
