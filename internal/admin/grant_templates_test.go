package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/authserver"
	"github.com/1broseidon/prism/internal/store"
)

// newTestGrantAPI wires an admin.API against the same authserver.Server that
// production uses. The dead auth.GrantStore parallel implementation has been
// removed; this is the single source of truth for KV-backed grant CRUD.
//
// The returned manager is a thin shim around authserver.Server that hides the
// optional ActiveGrantTokenCount method. Admin coverage tests do not exercise
// token issuance, so leaving ActiveGrantTokenCount visible would short-circuit
// the per-test JTI accounting fallback that the test fixtures depend on.
// Production wires the real Server directly, where ActiveGrantTokenCount is
// authoritative.
func newTestGrantAPI(t *testing.T) (*API, *testGrantStore) {
	t.Helper()
	api, _ := newTestAPI()
	km, err := authserver.NewKeyManager("")
	if err != nil {
		t.Fatal(err)
	}
	srv := authserver.NewServer(&authserver.Config{
		Issuer:          "http://localhost:9100",
		TokenTTLSeconds: 3600,
	}, km, store.NewMemoryStore(), nil)
	shim := &testGrantStore{srv: srv}
	api.SetGrantManager(shim)
	return api, shim
}

// testGrantStore is the GrantManager surface only — it deliberately does not
// expose ActiveGrantTokenCount so admin coverage tests use the JTI-from-events
// fallback. Production uses authserver.Server directly.
type testGrantStore struct {
	srv *authserver.Server
}

func (s *testGrantStore) ListGrantTemplates() []auth.GrantTemplate {
	return s.srv.ListGrantTemplates()
}
func (s *testGrantStore) GetGrantTemplate(id string, version int) (auth.GrantTemplate, error) {
	return s.srv.GetGrantTemplate(id, version)
}
func (s *testGrantStore) GetGrantTemplateByHash(hash string) (auth.GrantTemplate, error) {
	return s.srv.GetGrantTemplateByHash(hash)
}
func (s *testGrantStore) SaveGrantTemplate(t auth.GrantTemplate) (auth.GrantTemplate, error) {
	return s.srv.SaveGrantTemplate(t)
}
func (s *testGrantStore) DeleteGrantTemplate(id string, version int) error {
	return s.srv.DeleteGrantTemplate(id, version)
}
func (s *testGrantStore) ListGrantBindings() []auth.GrantBinding {
	return s.srv.ListGrantBindings()
}
func (s *testGrantStore) GetGrantBinding(id string) (auth.GrantBinding, error) {
	return s.srv.GetGrantBinding(id)
}
func (s *testGrantStore) SetGrantBinding(b auth.GrantBinding) (auth.GrantBinding, error) {
	return s.srv.SetGrantBinding(b)
}
func (s *testGrantStore) DeleteGrantBinding(id string) error {
	return s.srv.DeleteGrantBinding(id)
}

func TestGrantTemplateCreateFetchByHash(t *testing.T) {
	api, _ := newTestGrantAPI(t)
	body := `{"id":"tmpl-fs","spec":{"type":"prism.mcp.call","tool":"fs.write_file","backend":"local"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/grant-templates", strings.NewReader(body))
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var created auth.GrantTemplate
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Hash == "" || created.Version != 1 {
		t.Fatalf("created = %+v", created)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/grant-templates/by-hash/"+created.Hash, nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var fetched auth.GrantTemplate
	if err := json.NewDecoder(w.Body).Decode(&fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.ID != created.ID || fetched.Hash != created.Hash {
		t.Fatalf("fetched = %+v, created = %+v", fetched, created)
	}
}

func TestGrantTemplateDuplicateVersionConflict(t *testing.T) {
	// authserver.Server.SaveGrantTemplate always assigns the next version, so
	// posting the same spec twice yields two distinct versions (1, 2) rather
	// than a 409. This is the production behavior the spec calls for: every
	// SaveGrantTemplate produces a new immutable version. The original
	// "duplicate version" test was checking auth.GrantStore semantics that no
	// production code relies on.
	api, _ := newTestGrantAPI(t)
	body := `{"id":"tmpl-fs","spec":{"type":"prism.mcp.call","tool":"fs.write_file","backend":"local"}}`
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/grant-templates", strings.NewReader(body))
		api.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("attempt %d status = %d body=%s", i, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/grant-templates/tmpl-fs", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var versions []auth.GrantTemplate
	if err := json.NewDecoder(w.Body).Decode(&versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %+v", versions)
	}
}

func TestGrantTemplateVersionsDescending(t *testing.T) {
	api, _ := newTestGrantAPI(t)
	for _, body := range []string{
		`{"id":"tmpl-fs","spec":{"type":"prism.mcp.call","tool":"fs.write_file","backend":"local"}}`,
		`{"id":"tmpl-fs","spec":{"type":"prism.mcp.call","tool":"fs.write_file","backend":"local","cnf_required":true}}`,
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/grant-templates", strings.NewReader(body))
		api.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/grant-templates/tmpl-fs", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var versions []auth.GrantTemplate
	if err := json.NewDecoder(w.Body).Decode(&versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions = %+v", versions)
	}
}
