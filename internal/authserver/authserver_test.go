package authserver

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// memKV is an in-memory kvStore for testing.
type memKV struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemKV() *memKV {
	return &memKV{data: make(map[string][]byte)}
}

func (m *memKV) Get(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return v, nil
}

func (m *memKV) Set(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *memKV) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *memKV) List(prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

const testIssuer = "http://localhost:9100"

func newTestServer(t *testing.T) *Server {
	t.Helper()

	km, err := NewKeyManager("")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}

	cfg := &Config{
		ListenAddr:      ":9100",
		Issuer:          testIssuer,
		TokenTTLSeconds: 3600,
		Clients: []ClientConfig{
			{
				ClientID:      "ci-agent",
				ClientSecret:  "ci-secret",
				AllowedScopes: []string{"mcp:connect", "github:create_issue", "github:list_prs"},
				Description:   "CI/CD pipeline agent",
			},
			{
				ClientID:      "analyst",
				ClientSecret:  "analyst-secret",
				AllowedScopes: []string{"mcp:connect", "dns:check-dns"},
			},
		},
	}

	return NewServer(cfg, km, nil, nil)
}

func postToken(t *testing.T, srv *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(form.Encode())
	r := httptest.NewRequest(http.MethodPost, "/token", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	return w
}

func TestVerifyConsentCSRFRequiresMatchingCookieAndFormToken(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader("_csrf=token-1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: consentCSRFCookie, Value: "token-1"})
	if err := verifyConsentCSRF(r); err != nil {
		t.Fatalf("matching csrf token rejected: %v", err)
	}

	r = httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader("_csrf=token-1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: consentCSRFCookie, Value: "token-2"})
	if err := verifyConsentCSRF(r); err == nil {
		t.Fatal("expected csrf mismatch to be rejected")
	}

	r = httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader("_csrf=token-1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := verifyConsentCSRF(r); err == nil {
		t.Fatal("expected missing csrf cookie to be rejected")
	}
}

func TestTokenEndpoint_ValidClientCredentials(t *testing.T) {
	srv := newTestServer(t)

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"ci-agent"},
		"client_secret": {"ci-secret"},
		"scope":         {"mcp:connect github:create_issue"},
	}
	w := postToken(t, srv, form)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", resp.TokenType)
	}
	if resp.ExpiresIn != 3600 {
		t.Errorf("expires_in = %d, want 3600", resp.ExpiresIn)
	}
	if resp.AccessToken == "" {
		t.Fatal("access_token is empty")
	}

	tok, _, err := jwt.NewParser().ParseUnverified(resp.AccessToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse JWT: %v", err)
	}
	claims := tok.Claims.(jwt.MapClaims)

	if claims["sub"] != "ci-agent" {
		t.Errorf("sub = %v, want ci-agent", claims["sub"])
	}
	if claims["iss"] != testIssuer {
		t.Errorf("iss = %v, want %s", claims["iss"], testIssuer)
	}
	if claims["scope"] != "mcp:connect github:create_issue" {
		t.Errorf("scope = %v, want mcp:connect github:create_issue", claims["scope"])
	}
	if claims["client_id"] != "ci-agent" {
		t.Errorf("client_id = %v, want ci-agent", claims["client_id"])
	}
	if tok.Header["kid"] == "" {
		t.Error("JWT header missing kid")
	}
}

func TestTokenEndpoint_InvalidClient(t *testing.T) {
	srv := newTestServer(t)

	tests := []struct {
		name   string
		id     string
		secret string
	}{
		{"wrong secret", "ci-agent", "wrong-secret"},
		{"unknown client", "ghost", "ci-secret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{
				"grant_type":    {"client_credentials"},
				"client_id":     {tc.id},
				"client_secret": {tc.secret},
			}
			w := postToken(t, srv, form)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", w.Code)
			}
			var resp OAuthError
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if resp.Error != "invalid_client" {
				t.Errorf("error = %q, want invalid_client", resp.Error)
			}
		})
	}
}

func TestTokenEndpoint_ScopeSubset(t *testing.T) {
	srv := newTestServer(t)

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"ci-agent"},
		"client_secret": {"ci-secret"},
		"scope":         {"mcp:connect"},
	}
	w := postToken(t, srv, form)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Scope != "mcp:connect" {
		t.Errorf("scope = %q, want mcp:connect", resp.Scope)
	}
}

func TestTokenEndpoint_ScopeExceeded(t *testing.T) {
	srv := newTestServer(t)

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"analyst"},
		"client_secret": {"analyst-secret"},
		"scope":         {"github:create_issue"},
	}
	w := postToken(t, srv, form)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var resp OAuthError
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.Error != "invalid_scope" {
		t.Errorf("error = %q, want invalid_scope", resp.Error)
	}
}

func TestRefreshTokenExpiredAbsoluteAgeRejected(t *testing.T) {
	srv := newTestServer(t)
	refresh := "stale-refresh-token"
	hash := sha256Hash(refresh)

	srv.oauth.mu.Lock()
	srv.oauth.refresh[hash] = &refreshToken{
		clientID: "ci-agent",
		issuedAt: time.Now().Add(-refreshTokenMaxAge - time.Hour),
	}
	srv.oauth.mu.Unlock()

	w := postToken(t, srv, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var resp OAuthError
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.Error != "invalid_grant" {
		t.Fatalf("error = %q, want invalid_grant", resp.Error)
	}

	srv.oauth.mu.Lock()
	_, stillPresent := srv.oauth.refresh[hash]
	srv.oauth.mu.Unlock()
	if stillPresent {
		t.Fatal("expired refresh token should be consumed")
	}
}

func TestTokenEndpoint_BasicAuth(t *testing.T) {
	srv := newTestServer(t)

	body := strings.NewReader("grant_type=client_credentials")
	r := httptest.NewRequest(http.MethodPost, "/token", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth("ci-agent", "ci-secret")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken == "" {
		t.Error("access_token is empty")
	}
}

func TestTokenEndpoint_NoScope(t *testing.T) {
	srv := newTestServer(t)

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"analyst"},
		"client_secret": {"analyst-secret"},
	}
	w := postToken(t, srv, form)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	gotScopes := strings.Fields(resp.Scope)
	if len(gotScopes) != 2 {
		t.Errorf("expected 2 scopes, got %d: %q", len(gotScopes), resp.Scope)
	}
}

func TestJWKSEndpoint(t *testing.T) {
	srv := newTestServer(t)

	r := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", http.NoBody)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var jwks JWKSet
	if err := json.NewDecoder(w.Body).Decode(&jwks); err != nil {
		t.Fatalf("decode JWKS: %v", err)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(jwks.Keys))
	}

	k := jwks.Keys[0]
	if k.Kty != "RSA" {
		t.Errorf("kty = %q, want RSA", k.Kty)
	}
	if k.Use != "sig" {
		t.Errorf("use = %q, want sig", k.Use)
	}
	if k.Alg != "RS256" {
		t.Errorf("alg = %q, want RS256", k.Alg)
	}
	if k.Kid == "" {
		t.Error("kid is empty")
	}
	if k.N == "" || k.E == "" {
		t.Error("JWK missing n or e")
	}
}

func TestDiscoveryEndpoint(t *testing.T) {
	srv := newTestServer(t)

	r := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", http.NoBody)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var meta DiscoveryMeta
	if err := json.NewDecoder(w.Body).Decode(&meta); err != nil {
		t.Fatalf("decode discovery: %v", err)
	}

	if meta.Issuer != testIssuer {
		t.Errorf("issuer = %q, want %s", meta.Issuer, testIssuer)
	}
	if meta.TokenEndpoint != testIssuer+"/token" {
		t.Errorf("token_endpoint = %q", meta.TokenEndpoint)
	}
	if meta.JWKsURI != testIssuer+"/.well-known/jwks.json" {
		t.Errorf("jwks_uri = %q", meta.JWKsURI)
	}

	found := false
	for _, g := range meta.GrantTypesSupported {
		if g == "client_credentials" {
			found = true
		}
	}
	if !found {
		t.Error("grant_types_supported does not include client_credentials")
	}
}

func jwkToPublicKey(t *testing.T, k JWK) *rsa.PublicKey {
	t.Helper()

	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		t.Fatalf("decode n: %v", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		t.Fatalf("decode e: %v", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := int(new(big.Int).SetBytes(eBytes).Int64())
	return &rsa.PublicKey{N: n, E: e}
}

func TestEndToEnd_TokenValidation(t *testing.T) {
	srv := newTestServer(t)
	mux := srv.Routes()

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"ci-agent"},
		"client_secret": {"ci-secret"},
		"scope":         {"mcp:connect github:list_prs"},
	}
	body := strings.NewReader(form.Encode())
	r := httptest.NewRequest(http.MethodPost, "/token", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("token request failed: %d %s", w.Code, w.Body.String())
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode token response: %v", err)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", http.NoBody)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, r2)

	var jwks JWKSet
	if err := json.NewDecoder(w2.Body).Decode(&jwks); err != nil {
		t.Fatalf("decode JWKS: %v", err)
	}
	if len(jwks.Keys) == 0 {
		t.Fatal("JWKS has no keys")
	}
	pubKey := jwkToPublicKey(t, jwks.Keys[0])

	parser := jwt.NewParser(
		jwt.WithIssuer(testIssuer),
		jwt.WithAudience(testIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"RS256"}),
	)

	parsed, err := parser.Parse(tokenResp.AccessToken, func(_ *jwt.Token) (any, error) {
		return pubKey, nil
	})
	if err != nil {
		t.Fatalf("token validation failed: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("token is not valid")
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("unexpected claims type")
	}
	if claims["sub"] != "ci-agent" {
		t.Errorf("sub = %v, want ci-agent", claims["sub"])
	}
	if claims["scope"] != "mcp:connect github:list_prs" {
		t.Errorf("scope = %v, want mcp:connect github:list_prs", claims["scope"])
	}
	if claims["jti"] == "" {
		t.Error("jti claim is missing")
	}
}

// --- ReloadPolicy tests ---

// newTestServerWithKV creates a Server with a KV store and optional groups.
func newTestServerWithKV(t *testing.T, kv kvStore, clients []ClientConfig, groups map[string]GroupConfig, defaultScopes []string) *Server {
	t.Helper()

	km, err := NewKeyManager("")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}

	cfg := &Config{
		ListenAddr:      ":9100",
		Issuer:          testIssuer,
		TokenTTLSeconds: 3600,
		Clients:         clients,
		DefaultScopes:   defaultScopes,
	}

	return NewServer(cfg, km, kv, nil, groups)
}

func TestReloadPolicy_StaticClientsUpdated(t *testing.T) {
	initial := []ClientConfig{
		{ClientID: "agent-a", ClientSecret: "secret-a", AllowedScopes: []string{"mcp:connect", "tools:read"}},
	}
	srv := newTestServerWithKV(t, nil, initial, nil, nil)

	// Verify initial state.
	srv.mu.RLock()
	if _, ok := srv.clients["agent-a"]; !ok {
		t.Fatal("agent-a should exist before reload")
	}
	srv.mu.RUnlock()

	// Reload with different clients.
	newClients := []ClientConfig{
		{ClientID: "agent-b", ClientSecret: "secret-b", AllowedScopes: []string{"mcp:connect", "tools:write"}},
		{ClientID: "agent-c", ClientSecret: "secret-c", AllowedScopes: []string{"mcp:connect"}},
	}
	srv.ReloadPolicy(newClients, nil, nil)

	// agent-a should be gone, agent-b and agent-c should exist.
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	if _, ok := srv.clients["agent-a"]; ok {
		t.Error("agent-a should not exist after reload")
	}
	if c, ok := srv.clients["agent-b"]; !ok {
		t.Error("agent-b should exist after reload")
	} else {
		if c.ClientSecret != "secret-b" {
			t.Errorf("agent-b secret = %q, want secret-b", c.ClientSecret)
		}
		sort.Strings(c.AllowedScopes)
		want := []string{"mcp:connect", "tools:write"}
		sort.Strings(want)
		if strings.Join(c.AllowedScopes, ",") != strings.Join(want, ",") {
			t.Errorf("agent-b scopes = %v, want %v", c.AllowedScopes, want)
		}
	}
	if _, ok := srv.clients["agent-c"]; !ok {
		t.Error("agent-c should exist after reload")
	}
}

func TestReloadPolicy_DynamicClientsSurvive(t *testing.T) {
	kv := newMemKV()
	initial := []ClientConfig{
		{ClientID: "static-1", ClientSecret: "s1", AllowedScopes: []string{"mcp:connect"}},
	}
	srv := newTestServerWithKV(t, kv, initial, nil, []string{"tools:default"})

	// Simulate a DCR registration by injecting a dynamic client directly.
	secretHash := sha256Hash("dyn-secret-123")
	srv.mu.Lock()
	srv.clients["dyn-client"] = &ClientConfig{
		ClientID:      "dyn-client",
		ClientSecret:  secretHash,
		AllowedScopes: []string{"mcp:connect", "tools:default"},
		Description:   "My Agent",
	}
	srv.mu.Unlock()

	srv.oauth.mu.Lock()
	srv.oauth.dynamics["dyn-client"] = &dynamicClient{
		ClientID:   "dyn-client",
		ClientName: "My Agent",
		Scopes:     []string{"mcp:connect", "tools:default"},
		PrismID:    "prism-uuid-1",
	}
	srv.oauth.mu.Unlock()

	// Also inject a refresh token.
	srv.oauth.mu.Lock()
	srv.oauth.refresh["refresh-hash-abc"] = &refreshToken{clientID: "dyn-client"}
	srv.oauth.mu.Unlock()

	// Reload with different static clients.
	newClients := []ClientConfig{
		{ClientID: "static-2", ClientSecret: "s2", AllowedScopes: []string{"mcp:connect", "tools:new"}},
	}
	srv.ReloadPolicy(newClients, nil, []string{"tools:default"})

	srv.mu.RLock()
	defer srv.mu.RUnlock()

	// static-1 should be gone, static-2 present.
	if _, ok := srv.clients["static-1"]; ok {
		t.Error("static-1 should not exist after reload")
	}
	if _, ok := srv.clients["static-2"]; !ok {
		t.Error("static-2 should exist after reload")
	}

	// Dynamic client should survive.
	dc, ok := srv.clients["dyn-client"]
	if !ok {
		t.Fatal("dyn-client should survive reload")
	}

	// Secret should be preserved (the hash, not re-hashed).
	if dc.ClientSecret != secretHash {
		t.Errorf("dyn-client secret changed after reload: got %q, want %q", dc.ClientSecret, secretHash)
	}

	// Refresh token should survive.
	srv.oauth.mu.Lock()
	_, hasRefresh := srv.oauth.refresh["refresh-hash-abc"]
	srv.oauth.mu.Unlock()
	if !hasRefresh {
		t.Error("refresh token should survive reload")
	}

	// oauth.dynamics should still have the entry.
	srv.oauth.mu.Lock()
	_, hasDynamic := srv.oauth.dynamics["dyn-client"]
	srv.oauth.mu.Unlock()
	if !hasDynamic {
		t.Error("oauth.dynamics entry should survive reload")
	}
}

func TestReloadPolicy_GroupChangesPropagateToScopes(t *testing.T) {
	kv := newMemKV()

	groups := map[string]GroupConfig{
		"readers": {Scopes: []string{"tools:read", "mcp:connect"}},
	}
	srv := newTestServerWithKV(t, kv, nil, groups, nil)

	// Set up a dynamic client with a PrismID and a KV policy referencing "readers".
	srv.mu.Lock()
	srv.clients["dyn-1"] = &ClientConfig{
		ClientID:      "dyn-1",
		ClientSecret:  "hash-1",
		AllowedScopes: []string{"mcp:connect", "tools:read"},
	}
	srv.mu.Unlock()

	srv.oauth.mu.Lock()
	srv.oauth.dynamics["dyn-1"] = &dynamicClient{
		ClientID:   "dyn-1",
		ClientName: "Reader Agent",
		Scopes:     []string{"mcp:connect", "tools:read"},
		PrismID:    "prism-reader-1",
	}
	srv.oauth.mu.Unlock()

	// Store a KV policy: this agent is in group "readers".
	policy := AgentPolicy{Groups: []string{"readers"}}
	policyData, _ := json.Marshal(policy)
	_ = kv.Set(policyKeyPrefix+"prism-reader-1", policyData)

	// Reload with an expanded "readers" group.
	newGroups := map[string]GroupConfig{
		"readers": {Scopes: []string{"tools:read", "tools:list", "mcp:connect"}},
	}
	srv.ReloadPolicy(nil, newGroups, nil)

	srv.mu.RLock()
	dc, ok := srv.clients["dyn-1"]
	srv.mu.RUnlock()
	if !ok {
		t.Fatal("dyn-1 should survive reload")
	}

	// The dynamic client should now have the expanded scopes.
	scopeSet := make(map[string]struct{})
	for _, s := range dc.AllowedScopes {
		scopeSet[s] = struct{}{}
	}
	for _, want := range []string{"mcp:connect", "tools:read", "tools:list"} {
		if _, ok := scopeSet[want]; !ok {
			t.Errorf("missing expected scope %q after reload, got %v", want, dc.AllowedScopes)
		}
	}
}

func TestReloadPolicy_RemovedGroupContributesNoScopes(t *testing.T) {
	kv := newMemKV()

	groups := map[string]GroupConfig{
		"writers": {Scopes: []string{"tools:write", "mcp:connect"}},
	}
	srv := newTestServerWithKV(t, kv, nil, groups, nil)

	// Dynamic client referencing "writers" group.
	srv.mu.Lock()
	srv.clients["dyn-w"] = &ClientConfig{
		ClientID:      "dyn-w",
		ClientSecret:  "hash-w",
		AllowedScopes: []string{"mcp:connect", "tools:write"},
	}
	srv.mu.Unlock()

	srv.oauth.mu.Lock()
	srv.oauth.dynamics["dyn-w"] = &dynamicClient{
		ClientID:   "dyn-w",
		ClientName: "Writer Agent",
		PrismID:    "prism-writer-1",
	}
	srv.oauth.mu.Unlock()

	policy := AgentPolicy{Groups: []string{"writers"}}
	policyData, _ := json.Marshal(policy)
	_ = kv.Set(policyKeyPrefix+"prism-writer-1", policyData)

	// Reload with "writers" group REMOVED from config.
	srv.ReloadPolicy(nil, nil, nil)

	srv.mu.RLock()
	dc, ok := srv.clients["dyn-w"]
	srv.mu.RUnlock()
	if !ok {
		t.Fatal("dyn-w should survive reload even with removed group")
	}

	// The agent's policy references "writers" but that group no longer exists.
	// The only scope should be mcp:connect (always included).
	if len(dc.AllowedScopes) != 1 || dc.AllowedScopes[0] != "mcp:connect" {
		t.Errorf("expected only mcp:connect when group removed, got %v", dc.AllowedScopes)
	}
}

func TestReloadPolicy_DefaultScopesChange(t *testing.T) {
	kv := newMemKV()
	srv := newTestServerWithKV(t, kv, nil, nil, []string{"tools:basic"})

	// Dynamic client with no custom policy -- should get default scopes.
	srv.mu.Lock()
	srv.clients["dyn-d"] = &ClientConfig{
		ClientID:      "dyn-d",
		ClientSecret:  "hash-d",
		AllowedScopes: []string{"mcp:connect", "tools:basic"},
	}
	srv.mu.Unlock()

	srv.oauth.mu.Lock()
	srv.oauth.dynamics["dyn-d"] = &dynamicClient{
		ClientID:   "dyn-d",
		ClientName: "Default Agent",
		PrismID:    "prism-default-1",
	}
	srv.oauth.mu.Unlock()
	// No KV policy -- will fall back to default scopes.

	// Reload with new default scopes.
	srv.ReloadPolicy(nil, nil, []string{"tools:basic", "tools:extra"})

	srv.mu.RLock()
	dc, ok := srv.clients["dyn-d"]
	srv.mu.RUnlock()
	if !ok {
		t.Fatal("dyn-d should survive reload")
	}

	scopeSet := make(map[string]struct{})
	for _, s := range dc.AllowedScopes {
		scopeSet[s] = struct{}{}
	}
	for _, want := range []string{"mcp:connect", "tools:basic", "tools:extra"} {
		if _, ok := scopeSet[want]; !ok {
			t.Errorf("missing expected scope %q after default_scopes change, got %v", want, dc.AllowedScopes)
		}
	}
}
