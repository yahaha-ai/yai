package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yahaha-ai/yai/internal/auth"
	"github.com/yahaha-ai/yai/internal/config"
	"github.com/yahaha-ai/yai/internal/fallback"
	"github.com/yahaha-ai/yai/internal/health"
	"github.com/yahaha-ai/yai/internal/proxy"
)

// buildServer creates a full yai server with mock upstreams for integration testing.
func buildServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()

	// Mock upstream that returns 200
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": "Hello from mock!"},
			},
		})
	}))

	providers := []config.ProviderConfig{
		{
			Name:     "mock",
			Upstream: upstream.URL,
			Auth:     config.ProviderAuth{Type: "none"},
			HealthCheck: config.HealthCheckConfig{
				Method:   "GET",
				Path:     "/",
				Interval: config.Duration{Duration: 50 * time.Millisecond},
				Timeout:  config.Duration{Duration: 1 * time.Second},
			},
		},
	}

	tokenMap := map[string]string{
		"yai_test_token": "test-client",
	}

	p := proxy.New(providers)
	checker := health.New(providers)
	checker.Start()
	time.Sleep(100 * time.Millisecond)

	handler := fallback.New(p, checker, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		statuses := checker.AllStatuses()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statuses)
	})
	mux.Handle("/proxy/", auth.Middleware(tokenMap, handler))

	server := httptest.NewServer(mux)

	cleanup := func() {
		checker.Stop()
		upstream.Close()
		server.Close()
	}

	return server, cleanup
}

func TestIntegration_HealthEndpoint(t *testing.T) {
	server, cleanup := buildServer(t)
	defer cleanup()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}

	var statuses map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&statuses)
	if _, ok := statuses["mock"]; !ok {
		t.Error("health response should contain 'mock' provider")
	}
}

func TestIntegration_AuthRequired(t *testing.T) {
	server, cleanup := buildServer(t)
	defer cleanup()

	// No auth header
	resp, err := http.Post(server.URL+"/proxy/mock/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestIntegration_ProxyWithAuth(t *testing.T) {
	server, cleanup := buildServer(t)
	defer cleanup()

	req, _ := http.NewRequest("POST", server.URL+"/proxy/mock/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer yai_test_token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["content"] == nil {
		t.Error("expected content in response")
	}
}

func TestIntegration_WrongToken(t *testing.T) {
	server, cleanup := buildServer(t)
	defer cleanup()

	req, _ := http.NewRequest("POST", server.URL+"/proxy/mock/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_wrong")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestIntegration_UnknownProvider(t *testing.T) {
	server, cleanup := buildServer(t)
	defer cleanup()

	req, _ := http.NewRequest("POST", server.URL+"/proxy/unknown/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_test_token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestIntegration_SSEStreaming(t *testing.T) {
	// Build a custom server with SSE upstream
	sseUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"Hi\"}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, e := range events {
			fmt.Fprint(w, e)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer sseUpstream.Close()

	providers := []config.ProviderConfig{
		{
			Name: "sse-mock", Upstream: sseUpstream.URL,
			Auth: config.ProviderAuth{Type: "none"},
			HealthCheck: config.HealthCheckConfig{
				Method: "GET", Path: "/",
				Interval: config.Duration{Duration: 50 * time.Millisecond},
				Timeout:  config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	tokenMap := map[string]string{"yai_test": "test"}

	p := proxy.New(providers)
	checker := health.New(providers)
	checker.Start()
	defer checker.Stop()
	time.Sleep(100 * time.Millisecond)

	handler := fallback.New(p, checker, nil)
	mux := http.NewServeMux()
	mux.Handle("/proxy/", auth.Middleware(tokenMap, handler))
	server := httptest.NewServer(mux)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/proxy/sse-mock/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "message_start") {
		t.Error("response should contain SSE events")
	}
}
