package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGrantsDSLParseAndMatch(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		pass any
		fail any
	}{
		{"equals", `{"equals":"prod"}`, "prod", "dev"},
		{"prefix", `{"prefix":"/workspace/a/"}`, "/workspace/a/file", "/tmp/file"},
		{"oneOf", `{"oneOf":["read","write"]}`, "write", "delete"},
		{"pattern", `{"pattern":"^feat-[0-9]+$"}`, "feat-123", "bug-123"},
		{"size string", `{"size_max":3}`, "abc", "abcd"},
		{"size array", `{"size_max":2}`, []any{"a", "b"}, []any{"a", "b", "c"}},
		{"range min", `{"range":{"min":2}}`, float64(2), float64(1)},
		{"range max", `{"range":{"max":4}}`, float64(4), float64(5)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParsePredicate(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if ok, detail := Match(tt.pass, p); !ok || detail != "" {
				t.Fatalf("pass Match = %v %q", ok, detail)
			}
			if ok, detail := Match(tt.fail, p); ok || detail == "" {
				t.Fatalf("fail Match = %v %q", ok, detail)
			}
		})
	}
}

func TestGrantsDSLRejectsReservedAndNestedRegex(t *testing.T) {
	if _, err := ParsePredicate(json.RawMessage(`{"not":{"equals":"x"}}`)); err == nil {
		t.Fatal("expected reserved key rejection")
	}
	if _, err := ParsePredicate(json.RawMessage(`{"pattern":"(a+)+$"}`)); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("expected nested quantifier rejection, got %v", err)
	}
	if _, err := ParsePredicate(json.RawMessage(`{"pattern":"` + strings.Repeat("a", 257) + `"}`)); err == nil {
		t.Fatal("expected long pattern rejection")
	}
}

func TestGrantsDSLSizeMaxRawBytes(t *testing.T) {
	p, err := ParsePredicate(json.RawMessage(`{"size_max":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := Match([]byte{1, 2, 3}, p); !ok {
		t.Fatal("expected 3 raw bytes to pass")
	}
	if ok, _ := Match([]byte{1, 2, 3, 4}, p); ok {
		t.Fatal("expected 4 raw bytes to fail")
	}
}
