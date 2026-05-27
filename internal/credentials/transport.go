package credentials

import (
	"fmt"
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

// UpstreamAuthChallenge is the typed error the transport returns when the
// upstream backend answers with 401 + WWW-Authenticate. The gateway propagates
// the challenge upstream so the agent can step up.
//
// The response is still returned alongside this error so callers can read the
// body or surface a richer message.
type UpstreamAuthChallenge struct {
	BackendID       string
	URL             string
	Scheme          string
	WWWAuthenticate string
	Error_          string
	RequiredScope   string
	AcrValues       string
}

func (c *UpstreamAuthChallenge) Error() string {
	if c == nil {
		return ""
	}
	if c.Error_ != "" {
		return fmt.Sprintf("upstream auth challenge from backend %q (%s): %s", c.BackendID, c.Scheme, c.Error_)
	}
	return fmt.Sprintf("upstream auth challenge from backend %q (%s)", c.BackendID, c.Scheme)
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

	// 401 with a WWW-Authenticate header is a step-up challenge from the
	// upstream — wrap it so the gateway can propagate the challenge to the
	// agent. The body is preserved untouched; the caller is responsible
	// for closing it.
	if resp.StatusCode == http.StatusUnauthorized {
		if challenge := t.buildChallenge(resp); challenge != nil {
			return resp, challenge
		}
	}

	// Gap 5 (MCP auth spec §251-288): Detect 403 insufficient_scope responses.
	// When the upstream returns 403 with error="insufficient_scope" in
	// WWW-Authenticate, log a warning with the required scopes. Full step-up
	// re-authorization is not yet implemented for this case.
	if resp.StatusCode == http.StatusForbidden {
		t.detectInsufficientScope(resp)
	}

	return resp, nil
}

func (t *InjectingTransport) buildChallenge(resp *http.Response) *UpstreamAuthChallenge {
	headers := resp.Header.Values("WWW-Authenticate")
	if len(headers) == 0 {
		return nil
	}
	header := headers[0]
	scheme := authScheme(header)
	urlStr := ""
	if resp.Request != nil && resp.Request.URL != nil {
		urlStr = resp.Request.URL.String()
	}
	challenge := &UpstreamAuthChallenge{
		BackendID:       t.BackendID,
		URL:             urlStr,
		Scheme:          scheme,
		WWWAuthenticate: header,
		Error_:          extractParam(header, "error"),
		RequiredScope:   extractParam(header, "scope"),
		AcrValues:       extractParam(header, "acr_values"),
	}
	return challenge
}

// authScheme returns the leading scheme word from a WWW-Authenticate header
// (e.g., "Bearer" or "DPoP"). Returns "" for an empty header.
func authScheme(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	if i := strings.IndexAny(header, " \t"); i > 0 {
		return header[:i]
	}
	return header
}

// extractParam pulls a quoted or unquoted attribute value out of a
// WWW-Authenticate header. Returns "" when the attribute is missing.
//
// Accepts both `key="value"` and bare `key=value` forms; tolerant of
// commas inside quoted values. The header grammar (RFC 9110) is too
// permissive for a one-line parser, but the auth-server emits a strict
// shape and the test cases lock that down.
func extractParam(header, name string) string {
	header = strings.TrimSpace(header)
	if header == "" || name == "" {
		return ""
	}
	target := name + "="
	// Search for `target` preceded by whitespace, comma, or start-of-string.
	for i := 0; i < len(header); {
		idx := strings.Index(header[i:], target)
		if idx < 0 {
			return ""
		}
		start := i + idx
		// Reject matches inside other identifiers (e.g. "myscope=" matching "scope=").
		if start > 0 {
			prev := header[start-1]
			if prev != ' ' && prev != ',' && prev != '\t' {
				i = start + len(target)
				continue
			}
		}
		j := start + len(target)
		if j < len(header) && header[j] == '"' {
			j++
			vs := j
			for j < len(header) && header[j] != '"' {
				j++
			}
			return header[vs:j]
		}
		vs := j
		for j < len(header) && header[j] != ',' && header[j] != ' ' && header[j] != '\t' {
			j++
		}
		return header[vs:j]
	}
	return ""
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
		requiredScope := extractParam(h, "scope")
		logger.Warn("upstream returned 403 insufficient_scope — step-up re-authorization may be needed",
			"backend", t.BackendID,
			"required_scope", requiredScope,
			"url", resp.Request.URL.String(),
		)
		return
	}
}
