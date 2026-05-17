package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/gateway"
	"github.com/1broseidon/prism/internal/store"
)

func TestMCPResourceURL(t *testing.T) {
	if got := mcpResourceURL("https://mcp.dfam.one/"); got != "https://mcp.dfam.one/mcp" {
		t.Fatalf("resource url = %q", got)
	}
	if got := mcpResourceURL(""); got != "" {
		t.Fatalf("empty resource url = %q", got)
	}
}

func TestBuildMuxProtectedResourceMetadataIncludesMCPResource(t *testing.T) {
	cfg := &config.Loaded{
		EmbeddedAuth: &config.EmbeddedAuthConfig{
			Issuer: "https://mcp.dfam.one",
		},
	}
	mux := buildMux(
		cfg,
		http.NotFoundHandler(),
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"https://mcp.dfam.one/mcp",
		nil,
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var got struct {
		Resource string `json:"resource"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if got.Resource != "https://mcp.dfam.one/mcp" {
		t.Fatalf("resource = %q", got.Resource)
	}
}

func TestParseBridgeURLList(t *testing.T) {
	got := parseBridgeURLList(" http://a:3001/,http://b:3001 http://a:3001 ")
	want := []string{"http://a:3001", "http://b:3001"}
	if len(got) != len(want) {
		t.Fatalf("urls = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("urls = %v", got)
		}
	}
}

func TestSyncAdminAuthRedirectFromNetwork(t *testing.T) {
	kv := store.NewMemoryStore()
	if err := gateway.SaveNetworkSettings(kv, &admin.NetworkSettings{
		AdminPublicURL: "https://mcp.dfam.one/",
	}); err != nil {
		t.Fatalf("save network settings: %v", err)
	}
	cfg := &config.AdminAuthConfig{
		RedirectURL: "http://172.16.30.90:9086/auth/callback",
	}

	changed, err := syncAdminAuthRedirectFromNetwork(kv, cfg)
	if err != nil {
		t.Fatalf("sync redirect: %v", err)
	}
	if !changed {
		t.Fatal("expected redirect to change")
	}
	if cfg.RedirectURL != "https://mcp.dfam.one/auth/callback" {
		t.Fatalf("redirect url = %q", cfg.RedirectURL)
	}
}

func TestEnsureSigningKeyUsesEnvOverride(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "keys", "signing-key.pem")
	t.Setenv("PRISM_SIGNING_KEY_FILE", keyPath)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	got := ensureSigningKey(logger)
	if got != keyPath {
		t.Fatalf("key path = %q want %q", got, keyPath)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected signing key at override path: %v", err)
	}
}

func TestOpenStoreUsesDataDirEnv(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("PRISM_DATA_DIR", dataDir)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	kv := openStore(&config.Loaded{}, logger)
	defer func() { _ = kv.Close() }()

	if err := kv.Set("probe", []byte("ok")); err != nil {
		t.Fatalf("set probe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "prism.db")); err != nil {
		t.Fatalf("expected prism.db under PRISM_DATA_DIR: %v", err)
	}
}
