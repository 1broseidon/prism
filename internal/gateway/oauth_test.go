//go:build mcp_go_client_oauth

package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type sequenceTokenSource struct {
	tokens []*oauth2.Token
	calls  int
}

func (s *sequenceTokenSource) Token() (*oauth2.Token, error) {
	if s.calls >= len(s.tokens) {
		return s.tokens[len(s.tokens)-1], nil
	}
	token := s.tokens[s.calls]
	s.calls++
	return token, nil
}

func TestDiscoverAuthServerMetaRequiresExactIssuer(t *testing.T) {
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 issuer + "/alias",
			"authorization_endpoint": issuer + "/authorize",
			"token_endpoint":         issuer + "/token",
		})
	}))
	defer srv.Close()
	issuer = srv.URL

	_, _, err := discoverAuthServerMeta(context.Background(), issuer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected issuer mismatch error")
	}
	if !strings.Contains(err.Error(), "issuer mismatch") {
		t.Fatalf("expected issuer mismatch error, got %v", err)
	}
}

func TestDiscoverAuthServerMetaAcceptsExactIssuer(t *testing.T) {
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 issuer,
			"authorization_endpoint": issuer + "/authorize",
			"token_endpoint":         issuer + "/token",
		})
	}))
	defer srv.Close()
	issuer = srv.URL

	asm, discovered, err := discoverAuthServerMeta(context.Background(), issuer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("discover metadata: %v", err)
	}
	if asm == nil {
		t.Fatal("expected metadata")
	}
	if asm.Issuer != issuer {
		t.Fatalf("issuer = %q, want %q", asm.Issuer, issuer)
	}
	if discovered == "" {
		t.Fatal("expected discovered URL")
	}
}

func TestGetProtectedResourceMetadataAcceptsTrailingSlashCanonicalResource(t *testing.T) {
	var resource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              resource,
			"authorization_servers": []string{"https://auth.example.com"},
		})
	}))
	defer srv.Close()
	resource = srv.URL + "/mcp/"

	prm, err := getProtectedResourceMetadataForBackend(context.Background(), srv.URL+"/.well-known/oauth-protected-resource/mcp", srv.URL+"/mcp")
	if err != nil {
		t.Fatalf("get protected resource metadata: %v", err)
	}
	if prm.Resource != resource {
		t.Fatalf("resource = %q, want %q", prm.Resource, resource)
	}
}

func TestPersistingTokenSourcePersistsRotatedToken(t *testing.T) {
	initial := &oauth2.Token{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}
	rotated := &oauth2.Token{
		AccessToken:  "access-2",
		RefreshToken: "refresh-2",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(2 * time.Hour),
	}

	var persisted []*oauth2.Token
	src := &persistingTokenSource{
		backendID: "Linear",
		cfg:       &oauth2.Config{ClientID: "client"},
		src:       &sequenceTokenSource{tokens: []*oauth2.Token{initial, rotated, rotated}},
		last:      cloneOAuthToken(initial),
		persist: func(_ string, _ *oauth2.Config, token *oauth2.Token) {
			persisted = append(persisted, cloneOAuthToken(token))
		},
	}

	if _, err := src.Token(); err != nil {
		t.Fatalf("first token: %v", err)
	}
	if len(persisted) != 0 {
		t.Fatalf("persisted unchanged initial token: %+v", persisted)
	}
	if _, err := src.Token(); err != nil {
		t.Fatalf("rotated token: %v", err)
	}
	if len(persisted) != 1 || persisted[0].RefreshToken != "refresh-2" {
		t.Fatalf("persisted rotated tokens = %+v", persisted)
	}
	if _, err := src.Token(); err != nil {
		t.Fatalf("cached rotated token: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("persisted token repeatedly: %+v", persisted)
	}
}
