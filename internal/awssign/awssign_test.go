package awssign

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewSigner_RequiresRegion(t *testing.T) {
	_, err := NewSigner(Config{
		AccessKeyID:     "AKID",
		SecretAccessKey: "secret",
	})
	if err == nil {
		t.Fatal("expected error for missing region")
	}
	if !strings.Contains(err.Error(), "region") {
		t.Fatalf("expected region error, got: %v", err)
	}
}

func TestNewSigner_DefaultService(t *testing.T) {
	s, err := NewSigner(Config{
		AccessKeyID:     "AKID",
		SecretAccessKey: "secret",
		Region:          "us-east-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.service != "bedrock" {
		t.Fatalf("expected default service 'bedrock', got %q", s.service)
	}
}

func TestNewSigner_CustomService(t *testing.T) {
	s, err := NewSigner(Config{
		AccessKeyID:     "AKID",
		SecretAccessKey: "secret",
		Region:          "us-west-2",
		Service:         "sagemaker",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.service != "sagemaker" {
		t.Fatalf("expected service 'sagemaker', got %q", s.service)
	}
}

func TestSign_AddsAuthorizationHeader(t *testing.T) {
	creds := Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	s := NewSignerWithCredentials(creds, "us-east-1", "bedrock")

	body := []byte(`{"prompt": "hello"}`)
	req, _ := http.NewRequest("POST",
		"https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-v2/invoke",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	if err := s.Sign(req); err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	auth := req.Header.Get("Authorization")
	if auth == "" {
		t.Fatal("expected Authorization header after signing")
	}
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Fatalf("expected AWS4-HMAC-SHA256 prefix, got: %s", auth)
	}
	if !strings.Contains(auth, "bedrock") {
		t.Fatalf("expected service 'bedrock' in credential scope, got: %s", auth)
	}
	if !strings.Contains(auth, "us-east-1") {
		t.Fatalf("expected region 'us-east-1' in credential scope, got: %s", auth)
	}
	if !strings.Contains(auth, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("expected access key in credential, got: %s", auth)
	}

	// Body should still be readable after signing
	bodyAfter, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("failed to read body after sign: %v", err)
	}
	if !bytes.Equal(bodyAfter, body) {
		t.Fatalf("body changed after sign: got %q, want %q", bodyAfter, body)
	}

	// Required SigV4 headers
	if req.Header.Get("X-Amz-Content-Sha256") == "" {
		t.Fatal("expected X-Amz-Content-Sha256 header")
	}
	if req.Header.Get("X-Amz-Date") == "" {
		t.Fatal("expected X-Amz-Date header")
	}
}

func TestSign_EmptyBody(t *testing.T) {
	creds := Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	s := NewSignerWithCredentials(creds, "us-east-1", "bedrock")

	req, _ := http.NewRequest("GET",
		"https://bedrock-runtime.us-east-1.amazonaws.com/", nil)

	if err := s.Sign(req); err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Fatalf("expected AWS4-HMAC-SHA256 prefix, got: %s", auth)
	}
}

func TestSign_SessionToken(t *testing.T) {
	creds := Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionToken:    "my-session-token",
	}
	s := NewSignerWithCredentials(creds, "us-east-1", "bedrock")

	req, _ := http.NewRequest("GET",
		"https://bedrock-runtime.us-east-1.amazonaws.com/", nil)

	if err := s.Sign(req); err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	if req.Header.Get("X-Amz-Security-Token") != "my-session-token" {
		t.Fatalf("expected X-Amz-Security-Token, got: %q", req.Header.Get("X-Amz-Security-Token"))
	}
}

func TestSign_Deterministic(t *testing.T) {
	creds := Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	fixedTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	body := []byte(`{"model":"claude-v2","prompt":"test"}`)

	// Sign twice with same time → same signature
	req1, _ := http.NewRequest("POST",
		"https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-v2/invoke",
		bytes.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	if err := SignRequestForTest(req1, creds, "us-east-1", "bedrock", fixedTime); err != nil {
		t.Fatal(err)
	}

	req2, _ := http.NewRequest("POST",
		"https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-v2/invoke",
		bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	if err := SignRequestForTest(req2, creds, "us-east-1", "bedrock", fixedTime); err != nil {
		t.Fatal(err)
	}

	auth1 := req1.Header.Get("Authorization")
	auth2 := req2.Header.Get("Authorization")
	if auth1 != auth2 {
		t.Fatalf("signatures differ:\n  %s\n  %s", auth1, auth2)
	}
}

func TestSha256Hex(t *testing.T) {
	// SHA256 of empty input
	got := sha256Hex(nil)
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Fatalf("sha256Hex(nil) = %q, want %q", got, want)
	}

	got = sha256Hex([]byte("hello"))
	want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("sha256Hex(hello) = %q, want %q", got, want)
	}
}

func TestParseINI(t *testing.T) {
	content := `
[default]
aws_access_key_id = AKID_DEFAULT
aws_secret_access_key = SECRET_DEFAULT

[bedrock]
aws_access_key_id = AKID_BEDROCK
aws_secret_access_key = SECRET_BEDROCK
aws_session_token = TOKEN_BEDROCK
`

	creds := parseINI(content, "default")
	if creds.AccessKeyID != "AKID_DEFAULT" {
		t.Errorf("default access key = %q", creds.AccessKeyID)
	}
	if creds.SecretAccessKey != "SECRET_DEFAULT" {
		t.Errorf("default secret key = %q", creds.SecretAccessKey)
	}

	creds = parseINI(content, "bedrock")
	if creds.AccessKeyID != "AKID_BEDROCK" {
		t.Errorf("bedrock access key = %q", creds.AccessKeyID)
	}
	if creds.SessionToken != "TOKEN_BEDROCK" {
		t.Errorf("bedrock session token = %q", creds.SessionToken)
	}

	// Non-existent profile
	creds = parseINI(content, "nonexistent")
	if creds.AccessKeyID != "" {
		t.Errorf("nonexistent profile should have empty creds, got %q", creds.AccessKeyID)
	}
}

func TestURIEncode(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{"hello world", "hello%20world"},
		{"a/b", "a%2Fb"},
		{"test@example.com", "test%40example.com"},
		{"", ""},
	}
	for _, tt := range tests {
		got := uriEncode(tt.in)
		if got != tt.want {
			t.Errorf("uriEncode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDeriveSigningKey(t *testing.T) {
	// Verify the key derivation chain produces non-empty output
	key := deriveSigningKey("secret", "20240115", "us-east-1", "bedrock")
	if len(key) != 32 { // HMAC-SHA256 output is 32 bytes
		t.Fatalf("expected 32-byte key, got %d bytes", len(key))
	}
}

func TestBuildCanonicalRequest(t *testing.T) {
	body := []byte(`{"prompt":"test"}`)
	payloadHash := sha256Hex(body)

	req, _ := http.NewRequest("POST",
		"https://example.com/path/to/resource?foo=bar&baz=qux",
		bytes.NewReader(body))
	req.Header.Set("Host", "example.com")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Date", "20240115T120000Z")

	signedHeaders := buildSignedHeaders(req)
	canonical := buildCanonicalRequest(req, signedHeaders, payloadHash)

	if !strings.HasPrefix(canonical, "POST\n/path/to/resource\n") {
		t.Fatalf("unexpected canonical request start: %s", canonical)
	}
	if !strings.Contains(canonical, "baz=qux&foo=bar") {
		t.Fatalf("expected sorted query params, got: %s", canonical)
	}
}
