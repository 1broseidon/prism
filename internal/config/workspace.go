package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// WorkspaceTypeProxied syncs through a local workspace bridge.
	WorkspaceTypeProxied = "proxied"
	// WorkspaceTypeVirtual stores durable workspace state on the Prism server.
	WorkspaceTypeVirtual = "virtual"
	// WorkspaceTypeEphemeral stores temporary workspace state on the Prism server.
	WorkspaceTypeEphemeral = "ephemeral"

	// WorkspaceModeSnapshot copies a local workspace into the sandbox.
	WorkspaceModeSnapshot = "snapshot"

	// WorkspaceWriteSandboxOnly never applies sandbox changes locally.
	WorkspaceWriteSandboxOnly = "sandbox_only"
	// WorkspaceWriteStage stages sandbox changes for approval.
	WorkspaceWriteStage = "stage"
	// WorkspaceWriteAutoApply applies allowed sandbox changes after tool calls.
	WorkspaceWriteAutoApply = "auto_apply"

	// DefaultWorkspaceMaxBytes is the default maximum snapshot size.
	DefaultWorkspaceMaxBytes int64 = 32 << 20
)

var workspaceConfigIDRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// WorkspaceConfig connects a sandboxed stdio backend to a local workspace
// agent. The sandbox receives a snapshot copy; local mutation only happens
// through staged or auto-applied changes.
type WorkspaceConfig struct {
	ID        string   `json:"id,omitempty"`
	Type      string   `json:"type,omitempty"`
	Mode      string   `json:"mode,omitempty"`
	WriteMode string   `json:"write_mode,omitempty"`
	Include   []string `json:"include,omitempty"`
	Exclude   []string `json:"exclude,omitempty"`
	MaxBytes  int64    `json:"max_bytes,omitempty"`
}

// NormalizeWorkspaceConfig fills defaults and returns nil when no workspace is configured.
func NormalizeWorkspaceConfig(input *WorkspaceConfig) *WorkspaceConfig {
	if input == nil || strings.TrimSpace(input.ID) == "" {
		return nil
	}
	out := &WorkspaceConfig{
		ID:        strings.TrimSpace(input.ID),
		Type:      strings.TrimSpace(input.Type),
		Mode:      strings.TrimSpace(input.Mode),
		WriteMode: strings.TrimSpace(input.WriteMode),
		Include:   cleanStringSlice(input.Include),
		Exclude:   cleanStringSlice(input.Exclude),
		MaxBytes:  input.MaxBytes,
	}
	if out.Type == "" {
		out.Type = WorkspaceTypeProxied
	}
	if out.Mode == "" {
		out.Mode = WorkspaceModeSnapshot
	}
	if out.WriteMode == "" {
		out.WriteMode = WorkspaceWriteStage
	}
	if out.MaxBytes <= 0 {
		out.MaxBytes = DefaultWorkspaceMaxBytes
	}
	return out
}

// ValidateWorkspaceConfig rejects malformed workspace sandbox settings.
func ValidateWorkspaceConfig(input *WorkspaceConfig) error {
	if input == nil || strings.TrimSpace(input.ID) == "" {
		return nil
	}
	cfg := NormalizeWorkspaceConfig(input)
	if !workspaceConfigIDRE.MatchString(cfg.ID) {
		return errors.New("workspace.id must be 1-64 chars of [A-Za-z0-9_.-]")
	}
	switch cfg.Type {
	case WorkspaceTypeProxied, WorkspaceTypeVirtual, WorkspaceTypeEphemeral:
	default:
		return fmt.Errorf("workspace.type must be %q, %q, or %q", WorkspaceTypeProxied, WorkspaceTypeVirtual, WorkspaceTypeEphemeral)
	}
	if cfg.Mode != WorkspaceModeSnapshot {
		return fmt.Errorf("workspace.mode must be %q", WorkspaceModeSnapshot)
	}
	switch cfg.WriteMode {
	case WorkspaceWriteSandboxOnly, WorkspaceWriteStage, WorkspaceWriteAutoApply:
	default:
		return fmt.Errorf("workspace.write_mode must be %q, %q, or %q", WorkspaceWriteSandboxOnly, WorkspaceWriteStage, WorkspaceWriteAutoApply)
	}
	if cfg.MaxBytes <= 0 {
		return errors.New("workspace.max_bytes must be > 0")
	}
	if cfg.MaxBytes > 512<<20 {
		return errors.New("workspace.max_bytes must be <= 512MiB")
	}
	return nil
}

func cleanStringSlice(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
