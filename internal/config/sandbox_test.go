package config

import "testing"

func TestNormalizeSandboxDefaultsAndCompat(t *testing.T) {
	def := NormalizeSandboxConfig(nil, SandboxProfileDefault)
	if def.Profile != SandboxProfileDefault || def.RunsAsRoot() || !def.ReadOnlyRoot() {
		t.Fatalf("default sandbox = %+v", def)
	}
	if def.UID != DefaultSandboxUID || def.GID != DefaultSandboxGID || def.Memory != DefaultSandboxMemory {
		t.Fatalf("default sandbox limits = %+v", def)
	}

	compat := NormalizeSandboxConfig(nil, SandboxProfileCompat)
	if compat.Profile != SandboxProfileCompat || !compat.RunsAsRoot() || compat.ReadOnlyRoot() {
		t.Fatalf("compat sandbox = %+v", compat)
	}
	if compat.Memory != "" || compat.CPUs != 0 || compat.PidsLimit != 0 {
		t.Fatalf("compat sandbox should not add limits: %+v", compat)
	}
}

func TestValidateSandboxRejectsDangerousMounts(t *testing.T) {
	readonly := false
	bad := &SandboxConfig{Mounts: []SandboxMount{{
		Source:   "/var/run/docker.sock",
		Target:   "/workspace/docker.sock",
		ReadOnly: &readonly,
	}}}
	if err := ValidateSandboxConfig(bad); err == nil {
		t.Fatal("expected docker socket mount to be rejected")
	}

	bad = &SandboxConfig{Mounts: []SandboxMount{{
		Source: "/tmp/data",
		Target: "/proc/x",
	}}}
	if err := ValidateSandboxConfig(bad); err == nil {
		t.Fatal("expected /proc target to be rejected")
	}
}

func TestParseMemoryBytes(t *testing.T) {
	got, err := ParseMemoryBytes("1.5g")
	if err != nil {
		t.Fatalf("parse memory: %v", err)
	}
	want := int64(1.5 * 1024 * 1024 * 1024)
	if got != want {
		t.Fatalf("memory bytes = %d want %d", got, want)
	}
}

func TestWorkspaceConfigDefaultsAndValidation(t *testing.T) {
	cfg := NormalizeWorkspaceConfig(&WorkspaceConfig{ID: "repo"})
	if cfg == nil || cfg.Type != WorkspaceTypeProxied || cfg.Mode != WorkspaceModeSnapshot || cfg.WriteMode != WorkspaceWriteStage || cfg.MaxBytes != DefaultWorkspaceMaxBytes || cfg.QuotaBytes != 0 || cfg.RetentionSeconds != 0 {
		t.Fatalf("workspace defaults = %+v", cfg)
	}

	invalid := []WorkspaceConfig{
		{ID: "../repo"},
		{ID: "repo", Type: "remote-ish"},
		{ID: "repo", Mode: "live"},
		{ID: "repo", WriteMode: "unsafe"},
		{ID: "repo", MaxBytes: 513 << 20},
		{ID: "repo", QuotaBytes: -1},
		{ID: "repo", RetentionSeconds: -1},
	}
	for _, tc := range invalid {
		if err := ValidateWorkspaceConfig(&tc); err == nil {
			t.Fatalf("expected invalid workspace config: %+v", tc)
		}
	}
}
