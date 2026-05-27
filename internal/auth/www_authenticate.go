package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// wwwAuthenticateKeyOrder is the canonical key order written into a
// WWW-Authenticate challenge. realm is always first; everything else is
// emitted in this order so test snapshots and external consumers see a
// stable shape regardless of map iteration order.
var wwwAuthenticateKeyOrder = []string{
	"error",
	"error_description",
	"error_uri",
	"scope",
	"resource",
	"resource_metadata",
	"acr_values",
	"authorization_details_required",
	"max_age",
}

// writeWWWAuthenticateChallenge writes the OAuth 2.1 / DPoP-flavored
// WWW-Authenticate challenge header in a deterministic shape. The
// (resourceURI, status) parameters mirror the signature of the
// pre-existing writeWWWAuthenticate helper but the params map lets
// callers attach arbitrary RFC 6750 / RFC 9449 / RFC 9396 attributes
// without growing the call sites' arg list.
//
// When includeDPoPNonce is true the response also gets a fresh
// DPoP-Nonce header so the client can retry with a server-issued nonce
// — the canonical DPoP step-up choreography.
func writeWWWAuthenticateChallenge(w http.ResponseWriter, _ string, _ int, scheme string, params map[string]string, includeDPoPNonce bool) {
	if scheme == "" {
		scheme = "Bearer"
	}
	parts := []string{`realm="prism"`}
	emitted := map[string]struct{}{"realm": {}}
	for _, k := range wwwAuthenticateKeyOrder {
		if v, ok := params[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%q", k, v))
			emitted[k] = struct{}{}
		}
	}
	// Append any unknown keys after the canonical set in lexical order so
	// the helper stays useful for new attributes without re-touching the
	// fixed list. The leading attributes are already stable; the tail just
	// needs to be deterministic.
	extras := make([]string, 0, len(params))
	for k := range params {
		if _, done := emitted[k]; done {
			continue
		}
		extras = append(extras, k)
	}
	sortStrings(extras)
	for _, k := range extras {
		parts = append(parts, fmt.Sprintf("%s=%q", k, params[k]))
	}
	w.Header().Set("WWW-Authenticate", scheme+" "+strings.Join(parts, ", "))
	if includeDPoPNonce {
		w.Header().Set("DPoP-Nonce", newDPoPNonceToken())
	}
}

// newDPoPNonceToken produces an opaque random nonce. Production code
// generates these from a server secret + bucket so they roll forward
// deterministically; for the resource-side challenge any 16 bytes of
// crypto/rand suffice — the client just needs something to echo back.
func newDPoPNonceToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "dpop-nonce-fallback"
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// sortStrings is a tiny insertion-sort to avoid importing "sort" just to
// stabilize a 1- or 2-element extras list.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
