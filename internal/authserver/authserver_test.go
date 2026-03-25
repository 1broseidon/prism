package authserver

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

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

	return NewServer(cfg, km, nil)
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
