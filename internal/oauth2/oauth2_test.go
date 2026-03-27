package oauth2

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Helpers ---

func generateTestServiceAccountJSON(t *testing.T, tokenURI string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	sa := map[string]string{
		"type":         "service_account",
		"client_email": "test@test-project.iam.gserviceaccount.com",
		"private_key":  string(privPEM),
		"token_uri":    tokenURI,
	}
	data, _ := json.Marshal(sa)
	return data
}

func generateTestServiceAccountJSONPKCS8(t *testing.T, tokenURI string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal PKCS8: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	})

	sa := map[string]string{
		"type":         "service_account",
		"client_email": "test@test-project.iam.gserviceaccount.com",
		"private_key":  string(privPEM),
		"token_uri":    tokenURI,
	}
	data, _ := json.Marshal(sa)
	return data
}

func newFakeTokenServer(t *testing.T, accessToken string, expiresIn int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": accessToken,
			"expires_in":   expiresIn,
			"token_type":   "Bearer",
		})
	}))
}

// --- Token validity tests ---

func TestToken_Valid(t *testing.T) {
	tests := []struct {
		name  string
		token *Token
		want  bool
	}{
		{"nil token", nil, false},
		{"empty access token", &Token{AccessToken: "", ExpiresAt: time.Now().Add(time.Hour)}, false},
		{"expired", &Token{AccessToken: "tok", ExpiresAt: time.Now().Add(-time.Minute)}, false},
		{"expires within 60s margin", &Token{AccessToken: "tok", ExpiresAt: time.Now().Add(30 * time.Second)}, false},
		{"valid", &Token{AccessToken: "tok", ExpiresAt: time.Now().Add(5 * time.Minute)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Client Credentials tests ---

func TestClientCredentials_Success(t *testing.T) {
	server := newFakeTokenServer(t, "cc-access-token-123", 3600)
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "my-client-id",
		ClientSecret: "my-client-secret",
	})

	tok, err := src.Token()
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if tok.AccessToken != "cc-access-token-123" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "cc-access-token-123")
	}
	if time.Until(tok.ExpiresAt) < 3500*time.Second {
		t.Errorf("ExpiresAt too soon: %v", tok.ExpiresAt)
	}
}

func TestClientCredentials_CachesToken(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "cached-token",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "id",
		ClientSecret: "secret",
	})

	// Call Token() twice — should only hit server once
	tok1, err := src.Token()
	if err != nil {
		t.Fatalf("first Token() error: %v", err)
	}
	tok2, err := src.Token()
	if err != nil {
		t.Fatalf("second Token() error: %v", err)
	}

	if tok1.AccessToken != tok2.AccessToken {
		t.Errorf("tokens differ: %q vs %q", tok1.AccessToken, tok2.AccessToken)
	}
	if callCount.Load() != 1 {
		t.Errorf("server called %d times, want 1", callCount.Load())
	}
}

func TestClientCredentials_RefreshesExpiredToken(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": fmt.Sprintf("token-%d", n),
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "id",
		ClientSecret: "secret",
	})

	// Get first token
	tok1, err := src.Token()
	if err != nil {
		t.Fatalf("first Token() error: %v", err)
	}

	// Force expiration
	ccSrc := src.(*clientCredentialsSource)
	ccSrc.mu.Lock()
	ccSrc.token.ExpiresAt = time.Now().Add(-time.Minute)
	ccSrc.mu.Unlock()

	// Should refresh
	tok2, err := src.Token()
	if err != nil {
		t.Fatalf("second Token() error: %v", err)
	}

	if tok1.AccessToken == tok2.AccessToken {
		t.Error("expected different tokens after expiration")
	}
	if callCount.Load() != 2 {
		t.Errorf("server called %d times, want 2", callCount.Load())
	}
}

func TestClientCredentials_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "bad-id",
		ClientSecret: "bad-secret",
	})

	_, err := src.Token()
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want mention of 401", err.Error())
	}
}

func TestClientCredentials_OAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":             "invalid_grant",
			"error_description": "bad credentials",
		})
	}))
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "id",
		ClientSecret: "secret",
	})

	_, err := src.Token()
	if err == nil {
		t.Fatal("expected error for OAuth error response")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %q, want mention of invalid_grant", err.Error())
	}
}

func TestClientCredentials_SendsCorrectParams(t *testing.T) {
	var receivedParams map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		receivedParams = map[string]string{
			"grant_type":    r.FormValue("grant_type"),
			"client_id":     r.FormValue("client_id"),
			"client_secret": r.FormValue("client_secret"),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "my-id",
		ClientSecret: "my-secret",
	})
	src.Token()

	if receivedParams["grant_type"] != "client_credentials" {
		t.Errorf("grant_type = %q", receivedParams["grant_type"])
	}
	if receivedParams["client_id"] != "my-id" {
		t.Errorf("client_id = %q", receivedParams["client_id"])
	}
	if receivedParams["client_secret"] != "my-secret" {
		t.Errorf("client_secret = %q", receivedParams["client_secret"])
	}
}

func TestClientCredentials_Concurrent(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond) // simulate slow token endpoint
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "concurrent-token",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "id",
		ClientSecret: "secret",
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := src.Token()
			if err != nil {
				t.Errorf("Token() error: %v", err)
				return
			}
			if tok.AccessToken != "concurrent-token" {
				t.Errorf("AccessToken = %q", tok.AccessToken)
			}
		}()
	}
	wg.Wait()

	// With proper locking, server should be called very few times (ideally 1)
	if callCount.Load() > 3 {
		t.Errorf("server called %d times, expected ≤3 with mutex", callCount.Load())
	}
}

// --- Service Account tests ---

func TestServiceAccount_Success(t *testing.T) {
	server := newFakeTokenServer(t, "sa-access-token-456", 3600)
	defer server.Close()

	saJSON := generateTestServiceAccountJSON(t, server.URL)

	src, err := NewServiceAccountSource(ServiceAccountConfig{
		CredentialsJSON: saJSON,
		Scopes:          []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		t.Fatalf("NewServiceAccountSource error: %v", err)
	}

	tok, err := src.Token()
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if tok.AccessToken != "sa-access-token-456" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "sa-access-token-456")
	}
}

func TestServiceAccount_PKCS8Key(t *testing.T) {
	server := newFakeTokenServer(t, "pkcs8-token", 3600)
	defer server.Close()

	saJSON := generateTestServiceAccountJSONPKCS8(t, server.URL)

	src, err := NewServiceAccountSource(ServiceAccountConfig{
		CredentialsJSON: saJSON,
		Scopes:          []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		t.Fatalf("NewServiceAccountSource error: %v", err)
	}

	tok, err := src.Token()
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if tok.AccessToken != "pkcs8-token" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
}

func TestServiceAccount_CachesToken(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "cached-sa-token",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	saJSON := generateTestServiceAccountJSON(t, server.URL)
	src, err := NewServiceAccountSource(ServiceAccountConfig{
		CredentialsJSON: saJSON,
		Scopes:          []string{"scope1"},
	})
	if err != nil {
		t.Fatalf("NewServiceAccountSource error: %v", err)
	}

	src.Token()
	src.Token()

	if callCount.Load() != 1 {
		t.Errorf("server called %d times, want 1", callCount.Load())
	}
}

func TestServiceAccount_JWTFormat(t *testing.T) {
	var receivedAssertion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		receivedAssertion = r.FormValue("assertion")
		grantType := r.FormValue("grant_type")
		if grantType != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", grantType)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "jwt-verified",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	saJSON := generateTestServiceAccountJSON(t, server.URL)
	src, err := NewServiceAccountSource(ServiceAccountConfig{
		CredentialsJSON: saJSON,
		Scopes:          []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		t.Fatalf("NewServiceAccountSource error: %v", err)
	}

	src.Token()

	// JWT should have 3 parts separated by dots
	parts := strings.Split(receivedAssertion, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts, want 3", len(parts))
	}

	// Decode and verify claims
	claimsJSON, err := base64RawURLDecode(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}

	if claims["iss"] != "test@test-project.iam.gserviceaccount.com" {
		t.Errorf("iss = %v", claims["iss"])
	}
	if claims["scope"] != "https://www.googleapis.com/auth/cloud-platform" {
		t.Errorf("scope = %v", claims["scope"])
	}
	if claims["aud"] != server.URL {
		t.Errorf("aud = %v, want %v", claims["aud"], server.URL)
	}
}

func TestServiceAccount_InvalidJSON(t *testing.T) {
	_, err := NewServiceAccountSource(ServiceAccountConfig{
		CredentialsJSON: []byte("not json"),
		Scopes:          []string{"scope"},
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestServiceAccount_WrongType(t *testing.T) {
	data, _ := json.Marshal(map[string]string{
		"type":         "authorized_user",
		"client_email": "test@test.com",
		"private_key":  "key",
	})
	_, err := NewServiceAccountSource(ServiceAccountConfig{
		CredentialsJSON: data,
		Scopes:          []string{"scope"},
	})
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
	if !strings.Contains(err.Error(), "service_account") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestServiceAccount_MissingClientEmail(t *testing.T) {
	data, _ := json.Marshal(map[string]string{
		"type":        "service_account",
		"private_key": "key",
	})
	_, err := NewServiceAccountSource(ServiceAccountConfig{
		CredentialsJSON: data,
		Scopes:          []string{"scope"},
	})
	if err == nil {
		t.Fatal("expected error for missing client_email")
	}
}

func TestServiceAccount_InvalidPrivateKey(t *testing.T) {
	data, _ := json.Marshal(map[string]string{
		"type":         "service_account",
		"client_email": "test@test.com",
		"private_key":  "not-a-pem-key",
	})
	_, err := NewServiceAccountSource(ServiceAccountConfig{
		CredentialsJSON: data,
		Scopes:          []string{"scope"},
	})
	if err == nil {
		t.Fatal("expected error for invalid private key")
	}
}

func TestServiceAccount_DefaultTokenURI(t *testing.T) {
	// Generate with empty token_uri, should default to Google's
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	data, _ := json.Marshal(map[string]string{
		"type":         "service_account",
		"client_email": "test@test.com",
		"private_key":  string(privPEM),
		// no token_uri
	})

	src, err := NewServiceAccountSource(ServiceAccountConfig{
		CredentialsJSON: data,
		Scopes:          []string{"scope"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Access internal state to verify default
	saSrc := src.(*serviceAccountSource)
	if saSrc.key.TokenURI != "https://oauth2.googleapis.com/token" {
		t.Errorf("TokenURI = %q, want Google default", saSrc.key.TokenURI)
	}
}

// helper
func base64RawURLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func TestClientCredentials_WithScopes(t *testing.T) {
	var receivedScope string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		receivedScope = r.FormValue("scope")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "scoped-token",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "id",
		ClientSecret: "secret",
		Scopes:       []string{"https://cognitiveservices.azure.com/.default"},
	})

	tok, err := src.Token()
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if tok.AccessToken != "scoped-token" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if receivedScope != "https://cognitiveservices.azure.com/.default" {
		t.Errorf("scope = %q, want %q", receivedScope, "https://cognitiveservices.azure.com/.default")
	}
}

func TestClientCredentials_WithMultipleScopes(t *testing.T) {
	var receivedScope string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		receivedScope = r.FormValue("scope")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "multi-scope-token",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "id",
		ClientSecret: "secret",
		Scopes:       []string{"scope1", "scope2", "scope3"},
	})

	src.Token()

	if receivedScope != "scope1 scope2 scope3" {
		t.Errorf("scope = %q, want %q", receivedScope, "scope1 scope2 scope3")
	}
}

func TestClientCredentials_WithoutScopes_NoScopeParam(t *testing.T) {
	var hasScopeParam bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		_, hasScopeParam = r.Form["scope"]
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "no-scope-token",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	src := NewClientCredentialsSource(ClientCredentialsConfig{
		TokenURL:     server.URL,
		ClientID:     "id",
		ClientSecret: "secret",
		// No Scopes
	})

	src.Token()

	if hasScopeParam {
		t.Error("scope param should not be sent when Scopes is empty")
	}
}
