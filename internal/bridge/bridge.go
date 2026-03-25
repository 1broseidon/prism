// Package bridge provides helpers for connecting to stdio MCP servers
// via CommandTransport. Used by the gateway for command-based backends
// and by cmd/prism-bridge for standalone operation.
package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// StdioBackend holds an MCP client session connected to a stdio process.
type StdioBackend struct {
	Client  *mcp.Client
	Session *mcp.ClientSession
	Cmd     *exec.Cmd
}

// ConnectStdio spawns a command, connects to it as an MCP client via stdio,
// and returns the session. The caller is responsible for closing the session.
func ConnectStdio(ctx context.Context, command []string, env map[string]string, logger *slog.Logger) (*StdioBackend, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("no command specified")
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...) //nolint:gosec // command is from operator config
	for k, v := range env {
		cmd.Env = append(cmd.Environ(), k+"="+v)
	}

	transport := &mcp.CommandTransport{
		Command:           cmd,
		TerminateDuration: 5 * time.Second,
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "prism",
		Version: "0.1.0",
	}, nil)

	logger.Info("connecting to stdio server", "command", command)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to stdio server: %w", err)
	}

	return &StdioBackend{
		Client:  client,
		Session: session,
		Cmd:     cmd,
	}, nil
}
