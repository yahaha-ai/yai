package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidToken(t *testing.T) {
	tokens := map[string]string{
		"yai_xxx": "cicada-main",
		"yai_yyy": "cicada-pad",
	}
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
	tokens := map[string]string{
		"yai_xxx": "cicada-main",
	}
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
	tokens := map[string]string{
		"yai_xxx": "cicada-main",
	}
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
	// no Authorization header
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMalformedHeader(t *testing.T) {
	tokens := map[string]string{
		"yai_xxx": "cicada-main",
	}
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
	tokens := map[string]string{
		"yai_xxx": "cicada-main",
	}
	handler := Middleware(tokens, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	// no auth header
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d for /health", rr.Code, http.StatusOK)
	}
}
