package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/identity"
	"github.com/1broseidon/prism/internal/store"
)

type identityCompatGroupManager struct {
	groups map[string]*GroupInfo
}

func (m *identityCompatGroupManager) ListGroups() []GroupInfo {
	out := make([]GroupInfo, 0, len(m.groups))
	for _, g := range m.groups {
		out = append(out, *g)
	}
	return out
}

func (m *identityCompatGroupManager) GetGroup(name string) *GroupInfo {
	if g, ok := m.groups[name]; ok {
		return g
	}
	return nil
}

func (m *identityCompatGroupManager) SetGroup(name string, scopes []string) error {
	m.groups[name] = &GroupInfo{Name: name, Scopes: scopes, Source: "dynamic"}
	return nil
}

func (m *identityCompatGroupManager) SetGroupBackendPolicies(name string, policies map[string]auth.BackendPolicy) error {
	g := m.GetGroup(name)
	if g == nil {
		return nil
	}
	g.BackendPolicies = policies
	return nil
}

func (m *identityCompatGroupManager) DeleteGroup(name string) error {
	delete(m.groups, name)
	return nil
}

func (m *identityCompatGroupManager) DefaultScopes() []string { return nil }
func (m *identityCompatGroupManager) SetDefaultScopes([]string) error {
	return nil
}
func (m *identityCompatGroupManager) DefaultBackendPolicies() map[string]auth.BackendPolicy {
	return nil
}
func (m *identityCompatGroupManager) SetDefaultBackendPolicies(map[string]auth.BackendPolicy) error {
	return nil
}

func newIdentityCompatAPI(t *testing.T) (*API, identity.Dispatcher) {
	t.Helper()
	d := identity.New(store.NewMemoryStore())
	api := NewAPI(
		func() any { return nil },
		nil,
		func() []any { return nil },
		func(string) bool { return false },
		func() int { return 0 },
		func() []any { return nil },
		nil,
		&identityCompatGroupManager{groups: map[string]*GroupInfo{}},
		nil,
		nil,
	)
	api.SetIdentity(d)
	return api, d
}

func TestIdentityURLCompat_ULIDPassThrough(t *testing.T) {
	api, d := newIdentityCompatAPI(t)
	ent, err := d.Allocate(identity.KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	api.groupMgr.(*identityCompatGroupManager).groups[ent.ID] = &GroupInfo{
		Name:   ent.ID,
		Scopes: []string{"fs:write_file"},
		Source: "dynamic",
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/groups/"+ent.ID, nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("unexpected Location = %q", loc)
	}
}

func TestIdentityURLCompat_NameRedirectsToULID(t *testing.T) {
	api, d := newIdentityCompatAPI(t)
	ent, err := d.Allocate(identity.KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/groups/engineering?tab=members", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	want := "/api/v1/groups/" + ent.ID + "?tab=members"
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestIdentityURLCompat_NonexistentGroupReturns404(t *testing.T) {
	// Groups are explicit-create entities: nonexistent names are real 404s
	// from the compat layer (operators must pre-create via SetGroup).
	api, _ := newIdentityCompatAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/groups/nonexistent", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(strings.NewReader(strings.TrimSpace(w.Body.String()))).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := map[string]string{
		"error": "identity_not_found",
		"kind":  "group",
		"name":  "nonexistent",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("body[%q] = %q, want %q (full body %v)", k, got[k], v, got)
		}
	}
}
