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
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config is the top-level gateway configuration.
type Config struct {
	// Listen is the address for the MCP gateway. Default: ":8080".
	Listen string `json:"listen,omitempty"`

	// Admin is the address for the admin API. Default: ":9086".
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

	// AdminAuth, when set, requires operators to sign in via an OIDC
	// provider before they can reach the admin console or API. Absent →
	// admin runs open (appropriate for trusted home-lab networks).
	AdminAuth *AdminAuthConfig `json:"admin_auth,omitempty"`

	// PublicURL is the externally-reachable base URL for the MCP gateway.
	// Used as the OAuth issuer and in 401 resource_metadata hints.
	// Example: "http://172.16.30.90:8080" or "https://prism.example.com".
	// When omitted, derived from listen address or defaults to http://localhost:{port}.
	PublicURL string `json:"public_url,omitempty"`

	// AdminPublicURL is the externally-reachable base URL for the admin API.
	// Used for OAuth callback URLs when adding backends that require OAuth.
	// Example: "http://172.16.30.90:9086".
	// When omitted, derived from admin address or defaults to http://localhost:{port}.
	AdminPublicURL string `json:"admin_public_url,omitempty"`

	// BridgeURL is the URL of a prism-bridge running in manage mode.
	// When set, command-type backends added via the admin UI are delegated to the
	// bridge instead of being spawned inside the gateway process.
	BridgeURL string `json:"bridge_url,omitempty"`

	// BridgeURLs is the advanced multi-bridge form. When set, command-type
	// backends are assigned to a bridge deterministically by backend ID.
	BridgeURLs []string `json:"bridge_urls,omitempty"`

	// StdioSpawnMode controls how command-type MCP servers are spawned.
	// "auto" starts an internal Docker bridge when possible, "bridge_http"
	// requires bridge_url/bridge_urls, "internal_docker" requires a local Docker
	// socket, and "process" runs commands directly in the Prism process.
	StdioSpawnMode string `json:"stdio_spawn_mode,omitempty"`

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

// AdminAuthConfig protects the admin console and API behind an OIDC login.
// Mount point in the JSON config: "admin_auth". Absent means open.
type AdminAuthConfig struct {
	// Issuer is the OIDC issuer URL used for discovery
	// (e.g. https://accounts.google.com, https://your-tenant.okta.com).
	Issuer string `json:"issuer"`
	// ClientID is the OAuth client ID registered with the issuer.
	ClientID string `json:"client_id"`
	// ClientSecret is the OAuth client secret. Required for confidential clients.
	ClientSecret string `json:"client_secret"`
	// RedirectURL is the absolute callback URL registered with the issuer.
	// e.g. http://localhost:9086/auth/callback
	RedirectURL string `json:"redirect_url"`
	// Scopes are the OAuth scopes requested. Default: openid, profile, email.
	// Add provider-specific scopes (e.g. "groups") when using group RBAC.
	Scopes []string `json:"scopes,omitempty"`
	// GroupsClaim is the ID-token claim carrying group membership.
	// Default: "groups". Other common values: "roles", "cognito:groups".
	GroupsClaim string `json:"groups_claim,omitempty"`
	// SessionTTL is how long a logged-in session lasts. Default: "24h".
	SessionTTL Duration `json:"session_ttl,omitempty"`
	// CookieDomain optionally pins the session cookie Domain attribute.
	CookieDomain string `json:"cookie_domain,omitempty"`
	// CookieSecure forces Secure=true on the session cookie. Auto-on when TLS
	// is configured; set explicitly when terminating TLS at a reverse proxy.
	CookieSecure bool `json:"cookie_secure,omitempty"`
	// Rules grant roles by matching the authenticated user's email/domain/group
	// claims. The first matching rule wins. Users who match no rule are rejected.
	Rules []AdminAuthRule `json:"rules"`
}

// AdminAuthRule maps a set of OIDC matchers to a role. A rule matches when the
// authenticated user's email is in Emails, their email domain is in Domains,
// or any of their group claims is in Groups. Matching is case-insensitive for
// email/domain, case-sensitive for groups.
type AdminAuthRule struct {
	// Role is "admin" (full access) or "viewer" (read-only). Required.
	Role string `json:"role"`
	// Emails is a list of exact email addresses to grant this role.
	Emails []string `json:"emails,omitempty"`
	// Domains is a list of email domains (e.g. "example.com").
	Domains []string `json:"domains,omitempty"`
	// Groups is a list of group names from the OIDC groups claim.
	Groups []string `json:"groups,omitempty"`
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
	Enabled       bool   `json:"enabled"`
	Output        string `json:"output,omitempty"`         // "stderr" (default), "stdout", or file path
	RetentionDays int    `json:"retention_days,omitempty"` // days to keep audit entries in KV. Default: 30
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

	// Bridge-managed command metadata
	OriginalCommand []string
	BridgeManaged   bool
	BridgeRuntime   string

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
	Groups          map[string]GroupConfig // Group definitions for PrismID-based policy resolution.
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
	Listen         string
	Admin          string
	PublicURL      string // Externally-reachable base URL for the MCP gateway (OAuth issuer).
	AdminPublicURL string // Externally-reachable base URL for the admin API (OAuth callbacks).
	// PublicURLConfigured / AdminPublicURLConfigured are the raw config
	// values (empty when not set). Lets callers distinguish operator-pinned
	// URLs from addresses we guessed by looking at the listen interface.
	PublicURLConfigured      string
	AdminPublicURLConfigured string
	BridgeURL                string
	BridgeURLs               []string
	StdioSpawnMode           string
	Servers                  []ServerConfig
	EmbeddedAuth             *EmbeddedAuthConfig
	Store                    *StoreConfig
	TLS                      *TLSConfig
	Audit                    *AuditConfig
	RateLimit                *RateLimitConfig
	AdminAuth                *AdminAuthConfig
	ShutdownTimeout          Duration
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
	// mcpServers is optional — backends are managed via admin UI + KV store.
	// When present, they serve as a one-time seed on first boot.
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

	if cfg.AdminAuth != nil {
		if err := validateAdminAuth(cfg.AdminAuth); err != nil {
			return err
		}
	}

	if err := validateStdioSpawnMode(cfg.StdioSpawnMode); err != nil {
		return err
	}

	return validateRateLimit(cfg.RateLimit)
}

// ValidateAdminAuth validates an AdminAuthConfig the same way file-based
// Load() does. Exported so the admin API can re-validate runtime mutations.
func ValidateAdminAuth(a *AdminAuthConfig) error {
	return validateAdminAuth(a)
}

// ApplyAdminAuthDefaults fills in unset optional fields (scopes, groups_claim,
// session_ttl) so the resulting config can be handed straight to adminauth.
// Mirrors the defaults applied during file load.
func ApplyAdminAuthDefaults(a *AdminAuthConfig) {
	if a == nil {
		return
	}
	if len(a.Scopes) == 0 {
		a.Scopes = []string{"openid", "profile", "email"}
	}
	if a.GroupsClaim == "" {
		a.GroupsClaim = "groups"
	}
	if a.SessionTTL == 0 {
		a.SessionTTL = Duration(24 * time.Hour)
	}
}

func validateAdminAuth(a *AdminAuthConfig) error {
	if a.Issuer == "" {
		return errors.New("admin_auth.issuer is required")
	}
	if a.ClientID == "" {
		return errors.New("admin_auth.client_id is required")
	}
	if a.ClientSecret == "" {
		return errors.New("admin_auth.client_secret is required")
	}
	if a.RedirectURL == "" {
		return errors.New("admin_auth.redirect_url is required")
	}
	if len(a.Rules) == 0 {
		return errors.New("admin_auth.rules: at least one rule is required (otherwise no operator can sign in)")
	}
	for i, r := range a.Rules {
		switch r.Role {
		case "admin", "viewer":
		case "":
			return fmt.Errorf("admin_auth.rules[%d]: role is required (admin or viewer)", i)
		default:
			return fmt.Errorf("admin_auth.rules[%d]: role must be \"admin\" or \"viewer\", got %q", i, r.Role)
		}
		if len(r.Emails) == 0 && len(r.Domains) == 0 && len(r.Groups) == 0 {
			return fmt.Errorf("admin_auth.rules[%d]: at least one matcher (emails, domains, or groups) is required", i)
		}
	}
	return nil
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

func validateStdioSpawnMode(mode string) error {
	switch strings.TrimSpace(mode) {
	case "", "auto", "bridge_http", "internal_docker", "process", "disabled":
		return nil
	default:
		return fmt.Errorf("stdio_spawn_mode must be one of auto, bridge_http, internal_docker, process, disabled")
	}
}

// --- Expansion ---

// expand converts the user-facing Config into the internal Loaded representation.
func expand(cfg *Config) (*Loaded, error) {
	loaded := &Loaded{
		Listen:          cfg.Listen,
		Admin:           cfg.Admin,
		BridgeURL:       firstBridgeURL(cfg.BridgeURL, cfg.BridgeURLs),
		BridgeURLs:      normalizeBridgeURLs(cfg.BridgeURL, cfg.BridgeURLs),
		StdioSpawnMode:  normalizeStdioSpawnMode(cfg.StdioSpawnMode),
		Store:           cfg.Store,
		TLS:             cfg.TLS,
		Audit:           cfg.Audit,
		RateLimit:       cfg.RateLimit,
		AdminAuth:       cfg.AdminAuth,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}

	// Defaults for admin auth.
	ApplyAdminAuthDefaults(loaded.AdminAuth)

	// Apply defaults.
	if loaded.Listen == "" {
		loaded.Listen = ":8080"
	}
	if loaded.Admin == "" {
		loaded.Admin = ":9086"
	}
	if loaded.ShutdownTimeout == 0 {
		loaded.ShutdownTimeout = Duration(10 * time.Second)
	}

	// Derive PublicURL: explicit config > concrete listen address > localhost fallback.
	loaded.PublicURL = derivePublicURL(cfg.PublicURL, loaded.Listen, cfg.TLS != nil, "public_url", "listen")
	loaded.AdminPublicURL = derivePublicURL(cfg.AdminPublicURL, loaded.Admin, cfg.TLS != nil, "admin_public_url", "admin")
	// Keep raw configured values separately so callers can tell
	// "operator pinned this URL" from "we guessed". Empty means auto-derive.
	loaded.PublicURLConfigured = strings.TrimRight(cfg.PublicURL, "/")
	loaded.AdminPublicURLConfigured = strings.TrimRight(cfg.AdminPublicURL, "/")

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

func normalizeStdioSpawnMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "auto"
	}
	return mode
}

func normalizeBridgeURLs(primary string, rest []string) []string {
	inputs := make([]string, 0, len(rest)+1)
	if primary != "" {
		inputs = append(inputs, primary)
	}
	inputs = append(inputs, rest...)

	seen := make(map[string]bool, len(inputs))
	urls := make([]string, 0, len(inputs))
	for _, raw := range inputs {
		u := strings.TrimRight(strings.TrimSpace(raw), "/")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		urls = append(urls, u)
	}
	return urls
}

func firstBridgeURL(primary string, rest []string) string {
	urls := normalizeBridgeURLs(primary, rest)
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}

// derivePublicURL resolves the external base URL for a listener.
// Priority: explicit config value > concrete listen address > localhost fallback.
func derivePublicURL(explicit, listenAddr string, hasTLS bool, configField, addrField string) string {
	scheme := "http"
	if hasTLS {
		scheme = "https"
	}

	// If explicitly configured, use it directly (strip trailing slash).
	if explicit != "" {
		return strings.TrimRight(explicit, "/")
	}

	// If listen address has a concrete host (not just ":port"), derive from it.
	if !strings.HasPrefix(listenAddr, ":") {
		slog.Warn("using "+addrField+" address as "+configField+" — set "+configField+" in config for production",
			addrField, listenAddr)
		return scheme + "://" + listenAddr
	}

	// Fallback: localhost with the listen port.
	slog.Warn(configField+" not set — OAuth will only work from localhost",
		addrField, listenAddr)
	return scheme + "://localhost" + listenAddr
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
		Groups:          p.Groups,
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
