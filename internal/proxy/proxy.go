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
		rp := &httputil.ReverseProxy{
			Director: makeDirector(target),
			// FlushInterval -1 means flush immediately — critical for SSE streaming
			FlushInterval: -1,
		}
		p.providers[cfg.Name] = &providerProxy{
			config: cfg,
			target: target,
			proxy:  rp,
		}
	}
	return p
}

func makeDirector(target *url.URL) func(*http.Request) {
	return func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		// Path is already stripped of /proxy/{provider} prefix by ServeHTTP
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
