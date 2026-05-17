package main

import (
	"context"
	"net/http"

	"github.com/1broseidon/prism/internal/config"
	ws "github.com/1broseidon/prism/internal/workspace"
)

// Runtime abstracts how manage mode spawns and stops backends.
type Runtime interface {
	Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error)
	Stop(ctx context.Context, id string) error
	Status(ctx context.Context, id string) (*RuntimeStatus, error)
	Cleanup(ctx context.Context) error
}

// SpawnRequest describes a backend the manager should start.
type SpawnRequest struct {
	ID                string                  `json:"id"`
	Command           string                  `json:"command"`
	Args              []string                `json:"args,omitempty"`
	Env               map[string]string       `json:"env,omitempty"`
	Runtime           string                  `json:"runtime,omitempty"`
	Sandbox           *config.SandboxConfig   `json:"sandbox,omitempty"`
	Workspace         *config.WorkspaceConfig `json:"workspace,omitempty"`
	WorkspaceSnapshot *ws.Snapshot            `json:"workspace_snapshot,omitempty"`
}

// SpawnResult describes a started backend.
type SpawnResult struct {
	Endpoint    string
	Handler     http.Handler
	Tools       []string
	ContainerID string
	PID         int
	Status      string
	Runtime     string
}

// RuntimeStatus is the runtime-specific status for a backend.
type RuntimeStatus struct {
	ContainerID string
	PID         int
	Status      string
}
