package admin

import (
	"encoding/json"
	"net/http"
	"strings"
)

// AgentPolicy mirrors authserver.AgentPolicy for the admin API boundary.
type AgentPolicy struct {
	Groups []string `json:"groups"`
	Grant  []string `json:"grant,omitempty"`
	Deny   []string `json:"deny,omitempty"`
}

// AgentManager is the interface the admin API uses to manage agents and policy.
type AgentManager interface {
	// ListAgents returns all agents (static + DCR) with identity and policy info.
	ListAgents() []any
	// GetAgentByPrismID returns a single agent by PrismID, or nil if not found.
	GetAgentByPrismID(prismID string) any
	// SetAgentPolicy sets groups/grant/deny for a DCR agent by PrismID.
	SetAgentPolicy(prismID string, groups []string, grant []string, deny []string) error
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
	if prismID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prism_id required"})
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
	if prismID == "" || prismID == path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /agents/{prism_id}/policy"})
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

// handleDeleteAgentPolicy handles DELETE /agents/{prism_id}/policy — remove custom policy.
func (a *API) handleDeleteAgentPolicy(w http.ResponseWriter, r *http.Request) {
	if a.agentMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent management not available"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/agents/")
	prismID := strings.TrimSuffix(path, "/policy")
	if prismID == "" || prismID == path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /agents/{prism_id}/policy"})
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
