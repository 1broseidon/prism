package authserver

import (
	"log/slog"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/store"
)

func mustTestKeyManager(t *testing.T) *KeyManager {
	t.Helper()
	km, err := NewKeyManager("")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	return km
}

func TestGrantTemplateVersioningAndHashLookup(t *testing.T) {
	s := NewServer(&Config{Issuer: "https://auth.test", TokenTTLSeconds: 3600}, mustTestKeyManager(t), store.NewMemoryStore(), slog.Default())

	tmpl, err := s.SaveGrantTemplate(auth.GrantTemplate{
		ID: "tmpl-fs",
		Spec: auth.GrantSpec{
			Type:    auth.GrantTypeMCPCall,
			Tool:    "fs.write_file",
			Backend: "local",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Version != 1 || tmpl.Hash == "" {
		t.Fatalf("unexpected template: %+v", tmpl)
	}
	byHash, err := s.GetGrantTemplateByHash(tmpl.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if byHash.ID != tmpl.ID || byHash.Version != tmpl.Version {
		t.Fatalf("hash lookup = %+v, want %+v", byHash, tmpl)
	}

	tmpl.Spec.CnfRequired = true
	next, err := s.SaveGrantTemplate(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if next.Version != 2 || next.Supersedes != tmpl.Hash {
		t.Fatalf("unexpected next version: %+v", next)
	}
}

func TestGrantBindingSubjectLookup(t *testing.T) {
	s := NewServer(&Config{Issuer: "https://auth.test", TokenTTLSeconds: 3600}, mustTestKeyManager(t), store.NewMemoryStore(), slog.Default())
	tmpl, err := s.SaveGrantTemplate(auth.GrantTemplate{
		ID:   "tmpl-fs",
		Spec: auth.GrantSpec{Type: auth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SetGrantBinding(auth.GrantBinding{
		ID:           "bind-eng",
		TemplateHash: tmpl.Hash,
		Subjects:     auth.SubjectSelector{Groups: []string{"eng"}, RoleRequired: "senior"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.TemplateID != tmpl.ID {
		t.Fatalf("binding template id = %q, want %q", b.TemplateID, tmpl.ID)
	}
	matches := s.ListGrantBindingsForSubject("agent-a", []string{"eng"}, []string{"senior"})
	if len(matches) != 1 || matches[0].ID != b.ID {
		t.Fatalf("matches = %+v", matches)
	}
	matches = s.ListGrantBindingsForSubject("agent-a", []string{"eng"}, []string{"junior"})
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %+v", matches)
	}
	if err := s.DeleteGrantTemplate(tmpl.ID, tmpl.Version); err == nil {
		t.Fatal("expected delete to fail while binding references template")
	}
}

func TestGrantTemplateDeleteBlockedByActiveTokenRef(t *testing.T) {
	s := NewServer(&Config{Issuer: "https://auth.test", TokenTTLSeconds: 3600}, mustTestKeyManager(t), store.NewMemoryStore(), slog.Default())
	tmpl, err := s.SaveGrantTemplate(auth.GrantTemplate{
		ID:   "tmpl-fs",
		Spec: auth.GrantSpec{Type: auth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.mintTokenWithOptions(tokenIssueOptions{
		ClientID: "ci-agent",
		Scopes:   []string{"mcp:connect"},
		Grants: []auth.IssuedGrant{{
			Type:         auth.GrantTypeMCPCall,
			TemplateID:   tmpl.ID,
			TemplateHash: tmpl.Hash,
			Tool:         tmpl.Spec.Tool,
			Backend:      tmpl.Spec.Backend,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveGrantTokenCount(tmpl.Hash); got != 1 {
		t.Fatalf("active token refs = %d, want 1", got)
	}
	if err := s.DeleteGrantTemplate(tmpl.ID, tmpl.Version); err == nil {
		t.Fatal("expected delete to fail while active token references template")
	}

	s.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour).UTC() })
	if got := s.ActiveGrantTokenCount(tmpl.Hash); got != 0 {
		t.Fatalf("expired active token refs = %d, want 0", got)
	}
	if err := s.DeleteGrantTemplate(tmpl.ID, tmpl.Version); err != nil {
		t.Fatalf("delete after token expiry: %v", err)
	}
}
