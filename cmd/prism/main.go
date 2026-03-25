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

	// Create gateway
	gw := gateway.New(logger)
	defer gw.Close()

	// Wire audit logger
	gw.SetAuditLogger(buildAuditLogger(cfg, logger))

	// Connect to all backends
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, srv := range cfg.Servers {
		if err := gw.ConnectBackend(ctx, srv); err != nil {
			logger.Error("failed to connect backend", "id", srv.ID, "error", err)
			// Continue — don't fail the whole gateway for one backend
		}
	}

	// Build MCP handler with middleware
	var middlewares []middleware.Middleware

	// OAuth 2.1 auth (production) or simple API key auth (development)
	if cfg.Auth != nil && cfg.Auth.OAuth != nil {
		oauthCfg := cfg.Auth.OAuth
		validator := auth.NewTokenValidator(auth.TokenValidatorConfig{
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
		middlewares = append(middlewares, auth.Middleware(validator, resourceURI))
		logger.Info("OAuth 2.1 token validation enabled",
			"issuer", oauthCfg.IssuerURL,
			"audience", oauthCfg.Audience,
		)
	} else if cfg.Auth != nil && len(cfg.Auth.ValidKeys) > 0 {
		middlewares = append(middlewares, middleware.Auth(middleware.AuthConfig{
			Header:    cfg.Auth.Header,
			ValidKeys: cfg.Auth.ValidKeys,
		}))
		logger.Info("API key auth enabled")
	} else {
		logger.Warn("no authentication configured — gateway is open")
	}

	if cfg.RateLimit != nil {
		middlewares = append(middlewares, middleware.RateLimit(middleware.RateLimitConfig{
			RequestsPerSecond: cfg.RateLimit.RequestsPerSecond,
			Burst:             cfg.RateLimit.Burst,
		}))
	}

	var handler http.Handler = gw.Handler()
	if len(middlewares) > 0 {
		handler = middleware.Chain(middlewares...)(handler)
	}

	// Main MCP server
	mainMux := http.NewServeMux()

	// Protected Resource Metadata (RFC 9728) — unauthenticated, clients need this to discover auth
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
		mainMux.Handle("/.well-known/oauth-protected-resource", auth.DiscoveryHandler(meta))
		logger.Info("serving Protected Resource Metadata", "path", "/.well-known/oauth-protected-resource")
	}

	mainMux.Handle("/mcp", handler)
	mainMux.Handle("/mcp/", handler)

	mainServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mainMux,
	}

	// Admin server
	adminAPI := admin.NewAPI(func() any { return gw.Status() })
	adminServer := &http.Server{
		Addr:    cfg.AdminAddr,
		Handler: adminAPI.Handler(),
	}

	// Start servers
	errCh := make(chan error, 2)

	go func() {
		ln, err := net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			errCh <- fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
			return
		}
		logger.Info("MCP gateway listening", "addr", ln.Addr().String())
		errCh <- mainServer.Serve(ln)
	}()

	go func() {
		ln, err := net.Listen("tcp", cfg.AdminAddr)
		if err != nil {
			errCh <- fmt.Errorf("listen %s: %w", cfg.AdminAddr, err)
			return
		}
		logger.Info("admin API listening", "addr", ln.Addr().String())
		errCh <- adminServer.Serve(ln)
	}()

	// Wait for shutdown signal or error
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

	// Graceful shutdown
	logger.Info("shutting down...")
	shutdownTimeout := cfg.ShutdownTimeout.Duration()
	if shutdownTimeout == 0 {
		shutdownTimeout = 10 * time.Second
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	mainServer.Shutdown(shutdownCtx)  //nolint:errcheck
	adminServer.Shutdown(shutdownCtx) //nolint:errcheck
	gw.Close()

	logger.Info("shutdown complete")
}

// buildAuditLogger creates an audit.Logger from the audit config section.
// If audit logging is disabled or not configured, a no-op logger is returned.
func buildAuditLogger(cfg *config.Config, logger *slog.Logger) *audit.Logger {
	if cfg.Audit == nil || !cfg.Audit.Enabled {
		return audit.Noop()
	}

	output := cfg.Audit.Output
	if output == "" || output == "stderr" {
		logger.Info("audit logging enabled", "output", "stderr")
		return audit.New(os.Stderr)
	}

	if output == "stdout" {
		logger.Info("audit logging enabled", "output", "stdout")
		return audit.New(os.Stdout)
	}

	// Treat as a file path.
	f, err := os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		logger.Error("failed to open audit log file — falling back to stderr",
			"path", output, "error", err)
		return audit.New(os.Stderr)
	}

	// Note: the file is intentionally not closed here because the process
	// lifetime == file lifetime. The OS will close it on exit.
	logger.Info("audit logging enabled", "output", output)

	// Tee to stderr as well so operators see something, and to the file for
	// machine consumption. Remove the tee if you want silent file-only logging.
	return audit.New(io.MultiWriter(f, os.Stderr))
}
