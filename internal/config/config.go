package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// ServerConfig defines a backend MCP server to connect to.
type ServerConfig struct {
	ID          string            `json:"id"`
	URL         string            `json:"url"`
	Namespace   string            `json:"namespace"`
	Credentials *CredentialConfig `json:"credentials,omitempty"`

	// Per-server operational settings
	RateLimit      *RateLimitConfig      `json:"rate_limit,omitempty"`
	CircuitBreaker *CircuitBreakerConfig `json:"circuit_breaker,omitempty"`
	Timeout        Duration              `json:"timeout,omitempty"`
}

// CredentialConfig describes how Prism obtains the backend credential at call time.
// The agent never sees the raw value — Prism injects it into each outbound request.
//
// Supported types:
//   - "static"  — fixed header/value (API keys, long-lived Bearer tokens)
//   - "env"     — resolved from an environment variable at call time
//   - "file"    — read from a file path (mounted secrets, k8s service account tokens)
//   - "command" — execute a shell command and use stdout (AWS STS, Vault CLI, gcloud, etc.)
type CredentialConfig struct {
	// Type is one of: static, env, file, command.
	Type string `json:"type"`
	// Header is the HTTP header to set. Defaults to "Authorization".
	Header string `json:"header,omitempty"`
	// Value is the literal credential value. Required for type "static".
	Value string `json:"value,omitempty"`
	// EnvVar is the environment variable name. Required for type "env".
	EnvVar string `json:"env_var,omitempty"`
	// Path is the file to read. Required for type "file".
	Path string `json:"path,omitempty"`
	// Command is the shell command to execute. Required for type "command".
	Command string `json:"command,omitempty"`
	// TTL is how long to cache the result of a "command" credential.
	// Defaults to 5m if unset.
	TTL Duration `json:"ttl,omitempty"`
}

// AuthConfig holds client-facing authentication settings.
// Supports both simple API key auth and full OAuth 2.1.
type AuthConfig struct {
	// Simple API key auth (for development / internal use)
	Header    string   `json:"header,omitempty"`
	ValidKeys []string `json:"valid_keys,omitempty"`

	// OAuth 2.1 Resource Server auth (for production / agentic use)
	OAuth *OAuthConfig `json:"oauth,omitempty"`
}

// OAuthConfig configures OAuth 2.1 token validation.
// Prism acts as a Resource Server (RFC 9728) — it validates tokens
// issued by an external Authorization Server.
type OAuthConfig struct {
	// IssuerURL is the expected token issuer.
	IssuerURL string `json:"issuer_url"`

	// JWKSURL overrides automatic JWKS discovery. Optional.
	JWKSURL string `json:"jwks_url,omitempty"`

	// Audience is the gateway's resource identifier (per RFC 8707).
	// Tokens not issued for this audience are rejected.
	Audience string `json:"audience"`

	// ResourceURI is the canonical URI of this gateway for Protected Resource Metadata.
	// Defaults to "https://localhost" + ListenAddr if not set.
	ResourceURI string `json:"resource_uri,omitempty"`

	// RequiredScopes are scopes that MUST be present on every token.
	RequiredScopes []string `json:"required_scopes,omitempty"`

	// ScopesSupported lists all scopes this gateway recognizes.
	// Published in the Protected Resource Metadata document.
	ScopesSupported []string `json:"scopes_supported,omitempty"`

	// MaxTokenAge limits how old a token can be from issuance. Optional.
	MaxTokenAge Duration `json:"max_token_age,omitempty"`
}

// RateLimitConfig holds rate-limiting parameters.
type RateLimitConfig struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	Burst             int     `json:"burst"`
}

// CircuitBreakerConfig holds circuit-breaker parameters.
type CircuitBreakerConfig struct {
	Threshold   int      `json:"threshold"`
	Timeout     Duration `json:"timeout"`
	MaxHalfOpen int      `json:"max_half_open"`
}

// AuditConfig configures structured JSON audit logging for tool calls.
// When enabled, every tool call (allowed or denied) is written as a
// single-line JSON entry to the configured output.
type AuditConfig struct {
	// Enabled turns audit logging on or off.
	Enabled bool `json:"enabled"`
	// Output is where audit entries are written.
	// Accepted values: "stderr" (default), "stdout", or an absolute file path.
	Output string `json:"output,omitempty"`
}

// Config is the top-level gateway configuration.
type Config struct {
	ListenAddr      string                `json:"listen_addr"`
	AdminAddr       string                `json:"admin_addr"`
	Servers         []ServerConfig        `json:"servers"`
	Auth            *AuthConfig           `json:"auth,omitempty"`
	Audit           *AuditConfig          `json:"audit,omitempty"`
	RateLimit       *RateLimitConfig      `json:"rate_limit,omitempty"`
	CircuitBreaker  *CircuitBreakerConfig `json:"circuit_breaker,omitempty"`
	ShutdownTimeout Duration              `json:"shutdown_timeout,omitempty"`

	// ResourceURI is the canonical URI of this gateway (per RFC 8707).
	// Used for token audience validation and Protected Resource Metadata.
	ResourceURI string `json:"resource_uri,omitempty"`
}

// Duration is a time.Duration that marshals/unmarshals as a JSON string.
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// Try as number (nanoseconds)
		var n int64
		if err2 := json.Unmarshal(b, &n); err2 != nil {
			return err
		}
		*d = Duration(time.Duration(n))
		return nil
	}
	if s == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// Load reads a JSON config file, applies defaults, and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("config must contain a single JSON object")
	}

	applyDefaults(&cfg)
	return &cfg, validate(&cfg)
}

func applyDefaults(cfg *Config) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	if cfg.AdminAddr == "" {
		cfg.AdminAddr = ":9090"
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = Duration(10 * time.Second)
	}
	for i := range cfg.Servers {
		if cfg.Servers[i].Namespace == "" {
			cfg.Servers[i].Namespace = cfg.Servers[i].ID
		}
		if cfg.Servers[i].Timeout == 0 {
			cfg.Servers[i].Timeout = Duration(30 * time.Second)
		}
	}
}

func validate(cfg *Config) error {
	if cfg.ListenAddr == "" {
		return errors.New("listen_addr is required")
	}
	if len(cfg.Servers) == 0 {
		return errors.New("at least one server is required")
	}

	seenIDs := make(map[string]int, len(cfg.Servers))
	seenNamespaces := make(map[string]int, len(cfg.Servers))

	for i, s := range cfg.Servers {
		if s.ID == "" {
			return fmt.Errorf("server[%d]: id is required", i)
		}
		if s.URL == "" {
			return fmt.Errorf("server[%d]: url is required", i)
		}
		if !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") {
			return fmt.Errorf("server[%d]: url must start with http:// or https://", i)
		}
		if prev, dup := seenIDs[s.ID]; dup {
			return fmt.Errorf("server[%d]: duplicate id %q (first at server[%d])", i, s.ID, prev)
		}
		seenIDs[s.ID] = i

		ns := s.Namespace
		if ns == "" {
			ns = s.ID
		}
		if prev, dup := seenNamespaces[ns]; dup {
			return fmt.Errorf("server[%d]: duplicate namespace %q (first at server[%d])", i, ns, prev)
		}
		seenNamespaces[ns] = i

		if s.Credentials != nil {
			if err := validateCredential(i, s.Credentials); err != nil {
				return err
			}
		}

		if s.CircuitBreaker != nil {
			if s.CircuitBreaker.Threshold <= 0 {
				return fmt.Errorf("server[%d].circuit_breaker.threshold must be > 0", i)
			}
		}
		if s.RateLimit != nil {
			if s.RateLimit.RequestsPerSecond <= 0 {
				return fmt.Errorf("server[%d].rate_limit.requests_per_second must be > 0", i)
			}
			if s.RateLimit.Burst <= 0 {
				return fmt.Errorf("server[%d].rate_limit.burst must be > 0", i)
			}
		}
	}

	if cfg.RateLimit != nil {
		if cfg.RateLimit.RequestsPerSecond <= 0 {
			return errors.New("rate_limit.requests_per_second must be > 0")
		}
		if cfg.RateLimit.Burst <= 0 {
			return errors.New("rate_limit.burst must be > 0")
		}
	}

	return nil
}

func validateCredential(idx int, c *CredentialConfig) error {
	prefix := fmt.Sprintf("server[%d].credentials", idx)
	switch c.Type {
	case "static":
		if c.Value == "" {
			return fmt.Errorf("%s: type %q requires a non-empty value", prefix, c.Type)
		}
	case "env":
		if c.EnvVar == "" {
			return fmt.Errorf("%s: type %q requires env_var", prefix, c.Type)
		}
	case "file":
		if c.Path == "" {
			return fmt.Errorf("%s: type %q requires path", prefix, c.Type)
		}
	case "command":
		if c.Command == "" {
			return fmt.Errorf("%s: type %q requires command", prefix, c.Type)
		}
	case "":
		return fmt.Errorf("%s: type is required", prefix)
	default:
		return fmt.Errorf("%s: unknown type %q (must be static, env, file, or command)", prefix, c.Type)
	}
	return nil
}
