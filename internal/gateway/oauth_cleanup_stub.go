//go:build !mcp_go_client_oauth

package gateway

// cleanupOAuthForBackend is a no-op when OAuth client support is not compiled in.
func (g *Gateway) cleanupOAuthForBackend(_ string) {}
