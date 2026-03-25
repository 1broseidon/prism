package auth

import (
	"testing"
)

func TestPolicyCanAccessTool(t *testing.T) {
	tests := []struct {
		name      string
		scopes    string
		namespace string
		tool      string
		want      bool
	}{
		{"exact match", "github:create_issue", "github", "create_issue", true},
		{"namespace wildcard", "github:*", "github", "create_issue", true},
		{"superuser", "*", "anything", "anything", true},
		{"wrong namespace", "github:create_issue", "fs", "create_issue", false},
		{"wrong tool", "github:create_issue", "github", "delete_repo", false},
		{"empty scopes", "", "github", "create_issue", false},
		{"multiple scopes", "github:create_issue fs:read_file", "fs", "read_file", true},
		{"multiple scopes miss", "github:create_issue fs:read_file", "fs", "write_file", false},
		{"namespace wildcard only for that ns", "github:*", "fs", "read_file", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPolicy(tt.scopes)
			got := p.CanAccessTool(tt.namespace, tt.tool)
			if got != tt.want {
				t.Errorf("CanAccessTool(%q, %q) = %v, want %v (scopes: %q)",
					tt.namespace, tt.tool, got, tt.want, tt.scopes)
			}
		})
	}
}

func TestPolicyCanListTools(t *testing.T) {
	if NewPolicy("").CanListTools() {
		t.Error("empty policy should not allow listing")
	}
	if !NewPolicy("github:*").CanListTools() {
		t.Error("any scope should allow listing")
	}
}

func TestNilPolicy(t *testing.T) {
	var p *Policy
	if p.CanAccessTool("a", "b") {
		t.Error("nil policy should deny access")
	}
	if p.CanListTools() {
		t.Error("nil policy should deny listing")
	}
}
