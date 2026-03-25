package gateway

import (
	"testing"
)

func TestParseNamespacedTool(t *testing.T) {
	tests := []struct {
		input    string
		wantNS   string
		wantTool string
		wantOK   bool
	}{
		{"github__create_issue", "github", "create_issue", true},
		{"fs__read_file", "fs", "read_file", true},
		{"ns__deeply__nested", "ns", "deeply__nested", true},
		{"notool", "", "", false},
		{"__leading", "", "", false},
		{"trailing__", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		ns, tool, ok := parseNamespacedTool(tt.input)
		if ok != tt.wantOK || ns != tt.wantNS || tool != tt.wantTool {
			t.Errorf("parseNamespacedTool(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, ns, tool, ok, tt.wantNS, tt.wantTool, tt.wantOK)
		}
	}
}
