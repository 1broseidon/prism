package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/gateway"
)

func configureStdioSpawning(ctx context.Context, cfg *config.Loaded, gw *gateway.Gateway, logger *slog.Logger) (*localBridgeProcess, error) {
	mode := effectiveStdioSpawnMode(cfg)
	bridgeURLs := effectiveBridgeURLs(cfg)

	if mode == "disabled" {
		gw.DisableProcessStdio("stdio spawning is disabled by stdio_spawn_mode")
		return nil, nil
	}

	if len(bridgeURLs) > 0 {
		gw.SetBridgeURLs(bridgeURLs)
		logger.Info("using external bridge manager", "bridge_urls", bridgeURLs)
		return nil, nil
	}

	switch mode {
	case "bridge_http":
		return nil, fmt.Errorf("stdio_spawn_mode=bridge_http requires bridge_url or bridge_urls")
	case "internal_docker", "auto":
		localBridge, err := startLocalDockerBridge(ctx, logger)
		if err == nil {
			gw.SetBridgeURL(localBridge.url)
			return localBridge, nil
		}
		if mode == "internal_docker" {
			return nil, err
		}
		if runningInContainer() {
			gw.DisableProcessStdio("mount /var/run/docker.sock or configure bridge_url/bridge_urls for sandboxed stdio servers")
			logger.Warn("stdio Docker sandboxing unavailable", "error", err)
			return nil, nil
		}
		logger.Warn("local Docker bridge unavailable; falling back to direct process stdio", "error", err)
		return nil, nil
	case "process":
		logger.Info("using direct process stdio spawning")
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported stdio_spawn_mode %q", mode)
	}
}

func effectiveStdioSpawnMode(cfg *config.Loaded) string {
	if mode := strings.TrimSpace(os.Getenv("PRISM_STDIO_SPAWN_MODE")); mode != "" {
		return mode
	}
	if cfg != nil && cfg.StdioSpawnMode != "" {
		return cfg.StdioSpawnMode
	}
	return "auto"
}

func effectiveBridgeURLs(cfg *config.Loaded) []string {
	if raw := strings.TrimSpace(os.Getenv("PRISM_BRIDGE_URLS")); raw != "" {
		return parseBridgeURLList(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("PRISM_BRIDGE_URL")); raw != "" {
		return parseBridgeURLList(raw)
	}
	if cfg == nil {
		return nil
	}
	if len(cfg.BridgeURLs) > 0 {
		return append([]string(nil), cfg.BridgeURLs...)
	}
	if cfg.BridgeURL != "" {
		return []string{cfg.BridgeURL}
	}
	return nil
}

func parseBridgeURLList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	result := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		u := strings.TrimRight(strings.TrimSpace(field), "/")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		result = append(result, u)
	}
	return result
}

func runningInContainer() bool {
	if strings.TrimSpace(os.Getenv("PRISM_IN_CONTAINER")) == "1" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}
