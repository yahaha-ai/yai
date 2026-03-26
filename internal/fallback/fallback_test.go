package fallback

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yahaha-ai/yai/internal/config"
	"github.com/yahaha-ai/yai/internal/health"
	"github.com/yahaha-ai/yai/internal/proxy"
)

// setupTestEnv creates a Fallback with mock upstreams.
// upstreamBehaviors maps provider name to (statusCode, body).
func setupTestEnv(t *testing.T, behaviors map[string]struct {
	status int
	body   string
	delay  time.Duration
}, groupProviders []string) (*Handler, func()) {
	t.Helper()

	providers := make([]config.ProviderConfig, 0)
	upstreams := make([]*httptest.Server, 0)

	for name, b := range behaviors {
		b := b
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if b.delay > 0 {
				time.Sleep(b.delay)
			}
			w.WriteHeader(b.status)
			w.Write([]byte(b.body))
		}))
		upstreams = append(upstreams, srv)
		providers = append(providers, config.ProviderConfig{
			Name:     name,
			Upstream: srv.URL,
			Auth:     config.ProviderAuth{Type: "none"},
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/",
				Interval: config.Duration{Duration: 50 * time.Millisecond},
				Timeout:  config.Duration{Duration: 1 * time.Second},
			},
		})
	}

	p := proxy.New(providers)
	checker := health.New(providers)
	checker.Start()

	// Wait for initial health checks
	time.Sleep(100 * time.Millisecond)

	groups := []config.FallbackGroup{
		{
			Name:      "test-group",
			Providers: groupProviders,
			Strategy:  "priority",
			Retry: config.RetryConfig{
				MaxAttempts: 2,
				Timeout:     config.Duration{Duration: 5 * time.Second},
			},
		},
	}

	h := New(p, checker, groups)

	cleanup := func() {
		checker.Stop()
		for _, s := range upstreams {
			s.Close()
		}
	}

	return h, cleanup
}

func TestFirstProviderAvailable(t *testing.T) {
	h, cleanup := setupTestEnv(t, map[string]struct {
		status int
		body   string
		delay  time.Duration
	}{
		"primary":   {200, "primary-response", 0},
		"secondary": {200, "secondary-response", 0},
	}, []string{"primary", "secondary"})
	defer cleanup()

	req := httptest.NewRequest("POST", "/proxy/primary/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); body != "primary-response" {
		t.Errorf("body = %q, want %q", body, "primary-response")
	}
}

func TestFallbackOn5xx(t *testing.T) {
	h, cleanup := setupTestEnv(t, map[string]struct {
		status int
		body   string
		delay  time.Duration
	}{
		"primary":   {503, `{"error":"unavailable"}`, 0},
		"secondary": {200, "secondary-response", 0},
	}, []string{"primary", "secondary"})
	defer cleanup()

	req := httptest.NewRequest("POST", "/proxy/primary/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); body != "secondary-response" {
		t.Errorf("body = %q, want %q", body, "secondary-response")
	}
}

func TestFallbackOn429(t *testing.T) {
	h, cleanup := setupTestEnv(t, map[string]struct {
		status int
		body   string
		delay  time.Duration
	}{
		"primary":   {429, `{"error":"rate limited"}`, 0},
		"secondary": {200, "fallback-ok", 0},
	}, []string{"primary", "secondary"})
	defer cleanup()

	req := httptest.NewRequest("POST", "/proxy/primary/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestNoFallbackOn400(t *testing.T) {
	h, cleanup := setupTestEnv(t, map[string]struct {
		status int
		body   string
		delay  time.Duration
	}{
		"primary":   {400, `{"error":"bad request"}`, 0},
		"secondary": {200, "should-not-reach", 0},
	}, []string{"primary", "secondary"})
	defer cleanup()

	req := httptest.NewRequest("POST", "/proxy/primary/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// 400 should not trigger fallback
	if rr.Code != 400 {
		t.Errorf("status = %d, want 400 (no fallback for client errors)", rr.Code)
	}
}

func TestNoFallbackOn401(t *testing.T) {
	h, cleanup := setupTestEnv(t, map[string]struct {
		status int
		body   string
		delay  time.Duration
	}{
		"primary":   {401, `{"error":"unauthorized"}`, 0},
		"secondary": {200, "should-not-reach", 0},
	}, []string{"primary", "secondary"})
	defer cleanup()

	req := httptest.NewRequest("POST", "/proxy/primary/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 401 {
		t.Errorf("status = %d, want 401 (no fallback for auth errors)", rr.Code)
	}
}

func TestAllProvidersDown(t *testing.T) {
	h, cleanup := setupTestEnv(t, map[string]struct {
		status int
		body   string
		delay  time.Duration
	}{
		"a": {503, "down-a", 0},
		"b": {503, "down-b", 0},
		"c": {503, "down-c", 0},
	}, []string{"a", "b", "c"})
	defer cleanup()

	req := httptest.NewRequest("POST", "/proxy/a/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 502 {
		t.Errorf("status = %d, want 502 (all providers down)", rr.Code)
	}
}

func TestFallbackSkipsFirst_UsesThird(t *testing.T) {
	h, cleanup := setupTestEnv(t, map[string]struct {
		status int
		body   string
		delay  time.Duration
	}{
		"a": {503, "down", 0},
		"b": {503, "down", 0},
		"c": {200, "third-ok", 0},
	}, []string{"a", "b", "c"})
	defer cleanup()

	req := httptest.NewRequest("POST", "/proxy/a/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); body != "third-ok" {
		t.Errorf("body = %q, want %q", body, "third-ok")
	}
}

func TestNoFallbackGroupConfigured(t *testing.T) {
	// Provider not in any fallback group — should pass through directly
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		fmt.Fprint(w, "error")
	}))
	defer srv.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "solo",
			Upstream: srv.URL,
			Auth:     config.ProviderAuth{Type: "none"},
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/",
				Interval: config.Duration{Duration: 50 * time.Millisecond},
				Timeout:  config.Duration{Duration: 1 * time.Second},
			},
		},
	}

	p := proxy.New(providers)
	checker := health.New(providers)
	checker.Start()
	defer checker.Stop()
	time.Sleep(100 * time.Millisecond)

	// No fallback groups
	h := New(p, checker, nil)

	req := httptest.NewRequest("POST", "/proxy/solo/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Should return the error directly, no fallback
	if rr.Code != 503 {
		t.Errorf("status = %d, want 503 (no fallback configured)", rr.Code)
	}
}

func TestRequestBodyAvailableForRetry(t *testing.T) {
	// Verify that request body is buffered so it can be sent to fallback provider
	var secondBody string
	callCount := 0

	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(503)
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		secondBody = string(b)
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer srvB.Close()

	providers := []config.ProviderConfig{
		{
			Name: "a", Upstream: srvA.URL,
			Auth: config.ProviderAuth{Type: "none"},
			HealthCheck: config.HealthCheckConfig{
				Method: "GET", Path: "/",
				Interval: config.Duration{Duration: 50 * time.Millisecond},
				Timeout:  config.Duration{Duration: 1 * time.Second},
			},
		},
		{
			Name: "b", Upstream: srvB.URL,
			Auth: config.ProviderAuth{Type: "none"},
			HealthCheck: config.HealthCheckConfig{
				Method: "GET", Path: "/",
				Interval: config.Duration{Duration: 50 * time.Millisecond},
				Timeout:  config.Duration{Duration: 1 * time.Second},
			},
		},
	}

	p := proxy.New(providers)
	checker := health.New(providers)
	checker.Start()
	defer checker.Stop()
	time.Sleep(100 * time.Millisecond)

	groups := []config.FallbackGroup{
		{
			Name: "test", Providers: []string{"a", "b"}, Strategy: "priority",
			Retry: config.RetryConfig{MaxAttempts: 2, Timeout: config.Duration{Duration: 5 * time.Second}},
		},
	}
	h := New(p, checker, groups)

	body := `{"model":"test","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/proxy/a/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if secondBody != body {
		t.Errorf("fallback body = %q, want %q", secondBody, body)
	}
}
