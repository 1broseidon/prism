package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"encoding/base64"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents the validated claims from an OAuth 2.1 access token.
type Claims struct {
	Subject  string `json:"sub"`
	Issuer   string `json:"iss"`
	Audience string `json:"aud"`
	Scope    string `json:"scope"`
	ClientID string `json:"client_id,omitempty"`
	Exp      int64  `json:"exp"`
	Iat      int64  `json:"iat"`
}

// TokenValidatorConfig configures the JWT token validator.
type TokenValidatorConfig struct {
	// IssuerURL is the expected token issuer (e.g. "https://auth.example.com/realms/mcp").
	// Tokens with a different issuer are rejected.
	IssuerURL string `json:"issuer_url"`

	// JWKSURL is the URL to fetch the JSON Web Key Set for signature verification.
	// If empty, defaults to IssuerURL + "/.well-known/jwks.json".
	JWKSURL string `json:"jwks_url,omitempty"`

	// Audience is the expected audience claim (the gateway's resource identifier per RFC 8707).
	// Tokens not issued for this audience are rejected.
	Audience string `json:"audience"`

	// RequiredScopes is a set of scopes that MUST be present on every token.
	// Use this for baseline access (e.g. "mcp:connect").
	RequiredScopes []string `json:"required_scopes,omitempty"`

	// MaxTokenAge is the maximum age of a token from issuance.
	// Tokens older than this are rejected even if not expired.
	// Zero means no max age check (only exp is checked).
	MaxTokenAge time.Duration `json:"max_token_age,omitempty"`
}

// TokenValidator validates OAuth 2.1 JWT access tokens.
type TokenValidator struct {
	cfg    TokenValidatorConfig
	keySet *jwksKeySet
	parser *jwt.Parser
}

// NewTokenValidator creates a token validator.
func NewTokenValidator(cfg *TokenValidatorConfig) *TokenValidator {
	jwksURL := cfg.JWKSURL
	if jwksURL == "" {
		jwksURL = strings.TrimRight(cfg.IssuerURL, "/") + "/.well-known/jwks.json"
	}

	return &TokenValidator{
		cfg:    *cfg,
		keySet: newJWKSKeySet(jwksURL),
		parser: jwt.NewParser(
			jwt.WithIssuer(cfg.IssuerURL),
			jwt.WithAudience(cfg.Audience),
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
			jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}),
		),
	}
}

// Validate parses and validates a Bearer token string.
// Returns the validated claims and a policy derived from the token's scopes.
func (v *TokenValidator) Validate(ctx context.Context, tokenString string) (*Claims, *Policy, error) {
	token, err := v.parser.Parse(tokenString, func(token *jwt.Token) (any, error) {
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, errors.New("token missing kid header")
		}
		return v.keySet.GetKey(ctx, kid)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("token validation failed: %w", err)
	}

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, nil, errors.New("invalid claims format")
	}

	claims := extractClaims(mapClaims)

	if err := v.checkTokenAge(mapClaims); err != nil {
		return nil, nil, err
	}

	if err := v.checkRequiredScopes(claims.Scope); err != nil {
		return nil, nil, err
	}

	return claims, NewPolicy(claims.Scope), nil
}

// extractClaims pulls known fields from JWT map claims into a typed struct.
func extractClaims(mc jwt.MapClaims) *Claims {
	claims := &Claims{}

	if sub, ok := mc["sub"].(string); ok {
		claims.Subject = sub
	}
	if iss, ok := mc["iss"].(string); ok {
		claims.Issuer = iss
	}
	if scope, ok := mc["scope"].(string); ok {
		claims.Scope = scope
	}
	if clientID, ok := mc["client_id"].(string); ok {
		claims.ClientID = clientID
	}

	switch aud := mc["aud"].(type) {
	case string:
		claims.Audience = aud
	case []any:
		for _, a := range aud {
			if s, ok := a.(string); ok {
				claims.Audience = s
				break
			}
		}
	}

	return claims
}

// checkTokenAge rejects tokens older than MaxTokenAge.
func (v *TokenValidator) checkTokenAge(mc jwt.MapClaims) error {
	if v.cfg.MaxTokenAge <= 0 {
		return nil
	}
	iat, ok := mc["iat"].(float64)
	if !ok {
		return nil
	}
	issuedAt := time.Unix(int64(iat), 0)
	if time.Since(issuedAt) > v.cfg.MaxTokenAge {
		return fmt.Errorf("token too old: issued at %s, max age %s", issuedAt, v.cfg.MaxTokenAge)
	}
	return nil
}

// checkRequiredScopes ensures all required scopes are present.
func (v *TokenValidator) checkRequiredScopes(scopeStr string) error {
	if len(v.cfg.RequiredScopes) == 0 {
		return nil
	}
	granted := make(map[string]struct{})
	for _, s := range strings.Fields(scopeStr) {
		granted[s] = struct{}{}
	}
	for _, required := range v.cfg.RequiredScopes {
		if _, ok := granted[required]; !ok {
			return fmt.Errorf("missing required scope %q", required)
		}
	}
	return nil
}

// ExtractBearerToken extracts a Bearer token from an HTTP Authorization header.
func ExtractBearerToken(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", errors.New("missing Authorization header")
	}
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", errors.New("authorization header must use Bearer scheme")
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		return "", errors.New("empty bearer token")
	}
	return token, nil
}

// --- JWKS Key Set (auto-fetching, caching, per RFC 7517) ---

type jwksKeySet struct {
	url    string
	mu     sync.RWMutex
	keys   map[string]*rsa.PublicKey
	expiry time.Time
	client *http.Client
}

func newJWKSKeySet(url string) *jwksKeySet {
	return &jwksKeySet{
		url:    url,
		keys:   make(map[string]*rsa.PublicKey),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (ks *jwksKeySet) GetKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	ks.mu.RLock()
	key, ok := ks.keys[kid]
	expired := time.Now().After(ks.expiry)
	ks.mu.RUnlock()

	if ok && !expired {
		return key, nil
	}

	// Fetch or refresh
	if err := ks.refresh(ctx); err != nil {
		return nil, fmt.Errorf("JWKS fetch failed: %w", err)
	}

	ks.mu.RLock()
	defer ks.mu.RUnlock()
	key, ok = ks.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key %q not found in JWKS", kid)
	}
	return key, nil
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (ks *jwksKeySet) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ks.url, http.NoBody)
	if err != nil {
		return err
	}

	resp, err := ks.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return err
	}

	var jwks jwksResponse
	if err := json.Unmarshal(body, &jwks); err != nil {
		return fmt.Errorf("parse JWKS: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}

		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}

		n := new(big.Int).SetBytes(nBytes)
		e := new(big.Int).SetBytes(eBytes)

		pubKey := &rsa.PublicKey{
			N: n,
			E: int(e.Int64()),
		}
		newKeys[k.Kid] = pubKey
	}

	ks.mu.Lock()
	ks.keys = newKeys
	ks.expiry = time.Now().Add(5 * time.Minute) // Cache for 5 minutes
	ks.mu.Unlock()

	return nil
}
