package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/1broseidon/prism/internal/auth"
)

// AgentPolicy mirrors authserver.AgentPolicy for the admin API boundary.
type AgentPolicy struct {
	Groups          []string                      `json:"groups"`
	Grant           []string                      `json:"grant,omitempty"`
	Deny            []string                      `json:"deny,omitempty"`
	BackendPolicies map[string]auth.BackendPolicy `json:"backend_policies,omitempty"`
}

// AgentPolicyResolution describes the full per-backend policy decision for
// a single agent — workspace selection plus rate-limit, each with its own
// source and layer chain.
type AgentPolicyResolution struct {
	BackendID string                    `json:"backend_id"`
	Workspace *AgentWorkspaceResolution `json:"workspace,omitempty"`
	RateLimit *AgentRateLimitResolution `json:"rate_limit,omitempty"`
}

// AgentWorkspaceResolution is the workspace-dimension trace.
type AgentWorkspaceResolution struct {
	WorkspaceID string                       `json:"workspace_id,omitempty"`
	Selector    string                       `json:"selector"`
	Source      string                       `json:"source"`
	Layers      []AgentPolicyResolutionLayer `json:"layers,omitempty"`
	DenyReason  string                       `json:"deny_reason,omitempty"`
}

// AgentRateLimitResolution is the rate-limit-dimension trace. Limit is nil
// when no policy layer set one.
type AgentRateLimitResolution struct {
	RPS    float64                      `json:"rps,omitempty"`
	Burst  int                          `json:"burst,omitempty"`
	Source string                       `json:"source,omitempty"`
	Layers []AgentPolicyResolutionLayer `json:"layers,omitempty"`
}

// AgentPolicyResolutionLayer is one tier of the resolution trace, shared
// across dimensions.
type AgentPolicyResolutionLayer struct {
	Source   string `json:"source"`
	Selector string `json:"selector,omitempty"`
}

// BackendPolicyTraceProvider returns the full layered policy resolution for
// an agent across all known backends. Powers the admin "why this decision?"
// view on Agent detail.
type BackendPolicyTraceProvider interface {
	AgentPolicyResolutions(prismID string) []AgentPolicyResolution
}

// AgentManager is the interface the admin API uses to manage agents and policy.
type AgentManager interface {
	// ListAgents returns all agents (static + DCR) with identity and policy info.
	ListAgents() []any
	// GetAgentByPrismID returns a single agent by PrismID, or nil if not found.
	GetAgentByPrismID(prismID string) any
	// SetAgentPolicy sets groups/grant/deny for a DCR agent by PrismID.
	SetAgentPolicy(prismID string, groups []string, grant []string, deny []string) error
	// SetAgentBackendPolicies replaces the per-backend policies for an agent.
	// Empty map clears the entry.
	SetAgentBackendPolicies(prismID string, policies map[string]auth.BackendPolicy) error
	// DeleteAgentPolicy removes custom policy for a DCR agent (falls back to defaults).
	DeleteAgentPolicy(prismID string) error
	// RemoveAgent deletes a dynamic agent by client_id.
	RemoveAgent(clientID string) bool
	// RemoveStaleAgents deletes dynamic agents not used recently.
	RemoveStaleAgents() int
}

// handleAgentsPrismID handles GET /agents/{prism_id} — single agent details.
func (a *API) handleAgentByPrismID(w http.ResponseWriter, r *http.Request) {
	if a.agentMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent management not available"})
		return
	}

	prismID := strings.TrimPrefix(r.URL.Path, "/agents/")
	// Strip trailing /policy if present (this handler is for the agent, not policy).
	prismID = strings.TrimSuffix(prismID, "/policy")
	if !isValidID(prismID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid prism_id"})
		return
	}

	agent := a.agentMgr.GetAgentByPrismID(prismID)
	if agent == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// handleSetAgentPolicy handles PUT /agents/{prism_id}/policy — set policy for a DCR agent.
func (a *API) handleSetAgentPolicy(w http.ResponseWriter, r *http.Request) {
	if a.agentMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent management not available"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	// Extract prism_id from path: /agents/{prism_id}/policy
	path := strings.TrimPrefix(r.URL.Path, "/agents/")
	prismID := strings.TrimSuffix(path, "/policy")
	if prismID == path || !isValidID(prismID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /agents/{prism_id}/policy with a valid prism_id"})
		return
	}

	var body AgentPolicy
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if err := a.agentMgr.SetAgentPolicy(prismID, body.Groups, body.Grant, body.Deny); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Notify MCP clients to re-fetch tools/list with updated policy.
	if a.backendMgr != nil {
		a.backendMgr.NotifyToolsChanged()
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "prism_id": prismID})
}

// handleSetAgentBackendPolicies handles PUT /agents/{prism_id}/backend-policies.
func (a *API) handleSetAgentBackendPolicies(w http.ResponseWriter, r *http.Request) {
	if a.agentMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent management not available"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	path := strings.TrimPrefix(r.URL.Path, "/agents/")
	prismID := strings.TrimSuffix(path, "/backend-policies")
	if prismID == path || !isValidID(prismID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /agents/{prism_id}/backend-policies with a valid prism_id"})
		return
	}

	var body map[string]auth.BackendPolicy
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := a.agentMgr.SetAgentBackendPolicies(prismID, body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "prism_id": prismID})
}

// handleAgentPolicyResolution handles GET /agents/{prism_id}/policy-resolution.
func (a *API) handleAgentPolicyResolution(w http.ResponseWriter, r *http.Request) {
	if a.traceProvider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "policy resolution preview not available"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/agents/")
	prismID := strings.TrimSuffix(path, "/policy-resolution")
	if prismID == path || !isValidID(prismID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /agents/{prism_id}/policy-resolution with a valid prism_id"})
		return
	}
	writeJSON(w, http.StatusOK, a.traceProvider.AgentPolicyResolutions(prismID))
}

// handleDeleteAgentPolicy handles DELETE /agents/{prism_id}/policy — remove custom policy.
func (a *API) handleDeleteAgentPolicy(w http.ResponseWriter, r *http.Request) {
	if a.agentMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent management not available"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/agents/")
	prismID := strings.TrimSuffix(path, "/policy")
	if prismID == path || !isValidID(prismID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /agents/{prism_id}/policy with a valid prism_id"})
		return
	}

	if err := a.agentMgr.DeleteAgentPolicy(prismID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if a.backendMgr != nil {
		a.backendMgr.NotifyToolsChanged()
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "prism_id": prismID})
}
