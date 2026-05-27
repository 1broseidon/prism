package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/identity"
	"github.com/1broseidon/prism/internal/store"
)

func TestRunBackendIdentityMigrationSkipsWhenBackendRegistryNonEmpty(t *testing.T) {
	gw, kv, dispatcher := newBackendIdentityMigrationGateway(t)
	grants := newBackendMigrationGrantRewriter()
	if _, err := dispatcher.Allocate(identity.KindBackend, "already-migrated"); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	storeBackendMigrationConfig(t, kv, "github-prod", persistedBackend{URL: "http://github.example/mcp"})
	template := saveBackendMigrationTemplate(t, grants, "tmpl-github", "github-prod")

	if err := gw.RunBackendIdentityMigration(context.Background(), backendMigrationTestLogger(), grants); err != nil {
		t.Fatalf("RunBackendIdentityMigration: %v", err)
	}

	if _, err := kv.Get(backendKVPrefix + "github-prod"); err != nil {
		t.Fatalf("old backend key should remain after skipped migration: %v", err)
	}
	if got := dispatcher.List(identity.KindBackend); len(got) != 1 || got[0].DisplayName != "already-migrated" {
		t.Fatalf("backend identities after skip = %+v", got)
	}
	if templates := grants.ListGrantTemplates(); len(templates) != 1 || templates[0].Hash != template.Hash {
		t.Fatalf("templates changed on skipped migration: %+v", templates)
	}
}

func TestRunBackendIdentityMigrationHappyPath(t *testing.T) {
	gw, kv, dispatcher := newBackendIdentityMigrationGateway(t)
	grants := newBackendMigrationGrantRewriter()
	storeBackendMigrationConfig(t, kv, "github-prod", persistedBackend{URL: "http://github.example/mcp"})
	gw.backends["github-prod"] = &Backend{Config: &config.ServerConfig{
		ID:        "github-prod",
		Namespace: "github-prod",
		URL:       "http://github.example/mcp",
	}}

	if err := gw.RunBackendIdentityMigration(context.Background(), backendMigrationTestLogger(), grants); err != nil {
		t.Fatalf("RunBackendIdentityMigration: %v", err)
	}

	ent := backendMigrationOnlyEntity(t, dispatcher)
	if ent.DisplayName != "github-prod" || !identity.IsULID(ent.ID) {
		t.Fatalf("backend identity = %+v", ent)
	}
	if _, err := kv.Get(backendKVPrefix + "github-prod"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old backend key should be deleted, err=%v", err)
	}
	pb := loadBackendMigrationConfig(t, kv, ent.ID)
	if pb.DisplayName != "github-prod" || pb.URL != "http://github.example/mcp" {
		t.Fatalf("persisted backend = %+v", pb)
	}
	if _, ok := gw.backends["github-prod"]; ok {
		t.Fatal("in-memory backend map still has old key")
	}
	backend := gw.backends[ent.ID]
	if backend == nil || backend.Config.ID != ent.ID || backend.Config.DisplayName != "github-prod" || backend.DisplayName != "github-prod" {
		t.Fatalf("in-memory backend not rekeyed correctly: %+v", backend)
	}
	if backend.Config.Namespace != "github-prod" {
		t.Fatalf("namespace changed during identity migration: %q", backend.Config.Namespace)
	}
}

func TestRunBackendIdentityMigrationIdempotent(t *testing.T) {
	gw, kv, dispatcher := newBackendIdentityMigrationGateway(t)
	grants := newBackendMigrationGrantRewriter()
	storeBackendMigrationConfig(t, kv, "github-prod", persistedBackend{URL: "http://github.example/mcp"})
	template := saveBackendMigrationTemplate(t, grants, "tmpl-github", "github-prod")
	if _, err := grants.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-github",
		TemplateID:   template.ID,
		TemplateHash: template.Hash,
		Subjects:     auth.SubjectSelector{AgentIDs: []string{"agent-a"}},
	}); err != nil {
		t.Fatalf("SetGrantBinding: %v", err)
	}

	if err := gw.RunBackendIdentityMigration(context.Background(), backendMigrationTestLogger(), grants); err != nil {
		t.Fatalf("RunBackendIdentityMigration: %v", err)
	}
	ent := backendMigrationOnlyEntity(t, dispatcher)
	keysBefore := backendMigrationBackendKeys(t, kv)
	templateCountBefore := len(grants.ListGrantTemplates())
	bindingBefore := grants.binding("bind-github")

	if err := gw.RunBackendIdentityMigration(context.Background(), backendMigrationTestLogger(), grants); err != nil {
		t.Fatalf("second RunBackendIdentityMigration: %v", err)
	}

	if got := backendMigrationOnlyEntity(t, dispatcher); got.ID != ent.ID {
		t.Fatalf("backend reminted on second run: before=%+v after=%+v", ent, got)
	}
	keysAfter := backendMigrationBackendKeys(t, kv)
	if len(keysAfter) != len(keysBefore) || keysAfter[0] != keysBefore[0] {
		t.Fatalf("backend keys changed on second run: before=%v after=%v", keysBefore, keysAfter)
	}
	if got := len(grants.ListGrantTemplates()); got != templateCountBefore {
		t.Fatalf("template count changed on second run: before=%d after=%d", templateCountBefore, got)
	}
	if bindingAfter := grants.binding("bind-github"); bindingAfter.TemplateHash != bindingBefore.TemplateHash {
		t.Fatalf("binding changed on second run: before=%+v after=%+v", bindingBefore, bindingAfter)
	}
}

func TestRunBackendIdentityMigrationRewritesGrantTemplatesAndBindings(t *testing.T) {
	gw, kv, dispatcher := newBackendIdentityMigrationGateway(t)
	grants := newBackendMigrationGrantRewriter()
	storeBackendMigrationConfig(t, kv, "github-prod", persistedBackend{URL: "http://github.example/mcp"})
	oldTemplate := saveBackendMigrationTemplate(t, grants, "tmpl-github", "github-prod")
	untouched := saveBackendMigrationTemplate(t, grants, "tmpl-local", "local")
	if _, err := grants.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-github",
		TemplateID:   oldTemplate.ID,
		TemplateHash: oldTemplate.Hash,
		Subjects:     auth.SubjectSelector{AgentIDs: []string{"agent-a"}},
	}); err != nil {
		t.Fatalf("SetGrantBinding: %v", err)
	}

	if err := gw.RunBackendIdentityMigration(context.Background(), backendMigrationTestLogger(), grants); err != nil {
		t.Fatalf("RunBackendIdentityMigration: %v", err)
	}

	newBackendID := backendMigrationOnlyEntity(t, dispatcher).ID
	newTemplate, ok := grants.templateBySupersedes(oldTemplate.Hash)
	if !ok {
		t.Fatalf("new template superseding %q not found in %+v", oldTemplate.Hash, grants.ListGrantTemplates())
	}
	if newTemplate.ID != oldTemplate.ID || newTemplate.Spec.Backend != newBackendID || newTemplate.Hash == oldTemplate.Hash {
		t.Fatalf("rewritten template = %+v, old=%+v newBackend=%s", newTemplate, oldTemplate, newBackendID)
	}
	if oldStillPresent, ok := grants.templateByHash(oldTemplate.Hash); !ok || oldStillPresent.Spec.Backend != "github-prod" {
		t.Fatalf("old template version missing or mutated: %+v ok=%v", oldStillPresent, ok)
	}
	if other, ok := grants.templateByHash(untouched.Hash); !ok || other.Spec.Backend != "local" {
		t.Fatalf("unrelated template changed: %+v ok=%v", other, ok)
	}
	binding := grants.binding("bind-github")
	if binding.TemplateHash != newTemplate.Hash || binding.TemplateID != newTemplate.ID {
		t.Fatalf("binding was not repointed to new template: %+v newTemplate=%+v", binding, newTemplate)
	}
}

func newBackendIdentityMigrationGateway(t *testing.T) (*Gateway, *store.MemoryStore, identity.Dispatcher) {
	t.Helper()
	kv := store.NewMemoryStore()
	gw := New(backendMigrationTestLogger())
	gw.SetStore(kv)
	dispatcher := identity.New(kv)
	gw.SetIdentityDispatcher(dispatcher)
	return gw, kv, dispatcher
}

func storeBackendMigrationConfig(t *testing.T, kv store.Store, id string, pb persistedBackend) {
	t.Helper()
	data, err := json.Marshal(&pb)
	if err != nil {
		t.Fatalf("marshal persisted backend: %v", err)
	}
	if err := kv.Set(backendKVPrefix+id, data); err != nil {
		t.Fatalf("store backend %q: %v", id, err)
	}
}

func loadBackendMigrationConfig(t *testing.T, kv store.Store, id string) persistedBackend {
	t.Helper()
	data, err := kv.Get(backendKVPrefix + id)
	if err != nil {
		t.Fatalf("get backend %q: %v", id, err)
	}
	var pb persistedBackend
	if err := json.Unmarshal(data, &pb); err != nil {
		t.Fatalf("decode backend %q: %v", id, err)
	}
	return pb
}

func backendMigrationBackendKeys(t *testing.T, kv store.Store) []string {
	t.Helper()
	keys, err := kv.List(backendKVPrefix)
	if err != nil {
		t.Fatalf("list backend keys: %v", err)
	}
	return keys
}

func backendMigrationOnlyEntity(t *testing.T, dispatcher identity.Dispatcher) identity.Entity {
	t.Helper()
	entities := dispatcher.List(identity.KindBackend)
	if len(entities) != 1 {
		t.Fatalf("backend identities = %+v, want exactly one", entities)
	}
	return entities[0]
}

func saveBackendMigrationTemplate(t *testing.T, grants *backendMigrationGrantRewriter, id, backend string) auth.GrantTemplate {
	t.Helper()
	template, err := grants.SaveGrantTemplate(auth.GrantTemplate{
		ID:   id,
		Spec: auth.GrantSpec{Type: auth.GrantTypeMCPCall, Tool: "repo.create_issue", Backend: backend},
	})
	if err != nil {
		t.Fatalf("SaveGrantTemplate: %v", err)
	}
	return template
}

func backendMigrationTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type backendMigrationGrantRewriter struct {
	templates []auth.GrantTemplate
	bindings  []auth.GrantBinding
}

func newBackendMigrationGrantRewriter() *backendMigrationGrantRewriter {
	return &backendMigrationGrantRewriter{}
}

func (r *backendMigrationGrantRewriter) ListGrantTemplates() []auth.GrantTemplate {
	out := make([]auth.GrantTemplate, len(r.templates))
	copy(out, r.templates)
	return out
}

func (r *backendMigrationGrantRewriter) SaveGrantTemplate(t auth.GrantTemplate) (auth.GrantTemplate, error) {
	if t.Spec.Type == "" {
		t.Spec.Type = auth.GrantTypeMCPCall
	}
	if err := t.Spec.Validate(); err != nil {
		return auth.GrantTemplate{}, err
	}
	hash, err := auth.CanonicalGrantHash(t.Spec)
	if err != nil {
		return auth.GrantTemplate{}, err
	}
	latestVersion := 0
	latestHash := ""
	for _, existing := range r.templates {
		if existing.ID == t.ID && existing.Version >= latestVersion {
			latestVersion = existing.Version
			latestHash = existing.Hash
		}
	}
	t.Version = latestVersion + 1
	t.Hash = hash
	if latestHash != "" {
		t.Supersedes = latestHash
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	r.templates = append(r.templates, t)
	return t, nil
}

func (r *backendMigrationGrantRewriter) ListGrantBindings() []auth.GrantBinding {
	out := make([]auth.GrantBinding, len(r.bindings))
	copy(out, r.bindings)
	return out
}

func (r *backendMigrationGrantRewriter) SetGrantBinding(b auth.GrantBinding) (auth.GrantBinding, error) {
	if b.TemplateID == "" {
		if template, ok := r.templateByHash(b.TemplateHash); ok {
			b.TemplateID = template.ID
		}
	}
	for i := range r.bindings {
		if r.bindings[i].ID == b.ID {
			r.bindings[i] = b
			return b, nil
		}
	}
	r.bindings = append(r.bindings, b)
	return b, nil
}

func (r *backendMigrationGrantRewriter) binding(id string) auth.GrantBinding {
	for _, binding := range r.bindings {
		if binding.ID == id {
			return binding
		}
	}
	return auth.GrantBinding{}
}

func (r *backendMigrationGrantRewriter) templateByHash(hash string) (auth.GrantTemplate, bool) {
	for _, template := range r.templates {
		if template.Hash == hash {
			return template, true
		}
	}
	return auth.GrantTemplate{}, false
}

func (r *backendMigrationGrantRewriter) templateBySupersedes(hash string) (auth.GrantTemplate, bool) {
	for _, template := range r.templates {
		if template.Supersedes == hash {
			return template, true
		}
	}
	return auth.GrantTemplate{}, false
}
