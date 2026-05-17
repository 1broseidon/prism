package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseRSAJWKRejectsOversizedExponent(t *testing.T) {
	n := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x02, 0x03})
	e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09})

	_, err := parseRSAJWK(n, e)
	if err == nil {
		t.Fatal("expected oversized exponent error")
	}
	if !strings.Contains(err.Error(), "jwk.e too large") {
		t.Fatalf("expected oversized exponent error, got %v", err)
	}
}
