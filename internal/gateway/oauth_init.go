//go:build mcp_go_client_oauth

package gateway

import "net/http"

// adminPublicURL is the externally-reachable base URL for the admin API
// (e.g., "http://172.16.30.90:9086") used to construct the callback URL.
// Returns the callback HTTP handler for mounting on the admin mux.
func (g *Gateway) SetupOAuth(adminPublicURL string) http.Handler {
	g.InitAuthFlows(adminPublicURL)
	g.LoadPersistedOAuthCredentials()
	return g.OAuthCallbackHandler()
}
