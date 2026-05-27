package auth

import (
	"fmt"
	"net/http"
	"time"
)

// validateResourceDPoPProof validates the DPoP proof on an inbound MCP
// resource request. It computes the absolute request URL exactly as
// ParseAndValidateProof expects ("scheme://host/path"), passes the access
// token through so the embedded ath claim is verified, and shares the
// replay cache supplied by the caller for JTI uniqueness.
//
// Returns the parsed proof on success. On any failure (missing header,
// signature mismatch, replay, etc.) it returns the underlying error.
func validateResourceDPoPProof(r *http.Request, accessToken string, replay *ReplayCache, now time.Time) (*Proof, error) {
	header := r.Header.Get("DPoP")
	return ParseAndValidateProof(header, r.Method, absoluteResourceURL(r), "", accessToken, now, replay)
}

func absoluteResourceURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, r.URL.Path)
}
