package config

import (
	"strings"
	"testing"
)

func TestParseValidConfig(t *testing.T) {
	yaml := `
server:
  host: 0.0.0.0
  port: 8080

auth:
  tokens:
    - name: cicada-main
      token: yai_xxxxxxxxxxxx

providers:
  - name: anthropic
    upstream: https://api.anthropic.com
    auth:
      type: x-api-key
      key: sk-ant-api03-xxx
    extra_headers:
      anthropic-version: "2023-06-01"
    health_check:
      method: GET
      path: /v1/models
      interval: 30s
      timeout: 5s

  - name: deepseek
    upstream: https://api.deepseek.com
    auth:
      type: bearer
      key: sk-deepseek-xxx
    health_check:
      method: GET
      path: /v1/models
      interval: 30s
      timeout: 5s

  - name: ollama
    upstream: http://localhost:11434
    auth:
      type: none
    health_check:
      method: GET
      path: /v1/models
      interval: 10s
      timeout: 2s

fallback:
  groups:
    - name: openai-compat
      providers: [deepseek, ollama]
      strategy: priority
      retry:
        max_attempts: 2
        timeout: 30s
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Server
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("host = %q, want %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want %d", cfg.Server.Port, 8080)
	}

	// Auth tokens
	if len(cfg.Auth.Tokens) != 1 {
		t.Fatalf("tokens count = %d, want 1", len(cfg.Auth.Tokens))
	}
	if cfg.Auth.Tokens[0].Name != "cicada-main" {
		t.Errorf("token name = %q, want %q", cfg.Auth.Tokens[0].Name, "cicada-main")
	}
	if cfg.Auth.Tokens[0].Token != "yai_xxxxxxxxxxxx" {
		t.Errorf("token = %q, want %q", cfg.Auth.Tokens[0].Token, "yai_xxxxxxxxxxxx")
	}

	// Providers
	if len(cfg.Providers) != 3 {
		t.Fatalf("providers count = %d, want 3", len(cfg.Providers))
	}

	anthropic := cfg.Providers[0]
	if anthropic.Name != "anthropic" {
		t.Errorf("provider name = %q, want %q", anthropic.Name, "anthropic")
	}
	if anthropic.Upstream != "https://api.anthropic.com" {
		t.Errorf("upstream = %q", anthropic.Upstream)
	}
	if anthropic.Auth.Type != "x-api-key" {
		t.Errorf("auth type = %q, want %q", anthropic.Auth.Type, "x-api-key")
	}
	if anthropic.Auth.Key != "sk-ant-api03-xxx" {
		t.Errorf("auth key = %q", anthropic.Auth.Key)
	}
	if v, ok := anthropic.ExtraHeaders["anthropic-version"]; !ok || v != "2023-06-01" {
		t.Errorf("extra_headers anthropic-version = %q", v)
	}
	if anthropic.HealthCheck.Method != "GET" {
		t.Errorf("health_check method = %q", anthropic.HealthCheck.Method)
	}
	if anthropic.HealthCheck.Path != "/v1/models" {
		t.Errorf("health_check path = %q", anthropic.HealthCheck.Path)
	}
	if anthropic.HealthCheck.Interval.String() != "30s" {
		t.Errorf("health_check interval = %v", anthropic.HealthCheck.Interval)
	}
	if anthropic.HealthCheck.Timeout.String() != "5s" {
		t.Errorf("health_check timeout = %v", anthropic.HealthCheck.Timeout)
	}

	ollama := cfg.Providers[2]
	if ollama.Auth.Type != "none" {
		t.Errorf("ollama auth type = %q, want %q", ollama.Auth.Type, "none")
	}

	// Fallback
	if len(cfg.Fallback.Groups) != 1 {
		t.Fatalf("fallback groups = %d, want 1", len(cfg.Fallback.Groups))
	}
	group := cfg.Fallback.Groups[0]
	if group.Name != "openai-compat" {
		t.Errorf("group name = %q", group.Name)
	}
	if len(group.Providers) != 2 || group.Providers[0] != "deepseek" || group.Providers[1] != "ollama" {
		t.Errorf("group providers = %v", group.Providers)
	}
	if group.Strategy != "priority" {
		t.Errorf("group strategy = %q", group.Strategy)
	}
	if group.Retry.MaxAttempts != 2 {
		t.Errorf("retry max_attempts = %d", group.Retry.MaxAttempts)
	}
	if group.Retry.Timeout.String() != "30s" {
		t.Errorf("retry timeout = %v", group.Retry.Timeout)
	}
}

func TestValidate_MissingUpstream(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: broken
    auth:
      type: none
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing upstream")
	}
	if !strings.Contains(err.Error(), "upstream") {
		t.Errorf("error = %q, want mention of upstream", err.Error())
	}
}

func TestValidate_InvalidAuthType(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: broken
    upstream: https://example.com
    auth:
      type: magic
      key: xxx
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for invalid auth type")
	}
	if !strings.Contains(err.Error(), "auth type") {
		t.Errorf("error = %q, want mention of auth type", err.Error())
	}
}

func TestValidate_EmptyTokens(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens: []
providers:
  - name: test
    upstream: https://example.com
    auth:
      type: none
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for empty tokens")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error = %q, want mention of token", err.Error())
	}
}

func TestValidate_FallbackReferencesUnknownProvider(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: ollama
    upstream: http://localhost:11434
    auth:
      type: none
fallback:
  groups:
    - name: test-group
      providers: [ollama, nonexistent]
      strategy: priority
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for unknown provider in fallback")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error = %q, want mention of nonexistent", err.Error())
	}
}

func TestValidate_MissingProviderName(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - upstream: https://example.com
    auth:
      type: none
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing provider name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q, want mention of name", err.Error())
	}
}

func TestDefaultValues(t *testing.T) {
	yaml := `
server:
  port: 9090
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: ollama
    upstream: http://localhost:11434
    auth:
      type: none
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Default host
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("default host = %q, want %q", cfg.Server.Host, "0.0.0.0")
	}

	// Health check defaults
	hc := cfg.Providers[0].HealthCheck
	if hc.Method != "GET" {
		t.Errorf("default health_check method = %q, want GET", hc.Method)
	}
	if hc.Interval.String() != "30s" {
		t.Errorf("default health_check interval = %v, want 30s", hc.Interval)
	}
	if hc.Timeout.String() != "5s" {
		t.Errorf("default health_check timeout = %v, want 5s", hc.Timeout)
	}
}

func TestValidate_QueryParamAuth(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: gemini
    upstream: https://generativelanguage.googleapis.com
    auth:
      type: query-param
      key: AIzaSyXXXXXXXXXXXXXXXXX
      param_name: key
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Providers[0].Auth.Type != "query-param" {
		t.Errorf("auth type = %q, want query-param", cfg.Providers[0].Auth.Type)
	}
	if cfg.Providers[0].Auth.ParamName != "key" {
		t.Errorf("param_name = %q, want key", cfg.Providers[0].Auth.ParamName)
	}
}

func TestValidate_QueryParamMissingParamName(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: gemini
    upstream: https://generativelanguage.googleapis.com
    auth:
      type: query-param
      key: AIzaSyXXXXXXXXXXXXXXXXX
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for query-param without param_name")
	}
	if !strings.Contains(err.Error(), "param_name") {
		t.Errorf("error = %q, want mention of param_name", err.Error())
	}
}

func TestValidate_OAuth2ClientCredentials(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: baidu
    upstream: https://aip.baidubce.com
    auth:
      type: oauth2-client-credentials
      token_url: https://aip.baidubce.com/oauth/2.0/token
      client_id: my-client-id
      client_secret: my-client-secret
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := cfg.Providers[0].Auth
	if a.Type != "oauth2-client-credentials" {
		t.Errorf("type = %q", a.Type)
	}
	if a.TokenURL != "https://aip.baidubce.com/oauth/2.0/token" {
		t.Errorf("token_url = %q", a.TokenURL)
	}
	if a.ClientID != "my-client-id" {
		t.Errorf("client_id = %q", a.ClientID)
	}
	if a.ClientSecret != "my-client-secret" {
		t.Errorf("client_secret = %q", a.ClientSecret)
	}
}

func TestValidate_OAuth2ClientCredentialsMissingTokenURL(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: baidu
    upstream: https://aip.baidubce.com
    auth:
      type: oauth2-client-credentials
      client_id: id
      client_secret: secret
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing token_url")
	}
	if !strings.Contains(err.Error(), "token_url") {
		t.Errorf("error = %q, want mention of token_url", err.Error())
	}
}

func TestValidate_OAuth2ClientCredentialsMissingClientID(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: baidu
    upstream: https://aip.baidubce.com
    auth:
      type: oauth2-client-credentials
      token_url: https://example.com/token
      client_secret: secret
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing client_id")
	}
	if !strings.Contains(err.Error(), "client_id") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestValidate_OAuth2ClientCredentialsMissingClientSecret(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: baidu
    upstream: https://aip.baidubce.com
    auth:
      type: oauth2-client-credentials
      token_url: https://example.com/token
      client_id: id
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing client_secret")
	}
	if !strings.Contains(err.Error(), "client_secret") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestValidate_OAuth2ServiceAccount(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: vertex
    upstream: https://us-central1-aiplatform.googleapis.com
    auth:
      type: oauth2-service-account
      credentials_file: /path/to/service-account.json
      scopes:
        - https://www.googleapis.com/auth/cloud-platform
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := cfg.Providers[0].Auth
	if a.Type != "oauth2-service-account" {
		t.Errorf("type = %q", a.Type)
	}
	if a.CredentialsFile != "/path/to/service-account.json" {
		t.Errorf("credentials_file = %q", a.CredentialsFile)
	}
	if len(a.Scopes) != 1 || a.Scopes[0] != "https://www.googleapis.com/auth/cloud-platform" {
		t.Errorf("scopes = %v", a.Scopes)
	}
}

func TestValidate_OAuth2ServiceAccountMissingCredentialsFile(t *testing.T) {
	yaml := `
server:
  port: 8080
auth:
  tokens:
    - name: test
      token: yai_xxx
providers:
  - name: vertex
    upstream: https://us-central1-aiplatform.googleapis.com
    auth:
      type: oauth2-service-account
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing credentials_file")
	}
	if !strings.Contains(err.Error(), "credentials_file") {
		t.Errorf("error = %q", err.Error())
	}
}
