package config

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// SandboxProfileDefault applies Prism's recommended container isolation.
	SandboxProfileDefault = "default"
	// SandboxProfileCompat preserves the historical container behavior.
	SandboxProfileCompat = "compat"

	// SandboxNetworkStandard uses the bridge network required by the manager proxy.
	SandboxNetworkStandard = "standard"

	// DefaultSandboxUID is the non-root uid used for recommended sandboxes.
	DefaultSandboxUID = 65532
	// DefaultSandboxGID is the non-root gid used for recommended sandboxes.
	DefaultSandboxGID = 65532
	// DefaultSandboxMemory is the recommended memory limit for new sandboxes.
	DefaultSandboxMemory = "512m"
	// DefaultSandboxCPUs is the recommended CPU quota for new sandboxes.
	DefaultSandboxCPUs = 1
	// DefaultSandboxPidsLimit is the recommended process limit for new sandboxes.
	DefaultSandboxPidsLimit = 128
)

// SandboxConfig controls the Docker container used for stdio MCP servers.
type SandboxConfig struct {
	Profile        string         `json:"profile,omitempty"`
	NetworkProfile string         `json:"network_profile,omitempty"`
	RunAsRoot      *bool          `json:"run_as_root,omitempty"`
	UID            int            `json:"uid,omitempty"`
	GID            int            `json:"gid,omitempty"`
	ReadOnlyRootFS *bool          `json:"readonly_rootfs,omitempty"`
	Memory         string         `json:"memory,omitempty"`
	CPUs           float64        `json:"cpus,omitempty"`
	PidsLimit      int64          `json:"pids_limit,omitempty"`
	Mounts         []SandboxMount `json:"mounts,omitempty"`
}

// SandboxMount is an explicit host-path mount exposed to a sandbox.
type SandboxMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly *bool  `json:"readonly,omitempty"`
}

func boolPtr(v bool) *bool { return &v }

// DefaultSandboxConfig returns the safer default for newly-added stdio servers.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Profile:        SandboxProfileDefault,
		NetworkProfile: SandboxNetworkStandard,
		RunAsRoot:      boolPtr(false),
		UID:            DefaultSandboxUID,
		GID:            DefaultSandboxGID,
		ReadOnlyRootFS: boolPtr(true),
		Memory:         DefaultSandboxMemory,
		CPUs:           DefaultSandboxCPUs,
		PidsLimit:      DefaultSandboxPidsLimit,
	}
}

// CompatSandboxConfig preserves the historical bridge behavior for existing
// persisted backends that do not yet carry sandbox settings.
func CompatSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Profile:        SandboxProfileCompat,
		NetworkProfile: SandboxNetworkStandard,
		RunAsRoot:      boolPtr(true),
		ReadOnlyRootFS: boolPtr(false),
	}
}

// NormalizeSandboxConfig fills omitted fields using either the default or
// compatibility profile. The input is copied and can be safely mutated later.
func NormalizeSandboxConfig(input *SandboxConfig, fallbackProfile string) SandboxConfig {
	if fallbackProfile == "" {
		fallbackProfile = SandboxProfileDefault
	}
	profile := fallbackProfile
	if input != nil && strings.TrimSpace(input.Profile) != "" {
		profile = strings.TrimSpace(input.Profile)
	}

	var out SandboxConfig
	if profile == SandboxProfileCompat {
		out = CompatSandboxConfig()
	} else {
		out = DefaultSandboxConfig()
	}
	if input == nil {
		return out
	}

	if input.NetworkProfile != "" {
		out.NetworkProfile = strings.TrimSpace(input.NetworkProfile)
	}
	if input.RunAsRoot != nil {
		out.RunAsRoot = boolPtr(*input.RunAsRoot)
	}
	if input.UID != 0 {
		out.UID = input.UID
	}
	if input.GID != 0 {
		out.GID = input.GID
	}
	if input.ReadOnlyRootFS != nil {
		out.ReadOnlyRootFS = boolPtr(*input.ReadOnlyRootFS)
	}
	if strings.TrimSpace(input.Memory) != "" {
		out.Memory = strings.TrimSpace(input.Memory)
	}
	if input.CPUs != 0 {
		out.CPUs = input.CPUs
	}
	if input.PidsLimit != 0 {
		out.PidsLimit = input.PidsLimit
	}
	if input.Mounts != nil {
		out.Mounts = normalizeSandboxMounts(input.Mounts)
	}
	out.Profile = profile
	return out
}

func normalizeSandboxMounts(mounts []SandboxMount) []SandboxMount {
	out := make([]SandboxMount, 0, len(mounts))
	for _, m := range mounts {
		next := SandboxMount{
			Source: strings.TrimSpace(m.Source),
			Target: path.Clean(strings.TrimSpace(m.Target)),
		}
		if m.ReadOnly == nil {
			next.ReadOnly = boolPtr(true)
		} else {
			next.ReadOnly = boolPtr(*m.ReadOnly)
		}
		out = append(out, next)
	}
	return out
}

// ReadOnlyRoot reports whether the Docker root filesystem should be read-only.
func (s *SandboxConfig) ReadOnlyRoot() bool {
	return s.ReadOnlyRootFS != nil && *s.ReadOnlyRootFS
}

// RunsAsRoot reports whether the sandbox should run as uid 0.
func (s *SandboxConfig) RunsAsRoot() bool {
	return s != nil && s.RunAsRoot != nil && *s.RunAsRoot
}

// IsReadOnly reports whether a configured mount is read-only.
func (m SandboxMount) IsReadOnly() bool {
	return m.ReadOnly == nil || *m.ReadOnly
}

// ValidateSandboxConfig rejects malformed or dangerous sandbox settings.
func ValidateSandboxConfig(input *SandboxConfig) error {
	if input == nil {
		return nil
	}
	s := NormalizeSandboxConfig(input, input.Profile)
	switch s.Profile {
	case SandboxProfileDefault, SandboxProfileCompat:
	default:
		return fmt.Errorf("sandbox.profile must be %q or %q", SandboxProfileDefault, SandboxProfileCompat)
	}
	if s.NetworkProfile != SandboxNetworkStandard {
		return fmt.Errorf("sandbox.network_profile must be %q", SandboxNetworkStandard)
	}
	if s.UID < 0 || s.GID < 0 {
		return errors.New("sandbox uid/gid must be >= 0")
	}
	if !s.RunsAsRoot() && (s.UID == 0 || s.GID == 0) {
		return errors.New("sandbox non-root mode requires non-zero uid and gid")
	}
	if s.CPUs < 0 {
		return errors.New("sandbox.cpus must be >= 0")
	}
	if s.PidsLimit < 0 {
		return errors.New("sandbox.pids_limit must be >= 0")
	}
	if _, err := ParseMemoryBytes(s.Memory); err != nil {
		return fmt.Errorf("sandbox.memory: %w", err)
	}
	for i, m := range s.Mounts {
		if err := validateSandboxMount(m); err != nil {
			return fmt.Errorf("sandbox.mounts[%d]: %w", i, err)
		}
	}
	return nil
}

func validateSandboxMount(m SandboxMount) error {
	if strings.TrimSpace(m.Source) == "" || strings.TrimSpace(m.Target) == "" {
		return errors.New("source and target are required")
	}
	cleanSource := filepath.Clean(m.Source)
	cleanTarget := path.Clean(m.Target)
	if !filepath.IsAbs(cleanSource) {
		return errors.New("source must be an absolute host path")
	}
	if !path.IsAbs(cleanTarget) {
		return errors.New("target must be an absolute container path")
	}
	if cleanSource == "/var/run/docker.sock" {
		return errors.New("docker socket mounts are not allowed")
	}
	for _, prefix := range []string{"/proc", "/sys", "/dev", "/var/run"} {
		if cleanTarget == prefix || strings.HasPrefix(cleanTarget, prefix+"/") {
			return fmt.Errorf("target under %s is not allowed", prefix)
		}
	}
	return nil
}

// ParseMemoryBytes parses Docker-style memory strings like "512m" and "1g".
func ParseMemoryBytes(raw string) (int64, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, nil
	}
	multiplier := float64(1)
	for _, unit := range []struct {
		suffix string
		scale  float64
	}{
		{"kb", 1024},
		{"mb", 1024 * 1024},
		{"gb", 1024 * 1024 * 1024},
		{"tb", 1024 * 1024 * 1024 * 1024},
		{"k", 1024},
		{"m", 1024 * 1024},
		{"g", 1024 * 1024 * 1024},
		{"t", 1024 * 1024 * 1024 * 1024},
		{"b", 1},
	} {
		if strings.HasSuffix(raw, unit.suffix) {
			raw = strings.TrimSpace(strings.TrimSuffix(raw, unit.suffix))
			multiplier = unit.scale
			break
		}
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	if value < 0 {
		return 0, errors.New("must be >= 0")
	}
	return int64(value * multiplier), nil
}
