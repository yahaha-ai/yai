package awssign

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Signer signs HTTP requests with AWS Signature Version 4.
type Signer struct {
	region  string
	service string

	mu    sync.RWMutex
	creds Credentials
	// For credential refresh (e.g., STS assume-role results from shared config)
}

// Credentials holds AWS credentials.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string // optional, for temporary credentials
}

// Config for creating an AWS SigV4 signer.
type Config struct {
	// Static credentials (optional — if both are empty, uses default chain)
	AccessKeyID     string
	SecretAccessKey string

	Region  string // e.g. "us-east-1"
	Service string // e.g. "bedrock" (default)

	// Profile from ~/.aws/config (optional)
	Profile string
}

// NewSigner creates an AWS SigV4 signer.
func NewSigner(cfg Config) (*Signer, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("aws region is required")
	}
	if cfg.Service == "" {
		cfg.Service = "bedrock"
	}

	s := &Signer{
		region:  cfg.Region,
		service: cfg.Service,
	}

	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		s.creds = Credentials{
			AccessKeyID:     cfg.AccessKeyID,
			SecretAccessKey: cfg.SecretAccessKey,
		}
	} else {
		creds, err := resolveCredentials(cfg.Profile)
		if err != nil {
			return nil, fmt.Errorf("resolve aws credentials: %w", err)
		}
		s.creds = creds
	}

	return s, nil
}

// Sign signs an HTTP request with AWS SigV4.
// The request body is read, hashed, then restored so it can still be sent.
func (s *Signer) Sign(req *http.Request) error {
	s.mu.RLock()
	creds := s.creds
	s.mu.RUnlock()

	return signRequest(req, creds, s.region, s.service, time.Now().UTC())
}

// signRequest performs the actual SigV4 signing. Extracted for testability.
func signRequest(req *http.Request, creds Credentials, region, service string, now time.Time) error {
	// Read and buffer body
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return fmt.Errorf("read request body: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	payloadHash := sha256Hex(body)

	// Set required headers
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	// Ensure Host header is set
	if req.Header.Get("Host") == "" {
		req.Header.Set("Host", req.URL.Host)
	}

	// Build signed headers list
	signedHeaders := buildSignedHeaders(req)
	signedHeaderStr := strings.Join(signedHeaders, ";")

	// Canonical request
	canonicalReq := buildCanonicalRequest(req, signedHeaders, payloadHash)
	canonicalReqHash := sha256Hex([]byte(canonicalReq))

	// Credential scope
	credScope := dateStamp + "/" + region + "/" + service + "/aws4_request"

	// String to sign
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + credScope + "\n" + canonicalReqHash

	// Signing key
	signingKey := deriveSigningKey(creds.SecretAccessKey, dateStamp, region, service)

	// Signature
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Authorization header
	auth := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		creds.AccessKeyID, credScope, signedHeaderStr, signature)
	req.Header.Set("Authorization", auth)

	return nil
}

func buildSignedHeaders(req *http.Request) []string {
	headers := make([]string, 0, len(req.Header))
	for name := range req.Header {
		headers = append(headers, strings.ToLower(name))
	}
	sort.Strings(headers)
	return headers
}

func buildCanonicalRequest(req *http.Request, signedHeaders []string, payloadHash string) string {
	// Canonical URI
	path := req.URL.Path
	if path == "" {
		path = "/"
	}

	// Canonical query string
	query := req.URL.Query()
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var queryParts []string
	for _, k := range keys {
		vals := query[k]
		sort.Strings(vals)
		for _, v := range vals {
			queryParts = append(queryParts, uriEncode(k)+"="+uriEncode(v))
		}
	}
	canonicalQuery := strings.Join(queryParts, "&")

	// Canonical headers
	var canonicalHeaders strings.Builder
	for _, h := range signedHeaders {
		vals := req.Header.Values(http.CanonicalHeaderKey(h))
		canonicalHeaders.WriteString(h + ":" + strings.TrimSpace(strings.Join(vals, ",")) + "\n")
	}

	return strings.Join([]string{
		req.Method,
		path,
		canonicalQuery,
		canonicalHeaders.String(),
		strings.Join(signedHeaders, ";"),
		payloadHash,
	}, "\n")
}

func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// uriEncode encodes a string per AWS SigV4 rules (RFC 3986, but also encode '/').
func uriEncode(s string) string {
	var buf strings.Builder
	for _, b := range []byte(s) {
		if isUnreserved(b) {
			buf.WriteByte(b)
		} else {
			fmt.Fprintf(&buf, "%%%02X", b)
		}
	}
	return buf.String()
}

func isUnreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~'
}

// resolveCredentials loads credentials from environment, shared credentials file, or config file.
func resolveCredentials(profile string) (Credentials, error) {
	// 1. Environment variables
	if ak := os.Getenv("AWS_ACCESS_KEY_ID"); ak != "" {
		return Credentials{
			AccessKeyID:     ak,
			SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
			SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
		}, nil
	}

	if profile == "" {
		profile = os.Getenv("AWS_PROFILE")
	}
	if profile == "" {
		profile = "default"
	}

	// 2. Shared credentials file (~/.aws/credentials)
	credFile := os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	if credFile == "" {
		home, _ := os.UserHomeDir()
		credFile = filepath.Join(home, ".aws", "credentials")
	}

	creds, err := parseINICredentials(credFile, profile)
	if err == nil && creds.AccessKeyID != "" {
		return creds, nil
	}

	return Credentials{}, fmt.Errorf("no AWS credentials found (checked env vars and %s [%s])", credFile, profile)
}

// parseINICredentials is a minimal INI parser for AWS credential files.
func parseINICredentials(path, profile string) (Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	return parseINI(string(data), profile), nil
}

func parseINI(content, profile string) Credentials {
	var creds Credentials
	inSection := false
	target := "[" + profile + "]"

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inSection = (line == target)
			continue
		}
		if !inSection {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "aws_access_key_id":
			creds.AccessKeyID = val
		case "aws_secret_access_key":
			creds.SecretAccessKey = val
		case "aws_session_token":
			creds.SessionToken = val
		}
	}
	return creds
}

// CredentialsFromJSON loads credentials from a JSON file (for testing or alternative configs).
func CredentialsFromJSON(path string) (Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return Credentials{}, err
	}
	return creds, nil
}

// ResolveCredentialsForTest exposes resolveCredentials for testing.
var ResolveCredentialsForTest = resolveCredentials

// SignRequestForTest exposes signRequest for testing with a fixed time.
func SignRequestForTest(req *http.Request, creds Credentials, region, service string, now time.Time) error {
	return signRequest(req, creds, region, service, now)
}

// NewSignerWithCredentials creates a signer with explicit credentials (for testing).
func NewSignerWithCredentials(creds Credentials, region, service string) *Signer {
	if service == "" {
		service = "bedrock"
	}
	return &Signer{
		region:  region,
		service: service,
		creds:   creds,
	}
}

// unexported but needed for proxy package
func (s *Signer) SignWithContext(_ context.Context, req *http.Request) error {
	return s.Sign(req)
}
