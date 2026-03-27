package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/yahaha-ai/yai/internal/awssign"
	"github.com/yahaha-ai/yai/internal/config"
	"github.com/yahaha-ai/yai/internal/oauth2"
)

// TokenSource provides dynamic access tokens for oauth2 auth types.
type TokenSource = oauth2.TokenSource

// Proxy is an HTTP handler that routes /proxy/{provider}/... to the configured upstream.
type Proxy struct {
	providers map[string]*providerProxy
}

type providerProxy struct {
	config      config.ProviderConfig
	target      *url.URL
	proxy       *httputil.ReverseProxy
	tokenSource TokenSource  // non-nil for oauth2 auth types
	awsSigner   *awssign.Signer // non-nil for aws-sigv4 auth type
}

// Option configures Proxy creation.
type Option func(*options)

type options struct {
	tokenSources map[string]TokenSource // provider name -> token source override
}

// WithTokenSource overrides the TokenSource for a specific provider (for testing).
func WithTokenSource(providerName string, ts TokenSource) Option {
	return func(o *options) {
		if o.tokenSources == nil {
			o.tokenSources = make(map[string]TokenSource)
		}
		o.tokenSources[providerName] = ts
	}
}

// New creates a Proxy from provider configs.
func New(providers []config.ProviderConfig, opts ...Option) *Proxy {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	p := &Proxy{
		providers: make(map[string]*providerProxy),
	}
	for _, cfg := range providers {
		target, err := url.Parse(cfg.Upstream)
		if err != nil {
			continue
		}
		pp := &providerProxy{
			config: cfg,
			target: target,
		}

		// Set up token source for oauth2 types
		if ts, ok := o.tokenSources[cfg.Name]; ok {
			pp.tokenSource = ts
		} else {
			ts, err := createTokenSource(cfg)
			if err != nil {
				log.Printf("WARN: provider %q: failed to create token source: %v", cfg.Name, err)
				continue
			}
			pp.tokenSource = ts // may be nil for non-oauth2 types
		}

		// Set up AWS SigV4 signer
		if cfg.Auth.Type == "aws-sigv4" {
			signer, err := awssign.NewSigner(awssign.Config{
				AccessKeyID:     cfg.Auth.AWSAccessKey,
				SecretAccessKey: cfg.Auth.AWSSecretKey,
				Region:          cfg.Auth.AWSRegion,
				Service:         cfg.Auth.AWSService,
				Profile:         cfg.Auth.AWSProfile,
			})
			if err != nil {
				log.Printf("WARN: provider %q: failed to create AWS signer: %v", cfg.Name, err)
				continue
			}
			pp.awsSigner = signer
		}

		rp := &httputil.ReverseProxy{
			Director:      pp.director,
			FlushInterval: -1,
		}

		// Per-provider transport with configured timeouts
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   cfg.Timeout.Connect.Duration,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: cfg.Timeout.Read.Duration,
			TLSHandshakeTimeout:   10 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
		}

		// For AWS SigV4, wrap the transport to sign requests after director rewrites
		if pp.awsSigner != nil {
			rp.Transport = &awsSignTransport{
				base:   transport,
				signer: pp.awsSigner,
			}
		} else {
			rp.Transport = transport
		}
		pp.proxy = rp
		p.providers[cfg.Name] = pp
	}
	return p
}

// createTokenSource creates a TokenSource based on provider auth config.
// Returns nil for non-oauth2 auth types.
func createTokenSource(cfg config.ProviderConfig) (TokenSource, error) {
	switch cfg.Auth.Type {
	case "oauth2-client-credentials":
		return oauth2.NewClientCredentialsSource(oauth2.ClientCredentialsConfig{
			TokenURL:     cfg.Auth.TokenURL,
			ClientID:     cfg.Auth.ClientID,
			ClientSecret: cfg.Auth.ClientSecret,
			Scopes:       cfg.Auth.Scopes,
		}), nil

	case "oauth2-azure-ad":
		tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", cfg.Auth.TenantID)
		scopes := cfg.Auth.Scopes
		if len(scopes) == 0 {
			scopes = []string{"https://cognitiveservices.azure.com/.default"}
		}
		return oauth2.NewClientCredentialsSource(oauth2.ClientCredentialsConfig{
			TokenURL:     tokenURL,
			ClientID:     cfg.Auth.ClientID,
			ClientSecret: cfg.Auth.ClientSecret,
			Scopes:       scopes,
		}), nil

	case "oauth2-service-account":
		data, err := os.ReadFile(cfg.Auth.CredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("read credentials file %q: %w", cfg.Auth.CredentialsFile, err)
		}
		scopes := cfg.Auth.Scopes
		if len(scopes) == 0 {
			scopes = []string{"https://www.googleapis.com/auth/cloud-platform"}
		}
		return oauth2.NewServiceAccountSource(oauth2.ServiceAccountConfig{
			CredentialsJSON: data,
			Scopes:          scopes,
		})

	default:
		return nil, nil
	}
}

// director rewrites the request: sets target host/scheme, strips client auth, injects real key.
func (pp *providerProxy) director(req *http.Request) {
	req.URL.Scheme = pp.target.Scheme
	req.URL.Host = pp.target.Host
	req.Host = pp.target.Host

	// Strip client's auth headers
	req.Header.Del("Authorization")
	req.Header.Del("X-Api-Key")

	// Inject real credentials based on auth type
	switch pp.config.Auth.Type {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+pp.config.Auth.Key)
	case "x-api-key":
		req.Header.Set("X-Api-Key", pp.config.Auth.Key)
	case "query-param":
		q := req.URL.Query()
		q.Set(pp.config.Auth.ParamName, pp.config.Auth.Key)
		req.URL.RawQuery = q.Encode()
	case "oauth2-client-credentials", "oauth2-service-account", "oauth2-azure-ad":
		if pp.tokenSource != nil {
			token, err := pp.tokenSource.Token()
			if err != nil {
				log.Printf("ERROR: provider %q: oauth2 token refresh failed: %v", pp.config.Name, err)
				// Request will likely fail upstream, but we don't abort in director
			} else {
				req.Header.Set("Authorization", "Bearer "+token.AccessToken)
			}
		}
	case "aws-sigv4":
		// Signing is handled by awsSignTransport after URL rewrite
	case "none":
		// no auth header
	}

	// Inject extra headers (e.g., anthropic-version)
	for k, v := range pp.config.ExtraHeaders {
		req.Header.Set(k, v)
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse /proxy/{provider}/rest/of/path
	path := r.URL.Path
	if !strings.HasPrefix(path, "/proxy/") {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}

	remainder := strings.TrimPrefix(path, "/proxy/")
	slashIdx := strings.Index(remainder, "/")

	var providerName, upstreamPath string
	if slashIdx == -1 {
		providerName = remainder
		upstreamPath = "/"
	} else {
		providerName = remainder[:slashIdx]
		upstreamPath = remainder[slashIdx:]
	}

	pp, ok := p.providers[providerName]
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "unknown provider: " + providerName})
		return
	}

	// Rewrite path to upstream path
	r.URL.Path = upstreamPath

	pp.proxy.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// awsSignTransport wraps an http.RoundTripper to sign requests with AWS SigV4.
// This runs after the director has rewritten the URL, so the signature covers
// the final destination host/path.
type awsSignTransport struct {
	base   http.RoundTripper
	signer *awssign.Signer
}

func (t *awsSignTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.signer.Sign(req); err != nil {
		return nil, fmt.Errorf("aws sigv4 sign: %w", err)
	}
	return t.base.RoundTrip(req)
}
