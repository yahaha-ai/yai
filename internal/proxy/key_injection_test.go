package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yahaha-ai/yai/internal/config"
	"github.com/yahaha-ai/yai/internal/oauth2"
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

// --- OAuth2 key injection tests ---

// staticTokenSource returns a fixed token (for testing).
type staticTokenSource struct {
	token *oauth2.Token
	err   error
}

func (s *staticTokenSource) Token() (*oauth2.Token, error) {
	return s.token, s.err
}

func TestKeyInjection_OAuth2ClientCredentials(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "baidu",
			Upstream: hc.Server.URL,
			Auth: config.ProviderAuth{
				Type:         "oauth2-client-credentials",
				TokenURL:     "http://fake.example.com/token",
				ClientID:     "test-id",
				ClientSecret: "test-secret",
			},
		},
	}

	ts := &staticTokenSource{
		token: &oauth2.Token{
			AccessToken: "baidu-dynamic-token-789",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}

	p := New(providers, WithTokenSource("baidu", ts))
	req := httptest.NewRequest("POST", "/proxy/baidu/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	// Should inject dynamic bearer token from TokenSource
	if got := hc.Get("Authorization"); got != "Bearer baidu-dynamic-token-789" {
		t.Errorf("Authorization = %q, want Bearer baidu-dynamic-token-789", got)
	}
}

func TestKeyInjection_OAuth2ServiceAccount(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "vertex",
			Upstream: hc.Server.URL,
			Auth: config.ProviderAuth{
				Type:            "oauth2-service-account",
				CredentialsFile: "/tmp/nonexistent.json",
				Scopes:          []string{"https://www.googleapis.com/auth/cloud-platform"},
			},
		},
	}

	ts := &staticTokenSource{
		token: &oauth2.Token{
			AccessToken: "vertex-sa-token-456",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}

	p := New(providers, WithTokenSource("vertex", ts))
	req := httptest.NewRequest("POST", "/proxy/vertex/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	if got := hc.Get("Authorization"); got != "Bearer vertex-sa-token-456" {
		t.Errorf("Authorization = %q, want Bearer vertex-sa-token-456", got)
	}
}

func TestKeyInjection_OAuth2StripsClientAuth(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "vertex",
			Upstream: hc.Server.URL,
			Auth: config.ProviderAuth{
				Type:            "oauth2-service-account",
				CredentialsFile: "/tmp/nonexistent.json",
			},
		},
	}

	ts := &staticTokenSource{
		token: &oauth2.Token{
			AccessToken: "real-token",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}

	p := New(providers, WithTokenSource("vertex", ts))
	req := httptest.NewRequest("POST", "/proxy/vertex/v1/chat", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer yai_should_be_stripped")
	req.Header.Set("X-Api-Key", "should-also-be-stripped")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	// Real token should replace client auth
	if got := hc.Get("Authorization"); got != "Bearer real-token" {
		t.Errorf("Authorization = %q, want Bearer real-token", got)
	}
	// X-Api-Key should be stripped
	if hc.Has("X-Api-Key") {
		t.Errorf("X-Api-Key should be stripped, got %q", hc.Get("X-Api-Key"))
	}
}

func TestKeyInjection_OAuth2TokenError(t *testing.T) {
	hc := newHeaderCaptureServer()
	defer hc.Server.Close()

	providers := []config.ProviderConfig{
		{
			Name:     "broken",
			Upstream: hc.Server.URL,
			Auth: config.ProviderAuth{
				Type:         "oauth2-client-credentials",
				TokenURL:     "http://fake.example.com/token",
				ClientID:     "id",
				ClientSecret: "secret",
			},
		},
	}

	ts := &staticTokenSource{
		err: fmt.Errorf("token refresh failed"),
	}

	p := New(providers, WithTokenSource("broken", ts))
	req := httptest.NewRequest("POST", "/proxy/broken/v1/chat", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	// Request still goes through (upstream will likely reject it)
	// No Authorization header should be set
	if hc.Has("Authorization") {
		t.Errorf("Authorization should not be set when token refresh fails, got %q", hc.Get("Authorization"))
	}
}
