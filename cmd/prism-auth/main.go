package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	configPath := flag.String("config", "auth.json", "path to prism-auth config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := loadConfig(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	km, err := newKeyManager(cfg.SigningKey.Path)
	if err != nil {
		logger.Error("failed to initialize signing key", "error", err)
		os.Exit(1)
	}

	if cfg.SigningKey.Path == "" {
		logger.Warn("using ephemeral signing key (dev mode) — tokens become invalid after restart")
	}

	srv := newServer(cfg, km, logger)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("prism-auth listening",
		"addr", cfg.ListenAddr,
		"issuer", cfg.Issuer,
		"clients", len(cfg.Clients),
	)

	if err := httpServer.ListenAndServe(); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
