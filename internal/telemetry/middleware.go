package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Middleware wraps an http.Handler with OTel tracing and metrics.
// It extracts provider name from the URL path (/proxy/{provider}/...) and
// model from the JSON request body.
func Middleware(p *Provider, next http.Handler) http.Handler {
	if p == nil {
		return next // telemetry disabled, passthrough
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Extract provider from path: /proxy/{provider}/...
		provider := extractProvider(r.URL.Path)

		// Extract model from request body (best-effort, don't fail if can't parse)
		model := ""
		if r.Body != nil && r.ContentLength != 0 {
			bodyBytes, err := io.ReadAll(r.Body)
			if err == nil {
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				model = extractModel(bodyBytes)
			}
		}

		// Extract token name from context (set by auth middleware)
		tokenName := r.Header.Get("X-Yai-Token-Name")

		// Start span
		ctx, span := p.Tracer.Start(r.Context(), "yai.proxy",
			trace.WithAttributes(
				attribute.String("yai.provider", provider),
				attribute.String("yai.model", model),
				attribute.String("yai.token_name", tokenName),
			),
		)
		defer span.End()

		// Wrap response writer to capture status code
		rw := &statusRecorder{ResponseWriter: w, statusCode: 200}

		// Serve
		next.ServeHTTP(rw, r.WithContext(ctx))

		duration := time.Since(start).Seconds()

		// Record span attributes
		span.SetAttributes(attribute.Int("http.status_code", rw.statusCode))
		if rw.statusCode >= 400 {
			span.SetStatus(codes.Error, http.StatusText(rw.statusCode))
		}

		// Record metrics
		attrs := metric.WithAttributes(
			attribute.String("provider", provider),
			attribute.String("model", model),
			attribute.String("token_name", tokenName),
			attribute.Int("status", rw.statusCode),
		)
		p.RequestCounter.Add(ctx, 1, attrs)
		p.RequestDuration.Record(ctx, duration, metric.WithAttributes(
			attribute.String("provider", provider),
		))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode    int
	headerWritten bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.headerWritten {
		r.statusCode = code
		r.headerWritten = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func extractProvider(path string) string {
	// /proxy/{provider}/...
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return "unknown"
}

func extractModel(body []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &req) == nil && req.Model != "" {
		return req.Model
	}
	return ""
}
