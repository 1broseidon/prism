//go:build mcp_go_client_oauth

package gateway

import "net/http"

// SetupOAuth initializes OAuth flows using adminPublicURL as the
// externally-reachable base URL for the admin API
// (e.g., "http://172.16.30.90:9086") to construct the callback URL.
// It returns the callback HTTP handler for mounting on the admin mux.
func (g *Gateway) SetupOAuth(adminPublicURL string) http.Handler {
	g.InitAuthFlows(adminPublicURL)
	g.LoadPersistedOAuthCredentials()
	return g.OAuthCallbackHandler()
}
