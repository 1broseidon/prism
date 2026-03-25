package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runServe wraps a stdio MCP server as a Streamable HTTP endpoint.
//
// It spawns the command, connects as an MCP client via CommandTransport,
// discovers tools, then re-exposes them over Streamable HTTP. The bridge
// is transparent — tools appear with their original names.
func runServe(logger *slog.Logger, args []string) error {
	flags, cmdArgs := splitAtDashDash(args)
	if len(cmdArgs) == 0 {
		return fmt.Errorf("no command specified after --")
	}

	portStr, flags := parseFlag(flags, "port", "3001")
	host, _ := parseFlag(flags, "host", "0.0.0.0")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("invalid port: %s", portStr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Spawn the stdio MCP server and connect as a client.
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...) //nolint:gosec // command is from operator CLI args
	transport := &mcp.CommandTransport{
		Command:           cmd,
		TerminateDuration: 5 * time.Second,
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "prism-bridge",
		Version: "0.1.0",
	}, nil)

	logger.Info("connecting to stdio server", "command", cmdArgs)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect to stdio server: %w", err)
	}
	defer func() { _ = session.Close() }()

	// Discover tools from the backend.
	toolsResult, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return fmt.Errorf("list tools from backend: %w", err)
	}

	logger.Info("discovered tools", "count", len(toolsResult.Tools))

	// Build an MCP server that re-exposes the discovered tools over HTTP.
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "prism-bridge",
		Version: "0.1.0",
	}, nil)

	for _, tool := range toolsResult.Tools {
		toolName := tool.Name // capture for closure
		// Ensure the tool has an input schema — the SDK requires it for AddTool.
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{"type": "object"}
		}
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			logger.Debug("forwarding tool call", "tool", toolName)
			result, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      toolName,
				Arguments: req.Params.Arguments,
			})
			if err != nil {
				return nil, fmt.Errorf("backend call %q: %w", toolName, err)
			}
			return result, nil
		})
		logger.Info("registered tool", "name", tool.Name)
	}

	handler := mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Logger: logger},
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","tools":%d}`, len(toolsResult.Tools))
	})

	return listenAndServe(logger, host, port, mux)
}
