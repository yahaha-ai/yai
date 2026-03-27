package health

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/yahaha-ai/yai/internal/config"
)

// ProviderStatus represents the health state of a single provider.
type ProviderStatus struct {
	Healthy   bool      `json:"healthy"`
	LastCheck time.Time `json:"last_check"`
	Latency   string    `json:"latency,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// Checker periodically probes upstream providers and tracks their health.
type Checker struct {
	providers []config.ProviderConfig
	statuses  map[string]*ProviderStatus
	mu        sync.RWMutex
	cancel    context.CancelFunc
}

// New creates a Checker for the given providers.
func New(providers []config.ProviderConfig) *Checker {
	statuses := make(map[string]*ProviderStatus)
	for _, p := range providers {
		statuses[p.Name] = &ProviderStatus{Healthy: false}
	}
	return &Checker{
		providers: providers,
		statuses:  statuses,
	}
}

// Start begins periodic health checking in background goroutines.
func (c *Checker) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	for _, p := range c.providers {
		p := p
		client := &http.Client{
			Timeout: p.HealthCheck.Timeout.Duration,
		}
		go c.loop(ctx, p, client)
	}
}

// Stop cancels all health check goroutines.
func (c *Checker) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Checker) loop(ctx context.Context, p config.ProviderConfig, client *http.Client) {
	// Run immediately on start
	c.check(ctx, p, client)

	ticker := time.NewTicker(p.HealthCheck.Interval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.check(ctx, p, client)
		}
	}
}

func (c *Checker) check(ctx context.Context, p config.ProviderConfig, client *http.Client) {
	url := p.Upstream + p.HealthCheck.Path
	req, err := http.NewRequestWithContext(ctx, p.HealthCheck.Method, url, nil)
	if err != nil {
		c.updateStatus(p.Name, false, 0, err.Error())
		return
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		c.updateStatus(p.Name, false, latency, err.Error())
		return
	}
	resp.Body.Close()

	healthy := resp.StatusCode >= 200 && resp.StatusCode < 400
	errMsg := ""
	if !healthy {
		errMsg = "HTTP " + resp.Status
	}
	c.updateStatus(p.Name, healthy, latency, errMsg)
}

func (c *Checker) updateStatus(name string, healthy bool, latency time.Duration, errMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.statuses[name]
	if !ok {
		return
	}
	s.Healthy = healthy
	s.LastCheck = time.Now()
	s.Latency = latency.Round(time.Millisecond).String()
	s.Error = errMsg
}

// Status returns the current status of a single provider.
func (c *Checker) Status(name string) ProviderStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.statuses[name]
	if !ok {
		return ProviderStatus{Healthy: false, Error: "unknown provider"}
	}
	return *s
}

// IsHealthy returns true if the named provider is currently healthy.
func (c *Checker) IsHealthy(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.statuses[name]
	if !ok {
		return false
	}
	return s.Healthy
}

// AllStatuses returns a snapshot of all provider statuses.
func (c *Checker) AllStatuses() map[string]ProviderStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]ProviderStatus, len(c.statuses))
	for k, v := range c.statuses {
		result[k] = *v
	}
	return result
}
