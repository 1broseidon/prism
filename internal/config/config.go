// Package config handles loading and validation of Prism gateway configuration.
//
// The configuration uses the standard mcpServers map format familiar from
// Claude Desktop, Claude Code, and other MCP clients — extended with Prism's
// policy, credential injection, and operational controls.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the top-level gateway configuration.
type Config struct {
	// Listen is the address for the MCP gateway. Default: ":8080".
	Listen string `json:"listen,omitempty"`

	// Admin is the address for the admin API. Default: ":9090".
	Admin string `json:"admin,omitempty"`

	// McpServers defines the backend MCP servers to aggregate.
	// The map key becomes both the server ID and the tool namespace.
	// Supports stdio (command) and HTTP (url) transports.
	McpServers map[string]McpServerConfig `json:"mcpServers"`

	// Policy defines who can access what. When policy.agents is present,
	// Prism embeds an OAuth 2.1 authorization server in-process.
	// When omitted, the gateway runs open (no auth).
	Policy *PolicyConfig `json:"policy,omitempty"`

	// Audit configures structured JSON audit logging for tool calls.
	Audit *AuditConfig `json:"audit,omitempty"`

	// RateLimit sets global rate limiting. Per-server overrides are also supported.
	RateLimit *RateLimitConfig `json:"rate_limit,omitempty"`

	// Store configures the key-value backend for persisting state (DCR clients,
	// refresh tokens). Default: bbolt at ~/.prism/prism.db.
	Store *StoreConfig `json:"store,omitempty"`

	// TLS enables HTTPS on the gateway listener.
	// When set, the gateway serves TLS directly — no reverse proxy needed.
	TLS *TLSConfig `json:"tls,omitempty"`

	// ShutdownTimeout is the graceful shutdown duration. Default: "10s".
	ShutdownTimeout Duration `json:"shutdown_timeout,omitempty"`
}

// StoreConfig configures the key-value backend.
type StoreConfig struct {
	// Type is "bbolt" (default) or "redis".
	Type string `json:"type,omitempty"`
	// URL is the Redis connection URL. Only used when type is "redis".
	URL string `json:"url,omitempty"`
	// Path overrides the bbolt file path. Default: ~/.prism/prism.db.
	Path string `json:"path,omitempty"`
}

// TLSConfig enables direct TLS termination on the gateway.
type TLSConfig struct {
	// Cert is the path to the PEM-encoded certificate (or chain).
	Cert string `json:"cert"`
	// Key is the path to the PEM-encoded private key.
	Key string `json:"key"`
}

// McpServerConfig defines a backend MCP server.
// Supports two transport modes:
//   - stdio: set "command" (and optionally "args", "env") — Prism spawns the process
//   - HTTP:  set "url" — Prism connects to an existing HTTP endpoint
type McpServerConfig struct {
	// --- Standard MCP fields (copy-paste from claude_desktop_config.json) ---

	// Command is the executable to spawn for stdio transport.
	Command string `json:"command,omitempty"`
	// Args are the command arguments.
	Args []string `json:"args,omitempty"`
	// Env sets environment variables for the spawned process.
	Env map[string]string `json:"env,omitempty"`

	// --- HTTP transport (alternative to command) ---

	// URL is the HTTP endpoint for an already-running MCP server.
	URL string `json:"url,omitempty"`

	// --- Prism extensions ---

	// Credentials configures how Prism authenticates to this backend.
	// The agent never sees the raw credential — Prism injects it into each outbound request.
	// Only applies to HTTP backends.
	Credentials *CredentialConfig `json:"credentials,omitempty"`

	// RateLimit sets per-server rate limiting.
	RateLimit *RateLimitConfig `json:"rate_limit,omitempty"`
	// CircuitBreaker sets per-server circuit breaker.
	CircuitBreaker *CircuitBreakerConfig `json:"circuit_breaker,omitempty"`
	// Timeout is the per-request timeout for this backend. Default: "30s".
	Timeout Duration `json:"timeout,omitempty"`
}

// IsStdio reports whether this server uses stdio transport (command-based).
func (m *McpServerConfig) IsStdio() bool { return m.Command != "" }

// PolicyConfig defines the authorization model.
type PolicyConfig struct {
	// Agents maps agent name → agent config. Each agent gets OAuth client credentials.
	// When present, Prism embeds an OAuth 2.1 authorization server.
	Agents map[string]AgentConfig `json:"agents,omitempty"`

	// Groups maps group name → group config. Agents reference groups by name.
	Groups map[string]GroupConfig `json:"groups,omitempty"`

	// DefaultScopes are granted to agents with no group membership (e.g. DCR pending).
	// Empty means no tool access until the operator adds the agent to a group.
	DefaultScopes []string `json:"default_scopes,omitempty"`
}

// AgentConfig defines an agent's credentials and permissions.
type AgentConfig struct {
	// Secret is the client_secret for OAuth client credentials grant.
	Secret string `json:"secret"`

	// Groups lists the groups this agent belongs to.
	Groups []string `json:"groups,omitempty"`

	// Grant adds scopes beyond what groups provide.
	Grant []string `json:"grant,omitempty"`

	// Deny removes scopes even if groups grant them. Deny wins over grant.
	Deny []string `json:"deny,omitempty"`
}

// GroupConfig defines a named set of scopes.
type GroupConfig struct {
	// Scopes are the permissions granted to members of this group.
	Scopes []string `json:"scopes"`
}

// CredentialConfig describes how Prism obtains a backend credential.
// The type is inferred from which field is set:
//   - Value   → static header/value (API keys, long-lived tokens)
//   - Env     → resolved from an environment variable at call time
//   - File    → read from a file path (mounted secrets, k8s tokens)
//   - Command → execute a shell command and use stdout (Vault, AWS STS, etc.)
type CredentialConfig struct {
	// Header is the HTTP header to set. Default: "Authorization".
	Header string `json:"header,omitempty"`
	// Value is the literal credential value (static type).
	Value string `json:"value,omitempty"`
	// Env is the environment variable name (env type).
	Env string `json:"env,omitempty"`
	// File is the path to read (file type).
	File string `json:"file,omitempty"`
	// Command is the shell command to execute (command type).
	Command string `json:"command,omitempty"`
	// TTL is the cache duration for command credentials. Default: "5m".
	TTL Duration `json:"ttl,omitempty"`
}

// InferredType returns the credential type based on which field is set.
func (c *CredentialConfig) InferredType() string {
	switch {
	case c.Value != "":
		return "static"
	case c.Env != "":
		return "env"
	case c.File != "":
		return "file"
	case c.Command != "":
		return "command"
	default:
		return ""
	}
}

// RateLimitConfig holds rate-limiting parameters.
type RateLimitConfig struct {
	RequestsPerSecond float64 `json:"rps"`
	Burst             int     `json:"burst"`
}

// CircuitBreakerConfig holds circuit-breaker parameters.
type CircuitBreakerConfig struct {
	Threshold   int      `json:"threshold"`
	Timeout     Duration `json:"timeout"`
	MaxHalfOpen int      `json:"max_half_open"`
}

// AuditConfig configures structured JSON audit logging for tool calls.
type AuditConfig struct {
	Enabled bool   `json:"enabled"`
	Output  string `json:"output,omitempty"` // "stderr" (default), "stdout", or file path
}

// --- Duration type ---

// Duration is a time.Duration that marshals/unmarshals as a JSON string.
type Duration time.Duration

// Duration returns the underlying time.Duration value.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// MarshalJSON encodes the duration as a JSON string (e.g. "5m30s").
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON decodes a JSON string (e.g. "5m30s") or number (nanoseconds).
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
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

// --- Expanded types (used internally by the gateway) ---

// ServerConfig is the internal representation of a backend server,
// expanded from the mcpServers map by Load().
type ServerConfig struct {
	ID        string
	Namespace string

	// Stdio transport
	Command []string
	Env     map[string]string

	// HTTP transport
	URL string

	// Prism extensions
	Credentials    *CredentialConfig
	RateLimit      *RateLimitConfig
	CircuitBreaker *CircuitBreakerConfig
	Timeout        Duration
}

// IsStdio reports whether this server uses stdio transport.
func (s *ServerConfig) IsStdio() bool { return len(s.Command) > 0 }

// EmbeddedAuthConfig carries the configuration for the embedded auth server
// when policy.agents is present. Produced by Load(), consumed by cmd/prism.
type EmbeddedAuthConfig struct {
	Issuer          string
	Clients         []EmbeddedClient
	TokenTTLSeconds int
	RequiredScopes  []string
	ScopesSupported []string
	DefaultScopes   []string
}

// EmbeddedClient is an agent identity for the embedded auth server.
type EmbeddedClient struct {
	ClientID      string
	ClientSecret  string
	AllowedScopes []string
}

// Loaded is the fully-resolved configuration returned by Load().
type Loaded struct {
	Listen          string
	Admin           string
	Servers         []ServerConfig
	EmbeddedAuth    *EmbeddedAuthConfig
	Store           *StoreConfig
	TLS             *TLSConfig
	Audit           *AuditConfig
	RateLimit       *RateLimitConfig
	ShutdownTimeout Duration
}

// --- Loading ---

// Load reads a JSON config file, applies defaults, validates, and expands
// the unified config into the internal Loaded representation.
func Load(path string) (*Loaded, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Config path is from CLI flag
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return expand(&cfg)
}

// --- Validation ---

func validate(cfg *Config) error {
	if len(cfg.McpServers) == 0 {
		return errors.New("mcpServers: at least one server is required")
	}

	for name, srv := range cfg.McpServers {
		if err := validateServer(name, &srv); err != nil {
			return err
		}
	}

	if cfg.Policy != nil {
		if err := validatePolicy(cfg.Policy); err != nil {
			return err
		}
	}

	if cfg.TLS != nil {
		if cfg.TLS.Cert == "" || cfg.TLS.Key == "" {
			return errors.New("tls: both cert and key are required")
		}
	}

	return validateRateLimit(cfg.RateLimit)
}

func validateServer(name string, srv *McpServerConfig) error {
	if name == "" {
		return errors.New("mcpServers: server name cannot be empty")
	}

	if err := validateServerTransport(name, srv); err != nil {
		return err
	}

	if srv.Credentials != nil {
		if err := validateCredential(name, srv.Credentials); err != nil {
			return err
		}
	}

	return validateServerLimits(name, srv)
}

func validateServerTransport(name string, srv *McpServerConfig) error {
	hasCommand := srv.Command != ""
	hasURL := srv.URL != ""

	if !hasCommand && !hasURL {
		return fmt.Errorf("mcpServers.%s: either command or url is required", name)
	}
	if hasCommand && hasURL {
		return fmt.Errorf("mcpServers.%s: cannot set both command and url", name)
	}
	if hasURL && !strings.HasPrefix(srv.URL, "http://") && !strings.HasPrefix(srv.URL, "https://") {
		return fmt.Errorf("mcpServers.%s: url must start with http:// or https://", name)
	}
	if hasCommand && srv.Credentials != nil {
		return fmt.Errorf("mcpServers.%s: credentials are not supported for stdio backends (use env instead)", name)
	}
	return nil
}

func validateServerLimits(name string, srv *McpServerConfig) error {
	if srv.CircuitBreaker != nil && srv.CircuitBreaker.Threshold <= 0 {
		return fmt.Errorf("mcpServers.%s.circuit_breaker.threshold must be > 0", name)
	}
	if srv.RateLimit != nil {
		if srv.RateLimit.RequestsPerSecond <= 0 {
			return fmt.Errorf("mcpServers.%s.rate_limit.rps must be > 0", name)
		}
		if srv.RateLimit.Burst <= 0 {
			return fmt.Errorf("mcpServers.%s.rate_limit.burst must be > 0", name)
		}
	}
	return nil
}

func validateCredential(serverName string, c *CredentialConfig) error {
	t := c.InferredType()
	if t == "" {
		return fmt.Errorf("mcpServers.%s.credentials: one of value, env, file, or command is required", serverName)
	}
	return nil
}

func validatePolicy(p *PolicyConfig) error {
	for name, agent := range p.Agents {
		if agent.Secret == "" {
			return fmt.Errorf("policy.agents.%s: secret is required", name)
		}
		for _, g := range agent.Groups {
			if _, ok := p.Groups[g]; !ok {
				return fmt.Errorf("policy.agents.%s: references unknown group %q", name, g)
			}
		}
	}
	return nil
}

func validateRateLimit(rl *RateLimitConfig) error {
	if rl == nil {
		return nil
	}
	if rl.RequestsPerSecond <= 0 {
		return errors.New("rate_limit.rps must be > 0")
	}
	if rl.Burst <= 0 {
		return errors.New("rate_limit.burst must be > 0")
	}
	return nil
}

// --- Expansion ---

// expand converts the user-facing Config into the internal Loaded representation.
func expand(cfg *Config) (*Loaded, error) {
	loaded := &Loaded{
		Listen:          cfg.Listen,
		Admin:           cfg.Admin,
		Store:           cfg.Store,
		TLS:             cfg.TLS,
		Audit:           cfg.Audit,
		RateLimit:       cfg.RateLimit,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}

	// Apply defaults.
	if loaded.Listen == "" {
		loaded.Listen = ":8080"
	}
	if loaded.Admin == "" {
		loaded.Admin = ":9090"
	}
	if loaded.ShutdownTimeout == 0 {
		loaded.ShutdownTimeout = Duration(10 * time.Second)
	}

	// Expand mcpServers map → ServerConfig slice.
	for name, srv := range cfg.McpServers {
		sc := ServerConfig{
			ID:             name,
			Namespace:      name,
			URL:            srv.URL,
			Credentials:    srv.Credentials,
			RateLimit:      srv.RateLimit,
			CircuitBreaker: srv.CircuitBreaker,
			Timeout:        srv.Timeout,
			Env:            srv.Env,
		}

		if srv.IsStdio() {
			sc.Command = append([]string{srv.Command}, srv.Args...)
		}

		if sc.Timeout == 0 {
			sc.Timeout = Duration(30 * time.Second)
		}

		loaded.Servers = append(loaded.Servers, sc)
	}

	// Expand policy → embedded auth config.
	// Runs whenever policy is present (even with zero agents — DCR creates them at runtime).
	if cfg.Policy != nil {
		loaded.EmbeddedAuth = expandPolicy(cfg.Policy)
	}

	return loaded, nil
}

// expandPolicy converts policy agents/groups into embedded auth client configs.
func expandPolicy(p *PolicyConfig) *EmbeddedAuthConfig {
	clients := make([]EmbeddedClient, 0, len(p.Agents))
	scopeSet := make(map[string]struct{})

	for name, agent := range p.Agents {
		scopes := resolveAgentScopes(name, &agent, p)
		for _, s := range scopes {
			scopeSet[s] = struct{}{}
		}
		clients = append(clients, EmbeddedClient{
			ClientID:      name,
			ClientSecret:  agent.Secret,
			AllowedScopes: scopes,
		})
	}

	allScopes := make([]string, 0, len(scopeSet))
	for s := range scopeSet {
		allScopes = append(allScopes, s)
	}

	return &EmbeddedAuthConfig{
		// Issuer is set at runtime by cmd/prism once the listen address is known.
		Clients:         clients,
		TokenTTLSeconds: 3600,
		RequiredScopes:  []string{"mcp:connect"},
		ScopesSupported: allScopes,
		DefaultScopes:   p.DefaultScopes,
	}
}

// resolveAgentScopes computes the effective scopes for an agent:
//
//	(union of group scopes) + grant - deny + mcp:connect
func resolveAgentScopes(_ string, agent *AgentConfig, p *PolicyConfig) []string {
	scopeSet := make(map[string]struct{})

	// Start with group scopes.
	for _, groupName := range agent.Groups {
		if group, ok := p.Groups[groupName]; ok {
			for _, s := range group.Scopes {
				scopeSet[s] = struct{}{}
			}
		}
	}

	// If no groups and default scopes exist, use those.
	if len(agent.Groups) == 0 && len(p.DefaultScopes) > 0 {
		for _, s := range p.DefaultScopes {
			scopeSet[s] = struct{}{}
		}
	}

	// Apply grants.
	for _, s := range agent.Grant {
		scopeSet[s] = struct{}{}
	}

	// Apply denials.
	for _, s := range agent.Deny {
		delete(scopeSet, s)
	}

	// Always include mcp:connect — agents must be able to connect.
	scopeSet["mcp:connect"] = struct{}{}

	scopes := make([]string, 0, len(scopeSet))
	for s := range scopeSet {
		scopes = append(scopes, s)
	}
	return scopes
}
