package credentials

import (
	"log/slog"
	"net/http"
	"strings"
)

// InjectingTransport is an http.RoundTripper that resolves the credential for
// a backend and sets the appropriate HTTP header on every outbound request.
//
// The raw credential value is never logged or surfaced to the calling agent.
type InjectingTransport struct {
	Base      http.RoundTripper
	Store     *Store
	BackendID string
	Logger    *slog.Logger
}

// RoundTrip implements http.RoundTripper.
func (t *InjectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	header, value, err := t.Store.Resolve(req.Context(), t.BackendID)
	if err != nil {
		return nil, err
	}

	if header != "" && value != "" {
		// Clone the request to avoid mutating the caller's copy.
		clone := req.Clone(req.Context())
		clone.Header.Set(header, value)
		req = clone
	}

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// Gap 5 (MCP auth spec §251-288): Detect 403 insufficient_scope responses.
	// When the upstream returns 403 with error="insufficient_scope" in
	// WWW-Authenticate, log a warning with the required scopes. Full step-up
	// re-authorization is not yet implemented.
	if resp.StatusCode == http.StatusForbidden {
		t.detectInsufficientScope(resp)
	}

	return resp, nil
}

// detectInsufficientScope checks a 403 response for WWW-Authenticate headers
// indicating insufficient_scope and logs a warning with the required scopes.
func (t *InjectingTransport) detectInsufficientScope(resp *http.Response) {
	logger := t.Logger
	if logger == nil {
		logger = slog.Default()
	}

	wwwAuth := resp.Header.Values("WWW-Authenticate")
	for _, h := range wwwAuth {
		if !strings.Contains(h, "insufficient_scope") {
			continue
		}
		// Extract scope from the header value (simple parsing without importing oauthex).
		var requiredScope string
		for _, part := range strings.Split(h, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "scope=") || strings.Contains(part, " scope=") {
				idx := strings.Index(part, "scope=")
				scopeVal := part[idx+len("scope="):]
				scopeVal = strings.Trim(scopeVal, "\"")
				requiredScope = scopeVal
				break
			}
		}

		logger.Warn("upstream returned 403 insufficient_scope — step-up re-authorization may be needed",
			"backend", t.BackendID,
			"required_scope", requiredScope,
			"url", resp.Request.URL.String(),
		)
		return
	}
}
