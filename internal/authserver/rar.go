package authserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/1broseidon/prism/internal/auth"
)

type authorizationDetailRequest struct {
	Type      string                  `json:"type"`
	Tool      string                  `json:"tool"`
	Backend   string                  `json:"backend"`
	Args      map[string]any          `json:"args,omitempty"`
	Workspace *auth.WorkspaceInstance `json:"workspace,omitempty"`
}

func (s *Server) validateAuthorizationDetails(w http.ResponseWriter, r *http.Request, p *authorizeParams, prismID string) ([]auth.IssuedGrant, bool) {
	var entries []authorizationDetailRequest
	if err := json.Unmarshal([]byte(p.authorizationDetails), &entries); err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_authorization_details", "authorization_details must be a JSON array")
		return nil, false
	}
	if len(entries) == 0 || len(entries) > 8 {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_authorization_details", "authorization_details must contain 1-8 entries")
		return nil, false
	}
	for i, entry := range entries {
		if entry.Type != auth.GrantTypeMCPCall {
			s.writeOAuthError(w, http.StatusBadRequest, "invalid_authorization_details", fmt.Sprintf("entry %d has unsupported type", i))
			return nil, false
		}
		if !s.grantToolIsAvailable(entry.Backend, entry.Tool) {
			s.writeOAuthError(w, http.StatusBadRequest, "tool_disabled", fmt.Sprintf("entry %d names a disabled tool", i))
			return nil, false
		}
	}

	sub := s.subjectIdentity(p.clientID, prismID)
	bindings := s.ListGrantBindingsForSubject(sub.AgentID, sub.Groups, sub.Roles)
	if len(bindings) == 0 {
		s.writeOAuthError(w, http.StatusBadRequest, "binding_required", "no grant binding covers this subject")
		return nil, false
	}

	issued := make([]auth.IssuedGrant, 0, len(entries))
	for i, entry := range entries {
		grant, ok := s.matchAuthorizationDetail(entry, bindings, sub)
		if !ok {
			s.writeOAuthError(w, http.StatusBadRequest, "invalid_authorization_details", fmt.Sprintf("entry %d failed grant policy", i))
			return nil, false
		}
		if !s.workspaceRARAllowed(prismID, entry.Backend, entry.Workspace) {
			s.writeOAuthError(w, http.StatusBadRequest, "invalid_authorization_details", fmt.Sprintf("entry %d failed workspace policy", i))
			return nil, false
		}
		issued = append(issued, grant)
	}
	return issued, true
}

func (s *Server) matchAuthorizationDetail(entry authorizationDetailRequest, bindings []auth.GrantBinding, sub auth.SubjectIdentity) (auth.IssuedGrant, bool) {
	argsRaw, _ := json.Marshal(entry.Args)
	for _, b := range bindings {
		t, err := s.GetGrantTemplateByHash(b.TemplateHash)
		if err != nil {
			continue
		}
		spec, err := auth.SubstituteVars(t.Spec, auth.SubVars{
			AgentPrismID:  sub.AgentID,
			AgentClientID: sub.ClientID,
		})
		if err != nil || spec.Tool != entry.Tool || spec.Backend != entry.Backend {
			continue
		}
		grant := auth.IssuedGrant{
			Type:             auth.GrantTypeMCPCall,
			TemplateID:       t.ID,
			TemplateHash:     t.Hash,
			Actions:          []string{"tools/call"},
			Tool:             spec.Tool,
			Backend:          spec.Backend,
			Args:             spec.Args,
			Workspace:        entry.Workspace,
			Hours:            spec.Hours,
			NotBefore:        spec.NotBefore,
			ExpiresAt:        spec.ExpiresAt,
			AuthFreshnessMax: spec.AuthFreshnessMax,
			CnfRequired:      spec.CnfRequired,
			AcrRequired:      spec.AcrRequired,
		}
		if entry.Workspace != nil && !spec.Workspace.Match(*entry.Workspace) {
			continue
		}
		result := auth.MatchGrant(auth.CallContext{
			Tool:      entry.Tool,
			Backend:   entry.Backend,
			Arguments: argsRaw,
			Workspace: entry.Workspace,
			Now:       s.now(),
		}, []auth.IssuedGrant{grant})
		if result.Allowed || result.DenyDim == auth.GrantDenyNeedsStepUp || result.DenyDim == auth.GrantDenyACRRequired {
			return grant, true
		}
	}
	return auth.IssuedGrant{}, false
}

func (s *Server) subjectIdentity(clientID, prismID string) auth.SubjectIdentity {
	sub := auth.SubjectIdentity{AgentID: prismID, ClientID: clientID}
	if prismID == "" {
		return sub
	}
	policy, err := s.GetAgentPolicy(prismID)
	if err != nil || policy == nil {
		return sub
	}
	sub.Groups = append([]string(nil), policy.Groups...)
	for _, grant := range policy.Grant {
		if role, ok := strings.CutPrefix(grant, "role:"); ok && role != "" {
			sub.Roles = append(sub.Roles, role)
		}
	}
	return sub
}

func (s *Server) workspaceRARAllowed(prismID, backend string, workspace *auth.WorkspaceInstance) bool {
	if workspace == nil || prismID == "" {
		return true
	}
	policy, err := s.GetAgentPolicy(prismID)
	if err != nil || policy == nil {
		return true
	}
	rule, ok := policy.BackendPolicies[backend]
	if !ok || rule.WorkspaceSelector == "" || rule.WorkspaceSelector == "agent" || rule.WorkspaceSelector == "static" {
		return true
	}
	if expected, ok := strings.CutPrefix(rule.WorkspaceSelector, "id:"); ok {
		return workspace.ID == expected
	}
	return true
}

// renderGrantSummaries turns a set of issued grants into the operator-facing
// rows used by the consent page. Each row is a flattened tool/backend/
// workspace tuple plus the args predicate set so the operator can see the
// concrete authorization before approving it.
func renderGrantSummaries(grants []auth.IssuedGrant) []consentGrantRow {
	if len(grants) == 0 {
		return nil
	}
	out := make([]consentGrantRow, 0, len(grants))
	for _, g := range grants {
		row := consentGrantRow{
			Tool:    g.Tool,
			Backend: g.Backend,
		}
		if g.Workspace != nil {
			row.Workspace = g.Workspace.ID
		}
		if len(g.Args) > 0 {
			data, err := json.Marshal(g.Args)
			if err == nil {
				row.Args = string(data)
			}
		}
		out = append(out, row)
	}
	return out
}
