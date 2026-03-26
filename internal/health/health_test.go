package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yahaha-ai/yai/internal/config"
)

func TestHealthyUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	checker := New([]config.ProviderConfig{
		{
			Name:     "test",
			Upstream: upstream.URL,
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/v1/models",
				Interval: config.Duration{Duration: 100 * time.Millisecond},
				Timeout:  config.Duration{Duration: 2 * time.Second},
			},
		},
	})
	checker.Start()
	defer checker.Stop()

	// Wait for first check
	time.Sleep(200 * time.Millisecond)

	status := checker.Status("test")
	if !status.Healthy {
		t.Errorf("expected healthy, got unhealthy")
	}
}

func TestUnhealthyUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer upstream.Close()

	checker := New([]config.ProviderConfig{
		{
			Name:     "test",
			Upstream: upstream.URL,
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/v1/models",
				Interval: config.Duration{Duration: 100 * time.Millisecond},
				Timeout:  config.Duration{Duration: 2 * time.Second},
			},
		},
	})
	checker.Start()
	defer checker.Stop()

	time.Sleep(200 * time.Millisecond)

	status := checker.Status("test")
	if status.Healthy {
		t.Errorf("expected unhealthy, got healthy")
	}
}

func TestUnreachableUpstream(t *testing.T) {
	checker := New([]config.ProviderConfig{
		{
			Name:     "test",
			Upstream: "http://localhost:1", // nothing listening
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/v1/models",
				Interval: config.Duration{Duration: 100 * time.Millisecond},
				Timeout:  config.Duration{Duration: 500 * time.Millisecond},
			},
		},
	})
	checker.Start()
	defer checker.Stop()

	time.Sleep(800 * time.Millisecond)

	status := checker.Status("test")
	if status.Healthy {
		t.Errorf("expected unhealthy for unreachable upstream")
	}
	if status.Error == "" {
		t.Errorf("expected error message for unreachable upstream")
	}
}

func TestRecovery(t *testing.T) {
	healthy := true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()

	checker := New([]config.ProviderConfig{
		{
			Name:     "test",
			Upstream: upstream.URL,
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/v1/models",
				Interval: config.Duration{Duration: 100 * time.Millisecond},
				Timeout:  config.Duration{Duration: 2 * time.Second},
			},
		},
	})
	checker.Start()
	defer checker.Stop()

	// Initially healthy
	time.Sleep(200 * time.Millisecond)
	if !checker.Status("test").Healthy {
		t.Fatal("expected initially healthy")
	}

	// Go unhealthy
	healthy = false
	time.Sleep(200 * time.Millisecond)
	if checker.Status("test").Healthy {
		t.Fatal("expected unhealthy after 500s")
	}

	// Recover
	healthy = true
	time.Sleep(200 * time.Millisecond)
	if !checker.Status("test").Healthy {
		t.Fatal("expected recovery after going back to 200")
	}
}

func TestAllStatuses(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer up.Close()

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer down.Close()

	checker := New([]config.ProviderConfig{
		{
			Name:     "good",
			Upstream: up.URL,
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/",
				Interval: config.Duration{Duration: 100 * time.Millisecond},
				Timeout:  config.Duration{Duration: 2 * time.Second},
			},
		},
		{
			Name:     "bad",
			Upstream: down.URL,
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/",
				Interval: config.Duration{Duration: 100 * time.Millisecond},
				Timeout:  config.Duration{Duration: 2 * time.Second},
			},
		},
	})
	checker.Start()
	defer checker.Stop()

	time.Sleep(200 * time.Millisecond)

	all := checker.AllStatuses()
	if len(all) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(all))
	}

	if !all["good"].Healthy {
		t.Error("expected 'good' to be healthy")
	}
	if all["bad"].Healthy {
		t.Error("expected 'bad' to be unhealthy")
	}
}

func TestUnknownProvider(t *testing.T) {
	checker := New(nil)
	status := checker.Status("nonexistent")
	if status.Healthy {
		t.Error("unknown provider should not be healthy")
	}
}

func TestIsHealthy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer up.Close()

	checker := New([]config.ProviderConfig{
		{
			Name:     "test",
			Upstream: up.URL,
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/",
				Interval: config.Duration{Duration: 100 * time.Millisecond},
				Timeout:  config.Duration{Duration: 2 * time.Second},
			},
		},
	})
	checker.Start()
	defer checker.Stop()

	time.Sleep(200 * time.Millisecond)

	if !checker.IsHealthy("test") {
		t.Error("expected IsHealthy=true")
	}
	if checker.IsHealthy("nonexistent") {
		t.Error("expected IsHealthy=false for unknown provider")
	}
}

func TestHealthCheckPath(t *testing.T) {
	var receivedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	checker := New([]config.ProviderConfig{
		{
			Name:     "test",
			Upstream: upstream.URL,
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/v1/models",
				Interval: config.Duration{Duration: 100 * time.Millisecond},
				Timeout:  config.Duration{Duration: 2 * time.Second},
			},
		},
	})
	checker.Start()
	defer checker.Stop()

	time.Sleep(200 * time.Millisecond)

	if receivedPath != "/v1/models" {
		t.Errorf("health check path = %q, want %q", receivedPath, "/v1/models")
	}
}
