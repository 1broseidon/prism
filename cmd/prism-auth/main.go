// Package main implements prism-auth, a standalone OAuth 2.1 authorization server
// for the Prism MCP gateway. In most deployments, prism-auth is embedded in the
// gateway process via the unified config. This standalone binary is for advanced
// use cases (separate scaling, sidecar deployments).
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/1broseidon/prism/internal/authserver"
	"github.com/1broseidon/prism/internal/telemetry"
)

func main() {
	configPath := flag.String("config", "auth.json", "path to prism-auth config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	shutdownTracer := telemetry.Init("prism-auth", logger)
	defer func() { _ = shutdownTracer(context.Background()) }()

	cfg, err := authserver.LoadConfig(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		return
	}

	km, err := authserver.NewKeyManager(cfg.SigningKey.Path)
	if err != nil {
		logger.Error("failed to initialize signing key", "error", err)
		return
	}

	if cfg.SigningKey.Path == "" {
		logger.Warn("using ephemeral signing key (dev mode) — tokens become invalid after restart")
	}

	srv := authserver.NewServer(cfg, km, nil, logger)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("prism-auth listening",
		"addr", cfg.ListenAddr,
		"issuer", cfg.Issuer,
		"clients", len(cfg.Clients),
	)

	if err := httpServer.ListenAndServe(); err != nil {
		logger.Error("server stopped", "error", err)
	}
}
