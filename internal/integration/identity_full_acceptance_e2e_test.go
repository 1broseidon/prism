//go:build integration

package integration

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/analytics"
	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/authserver"
	"github.com/1broseidon/prism/internal/identity"
)

func TestE2E_Identity_FullAcceptance(t *testing.T) {
	s := newE2ESuite(t)
	idMgr := identity.New(s.kv)
	s.authSrv.SetIdentityDispatcher(idMgr)
	s.adminAPI.SetIdentity(idMgr)

	// Criterion 1: rename group; URL key stable; references and audit history preserved.
	ent, err := idMgr.Allocate(identity.KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate group: %v", err)
	}
	groupID := ent.ID
	if err := s.authSrv.SetGroup(groupID, &authserver.GroupConfig{Scopes: []string{"fs:read_file"}}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	if err := s.authSrv.SetAgentPolicy("agent-acceptance-1", &authserver.AgentPolicy{Groups: []string{groupID}}); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}
	template := acceptanceSaveTemplate(t, s, "tmpl-accept-1", "fs.read_file")
	if _, err := s.authSrv.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-accept-1",
		TemplateHash: template.Hash,
		Subjects:     auth.SubjectSelector{Groups: []string{groupID}},
	}); err != nil {
		t.Fatalf("SetGrantBinding: %v", err)
	}
	s.emitter.Emit(s.ctx, auth.GrantEvent{
		Timestamp:    s.clock.Now(),
		AgentID:      "agent-acceptance-1",
		Backend:      "local",
		Tool:         "fs.read_file",
		Outcome:      "granted",
		TemplateHash: "h1",
	})

	entBeforeRename, err := idMgr.ResolveByName(identity.KindGroup, "engineering")
	if err != nil {
		t.Fatalf("ResolveByName before rename: %v", err)
	}
	if entBeforeRename.ID != groupID {
		t.Fatalf("ResolveByName before rename ID = %q, want %q", entBeforeRename.ID, groupID)
	}
	renamed, err := idMgr.Rename(groupID, "platform-engineering")
	if err != nil {
		t.Fatalf("Rename group: %v", err)
	}
	if renamed.ID != groupID {
		t.Fatalf("rename changed URL key: got %q, want %q", renamed.ID, groupID)
	}

	policy, err := s.authSrv.GetAgentPolicy("agent-acceptance-1")
	if err != nil {
		t.Fatalf("GetAgentPolicy: %v", err)
	}
	if policy == nil || !slices.Contains(policy.Groups, groupID) {
		t.Fatalf("agent policy groups = %+v, want %q", policy, groupID)
	}
	binding, err := s.authSrv.GetGrantBinding("bind-accept-1")
	if err != nil {
		t.Fatalf("GetGrantBinding: %v", err)
	}
	if !slices.Contains(binding.Subjects.Groups, groupID) {
		t.Fatalf("binding groups = %v, want %q", binding.Subjects.Groups, groupID)
	}

	s.clock.Advance(time.Second)
	s.emitter.Emit(s.ctx, auth.GrantEvent{
		Timestamp:    s.clock.Now(),
		AgentID:      "agent-acceptance-1",
		Backend:      "local",
		Tool:         "fs.read_file",
		Outcome:      "granted",
		TemplateHash: "h2",
	})
	events, err := s.events.Query(analytics.QueryFilter{AgentID: "agent-acceptance-1"}, 10)
	if err != nil {
		t.Fatalf("Query events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2: %+v", len(events), events)
	}
	if events[0].TemplateHash != "h1" || events[0].Backend != "local" {
		t.Fatalf("pre-rename event was rewritten: %+v", events[0])
	}
	if events[1].TemplateHash != "h2" || events[1].Backend != "local" {
		t.Fatalf("post-rename event = %+v, want template h2/backend local", events[1])
	}
	entAfterRename, err := idMgr.ResolveByName(identity.KindGroup, "platform-engineering")
	if err != nil {
		t.Fatalf("ResolveByName after rename: %v", err)
	}
	if entAfterRename.ID != groupID {
		t.Fatalf("ResolveByName after rename ID = %q, want %q", entAfterRename.ID, groupID)
	}

	t.Run("criterion 2 URL compat", func(t *testing.T) {
		oldT := s.t
		s.t = t
		defer func() { s.t = oldT }()

		ent, err := idMgr.Allocate(identity.KindGroup, "engineering-c2")
		if err != nil {
			t.Fatalf("Allocate group: %v", err)
		}
		groupID := ent.ID
		if err := s.authSrv.SetGroup(groupID, &authserver.GroupConfig{Scopes: []string{"fs:read_file"}}); err != nil {
			t.Fatalf("SetGroup: %v", err)
		}
		capSpec := admin.CapabilitySpec{
			Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "write_file"},
		}
		s.adminJSON(http.MethodPost, "/policy/subjects/groups/"+groupID+"/capabilities", capSpec, http.StatusCreated, nil)

		resp := s.doRawAdmin(http.MethodGet, "/policy/subjects/groups/"+url.PathEscape("engineering-c2")+"/capabilities")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently {
			data, _ := io.ReadAll(resp.Body)
			t.Fatalf("want 301, got %d body=%s", resp.StatusCode, data)
		}
		loc := resp.Header.Get("Location")
		if !strings.Contains(loc, groupID) {
			t.Fatalf("Location %q doesn't contain ULID %s", loc, groupID)
		}

		followURL := loc
		if strings.HasPrefix(followURL, "/") {
			followURL = s.adminHTTP.URL + followURL
		}
		followResp, err := http.DefaultClient.Get(followURL)
		if err != nil {
			t.Fatalf("follow redirect: %v", err)
		}
		defer followResp.Body.Close()
		if followResp.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(followResp.Body)
			t.Fatalf("follow status=%d want=200 body=%s", followResp.StatusCode, data)
		}
	})

	// Criterion 3: display-name uniqueness enforced; conflicts return 409.
	var first identity.Entity
	s.adminJSON(http.MethodPost, "/identity",
		map[string]string{"kind": string(identity.KindGroup), "display_name": "conflict-c3-a"},
		http.StatusCreated, &first)
	var second identity.Entity
	s.adminJSON(http.MethodPost, "/identity",
		map[string]string{"kind": string(identity.KindGroup), "display_name": "conflict-c3-b"},
		http.StatusCreated, &second)

	var renameConflict map[string]string
	s.adminJSON(http.MethodPut, "/identity/"+second.ID+"/display-name",
		map[string]string{"display_name": first.DisplayName},
		http.StatusConflict, &renameConflict)
	if renameConflict["error"] != "display_name_in_use" {
		t.Fatalf("rename conflict body = %+v", renameConflict)
	}

	var allocateConflict map[string]string
	s.adminJSON(http.MethodPost, "/identity",
		map[string]string{"kind": string(identity.KindGroup), "display_name": first.DisplayName},
		http.StatusConflict, &allocateConflict)
	if !strings.Contains(allocateConflict["error"], "display name in use") {
		t.Fatalf("allocate conflict body = %+v", allocateConflict)
	}

	// Criterion 4: migration idempotency.
	const (
		migrationAgent = "agent-acceptance-c4"
		migrationGroup = "migration-c4-eng"
		migrationRole  = "migration-c4-senior"
	)
	s.authSrv.SetIdentityDispatcher(nil)
	if err := s.authSrv.SetGroup(migrationGroup, &authserver.GroupConfig{Scopes: []string{"fs:write_file"}}); err != nil {
		t.Fatalf("SetGroup migration fixture: %v", err)
	}
	if err := s.authSrv.SetAgentPolicy(migrationAgent, &authserver.AgentPolicy{
		Groups: []string{migrationGroup},
		Grant:  []string{"role:" + migrationRole},
	}); err != nil {
		t.Fatalf("SetAgentPolicy migration fixture: %v", err)
	}
	migrationTemplate := acceptanceSaveTemplate(t, s, "tmpl-accept-c4", "fs.write_file")
	if _, err := s.authSrv.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-accept-c4",
		TemplateHash: migrationTemplate.Hash,
		Subjects: auth.SubjectSelector{
			Groups:       []string{migrationGroup},
			Roles:        []string{migrationRole},
			RoleRequired: migrationRole,
		},
	}); err != nil {
		t.Fatalf("SetGrantBinding migration fixture: %v", err)
	}
	s.authSrv.SetIdentityDispatcher(idMgr)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := s.authSrv.RunIdentityMigration(s.ctx, logger); err != nil {
		t.Fatalf("first RunIdentityMigration: %v", err)
	}
	firstGroups := acceptanceIdentitySnapshot(t, identity.KindGroup, idMgr.List(identity.KindGroup))
	firstRoles := acceptanceIdentitySnapshot(t, identity.KindRole, idMgr.List(identity.KindRole))
	migrationGroupID := firstGroups[migrationGroup]
	migrationRoleID := firstRoles[migrationRole]
	if !identity.IsULID(migrationGroupID) {
		t.Fatalf("migration group ID = %q, want ULID", migrationGroupID)
	}
	if !identity.IsULID(migrationRoleID) {
		t.Fatalf("migration role ID = %q, want ULID", migrationRoleID)
	}

	migratedPolicy, err := s.authSrv.GetAgentPolicy(migrationAgent)
	if err != nil {
		t.Fatalf("GetAgentPolicy migration result: %v", err)
	}
	if migratedPolicy == nil || !slices.Contains(migratedPolicy.Groups, migrationGroupID) ||
		!slices.Contains(migratedPolicy.Grant, "role:"+migrationRoleID) {
		t.Fatalf("migrated policy = %+v, want group %q role %q", migratedPolicy, migrationGroupID, migrationRoleID)
	}
	migratedBinding, err := s.authSrv.GetGrantBinding("bind-accept-c4")
	if err != nil {
		t.Fatalf("GetGrantBinding migration result: %v", err)
	}
	if !slices.Contains(migratedBinding.Subjects.Groups, migrationGroupID) ||
		!slices.Contains(migratedBinding.Subjects.Roles, migrationRoleID) ||
		migratedBinding.Subjects.RoleRequired != migrationRoleID {
		t.Fatalf("migrated binding subjects = %+v, want group %q role %q", migratedBinding.Subjects, migrationGroupID, migrationRoleID)
	}

	if err := s.authSrv.RunIdentityMigration(s.ctx, logger); err != nil {
		t.Fatalf("second RunIdentityMigration: %v", err)
	}
	secondGroups := acceptanceIdentitySnapshot(t, identity.KindGroup, idMgr.List(identity.KindGroup))
	secondRoles := acceptanceIdentitySnapshot(t, identity.KindRole, idMgr.List(identity.KindRole))
	acceptanceRequireSameSnapshot(t, identity.KindGroup, firstGroups, secondGroups)
	acceptanceRequireSameSnapshot(t, identity.KindRole, firstRoles, secondRoles)

	// Criterion 5: verified by CI running all integration tests together.
}

func acceptanceSaveTemplate(t *testing.T, s *e2eSuite, id, tool string) auth.GrantTemplate {
	t.Helper()
	template, err := s.authSrv.SaveGrantTemplate(auth.GrantTemplate{
		ID: id,
		Spec: auth.GrantSpec{
			Type:    auth.GrantTypeMCPCall,
			Backend: "local",
			Tool:    tool,
		},
	})
	if err != nil {
		t.Fatalf("SaveGrantTemplate %s: %v", id, err)
	}
	return template
}

func acceptanceIdentitySnapshot(t *testing.T, kind identity.Kind, entities []identity.Entity) map[string]string {
	t.Helper()
	out := make(map[string]string, len(entities))
	for _, ent := range entities {
		if ent.Kind != kind {
			t.Fatalf("List(%s) returned %s entity: %+v", kind, ent.Kind, ent)
		}
		if existing := out[ent.DisplayName]; existing != "" {
			t.Fatalf("duplicate %s display_name %q: IDs %q and %q", kind, ent.DisplayName, existing, ent.ID)
		}
		out[ent.DisplayName] = ent.ID
	}
	return out
}

func acceptanceRequireSameSnapshot(t *testing.T, kind identity.Kind, before, after map[string]string) {
	t.Helper()
	if len(after) != len(before) {
		t.Fatalf("%s entity count changed after idempotent migration: before=%v after=%v", kind, before, after)
	}
	for name, beforeID := range before {
		if afterID := after[name]; afterID != beforeID {
			t.Fatalf("%s %q ID changed after idempotent migration: before=%q after=%q", kind, name, beforeID, afterID)
		}
	}
}
