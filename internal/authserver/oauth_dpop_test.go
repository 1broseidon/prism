package authserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	prismauth "github.com/1broseidon/prism/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	jwxjwt "github.com/lestrrat-go/jwx/v2/jwt"
)

func TestTokenDPoPCodeExchangeForCnfGrant(t *testing.T) {
	srv := newTestServer(t)
	code := addAuthCode(t, srv, "ci-agent", []prismauth.IssuedGrant{{Type: prismauth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local", CnfRequired: true}}, "urn:prism:mfa")
	priv, pub := testDPoPJWK(t)
	proof := signTokenDPoP(t, priv, pub, "POST", "http://example.com/token", "code-jti-1", srv.currentDPoPNonce(time.Now()))
	w := postCodeExchange(t, srv, code, proof)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TokenType != "DPoP" {
		t.Fatalf("token_type = %q", resp.TokenType)
	}
	claims := parseUnverifiedClaims(t, resp.AccessToken)
	if claims["cnf"] == nil || claims["authorization_details"] == nil || claims["acr"] != "urn:prism:mfa" {
		t.Fatalf("claims missing grant fields: %+v", claims)
	}
}

func TestTokenDPoPRequiredForCnfGrant(t *testing.T) {
	srv := newTestServer(t)
	code := addAuthCode(t, srv, "ci-agent", []prismauth.IssuedGrant{{Type: prismauth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local", CnfRequired: true}}, "")
	w := postCodeExchange(t, srv, code, "")
	assertOAuthError(t, w, http.StatusBadRequest, "invalid_request")
}

func TestTokenBearerCompatWithoutCnfGrant(t *testing.T) {
	srv := newTestServer(t)
	code := addAuthCode(t, srv, "ci-agent", []prismauth.IssuedGrant{{Type: prismauth.GrantTypeMCPCall, Tool: "fs.read_file", Backend: "local"}}, "")
	w := postCodeExchange(t, srv, code, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TokenType != "Bearer" {
		t.Fatalf("token_type = %q", resp.TokenType)
	}
}

func TestTokenDPoPOptInWithoutCnfGrant(t *testing.T) {
	srv := newTestServer(t)
	code := addAuthCode(t, srv, "ci-agent", []prismauth.IssuedGrant{{Type: prismauth.GrantTypeMCPCall, Tool: "fs.read_file", Backend: "local"}}, "")
	priv, pub := testDPoPJWK(t)
	w := postCodeExchange(t, srv, code, signTokenDPoP(t, priv, pub, "POST", "http://example.com/token", "code-jti-2", srv.currentDPoPNonce(time.Now())))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TokenType != "DPoP" {
		t.Fatalf("token_type = %q", resp.TokenType)
	}
}

func TestRefreshDPoPBinding(t *testing.T) {
	srv := newTestServer(t)
	code := addAuthCode(t, srv, "ci-agent", []prismauth.IssuedGrant{{Type: prismauth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local", CnfRequired: true}}, "")
	priv, pub := testDPoPJWK(t)
	w := postCodeExchange(t, srv, code, signTokenDPoP(t, priv, pub, "POST", "http://example.com/token", "code-jti-3", srv.currentDPoPNonce(time.Now())))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	w = postRefresh(t, srv, resp.RefreshToken, signTokenDPoP(t, priv, pub, "POST", "http://example.com/token", "refresh-jti-1", srv.currentDPoPNonce(time.Now())))
	if w.Code != http.StatusOK {
		t.Fatalf("refresh status = %d body=%s", w.Code, w.Body.String())
	}
	var refreshResp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&refreshResp); err != nil {
		t.Fatal(err)
	}
	if refreshResp.TokenType != "DPoP" || refreshResp.RefreshToken == "" || refreshResp.RefreshToken == resp.RefreshToken {
		t.Fatalf("refresh response = %+v", refreshResp)
	}
	claims := parseUnverifiedClaims(t, refreshResp.AccessToken)
	if claims["authorization_details"] == nil || claims["cnf"] == nil {
		t.Fatalf("refreshed token lost grant binding claims: %+v", claims)
	}
}

func TestRefreshDPoPRejectsMissingOrMismatchedProof(t *testing.T) {
	srv := newTestServer(t)
	code := addAuthCode(t, srv, "ci-agent", []prismauth.IssuedGrant{{Type: prismauth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local", CnfRequired: true}}, "")
	priv, pub := testDPoPJWK(t)
	w := postCodeExchange(t, srv, code, signTokenDPoP(t, priv, pub, "POST", "http://example.com/token", "code-jti-4", srv.currentDPoPNonce(time.Now())))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp TokenResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)

	// Missing proof consumes the one-time refresh token.
	w = postRefresh(t, srv, resp.RefreshToken, "")
	assertOAuthError(t, w, http.StatusUnauthorized, "invalid_grant")

	code = addAuthCode(t, srv, "ci-agent", []prismauth.IssuedGrant{{Type: prismauth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local", CnfRequired: true}}, "")
	w = postCodeExchange(t, srv, code, signTokenDPoP(t, priv, pub, "POST", "http://example.com/token", "code-jti-5", srv.currentDPoPNonce(time.Now())))
	_ = json.NewDecoder(w.Body).Decode(&resp)
	otherPriv, otherPub := testDPoPJWK(t)
	w = postRefresh(t, srv, resp.RefreshToken, signTokenDPoP(t, otherPriv, otherPub, "POST", "http://example.com/token", "refresh-jti-2", srv.currentDPoPNonce(time.Now())))
	assertOAuthError(t, w, http.StatusUnauthorized, "invalid_grant")
}

func TestTokenDPoPNonceChallengeDoesNotConsumeCode(t *testing.T) {
	srv := newTestServer(t)
	code := addAuthCode(t, srv, "ci-agent", []prismauth.IssuedGrant{{Type: prismauth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local", CnfRequired: true}}, "")
	priv, pub := testDPoPJWK(t)

	w := postCodeExchange(t, srv, code, signTokenDPoP(t, priv, pub, "POST", "http://example.com/token", "nonce-jti-1", ""))
	assertOAuthError(t, w, http.StatusBadRequest, "use_dpop_nonce")
	nonce := w.Header().Get("DPoP-Nonce")
	if nonce == "" {
		t.Fatal("missing DPoP-Nonce header")
	}

	w = postCodeExchange(t, srv, code, signTokenDPoP(t, priv, pub, "POST", "http://example.com/token", "nonce-jti-2", nonce))
	if w.Code != http.StatusOK {
		t.Fatalf("retry status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestTokenGrantClaimsRoundTripThroughValidator(t *testing.T) {
	srv := newTestServer(t)
	grant := prismauth.IssuedGrant{Type: prismauth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local", CnfRequired: true}
	code := addAuthCode(t, srv, "ci-agent", []prismauth.IssuedGrant{grant}, "urn:prism:mfa")
	priv, pub := testDPoPJWK(t)
	w := postCodeExchange(t, srv, code, signTokenDPoP(t, priv, pub, "POST", "http://example.com/token", "claims-jti-1", srv.currentDPoPNonce(time.Now())))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	validator := prismauth.NewTokenValidator(&prismauth.TokenValidatorConfig{
		IssuerURL:  testIssuer,
		Audience:   testIssuer,
		StaticJWKS: srv.km.JWKS(),
	})
	claims, _, err := validator.Validate(context.Background(), resp.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Cnf == nil || claims.Cnf.JKT == "" || claims.Acr != "urn:prism:mfa" || len(claims.AuthorizationDetails) != 1 {
		t.Fatalf("claims = %+v", claims)
	}
	if claims.AuthorizationDetails[0].Tool != grant.Tool || !claims.AuthorizationDetails[0].CnfRequired {
		t.Fatalf("authorization_details = %+v", claims.AuthorizationDetails)
	}
}

func TestTokenLegacyPathOmitsAuthorizationDetails(t *testing.T) {
	srv := newTestServer(t)
	token, err := srv.mintToken("ci-agent", []string{"mcp:connect"})
	if err != nil {
		t.Fatal(err)
	}
	claims := parseUnverifiedClaims(t, token)
	if _, ok := claims["authorization_details"]; ok {
		t.Fatalf("authorization_details should be omitted: %+v", claims)
	}
	if _, ok := claims["cnf"]; ok {
		t.Fatalf("cnf should be omitted: %+v", claims)
	}
}

func addAuthCode(t *testing.T, srv *Server, clientID string, grants []prismauth.IssuedGrant, acr string) string {
	t.Helper()
	verifier := "verifier-abcdefghijklmnopqrstuvwxyz0123456789"
	challenge := pkceChallenge(verifier)
	code := "code-" + base64.RawURLEncoding.EncodeToString([]byte(time.Now().String()))[:8]
	srv.oauth.mu.Lock()
	srv.oauth.codes[code] = &authCode{
		code: code, clientID: clientID, redirectURI: "http://client/cb",
		challenge: challenge, method: "S256", expiresAt: time.Now().Add(time.Minute),
		issuedGrants: grants, authTime: time.Now().Add(-time.Minute).Unix(), acr: acr,
	}
	srv.oauth.mu.Unlock()
	return code
}

func postCodeExchange(t *testing.T, srv *Server, code, proof string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://client/cb"},
		"code_verifier": {"verifier-abcdefghijklmnopqrstuvwxyz0123456789"},
		"client_id":     {"ci-agent"},
	}
	r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if proof != "" {
		r.Header.Set("DPoP", proof)
	}
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	return w
}

func postRefresh(t *testing.T, srv *Server, refresh, proof string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if proof != "" {
		r.Header.Set("DPoP", proof)
	}
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	return w
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

func testDPoPJWK(t *testing.T) (*ecdsa.PrivateKey, jwk.Key) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jwk.FromRaw(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

func signTokenDPoP(t *testing.T, priv *ecdsa.PrivateKey, pub jwk.Key, method, htu, jti, nonce string) string {
	t.Helper()
	tok := jwxjwt.New()
	_ = tok.Set("htm", method)
	_ = tok.Set("htu", htu)
	_ = tok.Set("iat", time.Now())
	_ = tok.Set("jti", jti)
	if nonce != "" {
		_ = tok.Set("nonce", nonce)
	}
	headers := jws.NewHeaders()
	_ = headers.Set("typ", "dpop+jwt")
	_ = headers.Set("jwk", pub)
	signed, err := jwxjwt.Sign(tok, jwxjwt.WithKey(jwa.ES256, priv, jws.WithProtectedHeaders(headers)))
	if err != nil {
		t.Fatal(err)
	}
	return string(signed)
}
