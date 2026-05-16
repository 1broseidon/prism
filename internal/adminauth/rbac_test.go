package adminauth

import (
	"testing"

	"github.com/1broseidon/prism/internal/config"
)

func TestResolveRole(t *testing.T) {
	rules := []config.AdminAuthRule{
		{Role: "admin", Emails: []string{"alice@example.com"}, Groups: []string{"prism-admins"}},
		{Role: "viewer", Domains: []string{"example.com"}, Groups: []string{"prism-viewers"}},
	}

	tests := []struct {
		name   string
		email  string
		groups []string
		want   Role
	}{
		{"exact email match → admin", "alice@example.com", nil, RoleAdmin},
		{"email match is case-insensitive", "ALICE@Example.COM", nil, RoleAdmin},
		{"admin group via second user", "carol@elsewhere.org", []string{"prism-admins"}, RoleAdmin},
		{"viewer by domain", "bob@example.com", nil, RoleViewer},
		{"viewer domain is case-insensitive", "bob@EXAMPLE.com", nil, RoleViewer},
		{"viewer by group", "dave@elsewhere.org", []string{"prism-viewers"}, RoleViewer},
		{"groups are case-sensitive — wrong case is no match", "eve@elsewhere.org", []string{"PRISM-VIEWERS"}, ""},
		{"no match → empty role", "outsider@nowhere.org", nil, ""},
		{"first matching rule wins (admin before viewer)", "alice@example.com", []string{"prism-viewers"}, RoleAdmin},
		{"empty email is rejected by email/domain matchers", "", []string{"prism-viewers"}, RoleViewer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveRole(tt.email, tt.groups, rules)
			if got != tt.want {
				t.Errorf("resolveRole(%q, %v) = %q, want %q", tt.email, tt.groups, got, tt.want)
			}
		})
	}
}

func TestResolveRoleNoRules(t *testing.T) {
	if got := resolveRole("alice@example.com", []string{"admins"}, nil); got != "" {
		t.Errorf("with no rules, every user should be rejected; got role %q", got)
	}
}
