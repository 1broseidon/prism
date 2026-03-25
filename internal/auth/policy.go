// Package auth implements OAuth 2.1 token validation and scope-based
// access control for the MCPGate gateway.
//
// MCPGate acts as an OAuth 2.1 Resource Server (RFC 9728). It does NOT
// implement an Authorization Server — it validates tokens issued by an
// external provider (Keycloak, Auth0, Entra ID, etc.).
//
// Design principles:
//   - Agents are not trusted by default. Every tool call requires a valid token.
//   - Scopes map to namespaces and tool names. No scope = no access.
//   - Token audience MUST match the gateway's resource identifier (RFC 8707).
//   - Short-lived tokens only. No long-lived API keys in production.
package auth

import (
	"fmt"
	"strings"
)

// Policy defines what a client is allowed to do based on granted scopes.
//
// Scope format: "namespace:tool" or "namespace:*" for full namespace access.
// Example scopes: "github:create_issue", "github:*", "fs:read_file"
type Policy struct {
	// AllowedScopes is the set of scopes granted by the token.
	AllowedScopes map[string]struct{}
}

// NewPolicy creates a Policy from a space-separated scope string (per OAuth 2.1).
func NewPolicy(scopeString string) *Policy {
	p := &Policy{AllowedScopes: make(map[string]struct{})}
	for _, s := range strings.Fields(scopeString) {
		p.AllowedScopes[s] = struct{}{}
	}
	return p
}

// CanAccessTool checks if the policy allows calling a specific namespaced tool.
// A scope of "namespace:*" grants access to all tools in that namespace.
// A scope of "namespace:tool" grants access to that specific tool.
// A scope of "*" grants access to everything (superuser — use with caution).
func (p *Policy) CanAccessTool(namespace, tool string) bool {
	if p == nil || len(p.AllowedScopes) == 0 {
		return false
	}

	// Superuser scope
	if _, ok := p.AllowedScopes["*"]; ok {
		return true
	}

	// Namespace wildcard
	if _, ok := p.AllowedScopes[namespace+":*"]; ok {
		return true
	}

	// Specific tool
	if _, ok := p.AllowedScopes[namespace+":"+tool]; ok {
		return true
	}

	return false
}

// CanListTools checks if the policy allows listing tools at all.
// Any valid scope grants list access — you can see what exists,
// but calls are still gated per-tool.
func (p *Policy) CanListTools() bool {
	return p != nil && len(p.AllowedScopes) > 0
}

// Describe returns a human-readable description of the policy.
func (p *Policy) Describe() string {
	if p == nil || len(p.AllowedScopes) == 0 {
		return "no access"
	}
	scopes := make([]string, 0, len(p.AllowedScopes))
	for s := range p.AllowedScopes {
		scopes = append(scopes, s)
	}
	return fmt.Sprintf("scopes: %s", strings.Join(scopes, " "))
}
