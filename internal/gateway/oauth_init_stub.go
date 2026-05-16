//go:build !mcp_go_client_oauth

package gateway

import "net/http"

// SetupOAuth is a no-op when OAuth client support is not compiled in.
// Build with -tags mcp_go_client_oauth to enable.
// adminPublicURL is unused in the stub.
func (g *Gateway) SetupOAuth(_ string) http.Handler {
	return nil
}
