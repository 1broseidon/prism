// Package main implements prism-bridge, a transport adapter that exposes
// stdio MCP servers or single-function tools as Streamable HTTP endpoints.
//
// Usage:
//
//	prism-bridge serve --port 3001 -- npx @modelcontextprotocol/server-github
//	prism-bridge tool  --port 3002 --manifest tool.json -- python check_dns.py
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	args := os.Args[2:]

	switch subcommand {
	case "serve":
		if err := runServe(logger, args); err != nil {
			logger.Error("serve failed", "error", err)
			os.Exit(1)
		}
	case "tool":
		if err := runTool(logger, args); err != nil {
			logger.Error("tool failed", "error", err)
			os.Exit(1)
		}
	case "manage":
		if err := runManage(logger, args); err != nil {
			logger.Error("manage failed", "error", err)
			os.Exit(1)
		}
	case "--help", "-h", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `prism-bridge — stdio→HTTP adapter for MCP servers

Usage:
  prism-bridge serve  [flags] -- <command> [args...]
  prism-bridge tool   [flags] -- <command> [args...]
  prism-bridge manage [flags]

Subcommands:
  serve    Wrap a stdio MCP server as a Streamable HTTP endpoint.
  tool     Wrap a single function (any script/binary) as an MCP tool.
  manage   Start an empty bridge and spawn/remove MCP backends dynamically.

Serve flags:
  --port <port>       Port to listen on (default: 3001)
  --host <host>       Host to bind to (default: 0.0.0.0)

Tool flags:
  --port <port>       Port to listen on (default: 3001)
  --host <host>       Host to bind to (default: 0.0.0.0)
  --manifest <file>   Tool manifest JSON (name, description, input schema)
  --name <name>       Tool name (alternative to manifest)
  --description <desc> Tool description (alternative to manifest)

Manage flags:
  --port <port>         Port to listen on (default: 3001)
  --host <host>         Host to bind to (default: 0.0.0.0)
  --max-backends <n>    Maximum concurrent backends (default: 20, 0 = unlimited)
  --runtime <mode>      Backend runtime: process or docker (default: process)
  --image <image>       Default Docker image for managed backends
  --network <network>   Docker network for managed containers
  --label-prefix <pfx>  Label prefix for managed containers (default: prism.bridge)
  --image-base <image>  Docker image for base/runtime-neutral backends
  --image-node <image>  Docker image for node-based backends
  --image-python <img>  Docker image for python-based backends
  --image-full <image>  Docker image fallback for managed backends

The command after -- is the stdio MCP server (serve mode) or the function
to execute per tool call (tool mode).

Tool mode function contract:
  stdin  → JSON object of tool arguments
  stdout → result text
  exit 0 → success
  exit 1 → error (stderr is the error message)

Examples:
  # Wrap a stdio MCP server
  prism-bridge serve --port 3001 -- npx @modelcontextprotocol/server-github

  # Wrap a Python function as a tool
  prism-bridge tool --name check-dns --description "Resolve DNS" \
    --port 3002 -- python3 check_dns.py

  # Wrap with a full manifest
  prism-bridge tool --manifest tools/dns.json --port 3002 -- python3 check_dns.py
`)
}

// listenAndServe starts an HTTP server with graceful shutdown on SIGINT/SIGTERM.
func listenAndServe(logger *slog.Logger, host string, port int, handler http.Handler) error {
	addr := fmt.Sprintf("%s:%d", host, port)

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	logger.Info("listening", "addr", ln.Addr().String())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// splitAtDashDash splits args at "--", returning flags before and command after.
func splitAtDashDash(args []string) (flags, command []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// parseFlag finds --<name> <value> in flags and returns (value, remainingFlags).
func parseFlag(flags []string, name, defaultVal string) (val string, remaining []string) {
	flag := "--" + name
	for i, f := range flags {
		if f == flag && i+1 < len(flags) {
			remaining := make([]string, 0, len(flags)-2)
			remaining = append(remaining, flags[:i]...)
			remaining = append(remaining, flags[i+2:]...)
			return flags[i+1], remaining
		}
	}
	return defaultVal, flags
}
