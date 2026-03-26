package credentials

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/oauth2"
)

// OAuth resolves an access token from an oauth2.TokenSource on every call.
// The underlying TokenSource should be wrapped with oauth2.ReuseTokenSource
// so that tokens are automatically refreshed when they expire.
type OAuth struct {
	mu          sync.Mutex
	tokenSource oauth2.TokenSource
	header      string
}

// NewOAuth creates an OAuth credential that resolves Bearer tokens from the given source.
// header overrides the HTTP header name; empty defaults to "Authorization".
func NewOAuth(ts oauth2.TokenSource, header string) *OAuth {
	return &OAuth{
		tokenSource: ts,
		header:      header,
	}
}

// Resolve returns the Authorization header with a current Bearer token.
// The token is refreshed automatically by the underlying oauth2.TokenSource.
func (o *OAuth) Resolve(_ context.Context) (header, value string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	token, err := o.tokenSource.Token()
	if err != nil {
		return "", "", fmt.Errorf("oauth token refresh: %w", err)
	}

	h := o.header
	if h == "" {
		h = "Authorization"
	}
	return h, token.Type() + " " + token.AccessToken, nil
}
