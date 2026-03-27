package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/yahaha-ai/yai/internal/config"
)

// Proxy is an HTTP handler that routes /proxy/{provider}/... to the configured upstream.
type Proxy struct {
	providers map[string]*providerProxy
}

type providerProxy struct {
	config config.ProviderConfig
	target *url.URL
	proxy  *httputil.ReverseProxy
}

// New creates a Proxy from provider configs.
func New(providers []config.ProviderConfig) *Proxy {
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
		rp := &httputil.ReverseProxy{
			Director:      pp.director,
			FlushInterval: -1,
		}
		pp.proxy = rp
		p.providers[cfg.Name] = pp
	}
	return p
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
	json.NewEncoder(w).Encode(v)
}
