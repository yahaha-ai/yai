package telemetry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yahaha-ai/yai/internal/config"
)

func TestNew_Disabled(t *testing.T) {
	p, err := New(context.Background(), config.TelemetryConfig{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatal("expected nil provider when disabled")
	}
}

func TestMiddleware_Passthrough_WhenNil(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})

	handler := Middleware(nil, inner)
	req := httptest.NewRequest("POST", "/proxy/anthropic/v1/messages", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Fatal("inner handler not called")
	}
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestExtractProvider(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/proxy/anthropic/v1/messages", "anthropic"},
		{"/proxy/deepseek/chat", "deepseek"},
		{"/proxy/gemini", "gemini"},
		{"/health", "unknown"},
	}
	for _, tt := range tests {
		got := extractProvider(tt.path)
		if got != tt.want {
			t.Errorf("extractProvider(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestExtractModel(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{`{"model":"claude-3-5-sonnet-20241022","messages":[]}`, "claude-3-5-sonnet-20241022"},
		{`{"messages":[]}`, ""},
		{`invalid`, ""},
	}
	for _, tt := range tests {
		got := extractModel([]byte(tt.body))
		if got != tt.want {
			t.Errorf("extractModel(%q) = %q, want %q", tt.body, got, tt.want)
		}
	}
}

func TestMiddleware_ExtractsFields(t *testing.T) {
	// We can't easily assert on OTel internals without a test exporter,
	// but we can verify the middleware doesn't break the request flow
	// and correctly passes through the body.
	bodyContent := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify body is still readable after middleware reads it
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		if string(body) != bodyContent {
			t.Errorf("body = %q, want %q", string(body), bodyContent)
		}
		// Verify internal header is accessible
		tokenName := r.Header.Get("X-Yai-Token-Name")
		if tokenName != "test-user" {
			t.Errorf("X-Yai-Token-Name = %q, want %q", tokenName, "test-user")
		}
		w.WriteHeader(200)
	})

	handler := Middleware(nil, inner) // nil provider = passthrough

	req := httptest.NewRequest("POST", "/proxy/openai/v1/chat/completions",
		bytes.NewReader([]byte(bodyContent)))
	req.Header.Set("X-Yai-Token-Name", "test-user")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestStatusRecorder_DefaultCode(t *testing.T) {
	w := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: w, statusCode: 200}

	// Writing without WriteHeader should keep default 200
	sr.Write([]byte("ok"))
	if sr.statusCode != 200 {
		t.Errorf("statusCode = %d, want 200", sr.statusCode)
	}
}

func TestStatusRecorder_CapturesCode(t *testing.T) {
	w := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: w, statusCode: 200}

	sr.WriteHeader(502)
	if sr.statusCode != 502 {
		t.Errorf("statusCode = %d, want 502", sr.statusCode)
	}

	// Second WriteHeader should not overwrite
	sr.WriteHeader(200)
	if sr.statusCode != 502 {
		t.Errorf("statusCode after second WriteHeader = %d, want 502", sr.statusCode)
	}
}

func TestStripScheme(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"http://localhost:4318", "localhost:4318"},
		{"https://otel.example.com:4318", "otel.example.com:4318"},
		{"localhost:4318", "localhost:4318"},
	}
	for _, tt := range tests {
		got := stripScheme(tt.input)
		if got != tt.want {
			t.Errorf("stripScheme(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsInsecure(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"http://localhost:4318", true},
		{"https://otel.example.com:4318", false},
		{"localhost:4318", false},
	}
	for _, tt := range tests {
		got := isInsecure(tt.input)
		if got != tt.want {
			t.Errorf("isInsecure(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
