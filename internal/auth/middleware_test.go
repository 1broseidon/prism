package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{"valid", "Bearer abc123", "abc123", false},
		{"missing", "", "", true},
		{"wrong scheme", "Basic abc123", "", true},
		{"empty token", "Bearer ", "", true},
		{"no space", "Bearerabc123", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			got, err := ExtractBearerToken(r)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractBearerToken() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ExtractBearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClaimsFromContext(t *testing.T) {
	// No claims in context
	if c := ClaimsFromContext(context.Background()); c != nil {
		t.Error("expected nil claims from empty context")
	}

	// With claims
	claims := &Claims{Subject: "agent-1", Scope: "github:*"}
	ctx := context.WithValue(context.Background(), claimsKey, claims)
	got := ClaimsFromContext(ctx)
	if got == nil || got.Subject != "agent-1" {
		t.Errorf("expected claims with subject agent-1, got %v", got)
	}
}

func TestPolicyFromContext(t *testing.T) {
	if p := PolicyFromContext(context.Background()); p != nil {
		t.Error("expected nil policy from empty context")
	}

	policy := NewPolicy("github:* fs:read_file")
	ctx := context.WithValue(context.Background(), policyKey, policy)
	got := PolicyFromContext(ctx)
	if got == nil || !got.CanAccessTool("github", "create_issue") {
		t.Error("expected policy to allow github:create_issue")
	}
}

func TestProtectedResourceMetadataURL(t *testing.T) {
	tests := []struct {
		resource string
		want     string
	}{
		{
			resource: "https://prism.example.com/mcp",
			want:     "https://prism.example.com/.well-known/oauth-protected-resource/mcp",
		},
		{
			resource: "https://prism.example.com/",
			want:     "https://prism.example.com/.well-known/oauth-protected-resource",
		},
	}

	for _, tt := range tests {
		if got := protectedResourceMetadataURL(tt.resource); got != tt.want {
			t.Fatalf("metadata URL for %q = %q, want %q", tt.resource, got, tt.want)
		}
	}
}

func TestRequireScope(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// No policy in context → 403
	wrapped := RequireScope("admin:*")(handler)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}

	// With matching scope
	policy := NewPolicy("admin:* github:*")
	ctx := context.WithValue(context.Background(), policyKey, policy)
	rec = httptest.NewRecorder()
	req = httptest.NewRequestWithContext(ctx, "GET", "/", nil)
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// With insufficient scope
	policy = NewPolicy("github:*")
	ctx = context.WithValue(context.Background(), policyKey, policy)
	rec = httptest.NewRecorder()
	req = httptest.NewRequestWithContext(ctx, "GET", "/", nil)
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header on insufficient scope")
	}
}
