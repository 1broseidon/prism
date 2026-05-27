package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

func TestDPoPHappyPathSupportedAlgorithms(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, alg := range []jwa.SignatureAlgorithm{jwa.ES256, jwa.ES384, jwa.EdDSA, jwa.RS256} {
		t.Run(alg.String(), func(t *testing.T) {
			priv, pub := testDPoPKey(t, alg)
			proof := signDPoPProof(t, alg, priv, pub, dpopClaims{
				HTM: "POST", HTU: "https://Example.COM:443/mcp?x=1#frag",
				IAT: now, JTI: "jti-" + alg.String(), Nonce: "n1", ATH: AccessTokenHash("token"),
			}, "dpop+jwt")
			got, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "n1", "token", now, NewReplayCache(time.Minute, 100))
			if err != nil {
				t.Fatal(err)
			}
			if got.Method != "POST" || got.URL != "https://example.com/mcp" || got.Thumbprint == "" {
				t.Fatalf("unexpected proof: %+v", got)
			}
		})
	}
}

func TestDPoPRejectsAlgNoneExplicitly(t *testing.T) {
	now := time.Unix(100, 0)
	header := map[string]any{"typ": "dpop+jwt", "alg": "none"}
	payload := map[string]any{"htm": "POST", "htu": "https://example.com/mcp", "iat": now.Unix(), "jti": "j1"}
	token := b64JSON(t, header) + "." + b64JSON(t, payload) + "."
	_, err := ParseAndValidateProof(token, "POST", "https://example.com/mcp", "", "", now, nil)
	if err == nil || !strings.Contains(err.Error(), "none") {
		t.Fatalf("expected none rejection, got %v", err)
	}
}

func TestDPoPRejectsWrongTyp(t *testing.T) {
	now := time.Unix(100, 0)
	priv, pub := testDPoPKey(t, jwa.ES256)
	proof := signDPoPProof(t, jwa.ES256, priv, pub, dpopClaims{
		HTM: "POST", HTU: "https://example.com/mcp", IAT: now, JTI: "j1",
	}, "JWT")
	_, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "", "", now, nil)
	if err == nil || !strings.Contains(err.Error(), "typ") {
		t.Fatalf("expected typ rejection, got %v", err)
	}
}

func TestDPoPRejectsHTMAndHTUMismatch(t *testing.T) {
	now := time.Unix(100, 0)
	priv, pub := testDPoPKey(t, jwa.ES256)
	proof := signDPoPProof(t, jwa.ES256, priv, pub, dpopClaims{
		HTM: "post", HTU: "https://example.com/mcp", IAT: now, JTI: "j1",
	}, "dpop+jwt")
	if _, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "", "", now, nil); err == nil {
		t.Fatal("expected htm mismatch")
	}
	proof = signDPoPProof(t, jwa.ES256, priv, pub, dpopClaims{
		HTM: "POST", HTU: "https://example.com/other", IAT: now, JTI: "j2",
	}, "dpop+jwt")
	if _, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "", "", now, nil); err == nil {
		t.Fatal("expected htu mismatch")
	}
}

func TestDPoPIATBoundary(t *testing.T) {
	now := time.Unix(100, 0)
	priv, pub := testDPoPKey(t, jwa.ES256)
	for _, tc := range []struct {
		name    string
		iat     time.Time
		wantErr bool
	}{
		{"minus60", now.Add(-60 * time.Second), false},
		{"plus60", now.Add(60 * time.Second), false},
		{"minus61", now.Add(-61 * time.Second), true},
		{"plus61", now.Add(61 * time.Second), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proof := signDPoPProof(t, jwa.ES256, priv, pub, dpopClaims{
				HTM: "POST", HTU: "https://example.com/mcp", IAT: tc.iat, JTI: tc.name,
			}, "dpop+jwt")
			_, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "", "", now, NewReplayCache(time.Minute, 100))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDPoPReplayNonceAndATH(t *testing.T) {
	now := time.Unix(100, 0)
	priv, pub := testDPoPKey(t, jwa.ES256)
	proof := signDPoPProof(t, jwa.ES256, priv, pub, dpopClaims{
		HTM: "POST", HTU: "https://example.com/mcp", IAT: now, JTI: "j1", Nonce: "n1", ATH: AccessTokenHash("token"),
	}, "dpop+jwt")
	cache := NewReplayCache(time.Minute, 100)
	if _, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "n1", "token", now, cache); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "n1", "token", now, cache); err == nil {
		t.Fatal("expected replay rejection")
	}
	if _, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "wrong", "token", now, NewReplayCache(time.Minute, 100)); err == nil {
		t.Fatal("expected nonce mismatch")
	}
	if _, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "n1", "wrong-token", now, NewReplayCache(time.Minute, 100)); err == nil {
		t.Fatal("expected ath mismatch")
	}
}

func TestDPoPRejectsSignatureMismatch(t *testing.T) {
	now := time.Unix(100, 0)
	privA, _ := testDPoPKey(t, jwa.ES256)
	_, pubB := testDPoPKey(t, jwa.ES256)
	proof := signDPoPProof(t, jwa.ES256, privA, pubB, dpopClaims{
		HTM: "POST", HTU: "https://example.com/mcp", IAT: now, JTI: "j1",
	}, "dpop+jwt")
	if _, err := ParseAndValidateProof(proof, "POST", "https://example.com/mcp", "", "", now, nil); err == nil {
		t.Fatal("expected signature mismatch")
	}
}

func TestDPoPCanonicalURL(t *testing.T) {
	a := canonicalDPoPURL("https://Example.COM:443/path?q=1#frag")
	b := canonicalDPoPURL("https://example.com/path")
	if a != b {
		t.Fatalf("canonical URLs differ: %q %q", a, b)
	}
}

type dpopClaims struct {
	HTM   string
	HTU   string
	IAT   time.Time
	JTI   string
	Nonce string
	ATH   string
}

func signDPoPProof(t *testing.T, alg jwa.SignatureAlgorithm, priv any, pub jwk.Key, claims dpopClaims, typ string) string {
	t.Helper()
	tok := jwt.New()
	if err := tok.Set("htm", claims.HTM); err != nil {
		t.Fatal(err)
	}
	if err := tok.Set("htu", claims.HTU); err != nil {
		t.Fatal(err)
	}
	if err := tok.Set("iat", claims.IAT); err != nil {
		t.Fatal(err)
	}
	if err := tok.Set("jti", claims.JTI); err != nil {
		t.Fatal(err)
	}
	if claims.Nonce != "" {
		_ = tok.Set("nonce", claims.Nonce)
	}
	if claims.ATH != "" {
		_ = tok.Set("ath", claims.ATH)
	}
	headers := jws.NewHeaders()
	if err := headers.Set("typ", typ); err != nil {
		t.Fatal(err)
	}
	if err := headers.Set("jwk", pub); err != nil {
		t.Fatal(err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(alg, priv, jws.WithProtectedHeaders(headers)))
	if err != nil {
		t.Fatal(err)
	}
	return string(signed)
}

func testDPoPKey(t *testing.T, alg jwa.SignatureAlgorithm) (any, jwk.Key) {
	t.Helper()
	var priv any
	var pub any
	switch alg {
	case jwa.ES256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		priv, pub = k, &k.PublicKey
	case jwa.ES384:
		k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		priv, pub = k, &k.PublicKey
	case jwa.EdDSA:
		pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		priv, pub = privKey, pubKey
	case jwa.RS256:
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		priv, pub = k, &k.PublicKey
	default:
		t.Fatalf("unsupported alg %s", alg)
	}
	jwkPub, err := jwk.FromRaw(pub)
	if err != nil {
		t.Fatal(err)
	}
	pubOnly, err := publicJWK(context.Background(), jwkPub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pubOnly
}

func b64JSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}
