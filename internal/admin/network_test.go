package admin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/adminauth"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/store"
)

type mockNetworkBackend struct {
	settings  *NetworkSettings
	persisted *NetworkSettings
}

func (m *mockNetworkBackend) AddBackend(context.Context, string, BackendConfig) error {
	return nil
}

func (m *mockNetworkBackend) RemoveBackend(string) error { return nil }

func (m *mockNetworkBackend) NotifyToolsChanged() {}

func (m *mockNetworkBackend) NetworkSettings() *NetworkSettings {
	if m.settings == nil {
		return &NetworkSettings{}
	}
	return m.settings
}

func (m *mockNetworkBackend) SetNetworkSettings(s *NetworkSettings) {
	next := *s
	m.settings = &next
}

func (m *mockNetworkBackend) PersistNetworkSettings(s *NetworkSettings) error {
	next := *s
	m.persisted = &next
	return nil
}

func TestPutNetworkSyncsAdminAuthRedirectURL(t *testing.T) {
	kv := store.NewMemoryStore()
	auth := adminauth.NewHolder(kv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state := &adminauth.State{
		Config: &config.AdminAuthConfig{
			Issuer:       "https://issuer.example",
			ClientID:     "client",
			ClientSecret: "secret",
			RedirectURL:  "http://172.16.30.90:9086/auth/callback",
			Rules:        []config.AdminAuthRule{{Role: "admin", Emails: []string{"admin@example.com"}}},
		},
	}
	if err := adminauth.SaveState(kv, state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	mgr := &mockNetworkBackend{}
	api := NewAPI(
		func() any { return nil },
		mgr,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		auth,
	)
	body := `{"admin_public_url":"https://mcp.dfam.one","public_url":"https://mcp.dfam.one:8443","trust_proxy_headers":true}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config/network", strings.NewReader(body))
	w := httptest.NewRecorder()

	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got, err := adminauth.LoadState(kv)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if got.Config.RedirectURL != "https://mcp.dfam.one/auth/callback" {
		t.Fatalf("redirect url = %q", got.Config.RedirectURL)
	}
	if mgr.settings == nil || mgr.persisted == nil {
		t.Fatal("network settings were not applied and persisted")
	}
	if !mgr.settings.TrustProxyHeaders {
		t.Fatal("trust_proxy_headers was not applied")
	}
}

func TestPutNetworkRollsBackWhenAdminAuthReloadFails(t *testing.T) {
	issuer := httptest.NewServer(http.NotFoundHandler())
	defer issuer.Close()

	kv := store.NewMemoryStore()
	auth := adminauth.NewHolder(kv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	oldRedirect := "https://old.example/auth/callback"
	state := &adminauth.State{
		Enabled: true,
		Config: &config.AdminAuthConfig{
			Issuer:       issuer.URL,
			ClientID:     "client",
			ClientSecret: "secret",
			RedirectURL:  oldRedirect,
			Rules:        []config.AdminAuthRule{{Role: "admin", Emails: []string{"admin@example.com"}}},
		},
	}
	if err := adminauth.SaveState(kv, state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	oldNetwork := &NetworkSettings{
		AdminPublicURL:    "https://old.example",
		PublicURL:         "https://old.example:8443",
		TrustProxyHeaders: true,
	}
	mgr := &mockNetworkBackend{settings: oldNetwork, persisted: oldNetwork}
	api := NewAPI(
		func() any { return nil },
		mgr,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		auth,
	)
	body := `{"admin_public_url":"https://new.example","public_url":"https://new.example:8443","trust_proxy_headers":false}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config/network", strings.NewReader(body))
	w := httptest.NewRecorder()

	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.settings.AdminPublicURL != oldNetwork.AdminPublicURL {
		t.Fatalf("runtime admin_public_url = %q, want %q", mgr.settings.AdminPublicURL, oldNetwork.AdminPublicURL)
	}
	if mgr.persisted.AdminPublicURL != oldNetwork.AdminPublicURL {
		t.Fatalf("persisted admin_public_url = %q, want %q", mgr.persisted.AdminPublicURL, oldNetwork.AdminPublicURL)
	}
	got, err := adminauth.LoadState(kv)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if got.Config.RedirectURL != oldRedirect {
		t.Fatalf("admin auth redirect = %q, want %q", got.Config.RedirectURL, oldRedirect)
	}
}
