package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yahaha-ai/yai/internal/config"
)

// newMockUpstream creates a test server that returns the given status and body.
func newMockUpstream(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

// newMockSSEUpstream creates a test server that returns SSE events with optional delays.
func newMockSSEUpstream(events []string, delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		for _, event := range events {
			fmt.Fprint(w, event)
			flusher.Flush()
			if delay > 0 {
				time.Sleep(delay)
			}
		}
	}))
}

func makeProviders(upstreams map[string]string) []config.ProviderConfig {
	var providers []config.ProviderConfig
	for name, upstream := range upstreams {
		providers = append(providers, config.ProviderConfig{
			Name:     name,
			Upstream: upstream,
			Auth:     config.ProviderAuth{Type: "none"},
		})
	}
	return providers
}

func TestRouteToCorrectUpstream(t *testing.T) {
	upstream := newMockUpstream(200, "anthropic-response")
	defer upstream.Close()

	p := New(makeProviders(map[string]string{"anthropic": upstream.URL}))

	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", strings.NewReader(`{"test":true}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); body != "anthropic-response" {
		t.Errorf("body = %q, want %q", body, "anthropic-response")
	}
}

func TestRouteUnknownProvider(t *testing.T) {
	p := New(makeProviders(map[string]string{"anthropic": "http://localhost:1"}))

	req := httptest.NewRequest("POST", "/proxy/unknown/v1/messages", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 404 {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestRoutePathPreserved(t *testing.T) {
	var receivedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	p := New(makeProviders(map[string]string{"openai": upstream.URL}))

	req := httptest.NewRequest("POST", "/proxy/openai/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if receivedPath != "/v1/chat/completions" {
		t.Errorf("upstream received path = %q, want %q", receivedPath, "/v1/chat/completions")
	}
}

func TestSSEStreamPassthrough(t *testing.T) {
	events := []string{
		"data: {\"type\":\"content_block_start\"}\n\n",
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hello\"}}\n\n",
		"data: {\"type\":\"message_stop\"}\n\n",
	}
	upstream := newMockSSEUpstream(events, 0)
	defer upstream.Close()

	p := New(makeProviders(map[string]string{"anthropic": upstream.URL}))

	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	body := rr.Body.String()
	expected := strings.Join(events, "")
	if body != expected {
		t.Errorf("body = %q, want %q", body, expected)
	}
}

func TestSSESlowStreamRealtime(t *testing.T) {
	events := []string{
		"data: {\"chunk\":1}\n\n",
		"data: {\"chunk\":2}\n\n",
		"data: {\"chunk\":3}\n\n",
	}
	// 100ms between events
	upstream := newMockSSEUpstream(events, 100*time.Millisecond)
	defer upstream.Close()

	p := New(makeProviders(map[string]string{"test": upstream.URL}))

	// Use a real TCP server so we can read the response incrementally
	server := httptest.NewServer(p)
	defer server.Close()

	resp, err := http.Post(server.URL+"/proxy/test/v1/stream", "application/json", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var received []string
	var timestamps []time.Time

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			received = append(received, line)
			timestamps = append(timestamps, time.Now())
		}
	}

	if len(received) != 3 {
		t.Fatalf("received %d events, want 3", len(received))
	}

	// Events should arrive with delays, not all at once
	if len(timestamps) >= 2 {
		gap := timestamps[1].Sub(timestamps[0])
		if gap < 50*time.Millisecond {
			t.Errorf("events arrived too fast (gap=%v), SSE may be buffered", gap)
		}
	}
}

func TestClientDisconnectCancelsUpstream(t *testing.T) {
	upstreamCanceled := make(chan bool, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)
		// Send one event so the client gets a response
		fmt.Fprint(w, "data: {\"hello\":true}\n\n")
		flusher.Flush()

		// Then block until canceled
		<-r.Context().Done()
		upstreamCanceled <- true
	}))
	defer upstream.Close()

	p := New(makeProviders(map[string]string{"test": upstream.URL}))
	server := httptest.NewServer(p)
	defer server.Close()

	resp, err := http.Post(server.URL+"/proxy/test/v1/stream", "application/json", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	// Read the first event then close
	buf := make([]byte, 256)
	_, _ = resp.Body.Read(buf)
	resp.Body.Close()

	select {
	case <-upstreamCanceled:
		// good — upstream was notified of client disconnect
	case <-time.After(2 * time.Second):
		t.Error("upstream was not canceled after client disconnect")
	}
}

func TestNonSSEResponsePassthrough(t *testing.T) {
	upstream := newMockUpstream(200, `{"models":["gpt-4"]}`)
	defer upstream.Close()

	p := New(makeProviders(map[string]string{"openai": upstream.URL}))

	req := httptest.NewRequest("GET", "/proxy/openai/v1/models", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); body != `{"models":["gpt-4"]}` {
		t.Errorf("body = %q", body)
	}
}

func TestLargeSSEEvent(t *testing.T) {
	// 100KB event
	largePayload := strings.Repeat("x", 100*1024)
	events := []string{
		fmt.Sprintf("data: %s\n\n", largePayload),
	}
	upstream := newMockSSEUpstream(events, 0)
	defer upstream.Close()

	p := New(makeProviders(map[string]string{"test": upstream.URL}))

	req := httptest.NewRequest("POST", "/proxy/test/v1/stream", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	body := rr.Body.String()
	if len(body) != len(events[0]) {
		t.Errorf("body length = %d, want %d (large event truncated?)", len(body), len(events[0]))
	}
}

func TestUpstreamErrorPassthrough(t *testing.T) {
	upstream := newMockUpstream(500, `{"error":"internal"}`)
	defer upstream.Close()

	p := New(makeProviders(map[string]string{"test": upstream.URL}))

	req := httptest.NewRequest("POST", "/proxy/test/v1/messages", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	// Without fallback, error should pass through
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

func TestRequestBodyForwarded(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	p := New(makeProviders(map[string]string{"test": upstream.URL}))

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/proxy/test/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if receivedBody != body {
		t.Errorf("upstream received body = %q, want %q", receivedBody, body)
	}
}
