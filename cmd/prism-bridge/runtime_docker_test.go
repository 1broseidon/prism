package main

import (
	"reflect"
	"testing"
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
