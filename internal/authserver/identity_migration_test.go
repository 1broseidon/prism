package authserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/identity"
	"github.com/1broseidon/prism/internal/store"
)

// TestRunIdentityMigrationSkipsWhenPoliciesAllULIDKeyed verifies the
// post-task-50 idempotency contract: migration skips only when every
// agent policy already uses ULIDs for Groups + role:* markers. Unrelated
// dispatcher entries (added eagerly by SetGroup, for instance) no longer
// gate the rewrite — the actual policy shape decides.
func TestRunIdentityMigrationSkipsWhenPoliciesAllULIDKeyed(t *testing.T) {
	srv, _, dispatcher := newIdentityMigrationTestServer(t)
	// Pre-existing ULID-keyed policy (e.g. from a previous migration run).
	groupEnt, err := dispatcher.Allocate(identity.KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate group: %v", err)
	}
	roleEnt, err := dispatcher.Allocate(identity.KindRole, "senior")
	if err != nil {
		t.Fatalf("Allocate role: %v", err)
	}
	if err := srv.SetAgentPolicy("prism-a", &AgentPolicy{
		Groups: []string{groupEnt.ID},
		Grant:  []string{"role:" + roleEnt.ID, "fs:write_file"},
	}); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}

	if err := srv.RunIdentityMigration(context.Background(), identityMigrationTestLogger()); err != nil {
		t.Fatalf("RunIdentityMigration: %v", err)
	}

	policy, err := srv.GetAgentPolicy("prism-a")
	if err != nil {
		t.Fatalf("GetAgentPolicy: %v", err)
	}
	// The policy was already ULID-keyed; migration must not touch it.
	if !slices.Equal(policy.Groups, []string{groupEnt.ID}) {
		t.Fatalf("policy.Groups changed: %+v", policy.Groups)
	}
	if !slices.Equal(policy.Grant, []string{"role:" + roleEnt.ID, "fs:write_file"}) {
		t.Fatalf("policy.Grant changed: %+v", policy.Grant)
	}
}

func TestRunIdentityMigrationHappyPath(t *testing.T) {
	srv, kv, dispatcher := newIdentityMigrationTestServer(t)
	if err := srv.SetGroup("engineering", &GroupConfig{Scopes: []string{"fs:write_file"}}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	if err := srv.SetAgentPolicy("prism-a", &AgentPolicy{
		Groups: []string{"engineering", "ops"},
		Grant:  []string{"role:senior", "fs:read_file", "role:approver"},
	}); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}
	template := identityMigrationSaveTemplate(t, srv)
	if _, err := srv.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-eng",
		TemplateHash: template.Hash,
		Subjects: auth.SubjectSelector{
			Groups:       []string{"engineering"},
			Roles:        []string{"senior"},
			RoleRequired: "approver",
		},
	}); err != nil {
		t.Fatalf("SetGrantBinding: %v", err)
	}

	if err := srv.RunIdentityMigration(context.Background(), identityMigrationTestLogger()); err != nil {
		t.Fatalf("RunIdentityMigration: %v", err)
	}

	engineeringID := identityMigrationEntityID(t, dispatcher, identity.KindGroup, "engineering")
	opsID := identityMigrationEntityID(t, dispatcher, identity.KindGroup, "ops")
	seniorID := identityMigrationEntityID(t, dispatcher, identity.KindRole, "senior")
	approverID := identityMigrationEntityID(t, dispatcher, identity.KindRole, "approver")

	policy, err := srv.GetAgentPolicy("prism-a")
	if err != nil {
		t.Fatalf("GetAgentPolicy: %v", err)
	}
	if !slices.Equal(policy.Groups, []string{engineeringID, opsID}) {
		t.Fatalf("policy groups = %v, want [%s %s]", policy.Groups, engineeringID, opsID)
	}
	wantGrant := []string{"role:" + seniorID, "fs:read_file", "role:" + approverID}
	if !slices.Equal(policy.Grant, wantGrant) {
		t.Fatalf("policy grant = %v, want %v", policy.Grant, wantGrant)
	}

	binding, err := srv.GetGrantBinding("bind-eng")
	if err != nil {
		t.Fatalf("GetGrantBinding: %v", err)
	}
	if !slices.Equal(binding.Subjects.Groups, []string{engineeringID}) {
		t.Fatalf("binding groups = %v, want [%s]", binding.Subjects.Groups, engineeringID)
	}
	if !slices.Equal(binding.Subjects.Roles, []string{seniorID}) {
		t.Fatalf("binding roles = %v, want [%s]", binding.Subjects.Roles, seniorID)
	}
	if binding.Subjects.RoleRequired != approverID {
		t.Fatalf("binding role_required = %q, want %q", binding.Subjects.RoleRequired, approverID)
	}

	if _, err := kv.Get(groupKeyPrefix + "engineering"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old group key should be deleted, err=%v", err)
	}
	if _, err := kv.Get(groupKeyPrefix + engineeringID); err != nil {
		t.Fatalf("new group key missing: %v", err)
	}
	if scopes := srv.ResolveScopesByPrismID("prism-a"); !slices.Contains(scopes, "fs:write_file") {
		t.Fatalf("rewritten group id did not preserve scope resolution: %v", scopes)
	}
}

// TestRunIdentityMigrationIdempotentOnRerun verifies that running the
// migration twice in a row leaves the same state (no duplicate dispatcher
// entries, no double-rewrite of policy fields). The first run rewrites
// name-keyed references to ULIDs; the second run sees ULID-keyed policies
// and skips per identityMigrationPoliciesAllULIDKeyed.
func TestRunIdentityMigrationIdempotentOnRerun(t *testing.T) {
	srv, kv, dispatcher := newIdentityMigrationTestServer(t)
	groupCfg := &GroupConfig{Scopes: []string{"fs:write_file"}}
	if err := srv.SetGroup("engineering", groupCfg); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	if err := srv.SetAgentPolicy("prism-a", &AgentPolicy{
		Groups: []string{"engineering"},
		Grant:  []string{"role:senior"},
	}); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}
	template := identityMigrationSaveTemplate(t, srv)
	if _, err := srv.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-eng",
		TemplateHash: template.Hash,
		Subjects:     auth.SubjectSelector{Groups: []string{"engineering"}, Roles: []string{"senior"}},
	}); err != nil {
		t.Fatalf("SetGrantBinding: %v", err)
	}

	if err := srv.RunIdentityMigration(context.Background(), identityMigrationTestLogger()); err != nil {
		t.Fatalf("first RunIdentityMigration: %v", err)
	}
	engineeringID := identityMigrationEntityID(t, dispatcher, identity.KindGroup, "engineering")
	seniorID := identityMigrationEntityID(t, dispatcher, identity.KindRole, "senior")

	// Second pass — must be a no-op for the dispatcher and the policies.
	if err := srv.RunIdentityMigration(context.Background(), identityMigrationTestLogger()); err != nil {
		t.Fatalf("second RunIdentityMigration: %v", err)
	}

	if got := dispatcher.List(identity.KindGroup); len(got) != 1 || got[0].ID != engineeringID {
		t.Fatalf("groups after rerun = %+v, want only %s", got, engineeringID)
	}
	if got := dispatcher.List(identity.KindRole); len(got) != 1 || got[0].ID != seniorID {
		t.Fatalf("roles after rerun = %+v, want only %s", got, seniorID)
	}
	if _, err := kv.Get(groupKeyPrefix + engineeringID); err != nil {
		t.Fatalf("new group key missing after rerun: %v", err)
	}
	policy, err := srv.GetAgentPolicy("prism-a")
	if err != nil {
		t.Fatalf("GetAgentPolicy: %v", err)
	}
	if !slices.Equal(policy.Groups, []string{engineeringID}) || !slices.Equal(policy.Grant, []string{"role:" + seniorID}) {
		t.Fatalf("policy changed on rerun: %+v", policy)
	}
	binding, err := srv.GetGrantBinding("bind-eng")
	if err != nil {
		t.Fatalf("GetGrantBinding: %v", err)
	}
	if !slices.Equal(binding.Subjects.Groups, []string{engineeringID}) || !slices.Equal(binding.Subjects.Roles, []string{seniorID}) {
		t.Fatalf("binding changed on rerun: %+v", binding.Subjects)
	}
}

func newIdentityMigrationTestServer(t *testing.T) (*Server, store.Store, identity.Dispatcher) {
	t.Helper()
	kv := store.NewMemoryStore()
	srv := NewServer(&Config{Issuer: testIssuer, TokenTTLSeconds: 3600}, mustTestKeyManager(t), kv, identityMigrationTestLogger())
	dispatcher := identity.New(kv)
	srv.SetIdentityDispatcher(dispatcher)
	return srv, kv, dispatcher
}

func identityMigrationSaveTemplate(t *testing.T, srv *Server) auth.GrantTemplate {
	t.Helper()
	template, err := srv.SaveGrantTemplate(auth.GrantTemplate{
		ID:   "tmpl-fs",
		Spec: auth.GrantSpec{Type: auth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local"},
	})
	if err != nil {
		t.Fatalf("SaveGrantTemplate: %v", err)
	}
	return template
}

func identityMigrationEntityID(t *testing.T, dispatcher identity.Dispatcher, kind identity.Kind, name string) string {
	t.Helper()
	ent, err := dispatcher.ResolveByName(kind, name)
	if err != nil {
		t.Fatalf("ResolveByName(%s, %q): %v", kind, name, err)
	}
	return ent.ID
}

func identityMigrationTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
