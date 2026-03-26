package authserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/1broseidon/prism/internal/auth"
)

// Policy key prefix for the KV store.
const policyKeyPrefix = "policy/agent/"

// tokenGenKeyPrefix is the KV key prefix for per-client generation counters.
const tokenGenKeyPrefix = "token_gen/"

// groupKeyPrefix is the KV key prefix for dynamically-created groups.
const groupKeyPrefix = "group/"

// GroupConfig defines a named set of scopes, mirroring config.GroupConfig.
// Copied here to avoid a dependency on the config package from authserver.
type GroupConfig struct {
	Scopes []string `json:"scopes"`
}

// GroupInfo describes a group with its source (config or dynamic).
type GroupInfo struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	Source string   `json:"source"` // "config" or "dynamic"
}

// PolicyBreakdown shows how an agent's effective scopes are computed.
type PolicyBreakdown struct {
	Defaults  []string            `json:"defaults"`
	Groups    map[string][]string `json:"groups,omitempty"`
	Grants    []string            `json:"grants,omitempty"`
	Denies    []string            `json:"denies,omitempty"`
	Effective []string            `json:"effective"`
}

// AgentPolicy is the per-agent policy stored in KV at policy/agent/{prism_id}.
// Same shape as config.AgentConfig (groups, grant, deny).
type AgentPolicy struct {
	Groups []string `json:"groups"`
	Grant  []string `json:"grant,omitempty"`
	Deny   []string `json:"deny,omitempty"`
}

// bumpTokenGeneration increments the generation counter for a client.
// Called when policy changes to invalidate existing tokens.
func (s *Server) bumpTokenGeneration(clientID string) {
	if s.store == nil || clientID == "" {
		return
	}
	gen := s.GetTokenGeneration(clientID)
	gen++
	_ = s.store.Set(tokenGenKeyPrefix+clientID, []byte(strconv.FormatInt(gen, 10)))
}

// GetTokenGeneration returns the current generation for a client (0 if unset).
// Public — used by the gateway's token validator via the GenerationChecker interface.
func (s *Server) GetTokenGeneration(clientID string) int64 {
	if s.store == nil {
		return 0
	}
	data, err := s.store.Get(tokenGenKeyPrefix + clientID)
	if err != nil {
		return 0
	}
	gen, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return 0
	}
	return gen
}

// clientIDByPrismID looks up the client_id for a given PrismID.
func (s *Server) clientIDByPrismID(prismID string) string {
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	for _, dc := range s.oauth.dynamics {
		if dc.PrismID == prismID {
			return dc.ClientID
		}
	}
	return ""
}

// GetAgentPolicy reads the policy for a PrismID from the KV store.
// Returns nil, nil if no custom policy exists.
func (s *Server) GetAgentPolicy(prismID string) (*AgentPolicy, error) {
	if s.store == nil {
		return nil, nil
	}
	data, err := s.store.Get(policyKeyPrefix + prismID)
	if err != nil {
		// Not found means no custom policy — fall back to defaults.
		return nil, nil
	}
	var p AgentPolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("unmarshal agent policy: %w", err)
	}
	return &p, nil
}

// SetAgentPolicy writes a policy for a PrismID to the KV store.
// After writing, bumps the token generation counter to invalidate existing tokens.
func (s *Server) SetAgentPolicy(prismID string, p *AgentPolicy) error {
	if s.store == nil {
		return errors.New("no KV store configured")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal agent policy: %w", err)
	}
	if err := s.store.Set(policyKeyPrefix+prismID, data); err != nil {
		return err
	}
	if clientID := s.clientIDByPrismID(prismID); clientID != "" {
		s.bumpTokenGeneration(clientID)
	}
	return nil
}

// DeleteAgentPolicy removes a custom policy for a PrismID.
// After deletion the agent falls back to default_scopes.
// Bumps the token generation counter to invalidate existing tokens.
func (s *Server) DeleteAgentPolicy(prismID string) error {
	if s.store == nil {
		return errors.New("no KV store configured")
	}
	if err := s.store.Delete(policyKeyPrefix + prismID); err != nil {
		return err
	}
	if clientID := s.clientIDByPrismID(prismID); clientID != "" {
		s.bumpTokenGeneration(clientID)
	}
	return nil
}

// ResolveScopesByPrismID reads the KV policy for a PrismID and resolves
// scopes through the group -> grant -> deny chain.
// Falls back to default_scopes if no custom policy exists.
func (s *Server) ResolveScopesByPrismID(prismID string) []string {
	policy, err := s.GetAgentPolicy(prismID)
	if err != nil {
		s.logger.Warn("failed to read agent policy, falling back to defaults",
			"prism_id", prismID, "error", err)
		return s.defaultScopes()
	}
	if policy == nil {
		return s.defaultScopes()
	}
	return s.resolvePolicy(policy)
}

// resolvePolicy applies the group -> grant -> deny logic using the
// server's group definitions.
func (s *Server) resolvePolicy(p *AgentPolicy) []string {
	scopeSet := make(map[string]struct{})

	// Expand group scopes.
	s.mu.RLock()
	groups := s.groups
	s.mu.RUnlock()

	for _, groupName := range p.Groups {
		if group, ok := groups[groupName]; ok {
			for _, sc := range group.Scopes {
				scopeSet[sc] = struct{}{}
			}
		}
	}

	// If no groups matched and we have defaults, use them.
	if len(p.Groups) == 0 {
		for _, sc := range s.cfg.DefaultScopes {
			scopeSet[sc] = struct{}{}
		}
	}

	// Apply grants.
	for _, sc := range p.Grant {
		scopeSet[sc] = struct{}{}
	}

	// Apply denials (deny wins over grant).
	for _, sc := range p.Deny {
		delete(scopeSet, sc)
	}

	// Always include mcp:connect.
	scopeSet["mcp:connect"] = struct{}{}

	scopes := make([]string, 0, len(scopeSet))
	for sc := range scopeSet {
		scopes = append(scopes, sc)
	}
	return scopes
}

// ResolvePolicy returns the current effective policy for a client.
// Satisfies auth.PolicyResolver. Uses PrismID for DCR agents,
// falls back to the client's configured scopes for static agents.
func (s *Server) ResolvePolicy(clientID, prismID string) *auth.Policy {
	var scopes []string
	if prismID != "" {
		scopes = s.ResolveScopesByPrismID(prismID)
	} else {
		// Static client — use configured AllowedScopes.
		s.mu.RLock()
		client, ok := s.clients[clientID]
		s.mu.RUnlock()
		if !ok {
			return nil
		}
		scopes = client.AllowedScopes
	}
	return auth.NewPolicy(strings.Join(scopes, " "))
}

// defaultScopes returns the configured default scopes plus mcp:connect.
func (s *Server) defaultScopes() []string {
	scopeSet := map[string]struct{}{"mcp:connect": {}}
	for _, sc := range s.cfg.DefaultScopes {
		scopeSet[sc] = struct{}{}
	}
	scopes := make([]string, 0, len(scopeSet))
	for sc := range scopeSet {
		scopes = append(scopes, sc)
	}
	return scopes
}

// DefaultScopes returns the configured default scopes (public accessor).
func (s *Server) DefaultScopes() []string {
	return s.cfg.DefaultScopes
}

// defaultScopesKey is the KV key for persisted runtime default scopes.
const defaultScopesKey = "policy/defaults"

// SetDefaultScopes updates the runtime default scopes.
// Persists to KV and updates in-memory config.
func (s *Server) SetDefaultScopes(scopes []string) error {
	if s.store == nil {
		// No KV store — just update in-memory.
		s.cfg.DefaultScopes = scopes
		return nil
	}
	data, err := json.Marshal(scopes)
	if err != nil {
		return fmt.Errorf("marshal default scopes: %w", err)
	}
	if err := s.store.Set(defaultScopesKey, data); err != nil {
		return err
	}
	s.cfg.DefaultScopes = scopes
	return nil
}

// loadPersistedDefaults restores default scopes from KV if they were
// changed at runtime via SetDefaultScopes. Called during startup to
// overlay config defaults with any persisted overrides.
func (s *Server) loadPersistedDefaults() {
	if s.store == nil {
		return
	}
	data, err := s.store.Get(defaultScopesKey)
	if err != nil {
		// Not found — keep config defaults.
		return
	}
	var scopes []string
	if err := json.Unmarshal(data, &scopes); err != nil {
		s.logger.Warn("failed to unmarshal persisted default scopes", "error", err)
		return
	}
	s.cfg.DefaultScopes = scopes
	s.logger.Info("restored persisted default scopes", "scopes", scopes)
}

// --- Group CRUD (KV-backed) ---

// GetGroup reads a group from the KV store. Returns nil, nil if not found.
func (s *Server) GetGroup(name string) (*GroupConfig, error) {
	if s.store == nil {
		return nil, nil
	}
	data, err := s.store.Get(groupKeyPrefix + name)
	if err != nil {
		return nil, nil
	}
	var g GroupConfig
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("unmarshal group: %w", err)
	}
	return &g, nil
}

// SetGroup writes a group to the KV store and updates the in-memory group map.
func (s *Server) SetGroup(name string, g *GroupConfig) error {
	if s.store == nil {
		return errors.New("no KV store configured")
	}
	data, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("marshal group: %w", err)
	}
	if err := s.store.Set(groupKeyPrefix+name, data); err != nil {
		return err
	}
	// Update in-memory map so policy resolution sees the change immediately.
	s.mu.Lock()
	if s.groups == nil {
		s.groups = make(map[string]GroupConfig)
	}
	s.groups[name] = *g
	s.mu.Unlock()
	return nil
}

// DeleteGroup removes a group from the KV store and the in-memory map.
func (s *Server) DeleteGroup(name string) error {
	if s.store == nil {
		return errors.New("no KV store configured")
	}
	if err := s.store.Delete(groupKeyPrefix + name); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.groups, name)
	s.mu.Unlock()
	return nil
}

// ListGroups merges config-sourced groups with KV-stored groups.
// KV groups win on name conflict. Config groups have source="config",
// KV groups have source="dynamic".
func (s *Server) ListGroups() []GroupInfo {
	s.mu.RLock()
	configGroups := s.groups
	s.mu.RUnlock()

	// Start with a map of config groups.
	merged := make(map[string]GroupInfo)
	for name, g := range configGroups {
		merged[name] = GroupInfo{
			Name:   name,
			Scopes: g.Scopes,
			Source: "config",
		}
	}

	// Overlay with KV-stored groups (source="dynamic", wins on conflict).
	if s.store != nil {
		keys, err := s.store.List(groupKeyPrefix)
		if err == nil {
			for _, key := range keys {
				name := strings.TrimPrefix(key, groupKeyPrefix)
				data, getErr := s.store.Get(key)
				if getErr != nil {
					continue
				}
				var g GroupConfig
				if jsonErr := json.Unmarshal(data, &g); jsonErr != nil {
					continue
				}
				merged[name] = GroupInfo{
					Name:   name,
					Scopes: g.Scopes,
					Source: "dynamic",
				}
			}
		}
	}

	result := make([]GroupInfo, 0, len(merged))
	for _, gi := range merged {
		result = append(result, gi)
	}
	return result
}

// IsConfigGroup returns true if the named group originates from config (not KV).
func (s *Server) IsConfigGroup(name string) bool {
	// A group is config-sourced if it exists in config AND is NOT in the KV store.
	s.mu.RLock()
	_, inMemory := s.groups[name]
	s.mu.RUnlock()
	if !inMemory {
		return false
	}
	if s.store == nil {
		return true
	}
	_, err := s.store.Get(groupKeyPrefix + name)
	// If the key doesn't exist in KV, it's a config group.
	return err != nil
}

// BuildBreakdown computes a PolicyBreakdown for an agent's policy.
// Uses the same resolvePolicy logic — the breakdown just shows the layers.
func (s *Server) BuildBreakdown(policy *AgentPolicy, effectiveScopes []string) *PolicyBreakdown {
	bd := &PolicyBreakdown{
		Defaults:  s.cfg.DefaultScopes,
		Effective: effectiveScopes,
	}
	if bd.Defaults == nil {
		bd.Defaults = []string{}
	}
	if bd.Effective == nil {
		bd.Effective = []string{}
	}

	if policy != nil {
		// Map each group to its scopes.
		if len(policy.Groups) > 0 {
			bd.Groups = make(map[string][]string)
			s.mu.RLock()
			groups := s.groups
			s.mu.RUnlock()
			for _, gn := range policy.Groups {
				if g, ok := groups[gn]; ok {
					bd.Groups[gn] = g.Scopes
				} else {
					bd.Groups[gn] = []string{}
				}
			}
		}
		bd.Grants = policy.Grant
		bd.Denies = policy.Deny
	}

	if bd.Grants == nil {
		bd.Grants = []string{}
	}
	if bd.Denies == nil {
		bd.Denies = []string{}
	}

	return bd
}
