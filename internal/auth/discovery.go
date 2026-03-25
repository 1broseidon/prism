package auth

import (
	"encoding/json"
	"net/http"
)

// ProtectedResourceMetadata implements RFC 9728.
// This tells MCP clients which authorization server to use and what scopes are available.
type ProtectedResourceMetadata struct {
	// Resource is the canonical URI of this MCP gateway (per RFC 8707).
	Resource string `json:"resource"`

	// AuthorizationServers lists the authorization server(s) that can issue tokens for this resource.
	AuthorizationServers []string `json:"authorization_servers"`

	// ScopesSupported lists the OAuth scopes this resource supports.
	ScopesSupported []string `json:"scopes_supported,omitempty"`

	// BearerMethodsSupported indicates how tokens can be presented.
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`

	// ResourceDocumentation is a URL to human-readable documentation.
	ResourceDocumentation string `json:"resource_documentation,omitempty"`
}

// DiscoveryHandler returns an http.Handler that serves the Protected Resource Metadata
// document at /.well-known/oauth-protected-resource (per RFC 9728).
//
// MCP clients use this to discover which authorization server to authenticate with.
func DiscoveryHandler(meta *ProtectedResourceMetadata) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_ = json.NewEncoder(w).Encode(meta)
	})
}
