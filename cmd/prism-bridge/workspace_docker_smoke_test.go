//go:build smoke

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/config"
	ws "github.com/1broseidon/prism/internal/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDockerWorkspaceSnapshotSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := ws.CreateSnapshot(root, ws.SnapshotPolicy{})
	if err != nil {
		t.Fatal(err)
	}

	runtime, err := NewDockerRuntime(nil, &DockerRuntimeOptions{
		FullImage: "prism-bridge:full",
		NodeImage: "prism-bridge:full",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := SpawnRequest{
		ID:      "workspace-smoke",
		Command: "npx",
		Args:    []string{"@modelcontextprotocol/server-filesystem", "/workspace"},
		Sandbox: &config.SandboxConfig{
			Profile:        config.SandboxProfileDefault,
			NetworkProfile: config.SandboxNetworkStandard,
			RunAsRoot:      smokeBoolPtr(false),
			UID:            config.DefaultSandboxUID,
			GID:            config.DefaultSandboxGID,
			ReadOnlyRootFS: smokeBoolPtr(true),
			Memory:         config.DefaultSandboxMemory,
			CPUs:           config.DefaultSandboxCPUs,
			PidsLimit:      config.DefaultSandboxPidsLimit,
		},
		Workspace: &config.WorkspaceConfig{
			ID:        "repo",
			Mode:      config.WorkspaceModeSnapshot,
			WriteMode: config.WorkspaceWriteStage,
		},
		WorkspaceSnapshot: snap,
	}
	if _, err := runtime.Spawn(ctx, req); err != nil {
		t.Fatal(err)
	}
	runtime.mu.RLock()
	endpoint := runtime.backends[req.ID].endpoint
	runtime.mu.RUnlock()
	defer func() { _ = runtime.Stop(context.Background(), req.ID) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "prism-smoke", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	callResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "write_file",
		Arguments: map[string]any{
			"path":    "/workspace/smoke.txt",
			"content": "from sandbox\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if callResult.IsError {
		t.Fatalf("write_file returned error: %s", contentText(callResult.Content))
	}

	changes, err := runtime.WorkspaceChanges(ctx, req.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ch := range changes.Files {
		if ch.Path == "smoke.txt" && ch.Type == "add" {
			found = true
		}
	}
	if !found {
		t.Fatalf("smoke.txt add not found in changes: %+v", changes.Files)
	}
}

func smokeBoolPtr(v bool) *bool { return &v }

func contentText(content []mcp.Content) string {
	out := ""
	for _, item := range content {
		if text, ok := item.(*mcp.TextContent); ok {
			out += text.Text
			continue
		}
		out += fmt.Sprintf("%T", item)
	}
	return out
}
