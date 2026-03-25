package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolManifest describes a single tool exposed by the bridge.
// It can be loaded from a JSON file or specified via CLI flags.
type ToolManifest struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// runTool wraps a single function (any script/binary) as an MCP tool.
//
// The function contract:
//   - stdin:  JSON object of tool arguments
//   - stdout: result text
//   - exit 0: success
//   - exit 1+: error (stderr is the error message)
func runTool(logger *slog.Logger, args []string) error {
	flags, cmdArgs := splitAtDashDash(args)
	if len(cmdArgs) == 0 {
		return fmt.Errorf("no command specified after --")
	}

	portStr, flags := parseFlag(flags, "port", "3001")
	host, flags := parseFlag(flags, "host", "0.0.0.0")
	manifestPath, flags := parseFlag(flags, "manifest", "")
	name, flags := parseFlag(flags, "name", "")
	description, _ := parseFlag(flags, "description", "")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("invalid port: %s", portStr)
	}

	manifest, err := loadManifest(manifestPath, name, description)
	if err != nil {
		return err
	}

	logger.Info("registering tool", "name", manifest.Name, "command", cmdArgs)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "prism-bridge-tool",
		Version: "0.1.0",
	}, nil)

	tool := &mcp.Tool{
		Name:        manifest.Name,
		Description: manifest.Description,
	}
	if manifest.InputSchema != nil {
		tool.InputSchema = manifest.InputSchema
	} else {
		tool.InputSchema = map[string]any{"type": "object"}
	}

	server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var arguments map[string]any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &arguments); err != nil {
				return nil, fmt.Errorf("unmarshal arguments: %w", err)
			}
		}
		return executeTool(ctx, logger, cmdArgs, arguments)
	})

	handler := mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Logger: logger},
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)
	toolName := manifest.Name
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp, _ := json.Marshal(map[string]string{"status": "ok", "tool": toolName})
		_, _ = w.Write(resp)
	})

	return listenAndServe(logger, host, port, mux)
}

// loadManifest loads tool metadata from a manifest file or CLI flags.
func loadManifest(path, name, description string) (*ToolManifest, error) {
	if path != "" {
		return loadManifestFile(path)
	}
	if name == "" {
		return nil, fmt.Errorf("either --manifest or --name is required")
	}
	if description == "" {
		description = name
	}
	return &ToolManifest{
		Name:        name,
		Description: description,
	}, nil
}

// loadManifestFile reads a tool manifest from a JSON file.
func loadManifestFile(path string) (*ToolManifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // manifest path is from operator CLI args
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m ToolManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if m.Name == "" {
		return nil, fmt.Errorf("manifest %s: name is required", path)
	}
	return &m, nil
}

// executeTool runs the command with tool arguments on stdin, captures stdout/stderr.
func executeTool(ctx context.Context, logger *slog.Logger, cmdArgs []string, arguments map[string]any) (*mcp.CallToolResult, error) {
	argsJSON, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("marshal arguments: %w", err)
	}

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...) //nolint:gosec // command is from operator CLI args
	cmd.Stdin = bytes.NewReader(argsJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logger.Debug("executing tool command", "command", cmdArgs, "args", string(argsJSON))

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		logger.Warn("tool command failed", "command", cmdArgs, "error", errMsg)
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				&mcp.TextContent{Text: errMsg},
			},
		}, nil
	}

	result := strings.TrimSpace(stdout.String())
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: result},
		},
	}, nil
}
