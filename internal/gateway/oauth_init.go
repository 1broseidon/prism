//go:build mcp_go_client_oauth

package gateway

import "net/http"

// SetupOAuth initializes OAuth client support on the gateway.
// adminAddr is the admin listen address (e.g., ":9086") used to construct
// the callback URL. Returns the callback HTTP handler for mounting on the
// admin mux.
func (g *Gateway) SetupOAuth(adminAddr string) http.Handler {
	g.InitAuthFlows(adminAddr)
	g.LoadPersistedOAuthCredentials()
	return g.OAuthCallbackHandler()
}
