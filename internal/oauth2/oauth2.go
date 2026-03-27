package oauth2

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Token represents an OAuth2 access token with expiry.
type Token struct {
	AccessToken string
	ExpiresAt   time.Time
}

// Valid reports whether the token is present and not expired (with 60s margin).
func (t *Token) Valid() bool {
	return t != nil && t.AccessToken != "" && time.Now().Before(t.ExpiresAt.Add(-60*time.Second))
}

// TokenSource provides access tokens. Implementations handle refresh logic.
type TokenSource interface {
	// Token returns a valid access token, refreshing if necessary.
	Token() (*Token, error)
}

// --- Client Credentials (百度千帆 etc.) ---

// ClientCredentialsConfig for OAuth2 client_credentials grant.
type ClientCredentialsConfig struct {
	TokenURL     string // e.g. "https://aip.baidubce.com/oauth/2.0/token"
	ClientID     string
	ClientSecret string
	Scopes       []string // optional; if set, sent as space-joined "scope" param (required by Azure AD)
	HTTPClient   *http.Client // optional, defaults to http.DefaultClient
}

type clientCredentialsSource struct {
	cfg   ClientCredentialsConfig
	mu    sync.Mutex
	token *Token
}

// NewClientCredentialsSource creates a TokenSource using client_credentials grant.
func NewClientCredentialsSource(cfg ClientCredentialsConfig) TokenSource {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &clientCredentialsSource{cfg: cfg}
}

func (s *clientCredentialsSource) Token() (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token.Valid() {
		return s.token, nil
	}

	token, err := s.refresh()
	if err != nil {
		return nil, err
	}
	s.token = token
	return token, nil
}

func (s *clientCredentialsSource) refresh() (*Token, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {s.cfg.ClientID},
		"client_secret": {s.cfg.ClientSecret},
	}
	if len(s.cfg.Scopes) > 0 {
		data.Set("scope", joinScopes(s.cfg.Scopes))
	}

	resp, err := s.cfg.HTTPClient.PostForm(s.cfg.TokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("oauth2 token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth2 read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("oauth2 token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("oauth2 parse response: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("oauth2 error: %s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("oauth2 response missing access_token")
	}

	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	return &Token{
		AccessToken: tokenResp.AccessToken,
		ExpiresAt:   expiresAt,
	}, nil
}

// --- GCP Service Account (Vertex AI) ---

// ServiceAccountConfig for GCP service account JWT-based OAuth2.
type ServiceAccountConfig struct {
	CredentialsJSON []byte   // raw service account JSON
	Scopes          []string // e.g. ["https://www.googleapis.com/auth/cloud-platform"]
	HTTPClient      *http.Client
}

// ServiceAccountKey represents the relevant fields from a GCP service account JSON.
type ServiceAccountKey struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	TokenURI     string `json:"token_uri"`
}

type serviceAccountSource struct {
	cfg    ServiceAccountConfig
	key    *ServiceAccountKey
	rsaKey *rsa.PrivateKey
	mu     sync.Mutex
	token  *Token
}

// NewServiceAccountSource creates a TokenSource from a GCP service account JSON.
func NewServiceAccountSource(cfg ServiceAccountConfig) (TokenSource, error) {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}

	var key ServiceAccountKey
	if err := json.Unmarshal(cfg.CredentialsJSON, &key); err != nil {
		return nil, fmt.Errorf("parse service account JSON: %w", err)
	}
	if key.Type != "service_account" {
		return nil, fmt.Errorf("expected type 'service_account', got %q", key.Type)
	}
	if key.ClientEmail == "" {
		return nil, fmt.Errorf("service account JSON missing client_email")
	}
	if key.PrivateKey == "" {
		return nil, fmt.Errorf("service account JSON missing private_key")
	}
	if key.TokenURI == "" {
		key.TokenURI = "https://oauth2.googleapis.com/token"
	}

	rsaKey, err := parseRSAPrivateKey(key.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	return &serviceAccountSource{
		cfg:    cfg,
		key:    &key,
		rsaKey: rsaKey,
	}, nil
}

func (s *serviceAccountSource) Token() (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token.Valid() {
		return s.token, nil
	}

	token, err := s.refresh()
	if err != nil {
		return nil, err
	}
	s.token = token
	return token, nil
}

func (s *serviceAccountSource) refresh() (*Token, error) {
	now := time.Now()
	jwt, err := s.signJWT(now)
	if err != nil {
		return nil, fmt.Errorf("sign JWT: %w", err)
	}

	data := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwt},
	}

	resp, err := s.cfg.HTTPClient.PostForm(s.key.TokenURI, data)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("response missing access_token: %s", body)
	}

	expiresAt := now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	return &Token{
		AccessToken: tokenResp.AccessToken,
		ExpiresAt:   expiresAt,
	}, nil
}

func joinScopes(scopes []string) string {
	return strings.Join(scopes, " ")
}

func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}

	// Try PKCS8 first (newer format), then PKCS1
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS8 key is not RSA")
		}
		return rsaKey, nil
	}

	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
