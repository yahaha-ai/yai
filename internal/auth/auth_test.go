package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yahaha-ai/yai/internal/ratelimit"
)

func makeTokenMap(tokens map[string]string) map[string]TokenInfo {
	m := make(map[string]TokenInfo)
	for tok, name := range tokens {
		m[tok] = TokenInfo{Name: name}
	}
	return m
}

func TestValidToken(t *testing.T) {
	tokens := makeTokenMap(map[string]string{
		"yai_xxx": "cicada-main",
		"yai_yyy": "cicada-pad",
	})
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestInvalidToken(t *testing.T) {
	tokens := makeTokenMap(map[string]string{
		"yai_xxx": "cicada-main",
	})
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer yai_wrong")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMissingHeader(t *testing.T) {
	tokens := makeTokenMap(map[string]string{
		"yai_xxx": "cicada-main",
	})
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMalformedHeader(t *testing.T) {
	tokens := makeTokenMap(map[string]string{
		"yai_xxx": "cicada-main",
	})
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
	req.Header.Set("Authorization", "yai_xxx") // missing "Bearer " prefix
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestHealthEndpointBypassesAuth(t *testing.T) {
	tokens := makeTokenMap(map[string]string{
		"yai_xxx": "cicada-main",
	})
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d for /health", rr.Code, http.StatusOK)
	}
}

func TestRateLimit_Enforced(t *testing.T) {
	limit := ratelimit.Limit{Count: 2, Window: 60_000_000_000} // 2/min
	tokens := map[string]TokenInfo{
		"yai_xxx": {Name: "cicada", Limiter: ratelimit.NewLimiter(limit)},
	}
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 2 requests should pass
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
		req.Header.Set("Authorization", "Bearer yai_xxx")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want %d", i+1, rr.Code, http.StatusOK)
		}
	}

	// 3rd should be rate limited
	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("3rd request: status = %d, want %d", rr.Code, http.StatusTooManyRequests)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}
	if rr.Header().Get("X-RateLimit-Limit") != "2" {
		t.Errorf("X-RateLimit-Limit = %q, want '2'", rr.Header().Get("X-RateLimit-Limit"))
	}
}

func TestRateLimit_NoLimiter(t *testing.T) {
	tokens := map[string]TokenInfo{
		"yai_xxx": {Name: "cicada"}, // no limiter
	}
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Should always pass — no rate limit configured
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
		req.Header.Set("Authorization", "Bearer yai_xxx")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i+1, rr.Code, http.StatusOK)
		}
	}
}

func TestRateLimit_Headers(t *testing.T) {
	limit := ratelimit.Limit{Count: 5, Window: 60_000_000_000}
	tokens := map[string]TokenInfo{
		"yai_xxx": {Name: "cicada", Limiter: ratelimit.NewLimiter(limit)},
	}
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer yai_xxx")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Header().Get("X-RateLimit-Limit") != "5" {
		t.Errorf("X-RateLimit-Limit = %q, want '5'", rr.Header().Get("X-RateLimit-Limit"))
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "4" {
		t.Errorf("X-RateLimit-Remaining = %q, want '4'", rr.Header().Get("X-RateLimit-Remaining"))
	}
}
