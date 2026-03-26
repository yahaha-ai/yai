package fallback

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/yahaha-ai/yai/internal/config"
	"github.com/yahaha-ai/yai/internal/health"
	"github.com/yahaha-ai/yai/internal/proxy"
)

// Handler wraps the proxy with fallback logic.
// When a request to a provider in a fallback group fails with a retriable error,
// it tries the next provider in the group.
type Handler struct {
	proxy   *proxy.Proxy
	checker *health.Checker
	// providerToGroup maps provider name to its fallback group
	providerToGroup map[string]*config.FallbackGroup
}

// New creates a Handler.
func New(p *proxy.Proxy, checker *health.Checker, groups []config.FallbackGroup) *Handler {
	ptg := make(map[string]*config.FallbackGroup)
	for i := range groups {
		g := &groups[i]
		for _, pname := range g.Providers {
			ptg[pname] = g
		}
	}
	return &Handler{
		proxy:           p,
		checker:         checker,
		providerToGroup: ptg,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract provider name from /proxy/{provider}/...
	providerName := extractProvider(r.URL.Path)

	group, inGroup := h.providerToGroup[providerName]
	if !inGroup {
		// No fallback group — direct proxy
		h.proxy.ServeHTTP(w, r)
		return
	}

	// Buffer body for potential retries
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			writeError(w, 502, "failed to read request body")
			return
		}
		r.Body.Close()
	}

	// Try each provider in group order
	for _, candidate := range group.Providers {
		// Clone the request for this attempt
		attemptReq := r.Clone(r.Context())
		if bodyBytes != nil {
			attemptReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			attemptReq.ContentLength = int64(len(bodyBytes))
		}

		// Rewrite path to target this provider
		path := r.URL.Path
		origPrefix := "/proxy/" + providerName + "/"
		rest := strings.TrimPrefix(path, origPrefix)
		if path == "/proxy/"+providerName {
			rest = ""
		}
		attemptReq.URL.Path = "/proxy/" + candidate + "/" + rest

		// Use httptest.ResponseRecorder to capture the response
		rec := httptest.NewRecorder()
		h.proxy.ServeHTTP(rec, attemptReq)

		if !isRetriable(rec.Code) {
			// Success or client error — forward to real response writer
			copyRecorder(w, rec)
			return
		}

		// Retriable error — try next provider
	}

	// All providers failed
	writeError(w, 502, "all providers in fallback group failed")
}

// isRetriable returns true for status codes that should trigger fallback.
// 429 (rate limit) and 5xx (server errors) are retriable.
// 4xx (except 429) are client errors and should not trigger fallback.
func isRetriable(code int) bool {
	return code == 429 || code >= 500
}

func extractProvider(path string) string {
	// /proxy/{provider}/... → provider
	trimmed := strings.TrimPrefix(path, "/proxy/")
	if idx := strings.Index(trimmed, "/"); idx != -1 {
		return trimmed[:idx]
	}
	return trimmed
}

func copyRecorder(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	w.Write(rec.Body.Bytes())
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
