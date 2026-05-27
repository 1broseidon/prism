//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/identity"
	"github.com/1broseidon/prism/internal/store"
)

func TestE2E_Identity_BackendRenameKeepsGrantReferencesStable(t *testing.T) {
	s := newE2ESuite(t)
	s.gw.SetStore(s.kv)
	idMgr := identity.New(s.kv)
	s.gw.SetIdentityDispatcher(idMgr)
	s.authSrv.SetIdentityDispatcher(idMgr)

	storeE2EBackendConfig(t, s.kv, "github-prod", map[string]any{
		"url": "http://github.example/mcp",
	})
	oldTemplate, err := s.authSrv.SaveGrantTemplate(auth.GrantTemplate{
		ID:   "tmpl-backend-identity-migration",
		Spec: auth.GrantSpec{Type: auth.GrantTypeMCPCall, Tool: "repo.create_issue", Backend: "github-prod"},
	})
	if err != nil {
		t.Fatalf("SaveGrantTemplate: %v", err)
	}
	if _, err := s.authSrv.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-backend-identity-migration",
		TemplateID:   oldTemplate.ID,
		TemplateHash: oldTemplate.Hash,
		Subjects:     auth.SubjectSelector{AgentIDs: []string{"agent-backend-migration"}},
	}); err != nil {
		t.Fatalf("SetGrantBinding: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := s.gw.RunBackendIdentityMigration(s.ctx, logger, s.authSrv); err != nil {
		t.Fatalf("RunBackendIdentityMigration: %v", err)
	}

	entities := idMgr.List(identity.KindBackend)
	if len(entities) != 1 || entities[0].DisplayName != "github-prod" || !identity.IsULID(entities[0].ID) {
		t.Fatalf("backend identities = %+v", entities)
	}
	backendID := entities[0].ID
	if _, err := s.kv.Get("backend/config/github-prod"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old backend key should be deleted, err=%v", err)
	}
	persisted := loadE2EBackendConfig(t, s.kv, backendID)
	if persisted.DisplayName != "github-prod" || persisted.URL != "http://github.example/mcp" {
		t.Fatalf("persisted backend = %+v", persisted)
	}

	newTemplate := findE2ETemplateBySupersedes(t, s.authSrv.ListGrantTemplates(), oldTemplate.Hash)
	if newTemplate.ID != oldTemplate.ID || newTemplate.Spec.Backend != backendID {
		t.Fatalf("new template = %+v, want backend %s", newTemplate, backendID)
	}
	oldByHash, err := s.authSrv.GetGrantTemplateByHash(oldTemplate.Hash)
	if err != nil || oldByHash.Spec.Backend != "github-prod" {
		t.Fatalf("old template hash no longer resolves correctly: template=%+v err=%v", oldByHash, err)
	}
	newByHash, err := s.authSrv.GetGrantTemplateByHash(newTemplate.Hash)
	if err != nil || newByHash.Spec.Backend != backendID {
		t.Fatalf("new template hash does not resolve correctly: template=%+v err=%v", newByHash, err)
	}
	binding, err := s.authSrv.GetGrantBinding("bind-backend-identity-migration")
	if err != nil {
		t.Fatalf("GetGrantBinding: %v", err)
	}
	if binding.TemplateHash != newTemplate.Hash || binding.TemplateID != newTemplate.ID {
		t.Fatalf("binding not repointed: %+v newTemplate=%+v", binding, newTemplate)
	}

	renamed, err := idMgr.Rename(backendID, "github-production")
	if err != nil {
		t.Fatalf("rename backend: %v", err)
	}
	if renamed.ID != backendID || renamed.DisplayName != "github-production" {
		t.Fatalf("renamed backend = %+v, want id %s display github-production", renamed, backendID)
	}
	if _, err := s.kv.Get("backend/config/" + backendID); err != nil {
		t.Fatalf("backend KV key moved after rename: %v", err)
	}

	templateCount := len(s.authSrv.ListGrantTemplates())
	if err := s.gw.RunBackendIdentityMigration(s.ctx, logger, s.authSrv); err != nil {
		t.Fatalf("second RunBackendIdentityMigration: %v", err)
	}
	after := idMgr.List(identity.KindBackend)
	if len(after) != 1 || after[0].ID != backendID || after[0].DisplayName != "github-production" {
		t.Fatalf("backend identity changed on idempotent rerun: %+v", after)
	}
	if got := len(s.authSrv.ListGrantTemplates()); got != templateCount {
		t.Fatalf("migration created templates on idempotent rerun: before=%d after=%d", templateCount, got)
	}
	if _, err := s.authSrv.GetGrantTemplateByHash(oldTemplate.Hash); err != nil {
		t.Fatalf("old template hash stopped resolving after rename/rerun: %v", err)
	}
	if _, err := s.authSrv.GetGrantTemplateByHash(newTemplate.Hash); err != nil {
		t.Fatalf("new template hash stopped resolving after rename/rerun: %v", err)
	}
}

type e2ePersistedBackend struct {
	DisplayName string `json:"display_name,omitempty"`
	URL         string `json:"url,omitempty"`
}

func storeE2EBackendConfig(t *testing.T, kv store.Store, id string, value map[string]any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal backend config: %v", err)
	}
	if err := kv.Set("backend/config/"+id, data); err != nil {
		t.Fatalf("set backend config: %v", err)
	}
}

func loadE2EBackendConfig(t *testing.T, kv store.Store, id string) e2ePersistedBackend {
	t.Helper()
	data, err := kv.Get("backend/config/" + id)
	if err != nil {
		t.Fatalf("get backend config: %v", err)
	}
	var pb e2ePersistedBackend
	if err := json.Unmarshal(data, &pb); err != nil {
		t.Fatalf("decode backend config: %v", err)
	}
	return pb
}

func findE2ETemplateBySupersedes(t *testing.T, templates []auth.GrantTemplate, oldHash string) auth.GrantTemplate {
	t.Helper()
	for _, template := range templates {
		if template.Supersedes == oldHash {
			return template
		}
	}
	t.Fatalf("template superseding %q not found in %+v", oldHash, templates)
	return auth.GrantTemplate{}
}
