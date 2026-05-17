package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/config"
	ws "github.com/1broseidon/prism/internal/workspace"
)

func TestDetectRuntimeProfile(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"npx", "node"},
		{"node", "node"},
		{"uvx", "python"},
		{"python3", "python"},
		{"./my-server", "full"},
	}
	for _, tc := range tests {
		if got := detectRuntimeProfile(tc.command); got != tc.want {
			t.Fatalf("detectRuntimeProfile(%q) = %q want %q", tc.command, got, tc.want)
		}
	}
}

func TestDockerRuntimeImageSelection(t *testing.T) {
	runtime := &DockerRuntime{
		defaultImage: "prism-bridge:full",
		images: map[string]string{
			"node":   "prism-bridge:node",
			"python": "prism-bridge:python",
			"full":   "prism-bridge:full",
		},
	}

	if got := runtime.imageForRequest(&SpawnRequest{Command: "npx"}); got != "prism-bridge:node" {
		t.Fatalf("node image = %q", got)
	}
	if got := runtime.imageForRequest(&SpawnRequest{Command: "uvx"}); got != "prism-bridge:python" {
		t.Fatalf("python image = %q", got)
	}
	if got := runtime.imageForRequest(&SpawnRequest{Command: "./custom"}); got != "prism-bridge:full" {
		t.Fatalf("full image = %q", got)
	}
	if got := runtime.imageForRequest(&SpawnRequest{Command: "npx", Runtime: "python"}); got != "prism-bridge:python" {
		t.Fatalf("override image = %q", got)
	}
}

func TestEnvFromMap(t *testing.T) {
	got := envFromMap(map[string]string{"B": "2", "A": "1"})
	want := []string{"A=1", "B=2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envFromMap = %v want %v", got, want)
	}
}

func TestDockerSpecBuildsIsolatedEnvAndLabels(t *testing.T) {
	runtime := &DockerRuntime{
		defaultImage: "prism-bridge:full",
		images:       map[string]string{"full": "prism-bridge:full"},
		network:      "prism_default",
		labelPrefix:  "prism.bridge",
		namePrefix:   "prism-managed-",
	}

	spec := runtime.buildSpec(&SpawnRequest{
		ID:      "github",
		Command: "npx",
		Args:    []string{"@modelcontextprotocol/server-github"},
		Env:     map[string]string{"GITHUB_TOKEN": "secret"},
	})

	if spec.containerCfg.Image != "prism-bridge:full" {
		t.Fatalf("image = %q", spec.containerCfg.Image)
	}
	if len(spec.containerCfg.Entrypoint) != 1 || spec.containerCfg.Entrypoint[0] != "prism-bridge" {
		t.Fatalf("entrypoint = %v", spec.containerCfg.Entrypoint)
	}
	if !reflect.DeepEqual(spec.containerCfg.Env, []string{"GITHUB_TOKEN=secret"}) {
		t.Fatalf("env = %v", spec.containerCfg.Env)
	}
	if spec.containerCfg.Labels["prism.bridge.managed"] != "true" {
		t.Fatalf("managed label missing: %+v", spec.containerCfg.Labels)
	}
	if spec.containerCfg.Labels["prism.bridge.id"] != "github" {
		t.Fatalf("id label missing: %+v", spec.containerCfg.Labels)
	}
	if spec.name != "prism-managed-github" {
		t.Fatalf("name = %q", spec.name)
	}
	if spec.endpoint != "http://prism-managed-github:3001/mcp" {
		t.Fatalf("endpoint = %q", spec.endpoint)
	}
	if len(spec.hostCfg.CapDrop) != 1 || spec.hostCfg.CapDrop[0] != "ALL" {
		t.Fatalf("cap drop = %v", spec.hostCfg.CapDrop)
	}
	if len(spec.hostCfg.SecurityOpt) != 1 || spec.hostCfg.SecurityOpt[0] != "no-new-privileges:true" {
		t.Fatalf("security opts = %v", spec.hostCfg.SecurityOpt)
	}
}

func TestDockerSpecAppliesDefaultSandbox(t *testing.T) {
	runtime := &DockerRuntime{
		defaultImage: "prism-bridge:full",
		images:       map[string]string{"full": "prism-bridge:full"},
		labelPrefix:  "prism.bridge",
		namePrefix:   "prism-managed-",
	}
	sandbox := config.DefaultSandboxConfig()
	readonly := false
	sandbox.Mounts = []config.SandboxMount{{
		Source:   "/tmp/project",
		Target:   "/workspace",
		ReadOnly: &readonly,
	}}

	spec := runtime.buildSpec(&SpawnRequest{
		ID:      "brainfile",
		Command: "npx",
		Args:    []string{"@brainfile/cli", "mcp"},
		Sandbox: &sandbox,
	})

	if spec.containerCfg.User != "65532:65532" {
		t.Fatalf("user = %q", spec.containerCfg.User)
	}
	if !spec.hostCfg.ReadonlyRootfs {
		t.Fatal("rootfs should be read-only")
	}
	if spec.hostCfg.Tmpfs["/tmp"] != "rw,nosuid,nodev,exec,size=256m" {
		t.Fatalf("tmpfs /tmp = %q", spec.hostCfg.Tmpfs["/tmp"])
	}
	if spec.hostCfg.Memory != 512*1024*1024 {
		t.Fatalf("memory = %d", spec.hostCfg.Memory)
	}
	if spec.hostCfg.NanoCPUs != 1_000_000_000 {
		t.Fatalf("nano cpus = %d", spec.hostCfg.NanoCPUs)
	}
	if spec.hostCfg.PidsLimit == nil || *spec.hostCfg.PidsLimit != 128 {
		t.Fatalf("pids limit = %v", spec.hostCfg.PidsLimit)
	}
	if len(spec.hostCfg.Mounts) != 1 || spec.hostCfg.Mounts[0].ReadOnly {
		t.Fatalf("mounts = %+v", spec.hostCfg.Mounts)
	}
	if !envContains(spec.containerCfg.Env, "HOME=/home/sandbox") ||
		!envContains(spec.containerCfg.Env, "NPM_CONFIG_CACHE=/tmp/.npm") {
		t.Fatalf("sandbox env = %v", spec.containerCfg.Env)
	}
}

func TestDockerSpecMountsWorkspaceSnapshotVolume(t *testing.T) { //nolint:gocyclo // verifies a cross-cutting Docker spec
	runtime := &DockerRuntime{
		defaultImage: "prism-bridge:full",
		images:       map[string]string{"full": "prism-bridge:full"},
		labelPrefix:  "prism.bridge",
		namePrefix:   "prism-managed-",
	}
	spec := runtime.buildSpec(&SpawnRequest{
		ID:      "brainfile",
		Command: "npx",
		Args:    []string{"@brainfile/cli", "mcp"},
		Sandbox: &config.SandboxConfig{
			Profile:        config.SandboxProfileDefault,
			NetworkProfile: config.SandboxNetworkStandard,
			RunAsRoot:      boolPtr(false),
			UID:            config.DefaultSandboxUID,
			GID:            config.DefaultSandboxGID,
			ReadOnlyRootFS: boolPtr(true),
		},
		Workspace: &config.WorkspaceConfig{
			ID:        "repo",
			Mode:      config.WorkspaceModeSnapshot,
			WriteMode: config.WorkspaceWriteStage,
		},
		WorkspaceSnapshot: &ws.Snapshot{BaseID: "base", Archive: []byte("archive")},
	})

	if spec.containerCfg.WorkingDir != "/workspace" {
		t.Fatalf("working dir = %q", spec.containerCfg.WorkingDir)
	}
	if spec.containerCfg.User != "" || spec.containerCfg.Entrypoint[0] != "sh" || !strings.Contains(spec.containerCfg.Cmd[0], "su-exec 65532:65532") {
		t.Fatalf("workspace entrypoint/user = user %q entrypoint %v cmd %v", spec.containerCfg.User, spec.containerCfg.Entrypoint, spec.containerCfg.Cmd)
	}
	if !reflect.DeepEqual([]string(spec.hostCfg.CapAdd), []string{"CHOWN", "SETGID", "SETUID"}) {
		t.Fatalf("cap add = %v", spec.hostCfg.CapAdd)
	}
	if spec.volumeName != "prism-managed-workspace-repo" {
		t.Fatalf("volume = %q", spec.volumeName)
	}
	if spec.cacheVolume != "prism-managed-brainfile-cache" {
		t.Fatalf("cache volume = %q", spec.cacheVolume)
	}
	if spec.installScript == "" || !strings.Contains(spec.installScript, "npm cache add") {
		t.Fatalf("install script = %q", spec.installScript)
	}
	workspaceFound := false
	cacheFound := false
	for _, m := range spec.hostCfg.Mounts {
		if m.Target == "/workspace" && string(m.Type) == "volume" && m.Source == spec.volumeName {
			workspaceFound = true
		}
		if m.Target == "/cache" && string(m.Type) == "volume" && m.Source == spec.cacheVolume {
			cacheFound = true
		}
	}
	if !workspaceFound || !cacheFound {
		t.Fatalf("workspace/cache volume mount missing: %+v", spec.hostCfg.Mounts)
	}
	if !envContains(spec.containerCfg.Env, "NPM_CONFIG_PREFER_OFFLINE=true") ||
		!envContains(spec.containerCfg.Env, "NPM_CONFIG_IGNORE_SCRIPTS=true") {
		t.Fatalf("package-manager hardening env missing: %v", spec.containerCfg.Env)
	}
}

func TestDockerSpecSharesWorkspaceVolumeByWorkspaceID(t *testing.T) {
	runtime := &DockerRuntime{
		defaultImage: "prism-bridge:full",
		images:       map[string]string{"full": "prism-bridge:full"},
		labelPrefix:  "prism.bridge",
		namePrefix:   "prism-managed-",
	}
	workspace := &config.WorkspaceConfig{
		ID:        "repo",
		Mode:      config.WorkspaceModeSnapshot,
		WriteMode: config.WorkspaceWriteStage,
	}

	brainfile := runtime.buildSpec(&SpawnRequest{
		ID:                "brainfile",
		Command:           "npx",
		Args:              []string{"@brainfile/cli", "mcp"},
		Workspace:         workspace,
		WorkspaceSnapshot: &ws.Snapshot{BaseID: "base", Archive: []byte("archive")},
	})
	recoil := runtime.buildSpec(&SpawnRequest{
		ID:                "recoil",
		Command:           "npx",
		Args:              []string{"recoil", "mcp"},
		Workspace:         workspace,
		WorkspaceSnapshot: &ws.Snapshot{BaseID: "base", Archive: []byte("archive")},
	})

	if brainfile.volumeName != recoil.volumeName {
		t.Fatalf("workspace volume should be shared, got %q and %q", brainfile.volumeName, recoil.volumeName)
	}
	if brainfile.cacheVolume == recoil.cacheVolume {
		t.Fatalf("package cache volumes should remain per backend, got %q", brainfile.cacheVolume)
	}
}

func TestDockerSpecMountsVirtualWorkspaceWithoutSnapshot(t *testing.T) {
	runtime := &DockerRuntime{
		defaultImage: "prism-bridge:full",
		images:       map[string]string{"full": "prism-bridge:full"},
		labelPrefix:  "prism.bridge",
		namePrefix:   "prism-managed-",
	}

	spec := runtime.buildSpec(&SpawnRequest{
		ID:      "notes",
		Command: "npx",
		Args:    []string{"team-notes", "mcp"},
		Workspace: &config.WorkspaceConfig{
			ID:               "team-a",
			Type:             config.WorkspaceTypeVirtual,
			QuotaBytes:       4096,
			RetentionSeconds: 120,
		},
	})

	if spec.volumeName != "prism-managed-workspace-team-a" {
		t.Fatalf("volume = %q", spec.volumeName)
	}
	if spec.containerCfg.WorkingDir != "/workspace" {
		t.Fatalf("working dir = %q", spec.containerCfg.WorkingDir)
	}
	if spec.containerCfg.Labels["prism.bridge.workspace_type"] != config.WorkspaceTypeVirtual {
		t.Fatalf("workspace type label = %q", spec.containerCfg.Labels["prism.bridge.workspace_type"])
	}
	if spec.containerCfg.Labels["prism.bridge.workspace_quota_bytes"] != "4096" ||
		spec.containerCfg.Labels["prism.bridge.workspace_retention_seconds"] != "120" {
		t.Fatalf("workspace lifecycle labels = %+v", spec.containerCfg.Labels)
	}
}

func envContains(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}

func boolPtr(v bool) *bool { return &v }
