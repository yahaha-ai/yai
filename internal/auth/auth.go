package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Middleware returns an HTTP handler that checks for a valid Bearer token.
// The tokens map is token -> name. /health is exempt from auth.
func Middleware(tokens map[string]string, next http.Handler) http.Handler {
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
		if _, ok := tokens[token]; !ok {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
