// Package auth implements OAuth 2.1 token validation and scope-based
// access control for the Prism gateway.
//
// Prism acts as an OAuth 2.1 Resource Server (RFC 9728). It does NOT
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
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
)

// PolicyResolver resolves live policy for a client, bypassing stale session context.
// Implementations should cache results to avoid per-request KV reads.
type PolicyResolver interface {
	// ResolvePolicy returns the current effective policy for a client.
	// Returns nil if the client has no policy (open access or unknown client).
	ResolvePolicy(clientID, prismID string) *Policy
}

// BackendPolicy is a stackable per-backend rule. Policies stack:
// agent → groups (alphabetical) → defaults → backend static floor.
// The first non-empty value at each dimension wins.
//
// Each dimension resolves independently — an agent-layer RateLimit and a
// defaults-layer WorkspaceSelector both apply on the same call.
type BackendPolicy struct {
	// WorkspaceSelector controls which workspace a tool call attaches to.
	// Empty means inherit from the next layer.
	//
	// Values:
	//   "static"        — use the backend's configured workspace (floor)
	//   "agent"         — resolve to the workspace owned by the calling agent
	//   "id:<id>"       — pin to a specific registered workspace id
	WorkspaceSelector string `json:"workspace_selector,omitempty"`

	// RateLimit caps how often the bound caller may call this backend.
	// Nil means inherit from the next layer; pointer (not zero-value) is
	// used so a layer can intentionally "no rate limit, override anything
	// below" by setting RPS=0.
	RateLimit *BackendRateLimit `json:"rate_limit,omitempty"`
}

// BackendRateLimit caps call frequency per (agent, backend) tuple.
// RPS=0 means unlimited; Burst defaults to ceil(RPS) when zero.
type BackendRateLimit struct {
	RPS   float64 `json:"rps"`
	Burst int     `json:"burst,omitempty"`
}

// BackendPolicyLayer is one tier of the stacked backend policy resolution.
// Sources are labeled so callers can attribute decisions back to the layer
// that produced them ("agent:<prism_id>", "group:<name>", "defaults").
type BackendPolicyLayer struct {
	Source   string                   `json:"source"`
	Policies map[string]BackendPolicy `json:"policies,omitempty"`
}

// BackendPolicyResolver returns the layered backend policy that applies to a
// caller, ordered from highest priority (agent) to lowest (defaults).
// Implementations should be cheap to call per request; cache at the
// authserver layer if needed.
type BackendPolicyResolver interface {
	ResolveBackendPolicy(claims *Claims) []BackendPolicyLayer
}

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
// A scope containing glob characters (e.g. "fs:read*") is matched using
// path.Match against the full "namespace:tool" string.
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

	full := namespace + ":" + tool

	// Specific tool (exact match)
	if _, ok := p.AllowedScopes[full]; ok {
		return true
	}

	// Glob pattern match — only reached if exact/wildcard fast paths miss.
	for scope := range p.AllowedScopes {
		if strings.ContainsAny(scope, "*?[") {
			if matched, _ := path.Match(scope, full); matched {
				return true
			}
		}
	}

	return false
}

// HasWorkspaceConstraints reports whether this policy opts into workspace
// authorization. Policies without workspace scopes keep legacy tool-only
// behavior for existing deployments.
func (p *Policy) HasWorkspaceConstraints() bool {
	if p == nil {
		return false
	}
	for scope := range p.AllowedScopes {
		if scope == "*" || strings.HasPrefix(scope, "workspace:") {
			return true
		}
	}
	return false
}

// CanAccessWorkspace checks whether the policy allows attaching tools to the
// given workspace ID. Workspace scopes use "workspace:<id>", "workspace:*",
// or glob scopes like "workspace:team-*".
func (p *Policy) CanAccessWorkspace(workspaceID string) bool {
	if p == nil || len(p.AllowedScopes) == 0 || workspaceID == "" {
		return false
	}
	if _, ok := p.AllowedScopes["*"]; ok {
		return true
	}
	if _, ok := p.AllowedScopes["workspace:*"]; ok {
		return true
	}
	full := "workspace:" + workspaceID
	if _, ok := p.AllowedScopes[full]; ok {
		return true
	}
	for scope := range p.AllowedScopes {
		if strings.HasPrefix(scope, "workspace:") && strings.ContainsAny(scope, "*?[") {
			if matched, _ := path.Match(scope, full); matched {
				return true
			}
		}
	}
	return false
}

// CanListTools checks if the policy allows listing tools at all.
// Any valid scope grants list access — you can see what exists,
// but calls are still gated per-tool.
func (p *Policy) CanListTools() bool {
	return p != nil && len(p.AllowedScopes) > 0
}

// LivePolicy returns the current effective policy for the request.
// If a PolicyResolver is provided and claims are available in the context,
// it resolves live policy (bypassing stale session context).
// Falls back to PolicyFromContext if no resolver is available.
func LivePolicy(ctx context.Context, resolver PolicyResolver) *Policy {
	claims := ClaimsFromContext(ctx)
	if resolver != nil && claims != nil && claims.ClientID != "" {
		if p := resolver.ResolvePolicy(claims.ClientID, claims.PrismID); p != nil {
			return p
		}
	}
	// Fallback: use the policy from context (set at HTTP auth time).
	return PolicyFromContext(ctx)
}

// CachedPolicyResolver wraps a PolicyResolver with a per-client TTL cache.
type CachedPolicyResolver struct {
	inner PolicyResolver
	mu    sync.RWMutex
	cache map[string]cachedPolicy
	ttl   time.Duration
}

type cachedPolicy struct {
	policy  *Policy
	fetched time.Time
}

// NewCachedPolicyResolver creates a cached wrapper around a PolicyResolver.
func NewCachedPolicyResolver(inner PolicyResolver, ttl time.Duration) *CachedPolicyResolver {
	return &CachedPolicyResolver{
		inner: inner,
		cache: make(map[string]cachedPolicy),
		ttl:   ttl,
	}
}

// ResolvePolicy returns cached policy or fetches from inner resolver.
func (c *CachedPolicyResolver) ResolvePolicy(clientID, prismID string) *Policy {
	key := clientID
	if prismID != "" {
		key = prismID // PrismID is more stable for DCR agents
	}

	c.mu.RLock()
	if cached, ok := c.cache[key]; ok && time.Since(cached.fetched) < c.ttl {
		c.mu.RUnlock()
		return cached.policy
	}
	c.mu.RUnlock()

	p := c.inner.ResolvePolicy(clientID, prismID)

	c.mu.Lock()
	c.cache[key] = cachedPolicy{policy: p, fetched: time.Now()}
	c.mu.Unlock()

	return p
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
