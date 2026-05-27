package authserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/auth"
)

// stepUpPOST issues a POST /stepup with the given form-encoded body.
func stepUpPOST(srv *Server, body url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/stepup", strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	return w
}

func TestStepUpPostWithoutStateRejected(t *testing.T) {
	srv := newRARServer(t)
	w := stepUpPOST(srv, url.Values{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == authSessionCookie {
			t.Fatalf("anonymous POST minted session cookie: %+v", c)
		}
	}
}

func TestStepUpPostWithUnknownStateRejected(t *testing.T) {
	srv := newRARServer(t)
	w := stepUpPOST(srv, url.Values{"state": {"this-token-was-never-issued"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == authSessionCookie {
			t.Fatalf("unknown state minted session cookie: %+v", c)
		}
	}
}

func TestStepUpPostWithAlreadyUsedStateRejected(t *testing.T) {
	srv := newRARServer(t)
	token, err := srv.issueStepUpState("/authorize?foo=bar")
	if err != nil {
		t.Fatal(err)
	}
	first := stepUpPOST(srv, url.Values{"state": {token}})
	if first.Code != http.StatusFound {
		t.Fatalf("first POST status = %d body=%s", first.Code, first.Body.String())
	}
	second := stepUpPOST(srv, url.Values{"state": {token}})
	if second.Code != http.StatusBadRequest {
		t.Fatalf("replay POST status = %d body=%s", second.Code, second.Body.String())
	}
}

func TestStepUpPostWithExpiredStateRejected(t *testing.T) {
	srv := newRARServer(t)
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return now })
	token, err := srv.issueStepUpState("/authorize")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(stepUpStateTTL + time.Second)
	w := stepUpPOST(srv, url.Values{"state": {token}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestStepUpHappyPathMintsSessionAndRedirects(t *testing.T) {
	srv := newRARServer(t)
	tmpl, err := srv.GetGrantTemplate("tmpl-fs", 0)
	if err != nil {
		t.Fatal(err)
	}
	tmpl.Spec.AuthFreshnessMax = 600
	if _, err := srv.SaveGrantTemplate(tmpl); err != nil {
		t.Fatal(err)
	}
	latest, _ := srv.GetGrantTemplate("tmpl-fs", 0)
	if err := srv.DeleteGrantBinding("bind-agent"); err != nil {
		t.Fatal(err)
	}
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
	if w.Code != http.StatusFound {
		t.Fatalf("authorize status = %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/stepup?state=") {
		t.Fatalf("authorize did not redirect to /stepup with state: %s", loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("redirect missing state token")
	}

	// GET /stepup?state=... renders the form with the state preserved.
	getReq := httptest.NewRequest(http.MethodGet, loc, nil)
	getResp := httptest.NewRecorder()
	srv.Routes().ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET stepup status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	if !strings.Contains(getResp.Body.String(), `name="state" value="`+state+`"`) {
		t.Fatalf("stepup form missing state input: %s", getResp.Body.String())
	}

	// POST /stepup with state mints session and redirects back to /authorize.
	post := stepUpPOST(srv, url.Values{"state": {state}})
	if post.Code != http.StatusFound {
		t.Fatalf("POST stepup status = %d body=%s", post.Code, post.Body.String())
	}
	cookies := post.Result().Cookies()
	gotSession := false
	for _, c := range cookies {
		if c.Name == authSessionCookie {
			gotSession = true
		}
	}
	if !gotSession {
		t.Fatal("expected session cookie after successful step-up")
	}
	if got := post.Header().Get("Location"); !strings.HasPrefix(got, "/authorize") {
		t.Fatalf("redirect = %q, want prefix /authorize", got)
	}

	// Second POST with the same state is rejected — proves single-use.
	replay := stepUpPOST(srv, url.Values{"state": {state}})
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("replay POST status = %d body=%s", replay.Code, replay.Body.String())
	}
}

func TestStepUpGetWithoutStateRejected(t *testing.T) {
	srv := newRARServer(t)
	r := httptest.NewRequest(http.MethodGet, "/stepup", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestStepUpGetWithUnknownStateRejected(t *testing.T) {
	srv := newRARServer(t)
	r := httptest.NewRequest(http.MethodGet, "/stepup?state=does-not-exist", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}
