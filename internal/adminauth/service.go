package adminauth

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/store"
)

// loginAttemptMaxAge bounds how long a login flow can be in-flight between
// /auth/login and /auth/callback. Stricter than session TTL by design.
const loginAttemptMaxAge = 10 * time.Minute

// Service is the admin OIDC subsystem. A nil *Service is valid and represents
// "auth disabled" — the middleware accepts every request.
type Service struct {
	cfg      *config.AdminAuthConfig
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
	store    *Store
	logger   *slog.Logger
}

// NewService returns the auth service when cfg is non-nil and provider
// discovery succeeds. When cfg is nil, returns (nil, nil) — meaning auth is
// disabled and every middleware call should pass through.
func NewService(ctx context.Context, cfg *config.AdminAuthConfig, kv store.Store, logger *slog.Logger) (*Service, error) {
	if cfg == nil {
		return nil, nil
	}
	if kv == nil {
		return nil, fmt.Errorf("adminauth: kv store is required when admin_auth is configured")
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", cfg.Issuer, err)
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.RedirectURL,
		Scopes:       cfg.Scopes,
	}
	return &Service{
		cfg:      cfg,
		provider: provider,
		verifier: verifier,
		oauth:    oauthCfg,
		store:    NewStore(kv),
		logger:   logger.With("component", "adminauth"),
	}, nil
}

// SessionTTL is the configured session lifetime.
func (s *Service) SessionTTL() time.Duration {
	return time.Duration(s.cfg.SessionTTL)
}

// CookieSecure returns true when the session cookie should be marked Secure.
func (s *Service) CookieSecure() bool {
	return s.cfg.CookieSecure
}

// CookieDomain returns the configured cookie Domain attribute (may be empty).
func (s *Service) CookieDomain() string {
	return s.cfg.CookieDomain
}

// Issuer returns the configured OIDC issuer URL (for display).
func (s *Service) Issuer() string {
	return s.cfg.Issuer
}
