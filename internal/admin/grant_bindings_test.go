package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
)

func TestGrantBindingCreateResolvesLatestHash(t *testing.T) {
	api, _ := newTestGrantAPI(t)
	_ = createTemplateVersion(t, api, false)
	latest := createTemplateVersion(t, api, true)
	body := `{"id":"bind-eng","template_id":"tmpl-fs","subjects":{"groups":["eng"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/grant-bindings", strings.NewReader(body))
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var binding auth.GrantBinding
	if err := json.NewDecoder(w.Body).Decode(&binding); err != nil {
		t.Fatal(err)
	}
	if binding.TemplateHash != latest.Hash {
		t.Fatalf("binding hash = %q, want %q", binding.TemplateHash, latest.Hash)
	}
}

func TestGrantBindingMismatchedHashConflict(t *testing.T) {
	api, _ := newTestGrantAPI(t)
	old := createTemplateVersion(t, api, false)
	_ = createTemplateVersion(t, api, true)
	body := `{"id":"bind-eng","template_id":"tmpl-fs","template_hash":"` + old.Hash + `","subjects":{"groups":["eng"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/grant-bindings", strings.NewReader(body))
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestGrantBindingDelete(t *testing.T) {
	api, _ := newTestGrantAPI(t)
	_ = createTemplateVersion(t, api, false)
	body := `{"id":"bind-eng","template_id":"tmpl-fs","subjects":{"groups":["eng"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/grant-bindings", strings.NewReader(body))
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodDelete, "/api/v1/grant-bindings/bind-eng", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/grant-bindings/bind-eng", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestAgentPolicyResolutionIncludesGrantBindings(t *testing.T) {
	api, _ := newTestAPI()
	api.SetBackendPolicyTraceProvider(&stubTraceProvider{
		resolutions: []AgentPolicyResolution{
			{
				BackendID: "local",
				Bindings: []BindingRef{
					{ID: "bind-direct", TemplateID: "tmpl-a", TemplateHash: "sha256-a", Source: "agent:prism-uuid-1"},
					{ID: "bind-group", TemplateID: "tmpl-b", TemplateHash: "sha256-b", Source: "group:eng"},
				},
			},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/prism-uuid-1/policy-resolution", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got []AgentPolicyResolution
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Bindings) != 2 || got[0].Bindings[1].Source != "group:eng" {
		t.Fatalf("resolution = %+v", got)
	}
}

func createTemplateVersion(t *testing.T, api *API, cnf bool) auth.GrantTemplate {
	t.Helper()
	body := `{"id":"tmpl-fs","spec":{"type":"prism.mcp.call","tool":"fs.write_file","backend":"local"`
	if cnf {
		body += `,"cnf_required":true`
	}
	body += `}}`
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
	return created
}
