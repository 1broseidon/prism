//go:build integration

package integration

import (
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/authserver"
	"github.com/1broseidon/prism/internal/identity"
)

func TestE2E_Identity_GroupRenameSurvivesPolicy(t *testing.T) {
	s := newE2ESuite(t)
	idMgr := identity.New(s.kv)
	s.authSrv.SetIdentityDispatcher(idMgr)

	if err := s.authSrv.SetGroup("engineering", &authserver.GroupConfig{Scopes: []string{"fs:write_file"}}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	prismID := "prism-migration-agent"
	if err := s.authSrv.SetAgentPolicy(prismID, &authserver.AgentPolicy{
		Groups: []string{"engineering"},
		Grant:  []string{"role:senior"},
	}); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}
	template, err := s.authSrv.SaveGrantTemplate(auth.GrantTemplate{
		ID:   "tmpl-identity-migration",
		Spec: auth.GrantSpec{Type: auth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local"},
	})
	if err != nil {
		t.Fatalf("SaveGrantTemplate: %v", err)
	}
	if _, err := s.authSrv.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-identity-migration",
		TemplateHash: template.Hash,
		Subjects: auth.SubjectSelector{
			Groups:       []string{"engineering"},
			Roles:        []string{"senior"},
			RoleRequired: "senior",
		},
	}); err != nil {
		t.Fatalf("SetGrantBinding: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := s.authSrv.RunIdentityMigration(s.ctx, logger); err != nil {
		t.Fatalf("RunIdentityMigration: %v", err)
	}

	policy, err := s.authSrv.GetAgentPolicy(prismID)
	if err != nil {
		t.Fatalf("GetAgentPolicy: %v", err)
	}
	if len(policy.Groups) != 1 || !identity.IsULID(policy.Groups[0]) {
		t.Fatalf("policy groups were not migrated to ULIDs: %+v", policy.Groups)
	}
	if len(policy.Grant) != 1 || !strings.HasPrefix(policy.Grant[0], "role:") || !identity.IsULID(strings.TrimPrefix(policy.Grant[0], "role:")) {
		t.Fatalf("policy grant was not migrated to role ULID marker: %+v", policy.Grant)
	}
	groupID := policy.Groups[0]
	roleID := strings.TrimPrefix(policy.Grant[0], "role:")

	binding, err := s.authSrv.GetGrantBinding("bind-identity-migration")
	if err != nil {
		t.Fatalf("GetGrantBinding: %v", err)
	}
	if !slices.Equal(binding.Subjects.Groups, []string{groupID}) {
		t.Fatalf("binding groups = %v, want [%s]", binding.Subjects.Groups, groupID)
	}
	if !slices.Equal(binding.Subjects.Roles, []string{roleID}) || binding.Subjects.RoleRequired != roleID {
		t.Fatalf("binding roles were not migrated to role ID: %+v", binding.Subjects)
	}

	if _, err := idMgr.Rename(groupID, "platform"); err != nil {
		t.Fatalf("rename group: %v", err)
	}
	if _, err := idMgr.Rename(roleID, "principal"); err != nil {
		t.Fatalf("rename role: %v", err)
	}

	policyAfterRename, err := s.authSrv.GetAgentPolicy(prismID)
	if err != nil {
		t.Fatalf("GetAgentPolicy after rename: %v", err)
	}
	if !slices.Equal(policyAfterRename.Groups, []string{groupID}) || !slices.Equal(policyAfterRename.Grant, []string{"role:" + roleID}) {
		t.Fatalf("policy references changed after identity rename: %+v", policyAfterRename)
	}
	bindingAfterRename, err := s.authSrv.GetGrantBinding("bind-identity-migration")
	if err != nil {
		t.Fatalf("GetGrantBinding after rename: %v", err)
	}
	if !slices.Equal(bindingAfterRename.Subjects.Groups, []string{groupID}) ||
		!slices.Equal(bindingAfterRename.Subjects.Roles, []string{roleID}) ||
		bindingAfterRename.Subjects.RoleRequired != roleID {
		t.Fatalf("binding references changed after identity rename: %+v", bindingAfterRename.Subjects)
	}
	if matches := s.authSrv.ListGrantBindingsForSubject(prismID, policyAfterRename.Groups, []string{roleID}); len(matches) != 1 || matches[0].ID != "bind-identity-migration" {
		t.Fatalf("renamed identities did not preserve grant eligibility: %+v", matches)
	}
}
