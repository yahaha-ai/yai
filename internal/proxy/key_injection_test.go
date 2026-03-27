package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yahaha-ai/yai/internal/config"
)

// headerCapture is a test server that records all received headers.
type headerCapture struct {
	mu      sync.Mutex
	Headers http.Header
	Server  *httptest.Server
}

func newHeaderCaptureServer() *headerCapture {
	hc := &headerCapture{
		Headers: make(http.Header),
	}
	hc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hc.mu.Lock()
		for k, v := range r.Header {
			hc.Headers[k] = v
		}
		hc.mu.Unlock()
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	return hc
}

func (hc *headerCapture) Get(key string) string {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	return hc.Headers.Get(key)
}

func (hc *headerCapture) Has(key string) bool {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	_, ok := hc.Headers[http.CanonicalHeaderKey(key)]
	return ok
}

func TestKeyInjection_Anthropic(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "anthropic",
			Upstream: hc.Server.URL,
			Auth:     config.ProviderAuth{Type: "x-api-key", Key: "sk-ant-api03-real"},
			ExtraHeaders: map[string]string{
				"anthropic-version": "2023-06-01",
			},
		},
	}

	p := New(providers)
	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_token_should_be_stripped")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	// Should have X-Api-Key set to the real key
	if got := hc.Get("X-Api-Key"); got != "sk-ant-api03-real" {
		t.Errorf("X-Api-Key = %q, want %q", got, "sk-ant-api03-real")
	}

	// Should have anthropic-version from extra_headers
	if got := hc.Get("Anthropic-Version"); got != "2023-06-01" {
		t.Errorf("Anthropic-Version = %q, want %q", got, "2023-06-01")
	}

	// Should NOT have the client's Authorization header
	if got := hc.Get("Authorization"); got == "Bearer yai_token_should_be_stripped" {
		t.Error("client's yai Authorization header was not stripped")
	}
}

func TestKeyInjection_Bearer(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "openai",
			Upstream: hc.Server.URL,
			Auth:     config.ProviderAuth{Type: "bearer", Key: "sk-openai-real"},
		},
	}

	p := New(providers)
	req := httptest.NewRequest("POST", "/proxy/openai/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	if got := hc.Get("Authorization"); got != "Bearer sk-openai-real" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sk-openai-real")
	}
}

func TestKeyInjection_DeepSeek(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "deepseek",
			Upstream: hc.Server.URL,
			Auth:     config.ProviderAuth{Type: "bearer", Key: "sk-deepseek-real"},
		},
	}

	p := New(providers)
	req := httptest.NewRequest("POST", "/proxy/deepseek/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if got := hc.Get("Authorization"); got != "Bearer sk-deepseek-real" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sk-deepseek-real")
	}
}

func TestKeyInjection_None(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "ollama",
			Upstream: hc.Server.URL,
			Auth:     config.ProviderAuth{Type: "none"},
		},
	}

	p := New(providers)
	req := httptest.NewRequest("POST", "/proxy/ollama/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	// Should NOT forward any auth header
	if hc.Has("Authorization") {
		t.Errorf("Authorization header should not be forwarded for auth type 'none', got %q", hc.Get("Authorization"))
	}
	if hc.Has("X-Api-Key") {
		t.Error("X-Api-Key header should not be present for auth type 'none'")
	}
}

func TestKeyInjection_ExtraHeaders(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "anthropic",
			Upstream: hc.Server.URL,
			Auth:     config.ProviderAuth{Type: "x-api-key", Key: "sk-ant-xxx"},
			ExtraHeaders: map[string]string{
				"anthropic-version": "2023-06-01",
				"anthropic-beta":    "interleaved-thinking-2025-05-14",
			},
		},
	}

	p := New(providers)
	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if got := hc.Get("Anthropic-Version"); got != "2023-06-01" {
		t.Errorf("Anthropic-Version = %q, want %q", got, "2023-06-01")
	}
	if got := hc.Get("Anthropic-Beta"); got != "interleaved-thinking-2025-05-14" {
		t.Errorf("Anthropic-Beta = %q, want %q", got, "interleaved-thinking-2025-05-14")
	}
}

// queryCapture records the query parameters received by the upstream.
type queryCapture struct {
	mu     sync.Mutex
	Query  map[string]string
	Server *httptest.Server
}

func newQueryCaptureServer() *queryCapture {
	qc := &queryCapture{
		Query: make(map[string]string),
	}
	qc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qc.mu.Lock()
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				qc.Query[k] = v[0]
			}
		}
		qc.mu.Unlock()
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	return qc
}

func TestKeyInjection_QueryParam(t *testing.T) {
	qc := newQueryCaptureServer()
	defer qc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "gemini",
			Upstream: qc.Server.URL,
			Auth:     config.ProviderAuth{Type: "query-param", Key: "AIzaSyFAKEKEY", ParamName: "key"},
		},
	}

	p := New(providers)
	req := httptest.NewRequest("POST", "/proxy/gemini/v1beta/models/gemini-2.5-flash:generateContent", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	qc.mu.Lock()
	defer qc.mu.Unlock()

	if got, ok := qc.Query["key"]; !ok || got != "AIzaSyFAKEKEY" {
		t.Errorf("query param key = %q, want %q", got, "AIzaSyFAKEKEY")
	}
}

func TestKeyInjection_QueryParamStripsAuthHeader(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "gemini",
			Upstream: hc.Server.URL,
			Auth:     config.ProviderAuth{Type: "query-param", Key: "AIzaSyFAKEKEY", ParamName: "key"},
		},
	}

	p := New(providers)
	req := httptest.NewRequest("POST", "/proxy/gemini/v1beta/models/gemini-2.5-flash:generateContent", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	// Should NOT forward any auth headers
	if hc.Has("Authorization") {
		t.Errorf("Authorization header should be stripped for query-param auth, got %q", hc.Get("Authorization"))
	}
}

func TestKeyInjection_QueryParamPreservesExistingParams(t *testing.T) {
	qc := newQueryCaptureServer()
	defer qc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "gemini",
			Upstream: qc.Server.URL,
			Auth:     config.ProviderAuth{Type: "query-param", Key: "AIzaSyFAKEKEY", ParamName: "key"},
		},
	}

	p := New(providers)
	// Request with existing query param
	req := httptest.NewRequest("POST", "/proxy/gemini/v1beta/models/gemini-2.5-flash:generateContent?alt=sse", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	qc.mu.Lock()
	defer qc.mu.Unlock()

	if got := qc.Query["key"]; got != "AIzaSyFAKEKEY" {
		t.Errorf("query param key = %q, want %q", got, "AIzaSyFAKEKEY")
	}
	if got := qc.Query["alt"]; got != "sse" {
		t.Errorf("query param alt = %q, want %q (should preserve existing params)", got, "sse")
	}
}
