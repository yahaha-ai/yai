package auth

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/yahaha-ai/yai/internal/ratelimit"
)

// TokenInfo holds the metadata for an auth token.
type TokenInfo struct {
	Name    string
	Limiter *ratelimit.Limiter // nil if no rate limit configured
}

// Middleware returns an HTTP handler that checks for a valid Bearer token
// and enforces per-token rate limits.
// The tokens map is token -> TokenInfo. /health is exempt from auth.
func Middleware(tokens map[string]TokenInfo, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health endpoint is public
		if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/health/") {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing Authorization header")
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "Authorization header must start with 'Bearer '")
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		info, ok := tokens[token]
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		// Rate limit check
		if info.Limiter != nil {
			result := info.Limiter.Allow(info.Name)
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", result.Limit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", result.Remaining))
			if !result.Allowed {
				retryAfter := int(math.Ceil(result.RetryAfter.Seconds()))
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
