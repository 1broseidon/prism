package adminauth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// ProbeIssuer runs OIDC discovery against the given issuer URL and returns
// the discovered authorization and token endpoints. Used by the admin
// "test connection" flow to validate config before committing it.
func ProbeIssuer(ctx context.Context, issuer string) (authURL, tokenURL string, err error) {
	if issuer == "" {
		return "", "", fmt.Errorf("issuer is required")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return "", "", err
	}
	ep := provider.Endpoint()
	return ep.AuthURL, ep.TokenURL, nil
}
