package auth

import (
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const dpopIATSkew = 60 * time.Second

// ErrDPoPNonceMismatch indicates that a proof nonce did not match the
// expected server challenge.
var ErrDPoPNonceMismatch = errors.New("dpop nonce mismatch")

// Proof is a validated DPoP proof JWT.
type Proof struct {
	Method     string
	URL        string
	IssuedAt   time.Time
	JTI        string
	Nonce      string
	AccessHash string
	JWK        jwk.Key
	Thumbprint string
}

// ParseAndValidateProof validates a compact DPoP proof JWT.
func ParseAndValidateProof(header string, expectedMethod, expectedURL string, expectedNonce string, accessToken string, now time.Time, replay *ReplayCache) (*Proof, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil, errors.New("dpop proof is required")
	}
	if now.IsZero() {
		return nil, errors.New("now is required")
	}

	msg, err := jws.Parse([]byte(header), jws.WithCompact())
	if err != nil {
		return nil, fmt.Errorf("parse dpop proof: %w", err)
	}
	if len(msg.Signatures()) != 1 {
		return nil, errors.New("dpop proof must contain exactly one signature")
	}
	protected := msg.Signatures()[0].ProtectedHeaders()
	if protected == nil {
		return nil, errors.New("dpop proof missing protected header")
	}
	if typ := protected.Type(); typ != "dpop+jwt" {
		return nil, fmt.Errorf("dpop typ must be dpop+jwt")
	}
	alg := protected.Algorithm()
	if alg == jwa.NoSignature {
		return nil, errors.New("dpop alg none is not allowed")
	}
	if !allowedDPoPAlg(alg) {
		return nil, fmt.Errorf("dpop alg %s is not allowed", alg)
	}
	key := protected.JWK()
	if key == nil {
		return nil, errors.New("dpop proof missing jwk header")
	}
	payload, err := jws.Verify([]byte(header), jws.WithKey(alg, key))
	if err != nil {
		return nil, fmt.Errorf("dpop signature invalid: %w", err)
	}
	tok, err := jwt.Parse(payload, jwt.WithVerify(false))
	if err != nil {
		return nil, fmt.Errorf("parse dpop claims: %w", err)
	}

	htm, ok := claimString(tok, "htm")
	// RFC 9449 §4.3: htm matched exactly. HTTP methods are case-sensitive
	// per RFC 7230 §3.1.1, so use byte equality (not EqualFold).
	if !ok || htm != expectedMethod {
		return nil, errors.New("dpop htm mismatch")
	}
	htu, ok := claimString(tok, "htu")
	if !ok || canonicalDPoPURL(htu) != canonicalDPoPURL(expectedURL) {
		return nil, errors.New("dpop htu mismatch")
	}
	iat := tok.IssuedAt()
	if iat.IsZero() {
		return nil, errors.New("dpop iat is required")
	}
	if iat.Before(now.Add(-dpopIATSkew)) || iat.After(now.Add(dpopIATSkew)) {
		return nil, errors.New("dpop iat outside allowed window")
	}
	jti := tok.JwtID()
	if strings.TrimSpace(jti) == "" {
		return nil, errors.New("dpop jti is required")
	}
	nonce, _ := claimString(tok, "nonce")
	if expectedNonce != "" && nonce != expectedNonce {
		return nil, ErrDPoPNonceMismatch
	}
	ath, _ := claimString(tok, "ath")
	if accessToken != "" {
		want := AccessTokenHash(accessToken)
		if ath != want {
			return nil, errors.New("dpop ath mismatch")
		}
	}
	jkt, err := ComputeJKT(key)
	if err != nil {
		return nil, err
	}
	if replay != nil && replay.Seen(jti, now) {
		return nil, errors.New("dpop jti replay")
	}
	return &Proof{
		Method:     htm,
		URL:        canonicalDPoPURL(htu),
		IssuedAt:   iat,
		JTI:        jti,
		Nonce:      nonce,
		AccessHash: ath,
		JWK:        key,
		Thumbprint: jkt,
	}, nil
}

// ComputeJKT returns the RFC 7638 SHA-256 thumbprint for a public JWK.
func ComputeJKT(key jwk.Key) (string, error) {
	if key == nil {
		return "", errors.New("jwk is required")
	}
	pub, err := key.PublicKey()
	if err != nil {
		return "", err
	}
	sum, err := pub.Thumbprint(crypto.SHA256)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sum), nil
}

// AccessTokenHash returns the DPoP ath value for an access token.
func AccessTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func allowedDPoPAlg(alg jwa.SignatureAlgorithm) bool {
	switch alg {
	case jwa.ES256, jwa.ES384, jwa.EdDSA, jwa.RS256:
		return true
	default:
		return false
	}
}

func claimString(tok jwt.Token, name string) (string, bool) {
	v, ok := tok.Get(name)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func canonicalDPoPURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" && !isDefaultPort(u.Scheme, port) {
		host = net.JoinHostPort(host, port)
	}
	u.Host = host
	u.RawQuery = ""
	u.Fragment = ""
	u.RawPath = ""
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String()
}

func isDefaultPort(scheme, port string) bool {
	return (scheme == "https" && port == "443") || (scheme == "http" && port == "80")
}

func publicJWK(ctx context.Context, key jwk.Key) (jwk.Key, error) {
	if key == nil {
		return nil, errors.New("jwk is required")
	}
	pub, err := key.PublicKey()
	if err != nil {
		return nil, err
	}
	_ = pub.Remove("kid")
	_ = pub.Remove("alg")
	_ = pub.Remove("use")
	_, _ = pub.AsMap(ctx)
	return pub, nil
}
