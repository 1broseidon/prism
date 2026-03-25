// Package main is the entry point for the Prism MCP gateway.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prism-gateway/prism/internal/admin"
	"github.com/prism-gateway/prism/internal/audit"
	"github.com/prism-gateway/prism/internal/auth"
	"github.com/prism-gateway/prism/internal/config"
	"github.com/prism-gateway/prism/internal/gateway"
	"github.com/prism-gateway/prism/internal/middleware"
)

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Info("loaded config",
		"listen", cfg.ListenAddr,
		"admin", cfg.AdminAddr,
		"servers", len(cfg.Servers),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gw := setupGateway(ctx, cfg, logger)
	defer gw.Close()

	handler := buildHandler(cfg, gw, logger)
	mainMux := buildMux(cfg, handler, logger)

	mainServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mainMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	adminAPI := admin.NewAPI(func() any { return gw.Status() })
	adminServer := &http.Server{
		Addr:              cfg.AdminAddr,
		Handler:           adminAPI.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	startServers(ctx, cfg, mainServer, adminServer, logger, errCh)
	waitForShutdown(cfg, mainServer, adminServer, gw, logger, errCh)
}

// setupGateway creates the gateway, wires the audit logger, and connects backends.
func setupGateway(ctx context.Context, cfg *config.Config, logger *slog.Logger) *gateway.Gateway {
	gw := gateway.New(logger)
	gw.SetAuditLogger(buildAuditLogger(cfg, logger))

	for i := range cfg.Servers {
		if err := gw.ConnectBackend(ctx, &cfg.Servers[i]); err != nil {
			logger.Error("failed to connect backend", "id", cfg.Servers[i].ID, "error", err)
		}
	}

	return gw
}

// buildHandler wraps the gateway handler with auth and rate-limit middleware.
func buildHandler(cfg *config.Config, gw *gateway.Gateway, logger *slog.Logger) http.Handler {
	var middlewares []middleware.Middleware

	switch {
	case cfg.Auth != nil && cfg.Auth.OAuth != nil:
		middlewares = append(middlewares, buildOAuthMiddleware(cfg, logger))
	case cfg.Auth != nil && len(cfg.Auth.ValidKeys) > 0:
		middlewares = append(middlewares, middleware.Auth(middleware.AuthConfig{
			Header:    cfg.Auth.Header,
			ValidKeys: cfg.Auth.ValidKeys,
		}))
		logger.Info("API key auth enabled")
	default:
		logger.Warn("no authentication configured — gateway is open")
	}

	if cfg.RateLimit != nil {
		middlewares = append(middlewares, middleware.RateLimit(middleware.RateLimitConfig{
			RequestsPerSecond: cfg.RateLimit.RequestsPerSecond,
			Burst:             cfg.RateLimit.Burst,
		}))
	}

	handler := gw.Handler()
	if len(middlewares) > 0 {
		handler = middleware.Chain(middlewares...)(handler)
	}

	return handler
}

// buildOAuthMiddleware creates the OAuth 2.1 token validation middleware.
func buildOAuthMiddleware(cfg *config.Config, logger *slog.Logger) middleware.Middleware {
	oauthCfg := cfg.Auth.OAuth
	validator := auth.NewTokenValidator(&auth.TokenValidatorConfig{
		IssuerURL:      oauthCfg.IssuerURL,
		JWKSURL:        oauthCfg.JWKSURL,
		Audience:       oauthCfg.Audience,
		RequiredScopes: oauthCfg.RequiredScopes,
		MaxTokenAge:    oauthCfg.MaxTokenAge.Duration(),
	})

	resourceURI := cfg.ResourceURI
	if resourceURI == "" && oauthCfg.ResourceURI != "" {
		resourceURI = oauthCfg.ResourceURI
	}

	logger.Info("OAuth 2.1 token validation enabled",
		"issuer", oauthCfg.IssuerURL,
		"audience", oauthCfg.Audience,
	)

	return auth.Middleware(validator, resourceURI)
}

// buildMux creates the HTTP mux with the MCP handler and optional discovery endpoint.
func buildMux(cfg *config.Config, handler http.Handler, logger *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()

	if cfg.Auth != nil && cfg.Auth.OAuth != nil {
		oauthCfg := cfg.Auth.OAuth
		resourceURI := cfg.ResourceURI
		if resourceURI == "" {
			resourceURI = oauthCfg.ResourceURI
		}
		meta := &auth.ProtectedResourceMetadata{
			Resource:               resourceURI,
			AuthorizationServers:   []string{oauthCfg.IssuerURL},
			ScopesSupported:        oauthCfg.ScopesSupported,
			BearerMethodsSupported: []string{"header"},
		}
		mux.Handle("/.well-known/oauth-protected-resource", auth.DiscoveryHandler(meta))
		logger.Info("serving Protected Resource Metadata", "path", "/.well-known/oauth-protected-resource")
	}

	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)

	return mux
}

// startServers launches the main and admin HTTP servers in background goroutines.
func startServers(ctx context.Context, cfg *config.Config, mainSrv, adminSrv *http.Server, logger *slog.Logger, errCh chan<- error) {
	lc := net.ListenConfig{}

	go func() {
		ln, err := lc.Listen(ctx, "tcp", cfg.ListenAddr)
		if err != nil {
			errCh <- fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
			return
		}
		logger.Info("MCP gateway listening", "addr", ln.Addr().String())
		errCh <- mainSrv.Serve(ln)
	}()

	go func() {
		ln, err := lc.Listen(ctx, "tcp", cfg.AdminAddr)
		if err != nil {
			errCh <- fmt.Errorf("listen %s: %w", cfg.AdminAddr, err)
			return
		}
		logger.Info("admin API listening", "addr", ln.Addr().String())
		errCh <- adminSrv.Serve(ln)
	}()
}

// waitForShutdown blocks until a signal or server error, then gracefully shuts down.
func waitForShutdown(cfg *config.Config, mainServer, adminServer *http.Server, gw *gateway.Gateway, logger *slog.Logger, errCh <-chan error) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	select {
	case sig := <-sigCh:
		logger.Info("received signal", "signal", sig)
		if sig == syscall.SIGHUP {
			logger.Info("SIGHUP received — hot reload not yet implemented")
		}
	case err := <-errCh:
		logger.Error("server error", "error", err)
	}

	logger.Info("shutting down...")
	shutdownTimeout := cfg.ShutdownTimeout.Duration()
	if shutdownTimeout == 0 {
		shutdownTimeout = 10 * time.Second
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := mainServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("main server shutdown error", "error", err)
	}
	if err := adminServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("admin server shutdown error", "error", err)
	}
	gw.Close()

	logger.Info("shutdown complete")
}

// buildAuditLogger creates an audit.Logger from the audit config section.
func buildAuditLogger(cfg *config.Config, logger *slog.Logger) *audit.Logger {
	if cfg.Audit == nil || !cfg.Audit.Enabled {
		return audit.Noop()
	}

	output := cfg.Audit.Output
	switch output {
	case "", "stderr":
		logger.Info("audit logging enabled", "output", "stderr")
		return audit.New(os.Stderr)
	case "stdout":
		logger.Info("audit logging enabled", "output", "stdout")
		return audit.New(os.Stdout)
	}

	f, err := os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640) //nolint:gosec // 0640 allows group read for log aggregation
	if err != nil {
		logger.Error("failed to open audit log file — falling back to stderr",
			"path", output, "error", err)
		return audit.New(os.Stderr)
	}

	logger.Info("audit logging enabled", "output", output)
	return audit.New(io.MultiWriter(f, os.Stderr))
}
