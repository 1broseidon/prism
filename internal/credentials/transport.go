package credentials

import "net/http"

// InjectingTransport is an http.RoundTripper that resolves the credential for
// a backend and sets the appropriate HTTP header on every outbound request.
//
// The raw credential value is never logged or surfaced to the calling agent.
type InjectingTransport struct {
	Base      http.RoundTripper
	Store     *Store
	BackendID string
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
	return base.RoundTrip(req)
}
