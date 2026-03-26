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

		// Glob pattern matching
		{"glob prefix match", "fs:read*", "fs", "read_file", true},
		{"glob prefix match longer", "fs:read*", "fs", "read_text_file", true},
		{"glob prefix no match", "fs:read*", "fs", "write_file", false},
		{"glob with underscore wildcard", "github:list_*", "github", "list_repos", true},
		{"glob namespace star", "*:read_file", "fs", "read_file", true},
		{"glob namespace star other ns", "*:read_file", "s3", "read_file", true},
		{"glob namespace star no match", "*:read_file", "fs", "write_file", false},
		{"glob suffix match", "fs:*_file", "fs", "read_file", true},
		{"glob suffix match write", "fs:*_file", "fs", "write_file", true},
		{"glob suffix no match", "fs:*_file", "fs", "read_dir", false},
		{"glob question mark", "fs:read_fil?", "fs", "read_file", true},
		{"glob question mark no match", "fs:read_fil?", "fs", "read_files", false},
		{"no glob is exact only", "fs:read_file", "fs", "read_files", false},
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
