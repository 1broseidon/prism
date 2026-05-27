package authserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
)

func TestRARAuthorizeMatchingBindingRendersConsent(t *testing.T) {
	srv := newRARServer(t)
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+rarAuthorizeValues(t, srv, `{"path":"/workspace/a/file.txt"}`, nil).Encode(), nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Capability grants") || !strings.Contains(w.Body.String(), "/workspace/a/") {
		t.Fatalf("consent page did not include concrete grant: %s", w.Body.String())
	}
}

func TestRARAuthorizeLegacyWithoutAuthorizationDetails(t *testing.T) {
	srv := newRARServer(t)
	values := baseAuthorizeValues()
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+values.Encode(), nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestRARAuthorizeRejectsUnknownType(t *testing.T) {
	srv := newRARServer(t)
	values := baseAuthorizeValues()
	values.Set("authorization_details", `[{"type":"other","tool":"fs.write_file","backend":"local"}]`)
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+values.Encode(), nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	assertOAuthError(t, w, http.StatusBadRequest, "invalid_authorization_details")
}

func TestRARAuthorizeRequiresBinding(t *testing.T) {
	srv := newRARServer(t)
	if err := srv.DeleteGrantBinding("bind-agent"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+rarAuthorizeValues(t, srv, `{"path":"/workspace/a/file.txt"}`, nil).Encode(), nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	assertOAuthError(t, w, http.StatusBadRequest, "binding_required")
}

func TestRARAuthorizeRejectsDisabledTool(t *testing.T) {
	srv := newRARServer(t)
	srv.SetGrantToolAvailabilityChecker(func(backend, tool string) bool {
		return backend != "local" || tool != "fs.write_file"
	})
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+rarAuthorizeValues(t, srv, `{"path":"/workspace/a/file.txt"}`, nil).Encode(), nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	assertOAuthError(t, w, http.StatusBadRequest, "tool_disabled")
}

func TestRARAuthorizeRejectsArgsWithoutLeakingField(t *testing.T) {
	srv := newRARServer(t)
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+rarAuthorizeValues(t, srv, `{"path":"/tmp/file.txt"}`, nil).Encode(), nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	body := w.Body.String()
	assertOAuthError(t, w, http.StatusBadRequest, "invalid_authorization_details")
	if strings.Contains(body, "path") {
		t.Fatalf("response leaked field detail: %s", body)
	}
	if !strings.Contains(body, "entry 0") {
		t.Fatalf("response should include entry index: %s", body)
	}
}

func TestRARAuthorizeRejectsWorkspaceConflict(t *testing.T) {
	srv := newRARServer(t)
	p, err := srv.GetAgentPolicy("prism-a")
	if err != nil {
		t.Fatal(err)
	}
	p.BackendPolicies = map[string]auth.BackendPolicy{"local": {WorkspaceSelector: "id:ws-good"}}
	if err := srv.SetAgentPolicy("prism-a", p); err != nil {
		t.Fatal(err)
	}
	ws := &auth.WorkspaceInstance{ID: "ws-bad", Type: "ephemeral", WriteMode: "stage"}
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+rarAuthorizeValues(t, srv, `{"path":"/workspace/a/file.txt"}`, ws).Encode(), nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	assertOAuthError(t, w, http.StatusBadRequest, "invalid_authorization_details")
}

func TestStepUpRedirectAndReturn(t *testing.T) {
	srv := newRARServer(t)
	tmpl, err := srv.GetGrantTemplate("tmpl-fs", 0)
	if err != nil {
		t.Fatal(err)
	}
	tmpl.Spec.AuthFreshnessMax = 600
	if _, err := srv.SaveGrantTemplate(tmpl); err != nil {
		t.Fatal(err)
	}
	if err := srv.DeleteGrantBinding("bind-agent"); err != nil {
		t.Fatal(err)
	}
	latest, _ := srv.GetGrantTemplate("tmpl-fs", 0)
	if _, err := srv.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-agent-fresh",
		TemplateID:   latest.ID,
		TemplateHash: latest.Hash,
		Subjects:     auth.SubjectSelector{AgentIDs: []string{"prism-a"}},
	}); err != nil {
		t.Fatal(err)
	}
	values := rarAuthorizeValues(t, srv, `{"path":"/workspace/a/file.txt"}`, nil)
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+values.Encode(), nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/stepup?") {
		t.Fatalf("status = %d location=%s body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	step := httptest.NewRequest(http.MethodPost, w.Header().Get("Location"), nil)
	stepW := httptest.NewRecorder()
	srv.Routes().ServeHTTP(stepW, step)
	if stepW.Code != http.StatusFound {
		t.Fatalf("step status = %d body=%s", stepW.Code, stepW.Body.String())
	}
	cookies := stepW.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected step-up session cookie")
	}
	r = httptest.NewRequest(http.MethodGet, stepW.Header().Get("Location"), nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w = httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("return status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestRARConsentPostStoresIssuedGrant(t *testing.T) {
	srv := newRARServer(t)
	values := rarAuthorizeValues(t, srv, `{"path":"/workspace/a/file.txt"}`, nil)
	getReq := httptest.NewRequest(http.MethodGet, "/authorize?"+values.Encode(), nil)
	getW := httptest.NewRecorder()
	srv.Routes().ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", getW.Code, getW.Body.String())
	}
	csrf := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(getW.Body.String())
	if len(csrf) != 2 {
		t.Fatalf("csrf token missing in %s", getW.Body.String())
	}
	form := values
	form.Set("_csrf", csrf[1])
	form.Set("label", "Agent A")
	postReq := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range getW.Result().Cookies() {
		postReq.AddCookie(c)
	}
	postW := httptest.NewRecorder()
	srv.Routes().ServeHTTP(postW, postReq)
	if postW.Code != http.StatusFound {
		t.Fatalf("post status = %d body=%s", postW.Code, postW.Body.String())
	}
	u, err := url.Parse(postW.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("redirect missing code: %s", postW.Header().Get("Location"))
	}
	srv.oauth.mu.Lock()
	ac := srv.oauth.codes[code]
	srv.oauth.mu.Unlock()
	if ac == nil || len(ac.issuedGrants) != 1 || ac.issuedGrants[0].TemplateID != "tmpl-fs" {
		t.Fatalf("auth code grants = %+v", ac)
	}
}

func newRARServer(t *testing.T) *Server {
	t.Helper()
	km, err := NewKeyManager("")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(&Config{
		Issuer:          testIssuer,
		TokenTTLSeconds: 3600,
		Clients:         nil,
	}, km, newMemKV(), nil)
	srv.oauth.dynamics["client-a"] = &dynamicClient{
		ClientID:     "client-a",
		ClientName:   "Agent A",
		RedirectURIs: []string{"http://client/cb"},
		PrismID:      "prism-a",
		Label:        "Agent A",
	}
	srv.clients["client-a"] = &ClientConfig{ClientID: "client-a", AllowedScopes: []string{"mcp:connect"}}
	if err := srv.SetAgentPolicy("prism-a", &AgentPolicy{Groups: []string{"eng"}}); err != nil {
		t.Fatal(err)
	}
	prefix := "/workspace/a/"
	tmpl, err := srv.SaveGrantTemplate(auth.GrantTemplate{
		ID: "tmpl-fs",
		Spec: auth.GrantSpec{
			Type:    auth.GrantTypeMCPCall,
			Tool:    "fs.write_file",
			Backend: "local",
			Args:    map[string]auth.Predicate{"path": {Prefix: &prefix}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-agent",
		TemplateID:   tmpl.ID,
		TemplateHash: tmpl.Hash,
		Subjects:     auth.SubjectSelector{AgentIDs: []string{"prism-a"}},
	}); err != nil {
		t.Fatal(err)
	}
	return srv
}

func baseAuthorizeValues() url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {"client-a"},
		"redirect_uri":          {"http://client/cb"},
		"state":                 {"s1"},
		"code_challenge":        {"abcdefghijklmnopqrstuvwxyz0123456789ABCDE"},
		"code_challenge_method": {"S256"},
	}
}

func rarAuthorizeValues(t *testing.T, _ *Server, args string, ws *auth.WorkspaceInstance) url.Values {
	t.Helper()
	entry := map[string]any{
		"type":    auth.GrantTypeMCPCall,
		"tool":    "fs.write_file",
		"backend": "local",
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(args), &decoded); err != nil {
		t.Fatal(err)
	}
	entry["args"] = decoded
	if ws != nil {
		entry["workspace"] = ws
	}
	data, err := json.Marshal([]any{entry})
	if err != nil {
		t.Fatal(err)
	}
	values := baseAuthorizeValues()
	values.Set("authorization_details", string(data))
	return values
}

func assertOAuthError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp OAuthError
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v body=%s", err, w.Body.String())
	}
	if resp.Error != code {
		t.Fatalf("error = %q, want %q body=%s", resp.Error, code, w.Body.String())
	}
}
