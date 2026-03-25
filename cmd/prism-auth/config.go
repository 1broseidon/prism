// Package main implements prism-auth, a lightweight OAuth 2.1 authorization server
// purpose-built for the Prism MCP gateway.
//
// prism-auth implements the client credentials grant (RFC 6749 §4.4) and serves
// JWKS and OAuth 2.1 Authorization Server Metadata (RFC 8414) so that Prism can
// validate issued tokens without any external dependency.
//
// It is intentionally minimal — it handles agent identity only. Human identity
// federation (authorization code + PKCE) is Phase 2. Any standard OIDC-compliant
// provider (Duo, Auth0, Okta, Keycloak, Entra ID) can be used instead by pointing
// Prism's issuer_url at that provider.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Config is the top-level prism-auth configuration.
type Config struct {
	// ListenAddr is the address to bind the HTTP server. Defaults to ":9100".
	ListenAddr string `json:"listen_addr"`

	// Issuer is the canonical URL of this authorization server (e.g. "http://localhost:9100").
	// It is embedded in issued tokens as the "iss" claim and used in discovery metadata.
	Issuer string `json:"issuer"`

	// SigningKey configures the RSA key used to sign JWTs.
	SigningKey SigningKeyConfig `json:"signing_key"`

	// Clients is the list of registered OAuth 2.1 clients (agent identities).
	Clients []ClientConfig `json:"clients"`

	// TokenTTLSeconds is the access token lifetime in seconds. Defaults to 3600 (1 hour).
	TokenTTLSeconds int `json:"token_ttl_seconds,omitempty"`
}

// SigningKeyConfig specifies where to load the RSA signing key.
type SigningKeyConfig struct {
	// Path is the path to a PEM-encoded RSA private key file (PKCS#1 or PKCS#8).
	// If empty, an ephemeral 2048-bit RSA key is generated on startup (dev mode).
	// Warning: ephemeral keys are lost on restart — all issued tokens become invalid.
	Path string `json:"path,omitempty"`
}

// ClientConfig defines a registered OAuth 2.1 client (agent identity).
type ClientConfig struct {
	// ClientID is the unique identifier for this client.
	ClientID string `json:"client_id"`

	// ClientSecret is the shared secret used to authenticate this client.
	// Store as a strong random string; avoid reusing secrets across clients.
	ClientSecret string `json:"client_secret"`

	// AllowedScopes is the set of OAuth scopes this client may request.
	// Tokens issued to this client will never exceed these scopes.
	AllowedScopes []string `json:"allowed_scopes"`

	// Description is an optional human-readable label for this client.
	Description string `json:"description,omitempty"`
}

// loadConfig reads and parses the JSON config file at path.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Config path is from CLI flag
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, validateConfig(&cfg)
}

// validateConfig applies defaults and validates required fields.
func validateConfig(cfg *Config) error {
	if cfg.Issuer == "" {
		return errors.New("issuer must be set")
	}
	if len(cfg.Clients) == 0 {
		return errors.New("at least one client must be configured")
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":9100"
	}
	if cfg.TokenTTLSeconds == 0 {
		cfg.TokenTTLSeconds = 3600
	}
	return validateClients(cfg.Clients)
}

// validateClients checks each client for required fields and duplicate IDs.
func validateClients(clients []ClientConfig) error {
	seen := make(map[string]struct{}, len(clients))
	for i, c := range clients {
		if c.ClientID == "" {
			return fmt.Errorf("client[%d]: client_id is required", i)
		}
		if c.ClientSecret == "" {
			return fmt.Errorf("client[%d] %q: client_secret is required", i, c.ClientID)
		}
		if _, dup := seen[c.ClientID]; dup {
			return fmt.Errorf("duplicate client_id: %q", c.ClientID)
		}
		seen[c.ClientID] = struct{}{}
	}
	return nil
}
