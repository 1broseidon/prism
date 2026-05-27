//go:build integration

// Package integration — Agents Tabs (Members | Groups | Roles) end-to-end
// suite for task-40.
//
// These tests exercise the admin HTTP endpoints that back the membership-
// management surface added on /agents:
//
//   - PUT /agents/{prism_id}/policy as the one mutation path for group +
//     role membership (no new endpoints were introduced).
//   - DELETE /groups/{name} with the new member-count guard.
//
// They sit alongside the broader policy_builder_e2e_test.go but stand on
// their own helpers — they don't share state with that suite. The runtime
// budget is well under the 60s TestE2E timeout.
package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// putAgentPolicy is a thin helper that mirrors what the AgentsGroupDetail
// page does on the wire: GET the agent, mutate the policy, PUT it back.
func (s *policySuite) putAgentPolicy(prismID string, body map[string]any) {
	s.t.Helper()
	s.adminJSON(http.MethodPut,
		"/agents/"+url.PathEscape(prismID)+"/policy",
		body, nil, http.StatusOK)
}

// deleteGroup issues DELETE /groups/{name} and returns the status + body so
// the test can assert the 409 guard surfaced by handleDeleteGroup. Follows
// the identity-URL-compat 301 redirect so the assertion can see the
// downstream handler's response, not the redirect itself.
func (s *policySuite) deleteGroup(name string) (int, string) {
	s.t.Helper()
	target := s.adminHTTP.URL + "/api/v1/groups/" + url.PathEscape(name)
	resp := s.doWithRedirect(http.MethodDelete, target, nil, false)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// TestE2E_AgentsTabs_AddRemoveGroupMember covers the Groups tab
// membership-management flow. We create an agent, place them in the
// engineering group via the same PUT /policy path the UI uses, verify the
// group manager sees the membership, then remove them.
func TestE2E_AgentsTabs_AddRemoveGroupMember(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)
	prismID := s.ensureAgent()

	// Add membership.
	s.putAgentPolicy(prismID, map[string]any{
		"groups": []string{group},
		"grant":  []string{},
		"deny":   []string{},
	})
	pol := s.agentPolicy(prismID)
	if !policySliceContains(pol.Groups, group) {
		t.Fatalf("expected agent in group %q after add, got groups=%v", group, pol.Groups)
	}

	// Remove membership (mirror UI: PUT the policy with the group filtered out).
	s.putAgentPolicy(prismID, map[string]any{
		"groups": []string{},
		"grant":  []string{},
		"deny":   []string{},
	})
	pol = s.agentPolicy(prismID)
	if policySliceContains(pol.Groups, group) {
		t.Fatalf("expected agent removed from group %q, got groups=%v", group, pol.Groups)
	}
}

// TestE2E_AgentsTabs_AddRemoveRoleMember covers the Roles tab membership-
// management flow. Role assignment uses the `role:<name>` marker in
// AgentPolicy.Grant — the same convention authserver.subjectIdentity reads
// at authorization time.
func TestE2E_AgentsTabs_AddRemoveRoleMember(t *testing.T) {
	s := newPolicySuite(t)
	prismID := s.ensureAgent()
	role := s.uniqueSubject("senior")
	marker := "role:" + role

	// Assign role via the PUT /policy path.
	s.putAgentPolicy(prismID, map[string]any{
		"groups": []string{},
		"grant":  []string{marker},
		"deny":   []string{},
	})
	pol := s.agentPolicy(prismID)
	if !policySliceContains(pol.Grant, marker) {
		t.Fatalf("expected agent to hold role marker %q, got grant=%v", marker, pol.Grant)
	}

	// Unassign role.
	s.putAgentPolicy(prismID, map[string]any{
		"groups": []string{},
		"grant":  []string{},
		"deny":   []string{},
	})
	pol = s.agentPolicy(prismID)
	if policySliceContains(pol.Grant, marker) {
		t.Fatalf("expected role marker %q removed, got grant=%v", marker, pol.Grant)
	}
}

// TestE2E_AgentsTabs_DeleteGroupGuardedByMembers covers the non-empty
// deletion guard. handleDeleteGroup must refuse with 409 while any agent
// still claims membership; once the operator removes the last member the
// delete should succeed.
func TestE2E_AgentsTabs_DeleteGroupGuardedByMembers(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)
	prismID := s.ensureAgent(group)

	// Sanity: ensureAgent placed the agent into the group via PUT /policy.
	pol := s.agentPolicy(prismID)
	if !policySliceContains(pol.Groups, group) {
		t.Fatalf("setup: expected agent in group %q, got groups=%v", group, pol.Groups)
	}

	// First delete attempt must be blocked while the group has a member.
	status, body := s.deleteGroup(group)
	if status != http.StatusConflict {
		t.Fatalf("non-empty group delete status=%d body=%s want=409", status, body)
	}
	var payload struct {
		Error   string `json:"error"`
		Members int    `json:"members"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode 409 body: %v (raw=%s)", err, body)
	}
	if payload.Members != 1 {
		t.Fatalf("expected members=1 in 409 payload, got %+v", payload)
	}
	if !strings.Contains(payload.Error, "members") {
		t.Fatalf("expected 409 error to mention members, got %q", payload.Error)
	}
	// Group must still exist after the refused delete — GET returns 200.
	s.adminJSON(http.MethodGet, "/groups/"+url.PathEscape(group), nil, nil, http.StatusOK)

	// Remove the member, then the delete should succeed.
	s.putAgentPolicy(prismID, map[string]any{
		"groups": []string{},
		"grant":  []string{},
		"deny":   []string{},
	})
	status, body = s.deleteGroup(group)
	if status != http.StatusOK {
		t.Fatalf("empty group delete status=%d body=%s want=200", status, body)
	}
}
