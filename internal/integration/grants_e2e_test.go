//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/analytics"
	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/authserver"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/gateway"
	"github.com/1broseidon/prism/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	jwxjwt "github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	e2eIssuer       = "http://prism-auth.e2e"
	redirectURI     = "http://client.example/callback"
	codeVerifier    = "verifier-abcdefghijklmnopqrstuvwxyz0123456789"
	templateFixture = "testdata/grants/templates/fs_write_ephemeral.json"
	bindingFixture  = "testdata/grants/bindings/engineering_senior.json"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type e2eSuite struct {
	t         *testing.T
	ctx       context.Context
	clock     *testClock
	kv        store.Store
	authSrv   *authserver.Server
	authHTTP  *httptest.Server
	adminAPI  *admin.API
	adminHTTP *httptest.Server
	gw        *gateway.Gateway
	gwHTTP    *httptest.Server
	events    *analytics.SQLiteStore
	ring      *analytics.RingBuffer
	emitter   *syncGrantEmitter
	http      *http.Client
	logBuf    *bytes.Buffer
	gate      *auth.BearerCompatGate

	clientID string
	prismID  string
}

type syncGrantEmitter struct {
	store analytics.Store
	ring  *analytics.RingBuffer
}

func (e *syncGrantEmitter) Emit(_ context.Context, event auth.GrantEvent) {
	if e.ring != nil {
		e.ring.Add(event)
	}
	if e.store != nil {
		_ = e.store.Insert(event)
	}
}

func newE2ESuite(t *testing.T) *e2eSuite {
	t.Helper()
	ctx := context.Background()
	clock := newTestClock()
	kv := store.NewMemoryStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	km, err := authserver.NewKeyManager("")
	if err != nil {
		t.Fatal(err)
	}
	authSrv := authserver.NewServer(&authserver.Config{
		Issuer:          e2eIssuer,
		TokenTTLSeconds: int((24 * time.Hour) / time.Second),
		DefaultScopes:   []string{},
	}, km, kv, logger, map[string]authserver.GroupConfig{
		"engineering": {Scopes: []string{"fs:write_file"}},
	})
	authSrv.SetClock(clock.Now)

	eventStore, err := analytics.OpenSQLiteStore(filepath.Join(t.TempDir(), "grant_events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eventStore.Close() })
	ring := analytics.NewRingBuffer(100)
	emitter := &syncGrantEmitter{store: eventStore, ring: ring}
	logBuf := &bytes.Buffer{}
	gate := auth.NewBearerCompatGate(auth.BearerCompatWarn, slog.New(slog.NewTextHandler(logBuf, nil)))

	backendHTTP := newFSBackend(t)
	gw := gateway.New(logger)
	gw.SetClock(clock.Now)
	gw.SetPolicyResolver(authSrv)
	gw.SetGrantEmitter(emitter)
	gw.SetBearerCompatGate(gate)
	if err := gw.ConnectBackend(ctx, &config.ServerConfig{
		ID:        "local",
		URL:       backendHTTP.URL + "/mcp",
		Namespace: "fs",
		Workspace: &config.WorkspaceConfig{
			ID:        "repo",
			Type:      config.WorkspaceTypeEphemeral,
			Mode:      config.WorkspaceModeSnapshot,
			WriteMode: config.WorkspaceWriteStage,
		},
	}); err != nil {
		t.Fatalf("ConnectBackend: %v", err)
	}
	t.Cleanup(gw.Close)

	validator := auth.NewTokenValidator(&auth.TokenValidatorConfig{
		IssuerURL:         e2eIssuer,
		Audience:          e2eIssuer,
		StaticJWKS:        km.JWKS(),
		GenerationChecker: auth.NewCachedGenerationChecker(authSrv, 0),
		Now:               clock.Now,
	})
	handler := auth.Middleware(validator, "http://prism-gateway.e2e/mcp",
		auth.WithDPoPReplayCache(authSrv.DPoPReplayCache()),
		auth.WithMiddlewareClock(clock.Now),
		auth.WithBearerCompatGate(gate),
	)(gw.Handler())

	s := &e2eSuite{
		t:       t,
		ctx:     ctx,
		clock:   clock,
		kv:      kv,
		authSrv: authSrv,
		events:  eventStore,
		ring:    ring,
		emitter: emitter,
		http: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		logBuf: logBuf,
		gate:   gate,
		gw:     gw,
	}
	s.authHTTP = httptest.NewServer(authSrv.Routes())
	t.Cleanup(s.authHTTP.Close)
	s.gwHTTP = httptest.NewServer(handler)
	t.Cleanup(s.gwHTTP.Close)

	agentMgr := &e2eAgentManager{srv: authSrv}
	groupMgr := &e2eGroupManager{srv: authSrv}
	adminAPI := admin.NewAPI(
		func() any { return map[string]string{"status": "ok"} },
		nil,
		agentMgr.ListAgents,
		agentMgr.RemoveAgent,
		agentMgr.RemoveStaleAgents,
		func() []any { return nil },
		agentMgr,
		groupMgr,
		nil,
		nil,
	)
	adminAPI.SetGrantManager(authSrv)
	adminAPI.SetAnalytics(eventStore, ring)
	s.adminAPI = adminAPI
	s.adminHTTP = httptest.NewServer(adminAPI.Handler())
	t.Cleanup(s.adminHTTP.Close)
	return s
}

func TestE2E_AuthorTemplate(t *testing.T) {
	s := newE2ESuite(t)
	tmpl := s.authorTemplateFromFixture()
	if tmpl.Hash == "" || tmpl.Version != 1 {
		t.Fatalf("template hash/version = %q/%d", tmpl.Hash, tmpl.Version)
	}
	var fetched auth.GrantTemplate
	s.adminJSON(http.MethodGet, "/grant-templates/by-hash/"+url.PathEscape(tmpl.Hash), nil, http.StatusOK, &fetched)
	if fetched.ID != tmpl.ID || fetched.Hash != tmpl.Hash || !fetched.Spec.CnfRequired || fetched.Spec.AcrRequired != "urn:prism:mfa" {
		t.Fatalf("fetched template = %+v", fetched)
	}
}

func TestE2E_CreateBinding(t *testing.T) {
	s := newE2ESuite(t)
	tmpl := s.authorTemplateFromFixture()
	binding := s.createEngineeringBinding()
	if binding.TemplateHash != tmpl.Hash {
		t.Fatalf("binding hash = %q, want %q", binding.TemplateHash, tmpl.Hash)
	}
	var bindings []auth.GrantBinding
	s.adminJSON(http.MethodGet, "/grant-bindings?template="+url.QueryEscape(tmpl.ID), nil, http.StatusOK, &bindings)
	if len(bindings) != 1 || bindings[0].ID != binding.ID {
		t.Fatalf("bindings = %+v", bindings)
	}
	keys, err := s.kv.List("grant_bindings_by_template/" + tmpl.ID + "/")
	if err != nil || len(keys) != 1 {
		t.Fatalf("binding reverse index keys = %v, err=%v", keys, err)
	}
}

func TestE2E_AuthorizeFlow(t *testing.T) {
	s := newE2ESuite(t)
	tmpl := s.authorTemplateFromFixture()
	s.createEngineeringBinding()
	s.ensureAgent()
	key := newDPoPKey(t)
	code, consentHTML := s.authorizeGrantCode(tmpl, "/workspace/a/file.txt")
	if !strings.Contains(consentHTML, "Capability grants") || !strings.Contains(consentHTML, "repo") {
		t.Fatalf("consent did not render concrete grant: %s", consentHTML)
	}
	resp := s.exchangeCodeDPoP(code, key)
	if resp.TokenType != "DPoP" {
		t.Fatalf("token_type = %q", resp.TokenType)
	}
	claims := parseUnverifiedClaims(t, resp.AccessToken)
	if claims["cnf"] == nil || claims["auth_time"] == nil || claims["acr"] != "urn:prism:mfa" {
		t.Fatalf("grant token claims = %+v", claims)
	}
	details, ok := claims["authorization_details"].([]any)
	if !ok || len(details) != 1 {
		t.Fatalf("authorization_details = %#v", claims["authorization_details"])
	}
	detail, _ := details[0].(map[string]any)
	if detail["template_hash"] != tmpl.Hash {
		t.Fatalf("template hash claim = %#v, want %s", detail["template_hash"], tmpl.Hash)
	}
}

func TestE2E_MatchingCallAllowed(t *testing.T) {
	s := newE2ESuite(t)
	token, key, tmpl := s.issueGrantToken(true, "/workspace/a/file.txt")
	res := s.callToolDPoP(token.AccessToken, key, "/workspace/a/file.txt")
	if res.IsError {
		t.Fatalf("tool call error: %s", firstText(res))
	}
	event := s.latestEvent(analytics.QueryFilter{AgentID: s.prismID, TemplateHash: tmpl.Hash})
	if event.Outcome != "allowed" || event.Trace.What.Verdict != "pass" {
		t.Fatalf("event = %+v", event)
	}
}

func TestE2E_ArgsConstraintViolation(t *testing.T) {
	s := newE2ESuite(t)
	token, key, _ := s.issueGrantToken(true, "/workspace/a/file.txt")
	res := s.callToolDPoP(token.AccessToken, key, "/outside/file.txt")
	if !res.IsError || !strings.Contains(firstText(res), "policy_mismatch") {
		t.Fatalf("tool result = error:%v text:%q", res.IsError, firstText(res))
	}
	event := s.latestEvent(analytics.QueryFilter{AgentID: s.prismID, DenyDim: auth.GrantDenyArgs})
	if event.Outcome != "denied" || event.Trace.DenyDim != auth.GrantDenyArgs {
		t.Fatalf("event = %+v", event)
	}
}

func TestE2E_OutOfWindow(t *testing.T) {
	s := newE2ESuite(t)
	token, key, _ := s.issueGrantToken(true, "/workspace/a/file.txt")
	s.clock.Set(time.Date(2026, 5, 18, 19, 0, 0, 0, time.UTC))
	res := s.callToolDPoP(token.AccessToken, key, "/workspace/a/file.txt")
	if !res.IsError || !strings.Contains(firstText(res), "policy_mismatch") {
		t.Fatalf("tool result = error:%v text:%q", res.IsError, firstText(res))
	}
	event := s.latestEvent(analytics.QueryFilter{AgentID: s.prismID, DenyDim: auth.GrantDenyOutOfWindow})
	if event.Trace.DenyDim != auth.GrantDenyOutOfWindow {
		t.Fatalf("event = %+v", event)
	}
}

func TestE2E_StepUpChallenge(t *testing.T) {
	s := newE2ESuite(t)
	token, key, _ := s.issueGrantToken(true, "/workspace/a/file.txt")
	s.clock.Advance(11 * time.Minute)
	res := s.callToolDPoP(token.AccessToken, key, "/workspace/a/file.txt")
	if !res.IsError || !strings.Contains(firstText(res), "insufficient_user_authentication") {
		t.Fatalf("tool result = error:%v text:%q", res.IsError, firstText(res))
	}
	event := s.latestEvent(analytics.QueryFilter{AgentID: s.prismID, DenyDim: auth.GrantDenyNeedsStepUp})
	if event.Trace.DenyDim != auth.GrantDenyNeedsStepUp {
		t.Fatalf("event = %+v", event)
	}
	fresh, freshKey, _ := s.issueGrantToken(true, "/workspace/a/file.txt")
	res = s.callToolDPoP(fresh.AccessToken, freshKey, "/workspace/a/file.txt")
	if res.IsError {
		t.Fatalf("fresh token call failed: %s", firstText(res))
	}
}

// TestE2E_StepUpReEntryFlow exercises the full user-facing step-up re-entry
// path documented in spec §17.5. After a stale-auth tool call:
//  1. Tool call returns 401 + WWW-Authenticate carrying acr_values
//  2. Agent re-runs /authorize with the same authorization_details
//  3. /authorize detects step-up needed → 302 to /stepup?state=X
//  4. GET /stepup?state=X → 200 with form
//  5. POST /stepup with state → 302 back to /authorize (consumes state)
//  6. /authorize re-entry → consent → fresh code with refreshed auth_time/acr
//  7. Token exchange yields a token whose AuthTime > the stale token's
//  8. Tool call with the fresh token succeeds
func TestE2E_StepUpReEntryFlow(t *testing.T) {
	s := newE2ESuite(t)
	stale, _, tmpl := s.issueGrantToken(true, "/workspace/a/file.txt")
	staleClaims := parseUnverifiedClaims(t, stale.AccessToken)
	staleAuthTime, _ := staleClaims["auth_time"].(float64)

	// Re-run /authorize to drive the step-up branch. We do NOT call the
	// tool first because the gateway/MCP plumbing returns the 401 via the
	// MCP error path (tested above); what's under test here is the
	// /authorize → /stepup → /authorize re-entry chain.
	s.clock.Advance(11 * time.Minute)

	values := s.baseAuthorizeValues()
	values.Set("authorization_details", authorizationDetailsJSON(s.t, tmpl, "/workspace/a/file.txt"))
	values.Set("acr_values", "urn:prism:mfa")

	// Step 1: /authorize triggers step-up redirect because auth_time is stale.
	authResp := s.doForm(http.MethodGet, s.authHTTP.URL+"/authorize?"+values.Encode(), nil, nil)
	if authResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(authResp.Body)
		_ = authResp.Body.Close()
		t.Fatalf("/authorize did not redirect: status=%d body=%s", authResp.StatusCode, body)
	}
	stepLoc := authResp.Header.Get("Location")
	authResp.Body.Close()
	if !strings.HasPrefix(stepLoc, "/stepup?state=") {
		t.Fatalf("expected /stepup redirect with state, got %q", stepLoc)
	}
	stepURL, err := url.Parse(stepLoc)
	if err != nil {
		t.Fatal(err)
	}
	state := stepURL.Query().Get("state")
	if state == "" {
		t.Fatal("redirect missing state token")
	}

	// Step 2: GET /stepup?state=X → 200 with form containing the state.
	getResp := s.doForm(http.MethodGet, s.authHTTP.URL+stepLoc, nil, nil)
	if getResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(getResp.Body)
		_ = getResp.Body.Close()
		t.Fatalf("GET /stepup status=%d body=%s", getResp.StatusCode, body)
	}
	getBody, _ := io.ReadAll(getResp.Body)
	_ = getResp.Body.Close()
	if !strings.Contains(string(getBody), `name="state" value="`+state+`"`) {
		t.Fatalf("step-up form missing state input: %s", getBody)
	}

	// Step 3: POST /stepup with state → mints session cookie + 302 to /authorize.
	postValues := url.Values{"state": {state}}
	postResp := s.doForm(http.MethodPost, s.authHTTP.URL+"/stepup", postValues, nil)
	if postResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(postResp.Body)
		_ = postResp.Body.Close()
		t.Fatalf("POST /stepup status=%d body=%s", postResp.StatusCode, body)
	}
	sessionCookies := postResp.Cookies()
	postLoc := postResp.Header.Get("Location")
	postResp.Body.Close()
	if !strings.HasPrefix(postLoc, "/authorize") {
		t.Fatalf("POST /stepup did not redirect to /authorize: %q", postLoc)
	}

	// Step 4: Replaying the same state must be rejected — proves single-use.
	replay := s.doForm(http.MethodPost, s.authHTTP.URL+"/stepup", postValues, nil)
	if replay.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(replay.Body)
		_ = replay.Body.Close()
		t.Fatalf("replayed POST /stepup status=%d body=%s", replay.StatusCode, body)
	}
	replay.Body.Close()

	// Step 5: /authorize re-entry with the session cookie now passes step-up
	// and renders the consent page.
	consentResp := s.doForm(http.MethodGet, s.authHTTP.URL+postLoc, nil, sessionCookies)
	if consentResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(consentResp.Body)
		_ = consentResp.Body.Close()
		t.Fatalf("/authorize re-entry status=%d body=%s", consentResp.StatusCode, body)
	}
	consentBody, csrfCookies := readBodyAndCookies(t, consentResp)
	allCookies := append([]*http.Cookie(nil), sessionCookies...)
	allCookies = append(allCookies, csrfCookies...)
	csrf := extractCSRF(t, consentBody)
	values.Set("_csrf", csrf)
	values.Set("label", "E2E Agent")

	// Step 6: Consent POST returns the auth code with the refreshed auth_time.
	codePost := s.doForm(http.MethodPost, s.authHTTP.URL+"/authorize", values, allCookies)
	if codePost.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(codePost.Body)
		_ = codePost.Body.Close()
		t.Fatalf("consent POST status=%d body=%s", codePost.StatusCode, body)
	}
	code := codeFromRedirect(t, codePost.Header.Get("Location"))
	codePost.Body.Close()

	// Step 7: Exchange the code, verify the new token has fresh auth_time/acr.
	freshKey := newDPoPKey(t)
	fresh := s.exchangeCodeDPoP(code, freshKey)
	freshClaims := parseUnverifiedClaims(t, fresh.AccessToken)
	freshAuthTime, _ := freshClaims["auth_time"].(float64)
	if freshAuthTime <= staleAuthTime {
		t.Fatalf("auth_time did not refresh: stale=%v fresh=%v", staleAuthTime, freshAuthTime)
	}
	if freshClaims["acr"] != "urn:prism:mfa" {
		t.Fatalf("fresh token acr = %v, want urn:prism:mfa", freshClaims["acr"])
	}

	// Step 8: Tool call with the fresh token succeeds.
	res := s.callToolDPoP(fresh.AccessToken, freshKey, "/workspace/a/file.txt")
	if res.IsError {
		t.Fatalf("re-entry fresh token call failed: %s", firstText(res))
	}
}

func TestE2E_WorkspaceDrift(t *testing.T) {
	s := newE2ESuite(t)
	token, key, _ := s.issueGrantToken(true, "/workspace/a/file.txt")
	backend := s.gw.BackendByID("local")
	if backend == nil || backend.Config == nil || backend.Config.Workspace == nil {
		t.Fatal("missing backend workspace")
	}
	backend.Config.Workspace.Type = config.WorkspaceTypeVirtual
	res := s.callToolDPoP(token.AccessToken, key, "/workspace/a/file.txt")
	if !res.IsError || !strings.Contains(firstText(res), "policy_mismatch") {
		t.Fatalf("tool result = error:%v text:%q", res.IsError, firstText(res))
	}
	event := s.latestEvent(analytics.QueryFilter{AgentID: s.prismID, DenyDim: auth.GrantDenyWorkspaceDrift})
	if event.Trace.Drift == nil || event.Trace.Drift.GrantHash == "" || event.Trace.Drift.LiveHash == "" {
		t.Fatalf("drift event = %+v", event)
	}
	var agent struct {
		Grant struct {
			DriftCount24h   int               `json:"drift_count_24h"`
			RecentDecisions []auth.GrantEvent `json:"recent_decisions"`
		} `json:"grant_resolution"`
	}
	s.adminJSON(http.MethodGet, "/agents/"+url.PathEscape(s.prismID), nil, http.StatusOK, &agent)
	if agent.Grant.DriftCount24h == 0 || len(agent.Grant.RecentDecisions) == 0 || agent.Grant.RecentDecisions[0].Trace.Drift == nil {
		t.Fatalf("agent grant resolution = %+v", agent.Grant)
	}
}

func TestE2E_LegacyScopeUnaffected(t *testing.T) {
	s := newE2ESuite(t)
	s.authorTemplateFromFixture()
	s.createEngineeringBinding()
	s.ensureAgent()
	token := s.issueLegacyToken()
	res := s.callToolBearer(token.AccessToken, "/outside/file.txt")
	if res.IsError {
		t.Fatalf("legacy call failed: %s", firstText(res))
	}
	events, err := s.events.Query(analytics.QueryFilter{AgentID: s.prismID}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("legacy path emitted grant events: %+v", events)
	}
}

func TestE2E_AnalyticsViews(t *testing.T) {
	s := newE2ESuite(t)
	token, key, tmpl := s.issueGrantToken(true, "/workspace/a/file.txt")
	_ = s.callToolDPoP(token.AccessToken, key, "/workspace/a/one.txt")
	_ = s.callToolDPoP(token.AccessToken, key, "/outside/one.txt")
	s.clock.Set(time.Date(2026, 5, 18, 19, 0, 0, 0, time.UTC))
	_ = s.callToolDPoP(token.AccessToken, key, "/workspace/a/two.txt")
	s.clock.Set(time.Date(2026, 5, 18, 15, 5, 0, 0, time.UTC))
	_ = s.callToolDPoP(token.AccessToken, key, "/workspace/a/three.txt")
	s.clock.Advance(11 * time.Minute)
	_ = s.callToolDPoP(token.AccessToken, key, "/workspace/a/four.txt")
	fresh, freshKey, _ := s.issueGrantToken(true, "/workspace/a/file.txt")
	_ = s.callToolDPoP(fresh.AccessToken, freshKey, "/workspace/a/five.txt")

	var agent struct {
		Grant struct {
			Bindings        []any             `json:"bindings"`
			RecentDecisions []auth.GrantEvent `json:"recent_decisions"`
		} `json:"grant_resolution"`
	}
	s.adminJSON(http.MethodGet, "/agents/"+url.PathEscape(s.prismID), nil, http.StatusOK, &agent)
	if len(agent.Grant.Bindings) == 0 || len(agent.Grant.RecentDecisions) < 6 {
		t.Fatalf("agent analytics = %+v", agent.Grant)
	}

	var denied []auth.GrantEvent
	s.adminJSON(http.MethodGet, "/analytics/events?agent_id="+url.QueryEscape(s.prismID)+"&outcome=denied", nil, http.StatusOK, &denied)
	if len(denied) < 3 {
		t.Fatalf("denied decision log = %+v", denied)
	}

	var coverage []struct {
		TemplateHash     string `json:"template_hash"`
		Allow24h         int    `json:"allow_24h"`
		Deny24h          int    `json:"deny_24h"`
		ActiveTokenCount int    `json:"active_token_count"`
	}
	s.adminJSON(http.MethodGet, "/analytics/templates?window=24h", nil, http.StatusOK, &coverage)
	if len(coverage) != 1 || coverage[0].TemplateHash != tmpl.Hash || coverage[0].Allow24h == 0 || coverage[0].Deny24h == 0 || coverage[0].ActiveTokenCount == 0 {
		t.Fatalf("coverage = %+v", coverage)
	}
}

func TestE2E_BearerCompatGate_Warn(t *testing.T) {
	s := newE2ESuite(t)
	token, _, _ := s.issueGrantToken(false, "/workspace/a/file.txt")
	res := s.callToolBearer(token.AccessToken, "/workspace/a/file.txt")
	if res.IsError {
		t.Fatalf("Bearer warn call failed: %s", firstText(res))
	}
	if !strings.Contains(s.logBuf.String(), "Bearer token used with grant-bearing token") {
		t.Fatalf("expected compat warning log, got %s", s.logBuf.String())
	}
}

func TestE2E_BearerCompatGate_Deny(t *testing.T) {
	s := newE2ESuite(t)
	token, _, _ := s.issueGrantToken(false, "/workspace/a/file.txt")
	s.gate.Update(auth.BearerCompatDeny)
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.gwHTTP.URL+"/mcp", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	resp, err := s.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "dpop_required") {
		t.Fatalf("status=%d www-auth=%q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

func TestE2E_RefreshTokenJktBinding(t *testing.T) {
	s := newE2ESuite(t)
	token, key, _ := s.issueGrantToken(true, "/workspace/a/file.txt")
	refreshed := s.refreshDPoP(token.RefreshToken, key, http.StatusOK)
	if refreshed.TokenType != "DPoP" || refreshed.RefreshToken == "" || refreshed.RefreshToken == token.RefreshToken {
		t.Fatalf("refresh response = %+v", refreshed)
	}
	otherKey := newDPoPKey(t)
	_ = s.refreshDPoP(refreshed.RefreshToken, otherKey, http.StatusUnauthorized)
}

func TestE2E_TemplateVersionPinning(t *testing.T) {
	s := newE2ESuite(t)
	v1Token, v1Key, v1 := s.issueGrantToken(true, "/workspace/a/file.txt")
	v2 := v1
	stricter := "/workspace/a/strict/"
	v2.Spec.Args = map[string]auth.Predicate{"path": {Prefix: &stricter}}
	v2.Hash = ""
	v2.Version = 0
	var savedV2 auth.GrantTemplate
	s.adminJSON(http.MethodPut, "/grant-templates/"+url.PathEscape(v1.ID), v2, http.StatusOK, &savedV2)
	if savedV2.Version != 2 || savedV2.Hash == v1.Hash {
		t.Fatalf("v2 template = %+v", savedV2)
	}
	var updated auth.GrantBinding
	s.adminJSON(http.MethodPut, "/grant-bindings/bind-engineering-senior", auth.GrantBinding{
		TemplateID: savedV2.ID,
		Subjects:   auth.SubjectSelector{Groups: []string{"engineering"}, RoleRequired: "senior"},
	}, http.StatusOK, &updated)
	if updated.TemplateHash != savedV2.Hash {
		t.Fatalf("updated binding = %+v", updated)
	}

	res := s.callToolDPoP(v1Token.AccessToken, v1Key, "/workspace/a/legacy.txt")
	if res.IsError {
		t.Fatalf("v1 pinned token should retain v1 semantics: %s", firstText(res))
	}
	v2Token, v2Key := s.issueGrantTokenForTemplate(savedV2, true, "/workspace/a/strict/file.txt")
	res = s.callToolDPoP(v2Token.AccessToken, v2Key, "/workspace/a/strict/file.txt")
	if res.IsError {
		t.Fatalf("v2 token call failed: %s", firstText(res))
	}
	var coverage []struct {
		TemplateHash     string `json:"template_hash"`
		ActiveTokenCount int    `json:"active_token_count"`
	}
	s.adminJSON(http.MethodGet, "/analytics/templates?window=24h", nil, http.StatusOK, &coverage)
	seen := map[string]int{}
	for _, row := range coverage {
		seen[row.TemplateHash] = row.ActiveTokenCount
	}
	if seen[v1.Hash] == 0 || seen[savedV2.Hash] == 0 {
		t.Fatalf("active token counts = %+v", coverage)
	}
}

func (s *e2eSuite) issueGrantToken(cnfRequired bool, path string) (authserver.TokenResponse, *dpopKey, auth.GrantTemplate) {
	tmpl := s.authorTemplate(cnfRequired)
	s.createEngineeringBinding()
	s.ensureAgent()
	token, key := s.issueGrantTokenForTemplate(tmpl, cnfRequired, path)
	return token, key, tmpl
}

func (s *e2eSuite) issueGrantTokenForTemplate(tmpl auth.GrantTemplate, cnfRequired bool, path string) (authserver.TokenResponse, *dpopKey) {
	key := newDPoPKey(s.t)
	code, _ := s.authorizeGrantCode(tmpl, path)
	if cnfRequired {
		return s.exchangeCodeDPoP(code, key), key
	}
	return s.exchangeCodeBearer(code), key
}

func (s *e2eSuite) authorTemplateFromFixture() auth.GrantTemplate {
	var body auth.GrantTemplate
	readJSONFixture(s.t, templateFixture, &body)
	var out auth.GrantTemplate
	s.adminJSON(http.MethodPost, "/grant-templates", body, http.StatusCreated, &out)
	return out
}

func (s *e2eSuite) authorTemplate(cnfRequired bool) auth.GrantTemplate {
	if cnfRequired {
		return s.authorTemplateFromFixture()
	}
	prefix := "/workspace/a/"
	tmpl := auth.GrantTemplate{
		ID:        "tmpl-fs-write",
		CreatedBy: "operator",
		Spec: auth.GrantSpec{
			Type:    auth.GrantTypeMCPCall,
			Tool:    "fs.write_file",
			Backend: "local",
			Args: map[string]auth.Predicate{
				"path": {Prefix: &prefix},
			},
			Workspace: auth.WorkspaceConstraint{
				ID:        &auth.Predicate{Equals: "repo"},
				Type:      &auth.Predicate{Equals: "ephemeral"},
				WriteMode: &auth.Predicate{Equals: "stage"},
			},
			Hours: "09:00-18:00 UTC",
		},
	}
	var out auth.GrantTemplate
	s.adminJSON(http.MethodPost, "/grant-templates", tmpl, http.StatusCreated, &out)
	return out
}

func (s *e2eSuite) createEngineeringBinding() auth.GrantBinding {
	var body auth.GrantBinding
	readJSONFixture(s.t, bindingFixture, &body)
	var out auth.GrantBinding
	s.adminJSON(http.MethodPost, "/grant-bindings", body, http.StatusCreated, &out)
	return out
}

func (s *e2eSuite) ensureAgent() {
	if s.prismID != "" {
		return
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	s.authJSON(http.MethodPost, "/register", map[string]any{
		"client_name":   "E2E Agent",
		"redirect_uris": []string{redirectURI},
	}, http.StatusCreated, &reg)
	s.clientID = reg.ClientID

	values := s.baseAuthorizeValues()
	getResp := s.doForm(http.MethodGet, s.authHTTP.URL+"/authorize?"+values.Encode(), nil, nil)
	if getResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(getResp.Body)
		_ = getResp.Body.Close()
		s.t.Fatalf("initial consent GET status=%d body=%s", getResp.StatusCode, body)
	}
	consentBody, cookies := readBodyAndCookies(s.t, getResp)
	csrf := extractCSRF(s.t, consentBody)
	values.Set("_csrf", csrf)
	values.Set("label", "E2E Agent")
	postResp := s.doForm(http.MethodPost, s.authHTTP.URL+"/authorize", values, cookies)
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(postResp.Body)
		s.t.Fatalf("initial consent POST status=%d body=%s", postResp.StatusCode, body)
	}
	for _, agent := range s.authSrv.ListAgents() {
		if agent.ClientID == s.clientID {
			s.prismID = agent.PrismID
			break
		}
	}
	if s.prismID == "" {
		s.t.Fatal("consented agent missing prism_id")
	}
	s.adminJSON(http.MethodPut, "/agents/"+url.PathEscape(s.prismID)+"/policy", map[string]any{
		"groups": []string{"engineering"},
		"grant":  []string{"role:senior"},
		"deny":   []string{},
	}, http.StatusOK, nil)
}

func (s *e2eSuite) authorizeGrantCode(tmpl auth.GrantTemplate, path string) (string, string) {
	values := s.baseAuthorizeValues()
	values.Set("authorization_details", authorizationDetailsJSON(s.t, tmpl, path))
	values.Set("acr_values", "urn:prism:mfa")
	resp := s.doForm(http.MethodGet, s.authHTTP.URL+"/authorize?"+values.Encode(), nil, nil)
	var cookies []*http.Cookie
	if resp.StatusCode == http.StatusFound && strings.HasPrefix(resp.Header.Get("Location"), "/stepup?") {
		resp.Body.Close()
		stepResp := s.doForm(http.MethodPost, s.authHTTP.URL+resp.Header.Get("Location"), nil, nil)
		if stepResp.StatusCode != http.StatusFound {
			body, _ := io.ReadAll(stepResp.Body)
			_ = stepResp.Body.Close()
			s.t.Fatalf("step-up status=%d body=%s", stepResp.StatusCode, body)
		}
		cookies = append(cookies, stepResp.Cookies()...)
		ret := stepResp.Header.Get("Location")
		stepResp.Body.Close()
		resp = s.doForm(http.MethodGet, s.authHTTP.URL+ret, nil, cookies)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		s.t.Fatalf("grant consent GET status=%d body=%s", resp.StatusCode, body)
	}
	body, csrfCookies := readBodyAndCookies(s.t, resp)
	cookies = append(cookies, csrfCookies...)
	csrf := extractCSRF(s.t, body)
	values.Set("_csrf", csrf)
	values.Set("label", "E2E Agent")
	postResp := s.doForm(http.MethodPost, s.authHTTP.URL+"/authorize", values, cookies)
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusFound {
		postBody, _ := io.ReadAll(postResp.Body)
		s.t.Fatalf("grant consent POST status=%d body=%s", postResp.StatusCode, postBody)
	}
	code := codeFromRedirect(s.t, postResp.Header.Get("Location"))
	return code, body
}

func (s *e2eSuite) issueLegacyToken() authserver.TokenResponse {
	values := s.baseAuthorizeValues()
	resp := s.doForm(http.MethodGet, s.authHTTP.URL+"/authorize?"+values.Encode(), nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("legacy authorize status=%d body=%s", resp.StatusCode, body)
	}
	return s.exchangeCodeBearer(codeFromRedirect(s.t, resp.Header.Get("Location")))
}

func (s *e2eSuite) exchangeCodeDPoP(code string, key *dpopKey) authserver.TokenResponse {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
		"client_id":     {s.clientID},
	}
	return s.postTokenWithDPoP(form, key, http.StatusOK)
}

func (s *e2eSuite) refreshDPoP(refresh string, key *dpopKey, wantStatus int) authserver.TokenResponse {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	return s.postTokenWithDPoP(form, key, wantStatus)
}

func (s *e2eSuite) postTokenWithDPoP(form url.Values, key *dpopKey, wantStatus int) authserver.TokenResponse {
	htu := s.authHTTP.URL + "/token"
	first := s.doToken(form, signDPoP(s.t, key, http.MethodPost, htu, "", "", s.clock.Now()))
	if first.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(first.Body)
		_ = first.Body.Close()
		s.t.Fatalf("expected nonce challenge, status=%d body=%s", first.StatusCode, body)
	}
	nonce := first.Header.Get("DPoP-Nonce")
	first.Body.Close()
	if nonce == "" {
		s.t.Fatal("token endpoint omitted DPoP-Nonce")
	}
	resp := s.doToken(form, signDPoP(s.t, key, http.MethodPost, htu, nonce, "", s.clock.Now()))
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("token status=%d want=%d body=%s", resp.StatusCode, wantStatus, body)
	}
	if wantStatus != http.StatusOK {
		return authserver.TokenResponse{}
	}
	var out authserver.TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		s.t.Fatal(err)
	}
	return out
}

func (s *e2eSuite) exchangeCodeBearer(code string) authserver.TokenResponse {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
		"client_id":     {s.clientID},
	}
	resp := s.doToken(form, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("Bearer token status=%d body=%s", resp.StatusCode, body)
	}
	var out authserver.TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		s.t.Fatal(err)
	}
	return out
}

func (s *e2eSuite) doToken(form url.Values, proof string) *http.Response {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.authHTTP.URL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if proof != "" {
		req.Header.Set("DPoP", proof)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp
}

func (s *e2eSuite) callToolDPoP(token string, key *dpopKey, path string) *mcp.CallToolResult {
	return s.callTool(token, auth.AuthSchemeDPoP, key, path)
}

func (s *e2eSuite) callToolBearer(token, path string) *mcp.CallToolResult {
	return s.callTool(token, auth.AuthSchemeBearer, nil, path)
}

func (s *e2eSuite) callTool(token, scheme string, key *dpopKey, path string) *mcp.CallToolResult {
	session := s.connectMCP(token, scheme, key)
	defer session.Close()
	res, err := session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "fs__write_file",
		Arguments: map[string]any{"path": path},
	})
	if err != nil {
		s.t.Fatalf("CallTool: %v", err)
	}
	return res
}

func (s *e2eSuite) connectMCP(token, scheme string, key *dpopKey) *mcp.ClientSession {
	client := mcp.NewClient(&mcp.Implementation{Name: "grants-e2e-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(s.ctx, &mcp.StreamableClientTransport{
		Endpoint:             s.gwHTTP.URL + "/mcp",
		DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: &authTransport{
			base:   http.DefaultTransport,
			token:  token,
			scheme: scheme,
			key:    key,
			clock:  s.clock,
		}},
	}, nil)
	if err != nil {
		s.t.Fatalf("connect MCP: %v", err)
	}
	return session
}

func (s *e2eSuite) latestEvent(filter analytics.QueryFilter) auth.GrantEvent {
	events, err := s.events.Query(filter, 100)
	if err != nil {
		s.t.Fatal(err)
	}
	if len(events) == 0 {
		s.t.Fatalf("no events for filter %s", filter)
	}
	return events[len(events)-1]
}

func (s *e2eSuite) baseAuthorizeValues() url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {s.clientID},
		"redirect_uri":          {redirectURI},
		"state":                 {"state-1"},
		"code_challenge":        {pkceChallenge(codeVerifier)},
		"code_challenge_method": {"S256"},
	}
}

func (s *e2eSuite) adminJSON(method, path string, body any, want int, out any) {
	s.doJSON(s.adminHTTP.URL+"/api/v1"+path, method, body, want, out)
}

func (s *e2eSuite) doRawAdmin(method, path string) *http.Response {
	s.t.Helper()
	req, err := http.NewRequestWithContext(s.ctx, method, s.adminHTTP.URL+"/api/v1"+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp
}

func (s *e2eSuite) authJSON(method, path string, body any, want int, out any) {
	s.doJSON(s.authHTTP.URL+path, method, body, want, out)
}

func (s *e2eSuite) doJSON(target, method string, body any, want int, out any) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(s.ctx, method, target, reader)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		data, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("%s %s status=%d want=%d body=%s", method, target, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			s.t.Fatal(err)
		}
	}
}

func (s *e2eSuite) doForm(method, target string, values url.Values, cookies []*http.Cookie) *http.Response {
	var body io.Reader
	if values != nil && method == http.MethodPost {
		body = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequestWithContext(s.ctx, method, target, body)
	if err != nil {
		s.t.Fatal(err)
	}
	if values != nil && method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp
}

type authTransport struct {
	base   http.RoundTripper
	token  string
	scheme string
	key    *dpopKey
	clock  *testClock
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	next := req.Clone(req.Context())
	next.Header = req.Header.Clone()
	next.Header.Set("Authorization", t.scheme+" "+t.token)
	if t.scheme == auth.AuthSchemeDPoP {
		htu := next.URL.Scheme + "://" + next.URL.Host + next.URL.Path
		next.Header.Set("DPoP", signDPoPWithTime(t.key, next.Method, htu, "", t.token, uniqueJTI("resource"), t.clock.Now()))
	}
	return base.RoundTrip(next)
}

type dpopKey struct {
	priv *ecdsa.PrivateKey
	pub  jwk.Key
}

func newDPoPKey(t *testing.T) *dpopKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jwk.FromRaw(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return &dpopKey{priv: priv, pub: pub}
}

func signDPoP(t *testing.T, key *dpopKey, method, htu, nonce, accessToken string, now time.Time) string {
	t.Helper()
	return signDPoPWithTime(key, method, htu, nonce, accessToken, uniqueJTI("token"), now)
}

func signDPoPWithTime(key *dpopKey, method, htu, nonce, accessToken, jti string, now time.Time) string {
	tok := jwxjwt.New()
	_ = tok.Set("htm", strings.ToUpper(method))
	_ = tok.Set("htu", htu)
	_ = tok.Set("iat", now)
	_ = tok.Set("jti", jti)
	if nonce != "" {
		_ = tok.Set("nonce", nonce)
	}
	if accessToken != "" {
		_ = tok.Set("ath", auth.AccessTokenHash(accessToken))
	}
	headers := jws.NewHeaders()
	_ = headers.Set("typ", "dpop+jwt")
	_ = headers.Set("jwk", key.pub)
	signed, err := jwxjwt.Sign(tok, jwxjwt.WithKey(jwa.ES256, key.priv, jws.WithProtectedHeaders(headers)))
	if err != nil {
		panic(fmt.Sprintf("sign dpop: %v", err))
	}
	return string(signed)
}

var jtiCounter uint64

func uniqueJTI(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, atomic.AddUint64(&jtiCounter, 1))
}

func authorizationDetailsJSON(t *testing.T, tmpl auth.GrantTemplate, path string) string {
	t.Helper()
	entry := map[string]any{
		"type":    auth.GrantTypeMCPCall,
		"tool":    tmpl.Spec.Tool,
		"backend": tmpl.Spec.Backend,
		"args":    map[string]any{"path": path},
		"workspace": map[string]any{
			"id":         "repo",
			"type":       config.WorkspaceTypeEphemeral,
			"write_mode": config.WorkspaceWriteStage,
		},
	}
	data, err := json.Marshal([]any{entry})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func parseUnverifiedClaims(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Claims.(jwt.MapClaims)
}

func extractCSRF(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("csrf missing in %s", body)
	}
	return m[1]
}

func codeFromRedirect(t *testing.T, location string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("redirect missing code: %s", location)
	}
	return code
}

func readBodyAndCookies(t *testing.T, resp *http.Response) (string, []*http.Cookie) {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), resp.Cookies()
}

func readJSONFixture(t *testing.T, rel string, out any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("go.mod not found")
		}
		wd = parent
	}
}

func newFSBackend(t *testing.T) *httptest.Server {
	t.Helper()
	backend := mcp.NewServer(&mcp.Implementation{Name: "fs-backend", Version: "0.1.0"}, nil)
	mcp.AddTool(backend, &mcp.Tool{
		Name:        "write_file",
		Description: "write a file",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, params struct {
		Path string `json:"path"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "wrote " + params.Path}},
		}, nil, nil
	})
	server := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return backend
	}, nil))
	t.Cleanup(server.Close)
	return server
}

func firstText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	for _, item := range res.Content {
		if text, ok := item.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	return fmt.Sprintf("%v", res.Content)
}

type e2eAgentManager struct {
	srv *authserver.Server
}

func (m *e2eAgentManager) ListAgents() []any {
	agents := m.srv.ListAgents()
	out := make([]any, len(agents))
	for i := range agents {
		out[i] = agents[i]
	}
	return out
}

func (m *e2eAgentManager) GetAgentByPrismID(prismID string) any {
	return m.srv.GetAgentByPrismID(prismID)
}

func (m *e2eAgentManager) SetAgentPolicy(prismID string, groups, grant, deny []string) error {
	existing, _ := m.srv.GetAgentPolicy(prismID)
	policy := &authserver.AgentPolicy{Groups: groups, Grant: grant, Deny: deny}
	if existing != nil {
		policy.BackendPolicies = existing.BackendPolicies
	}
	return m.srv.SetAgentPolicy(prismID, policy)
}

func (m *e2eAgentManager) SetAgentBackendPolicies(prismID string, policies map[string]auth.BackendPolicy) error {
	existing, _ := m.srv.GetAgentPolicy(prismID)
	policy := &authserver.AgentPolicy{}
	if existing != nil {
		policy = existing
	}
	policy.BackendPolicies = policies
	return m.srv.SetAgentPolicy(prismID, policy)
}

func (m *e2eAgentManager) DeleteAgentPolicy(prismID string) error {
	return m.srv.DeleteAgentPolicy(prismID)
}

func (m *e2eAgentManager) RemoveAgent(clientID string) bool {
	return m.srv.RemoveAgent(clientID)
}

func (m *e2eAgentManager) RemoveStaleAgents() int {
	return m.srv.RemoveStaleAgents(7 * 24 * time.Hour)
}

// GetAgentPolicy satisfies admin.PolicyAgentReader so the policy builder can
// resolve an agent's groups + roles when composing inherited capability
// views.
func (m *e2eAgentManager) GetAgentPolicy(prismID string) (*admin.AgentPolicy, error) {
	p, err := m.srv.GetAgentPolicy(prismID)
	if err != nil || p == nil {
		return nil, err
	}
	return &admin.AgentPolicy{
		Groups:          append([]string(nil), p.Groups...),
		Grant:           append([]string(nil), p.Grant...),
		Deny:            append([]string(nil), p.Deny...),
		BackendPolicies: p.BackendPolicies,
	}, nil
}

type e2eGroupManager struct {
	srv *authserver.Server
}

func (m *e2eGroupManager) ListGroups() []admin.GroupInfo {
	groups := m.srv.ListGroups()
	out := make([]admin.GroupInfo, len(groups))
	for i, group := range groups {
		out[i] = admin.GroupInfo{
			Name:            group.Name,
			Scopes:          group.Scopes,
			Source:          group.Source,
			BackendPolicies: group.BackendPolicies,
		}
	}
	return out
}

func (m *e2eGroupManager) GetGroup(name string) *admin.GroupInfo {
	for _, group := range m.ListGroups() {
		if group.Name == name {
			return &group
		}
	}
	return nil
}

func (m *e2eGroupManager) SetGroup(name string, scopes []string) error {
	return m.srv.SetGroup(name, &authserver.GroupConfig{Scopes: scopes})
}

func (m *e2eGroupManager) SetGroupBackendPolicies(name string, policies map[string]auth.BackendPolicy) error {
	existing, _ := m.srv.GetGroup(name)
	if existing == nil {
		existing = &authserver.GroupConfig{}
	}
	existing.BackendPolicies = policies
	return m.srv.SetGroup(name, existing)
}

func (m *e2eGroupManager) DeleteGroup(name string) error {
	return m.srv.DeleteGroup(name)
}

func (m *e2eGroupManager) DefaultScopes() []string {
	return m.srv.DefaultScopes()
}

func (m *e2eGroupManager) SetDefaultScopes(scopes []string) error {
	return m.srv.SetDefaultScopes(scopes)
}

func (m *e2eGroupManager) DefaultBackendPolicies() map[string]auth.BackendPolicy {
	return m.srv.DefaultBackendPolicies()
}

func (m *e2eGroupManager) SetDefaultBackendPolicies(policies map[string]auth.BackendPolicy) error {
	return m.srv.SetDefaultBackendPolicies(policies)
}
