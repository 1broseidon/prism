//go:build mcp_go_client_oauth

package gateway

// cleanupOAuthForBackend removes persisted OAuth tokens when a backend disconnects.
func (g *Gateway) cleanupOAuthForBackend(id string) {
	g.deletePersistedOAuthTokens(id)
}
