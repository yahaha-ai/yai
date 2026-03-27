package config

import (
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration for YAML unmarshaling.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = dur
	return nil
}

func (d Duration) MarshalYAML() (interface{}, error) {
	return d.Duration.String(), nil
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type TokenEntry struct {
	Name  string `yaml:"name"`
	Token string `yaml:"token"`
}

type AuthConfig struct {
	Tokens []TokenEntry `yaml:"tokens"`
}

type ProviderAuth struct {
	Type            string `yaml:"type"`             // "bearer", "x-api-key", "query-param", "oauth2-client-credentials", "oauth2-service-account", "oauth2-azure-ad", "none"
	Key             string `yaml:"key"`
	ParamName       string `yaml:"param_name"`       // for query-param auth, e.g. "key"
	TokenURL        string `yaml:"token_url"`        // for oauth2-client-credentials
	ClientID        string `yaml:"client_id"`        // for oauth2-client-credentials, oauth2-azure-ad
	ClientSecret    string `yaml:"client_secret"`    // for oauth2-client-credentials, oauth2-azure-ad
	CredentialsFile string `yaml:"credentials_file"` // for oauth2-service-account (GCP JSON path)
	Scopes          []string `yaml:"scopes"`         // for oauth2-service-account, oauth2-azure-ad (optional)
	TenantID        string `yaml:"tenant_id"`        // for oauth2-azure-ad
}

type HealthCheckConfig struct {
	Method   string   `yaml:"method"`
	Path     string   `yaml:"path"`
	Interval Duration `yaml:"interval"`
	Timeout  Duration `yaml:"timeout"`
}

type ProviderConfig struct {
	Name         string            `yaml:"name"`
	Upstream     string            `yaml:"upstream"`
	Auth         ProviderAuth      `yaml:"auth"`
	ExtraHeaders map[string]string `yaml:"extra_headers"`
	HealthCheck  HealthCheckConfig `yaml:"health_check"`
}

type RetryConfig struct {
	MaxAttempts int      `yaml:"max_attempts"`
	Timeout     Duration `yaml:"timeout"`
}

type FallbackGroup struct {
	Name      string   `yaml:"name"`
	Providers []string `yaml:"providers"`
	Strategy  string   `yaml:"strategy"`
	Retry     RetryConfig `yaml:"retry"`
}

type FallbackConfig struct {
	Groups []FallbackGroup `yaml:"groups"`
}

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Auth     AuthConfig     `yaml:"auth"`
	Providers []ProviderConfig `yaml:"providers"`
	Fallback FallbackConfig `yaml:"fallback"`
}

// Parse reads YAML from r and returns a validated Config.
func Parse(r io.Reader) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("yaml decode: %w", err)
	}
	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if p.HealthCheck.Method == "" {
			p.HealthCheck.Method = "GET"
		}
		if p.HealthCheck.Interval.Duration == 0 {
			p.HealthCheck.Interval.Duration = 30 * time.Second
		}
		if p.HealthCheck.Timeout.Duration == 0 {
			p.HealthCheck.Timeout.Duration = 5 * time.Second
		}
	}
}

var validAuthTypes = map[string]bool{
	"bearer":                     true,
	"x-api-key":                  true,
	"query-param":                true,
	"oauth2-client-credentials":  true,
	"oauth2-service-account":     true,
	"oauth2-azure-ad":            true,
	"none":                       true,
}

func validate(cfg *Config) error {
	if len(cfg.Auth.Tokens) == 0 {
		return fmt.Errorf("auth: at least one token is required")
	}
	for i, tok := range cfg.Auth.Tokens {
		if tok.Name == "" {
			return fmt.Errorf("auth.tokens[%d]: name is required", i)
		}
		if tok.Token == "" {
			return fmt.Errorf("auth.tokens[%d]: token is required", i)
		}
	}

	providerNames := make(map[string]bool)
	for i, p := range cfg.Providers {
		if p.Name == "" {
			return fmt.Errorf("providers[%d]: name is required", i)
		}
		if p.Upstream == "" {
			return fmt.Errorf("providers[%d] %q: upstream is required", i, p.Name)
		}
		if !validAuthTypes[p.Auth.Type] {
			return fmt.Errorf("providers[%d] %q: invalid auth type %q (valid: bearer, x-api-key, query-param, oauth2-client-credentials, oauth2-service-account, oauth2-azure-ad, none)", i, p.Name, p.Auth.Type)
		}
		if p.Auth.Type == "query-param" && p.Auth.ParamName == "" {
			return fmt.Errorf("providers[%d] %q: auth type query-param requires param_name", i, p.Name)
		}
		if p.Auth.Type == "oauth2-client-credentials" {
			if p.Auth.TokenURL == "" {
				return fmt.Errorf("providers[%d] %q: auth type oauth2-client-credentials requires token_url", i, p.Name)
			}
			if p.Auth.ClientID == "" {
				return fmt.Errorf("providers[%d] %q: auth type oauth2-client-credentials requires client_id", i, p.Name)
			}
			if p.Auth.ClientSecret == "" {
				return fmt.Errorf("providers[%d] %q: auth type oauth2-client-credentials requires client_secret", i, p.Name)
			}
		}
		if p.Auth.Type == "oauth2-azure-ad" {
			if p.Auth.TenantID == "" {
				return fmt.Errorf("providers[%d] %q: auth type oauth2-azure-ad requires tenant_id", i, p.Name)
			}
			if p.Auth.ClientID == "" {
				return fmt.Errorf("providers[%d] %q: auth type oauth2-azure-ad requires client_id", i, p.Name)
			}
			if p.Auth.ClientSecret == "" {
				return fmt.Errorf("providers[%d] %q: auth type oauth2-azure-ad requires client_secret", i, p.Name)
			}
		}
		if p.Auth.Type == "oauth2-service-account" {
			if p.Auth.CredentialsFile == "" {
				return fmt.Errorf("providers[%d] %q: auth type oauth2-service-account requires credentials_file", i, p.Name)
			}
		}
		providerNames[p.Name] = true
	}

	for _, g := range cfg.Fallback.Groups {
		for _, pname := range g.Providers {
			if !providerNames[pname] {
				return fmt.Errorf("fallback group %q: provider %q not found in providers list", g.Name, pname)
			}
		}
	}

	return nil
}
