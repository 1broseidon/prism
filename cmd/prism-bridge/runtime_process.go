package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"

	"github.com/1broseidon/prism/internal/bridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ProcessRuntime struct {
	mu       sync.RWMutex
	backends map[string]*processBackend
	logger   *slog.Logger
}

type processBackend struct {
	stdio   *bridge.StdioBackend
	handler http.Handler
	status  string
}

func NewProcessRuntime(logger *slog.Logger) *ProcessRuntime {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProcessRuntime{
		backends: make(map[string]*processBackend),
		logger:   logger,
	}
}

func (r *ProcessRuntime) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error) { //nolint:gocritic // Runtime interface fixes signature
	command := append([]string{req.Command}, req.Args...)
	stdio, err := bridge.ConnectStdio(ctx, command, req.Env, r.logger)
	if err != nil {
		return nil, err
	}

	toolsResult, err := stdio.Session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		_ = stdio.Session.Close()
		return nil, fmt.Errorf("list tools from backend: %w", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "prism-bridge-manage",
		Version: "0.1.0",
	}, nil)

	toolNames := make([]string, 0, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		toolName := tool.Name
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{"type": "object"}
		}
		server.AddTool(tool, func(ctx context.Context, call *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return stdio.Session.CallTool(ctx, &mcp.CallToolParams{
				Name:      toolName,
				Arguments: call.Params.Arguments,
			})
		})
		toolNames = append(toolNames, toolName)
	}
	sort.Strings(toolNames)

	handler := mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Logger: r.logger},
	)

	backend := &processBackend{
		stdio:   stdio,
		handler: handler,
		status:  "running",
	}

	r.mu.Lock()
	r.backends[req.ID] = backend
	r.mu.Unlock()

	pid := 0
	if stdio.Cmd != nil && stdio.Cmd.Process != nil {
		pid = stdio.Cmd.Process.Pid
	}

	return &SpawnResult{
		Endpoint: "/mcp/" + req.ID,
		Handler:  handler,
		Tools:    toolNames,
		PID:      pid,
		Status:   "running",
		Runtime:  "process",
	}, nil
}

func (r *ProcessRuntime) Stop(_ context.Context, id string) error {
	r.mu.RLock()
	backend, ok := r.backends[id]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("backend %q not found", id)
	}
	if backend.stdio != nil && backend.stdio.Session != nil {
		if err := backend.stdio.Session.Close(); err != nil {
			return err
		}
	}
	r.mu.Lock()
	delete(r.backends, id)
	r.mu.Unlock()
	backend.status = "stopped"
	return nil
}

func (r *ProcessRuntime) Status(_ context.Context, id string) (*RuntimeStatus, error) {
	r.mu.RLock()
	backend, ok := r.backends[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("backend %q not found", id)
	}
	pid := 0
	if backend.stdio != nil && backend.stdio.Cmd != nil && backend.stdio.Cmd.Process != nil {
		pid = backend.stdio.Cmd.Process.Pid
	}
	return &RuntimeStatus{PID: pid, Status: backend.status}, nil
}

func (r *ProcessRuntime) Cleanup(ctx context.Context) error {
	r.mu.RLock()
	ids := make([]string, 0, len(r.backends))
	for id := range r.backends {
		ids = append(ids, id)
	}
	r.mu.RUnlock()

	var firstErr error
	for _, id := range ids {
		if err := r.Stop(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
