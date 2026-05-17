// Package main is the entry point for the Prism MCP gateway.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/adminauth"
	"github.com/1broseidon/prism/internal/audit"
	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/authserver"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/gateway"
	"github.com/1broseidon/prism/internal/metrics"
	"github.com/1broseidon/prism/internal/middleware"
	"github.com/1broseidon/prism/internal/store"
	"github.com/1broseidon/prism/internal/telemetry"
)

func main() {
	// Dispatch subcommands: serve (default), service.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "service":
			runService(os.Args[2:])
			return
		case "serve":
			os.Args = append(os.Args[:1], os.Args[2:]...) // strip "serve" for flag.Parse
		}
	}

	runServe()
}

func runServe() {
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	shutdownTracer := telemetry.Init("prism", logger)
	defer func() { _ = shutdownTracer(context.Background()) }()

	metrics.Init()

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		return
	}

	// Write PID file for service management.
	if writeErr := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644); writeErr != nil { //nolint:gosec // pid file is not sensitive
		logger.Warn("failed to write pid file", "error", writeErr)
	}
	defer func() { _ = os.Remove(pidFile) }()

	logger.Info("loaded config",
		"listen", cfg.Listen,
		"admin", cfg.Admin,
		"servers", len(cfg.Servers),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gw := setupGateway(logger)
	defer gw.Close()

	// Open the KV store for persisting DCR clients and refresh tokens.
	kvStore := openStore(cfg, logger)
	defer func() { _ = kvStore.Close() }()

	// Give the gateway access to the KV store for persisting runtime configs.
	gw.SetStore(kvStore)
	initWorkspaceBridge(gw, kvStore, logger)
	localBridge, err := configureStdioSpawning(ctx, cfg, gw, logger)
	if err != nil {
		logger.Error("failed to configure stdio spawning", "error", err)
		return
	}
	defer localBridge.Stop()
	gw.LoadPersistedCredentials()

	// Network settings: prefer KV (set via the Settings page) over file config.
	gatewayPublicURL := cfg.PublicURL
	if ns, nsErr := gateway.LoadNetworkSettings(kvStore); nsErr != nil {
		logger.Warn("failed to load network settings from KV", "error", nsErr)
	} else {
		// Seed from raw file values only — empty means "auto-derive".
		// The UI shows what the operator explicitly pinned.
		if ns.PublicURL == "" {
			ns.PublicURL = cfg.PublicURLConfigured
		}
		if ns.AdminPublicURL == "" {
			ns.AdminPublicURL = cfg.AdminPublicURLConfigured
		}
		gw.SetNetworkSettings(ns)
		gatewayPublicURL = ns.PublicURL
		if gatewayPublicURL == "" {
			gatewayPublicURL = cfg.PublicURL
		}
	}

	// Initialize OAuth client support for upstream MCP servers that require authentication.
	// This must happen after SetStore (needs KV for persisted tokens) and before LoadPersistedBackends
	// (so OAuth credentials are registered before backends try to connect).
	// Pass the *configured* admin URL (empty when unset). When empty, OAuth
	// callbacks derive from the inbound admin request's Host header so the
	// flow returns to the same host the operator hit.
	oauthCallback := gw.SetupOAuth(cfg.AdminPublicURLConfigured)

	connectConfigBackends(ctx, gw, cfg, logger)
	gw.LoadPersistedBackends(ctx)

	// Always start the embedded auth server — agents connect via OAuth DCR.
	if cfg.EmbeddedAuth == nil {
		cfg.EmbeddedAuth = &config.EmbeddedAuthConfig{
			TokenTTLSeconds: 3600,
			RequiredScopes:  []string{"mcp:connect"},
		}
	}
	authSrv, authJWKS := setupEmbeddedAuth(cfg, kvStore, logger, gatewayPublicURL)

	resourceURI := mcpResourceURL(gatewayPublicURL)
	handler := buildHandler(cfg, gw, authJWKS, authSrv, logger, resourceURI)
	mainMux := buildMux(cfg, handler, authSrv, logger, resourceURI, gw.WorkspaceBridgeHandler())

	mainServer := &http.Server{
		Handler:           mainMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	auditor := buildAuditLogger(cfg, logger)
	auditor.SetStore(kvStore)
	if cfg.Audit != nil && cfg.Audit.RetentionDays > 0 {
		auditor.SetRetention(time.Duration(cfg.Audit.RetentionDays) * 24 * time.Hour)
	}
	auditor.LoadPersistedEntries()
	auditor.Cleanup() // clean up old entries on startup
	// Run cleanup every hour in the background.
	go func() {
		for range time.Tick(1 * time.Hour) {
			auditor.Cleanup()
		}
	}()
	gw.SetAuditLogger(auditor)

	// Live policy resolver: scope enforcement reads from KV store on every tool call,
	// bypassing stale MCP session context. Cached 5 seconds to limit KV reads.
	policyResolver := auth.NewCachedPolicyResolver(authSrv, 5*time.Second)
	gw.SetPolicyResolver(policyResolver)
	// Backend policy resolver: drives the stacked workspace selection at
	// call time. Shares the same authserver as the scope resolver.
	gw.SetBackendPolicyResolver(authSrv)

	// Build admin API with agent/audit adapters.
	agentsFn := func() []any {
		agents := authSrv.ListAgents()
		result := make([]any, len(agents))
		for i := range agents {
			result[i] = agents[i]
		}
		return result
	}
	eventsFn := func() []any {
		entries := auditor.Recent()
		result := make([]any, len(entries))
		for i := range entries {
			result[i] = entries[i]
		}
		return result
	}
	removeFn := func(id string) bool {
		return authSrv.RemoveAgent(id)
	}
	removeStaleFn := func() int {
		return authSrv.RemoveStaleAgents(7 * 24 * time.Hour)
	}
	agentMgr := &authServerAgentManager{srv: authSrv}
	groupMgr := &authServerGroupManager{srv: authSrv}

	adminAuthHolder, err := initAdminAuth(ctx, cfg.AdminAuth, kvStore, logger)
	if err != nil {
		logger.Error("admin auth init failed", "error", err)
		_ = os.Remove(pidFile)
		return
	}

	adminAPI := admin.NewAPI(func() any { return gw.Status() }, gw, agentsFn, removeFn, removeStaleFn, eventsFn, agentMgr, groupMgr, oauthCallback, adminAuthHolder)
	adminAPI.SetBackendPolicyTraceProvider(&backendPolicyTraceProvider{gw: gw, srv: authSrv})
	adminServer := &http.Server{
		Handler:           adminAPI.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	startServers(cfg, mainServer, adminServer, logger, errCh)
	printStartupBanner(cfg, logger)
	waitForShutdown(cfg, *configPath, mainServer, adminServer, gw, authSrv, logger, errCh)
}

// initAdminAuth constructs the admin auth Holder. KV-persisted state takes
// precedence over the file config so console mutations survive restarts.
// File config is used only on first boot — to seed KV — keeping a config
// file workflow viable for operators who prefer it.
func initAdminAuth(ctx context.Context, fileCfg *config.AdminAuthConfig, kv store.Store, logger *slog.Logger) (*adminauth.Holder, error) {
	holder := adminauth.NewHolder(kv, logger)
	// Periodically purge expired sessions and login attempts. Goroutine
	// exits when ctx is canceled at process shutdown.
	holder.StartSweeper(ctx)
	st, err := adminauth.LoadState(kv)
	if err != nil {
		return nil, err
	}
	// Seed KV from file config on first boot.
	if st.Config == nil && fileCfg != nil {
		st.Config = fileCfg
		st.Enabled = true
		if err := adminauth.SaveState(kv, st); err != nil {
			logger.Warn("seed admin auth state failed", "error", err)
		}
	}
	if st.Config != nil {
		changed, err := syncAdminAuthRedirectFromNetwork(kv, st.Config)
		if err != nil {
			logger.Warn("sync admin auth redirect from network settings failed", "error", err)
		} else if changed {
			if err := adminauth.SaveState(kv, st); err != nil {
				logger.Warn("persist synced admin auth redirect failed", "error", err)
			} else {
				logger.Info("synced admin auth redirect from network settings", "redirect_url", st.Config.RedirectURL)
			}
		}
	}
	if st.Enabled && st.Config != nil {
		config.ApplyAdminAuthDefaults(st.Config)
		if err := holder.Reload(ctx, st.Config); err != nil {
			// Don't refuse to start: keep auth disabled so the operator can
			// log in via the open console and fix the broken config.
			logger.Error("admin auth disabled: discovery failed at boot", "error", err)
		} else {
			logger.Info("admin auth enabled", "issuer", st.Config.Issuer)
			return holder, nil
		}
	}
	logger.Info("admin auth disabled — running open")
	return holder, nil
}

func initWorkspaceBridge(gw *gateway.Gateway, kv store.Store, logger *slog.Logger) {
	if workspaceErr := gw.InitWorkspaceBridge(kv, os.Getenv("PRISM_WORKSPACE_TOKEN")); workspaceErr != nil {
		logger.Warn("failed to initialize workspace bridge settings", "error", workspaceErr)
	}
}

func syncAdminAuthRedirectFromNetwork(kv store.Store, cfg *config.AdminAuthConfig) (bool, error) {
	if cfg == nil {
		return false, nil
	}
	network, err := gateway.LoadNetworkSettings(kv)
	if err != nil {
		return false, err
	}
	if network.AdminPublicURL == "" {
		return false, nil
	}
	redirectURL := strings.TrimRight(network.AdminPublicURL, "/") + "/auth/callback"
	if cfg.RedirectURL == redirectURL {
		return false, nil
	}
	cfg.RedirectURL = redirectURL
	return true, nil
}

// setupGateway creates the gateway.
// Audit logger is wired separately in main() so it can be shared with the admin API.
func setupGateway(logger *slog.Logger) *gateway.Gateway {
	return gateway.New(logger)
}

func connectConfigBackends(ctx context.Context, gw *gateway.Gateway, cfg *config.Loaded, logger *slog.Logger) {
	for i := range cfg.Servers {
		gw.ApplyPersistedBackendSettings(&cfg.Servers[i])
		if !cfg.Servers[i].Enabled {
			logger.Info("skipping disabled backend", "id", cfg.Servers[i].ID)
			continue
		}
		if err := gw.ConnectBackendViaBridge(ctx, &cfg.Servers[i]); err != nil {
			logger.Error("failed to connect backend", "id", cfg.Servers[i].ID, "error", err)
		}
	}
}

// setupEmbeddedAuth creates an in-process OAuth 2.1 authorization server
// from the policy.agents config. Returns the server and its JWKS bytes
// (for pre-seeding the token validator).
func setupEmbeddedAuth(cfg *config.Loaded, kvStore store.Store, logger *slog.Logger, publicURL string) (srv *authserver.Server, jwksData []byte) {
	ea := cfg.EmbeddedAuth

	// Issuer must equal the externally-reachable URL — discovery docs and
	// JWT `iss` claims advertise it, and DCR clients use it to derive the
	// token/authorize/register endpoints. Pinned at startup; runtime changes
	// to public_url require a restart so existing tokens stay verifiable.
	issuer := strings.TrimRight(publicURL, "/")
	if issuer == "" {
		issuer = "http://localhost" + cfg.Listen
	}
	ea.Issuer = issuer

	// Convert embedded clients to authserver clients.
	clients := make([]authserver.ClientConfig, len(ea.Clients))
	for i, c := range ea.Clients {
		clients[i] = authserver.ClientConfig{
			ClientID:      c.ClientID,
			ClientSecret:  c.ClientSecret,
			AllowedScopes: c.AllowedScopes,
		}
	}

	authCfg := &authserver.Config{
		Issuer:          issuer,
		Clients:         clients,
		TokenTTLSeconds: ea.TokenTTLSeconds,
		DefaultScopes:   ea.DefaultScopes,
	}

	// Convert config group definitions into authserver GroupConfig for policy resolution.
	var groups map[string]authserver.GroupConfig
	if ea.Groups != nil {
		groups = make(map[string]authserver.GroupConfig, len(ea.Groups))
		for name, g := range ea.Groups {
			groups[name] = authserver.GroupConfig{Scopes: g.Scopes}
		}
	}

	keyPath := ensureSigningKey(logger)
	km, err := authserver.NewKeyManager(keyPath)
	if err != nil {
		logger.Error("failed to initialize embedded auth signing key", "error", err)
		os.Exit(1)
	}

	srv = authserver.NewServer(authCfg, km, kvStore, logger, groups)
	jwksData = km.JWKS()

	logger.Info("embedded auth server enabled",
		"issuer", issuer,
		"agents", len(clients),
	)

	return srv, jwksData
}

// authServerAgentManager adapts authserver.Server to the admin.AgentManager interface.
type authServerAgentManager struct {
	srv *authserver.Server
}

func (m *authServerAgentManager) ListAgents() []any {
	agents := m.srv.ListAgents()
	result := make([]any, len(agents))
	for i := range agents {
		result[i] = agents[i]
	}
	return result
}

func (m *authServerAgentManager) GetAgentByPrismID(prismID string) any {
	return m.srv.GetAgentByPrismID(prismID)
}

func (m *authServerAgentManager) SetAgentPolicy(prismID string, groups, grant, deny []string) error {
	// Preserve any existing BackendPolicies — the scope-policy endpoint
	// shouldn't reset per-backend rules that were set independently.
	existing, _ := m.srv.GetAgentPolicy(prismID)
	policy := &authserver.AgentPolicy{
		Groups: groups,
		Grant:  grant,
		Deny:   deny,
	}
	if existing != nil {
		policy.BackendPolicies = existing.BackendPolicies
	}
	return m.srv.SetAgentPolicy(prismID, policy)
}

func (m *authServerAgentManager) SetAgentBackendPolicies(prismID string, policies map[string]auth.BackendPolicy) error {
	existing, _ := m.srv.GetAgentPolicy(prismID)
	policy := &authserver.AgentPolicy{}
	if existing != nil {
		policy = existing
	}
	if len(policies) == 0 {
		policy.BackendPolicies = nil
	} else {
		policy.BackendPolicies = policies
	}
	return m.srv.SetAgentPolicy(prismID, policy)
}

func (m *authServerAgentManager) DeleteAgentPolicy(prismID string) error {
	return m.srv.DeleteAgentPolicy(prismID)
}

func (m *authServerAgentManager) RemoveAgent(clientID string) bool {
	return m.srv.RemoveAgent(clientID)
}

func (m *authServerAgentManager) RemoveStaleAgents() int {
	return m.srv.RemoveStaleAgents(7 * 24 * time.Hour)
}

// backendPolicyTraceProvider implements admin.BackendPolicyTraceProvider by
// synthesizing a Claims for the agent (PrismID + ClientID, plus persisted
// policy.Groups so the group tier participates) and asking the gateway to
// preview resolution across all known backends.
type backendPolicyTraceProvider struct {
	gw  *gateway.Gateway
	srv *authserver.Server
}

func (p *backendPolicyTraceProvider) AgentStorageResolutions(prismID string) []admin.AgentStorageResolution {
	if p.gw == nil || p.srv == nil {
		return nil
	}
	claims := &auth.Claims{PrismID: prismID}
	if a := p.srv.GetAgentByPrismID(prismID); a != nil {
		claims.ClientID = a.ClientID
	}
	if pol, err := p.srv.GetAgentPolicy(prismID); err == nil && pol != nil {
		claims.Groups = append(claims.Groups, pol.Groups...)
	}
	gateways := p.gw.PreviewAgentBackendResolutions(claims)
	out := make([]admin.AgentStorageResolution, 0, len(gateways))
	for _, g := range gateways {
		layers := make([]admin.AgentStorageResolutionLayer, 0, len(g.Layers))
		for _, l := range g.Layers {
			layers = append(layers, admin.AgentStorageResolutionLayer{
				Source:   l.Source,
				Selector: l.Selector,
			})
		}
		out = append(out, admin.AgentStorageResolution{
			BackendID:   g.BackendID,
			WorkspaceID: g.WorkspaceID,
			Selector:    g.Selector,
			Source:      g.Source,
			Layers:      layers,
			DenyReason:  g.DenyReason,
		})
	}
	return out
}

// authServerGroupManager adapts authserver.Server to the admin.GroupManager interface.
type authServerGroupManager struct {
	srv *authserver.Server
}

func (m *authServerGroupManager) ListGroups() []admin.GroupInfo {
	serverGroups := m.srv.ListGroups()
	result := make([]admin.GroupInfo, len(serverGroups))
	for i, g := range serverGroups {
		result[i] = admin.GroupInfo{
			Name:            g.Name,
			Scopes:          g.Scopes,
			Source:          g.Source,
			BackendPolicies: g.BackendPolicies,
		}
	}
	return result
}

func (m *authServerGroupManager) GetGroup(name string) *admin.GroupInfo {
	groups := m.srv.ListGroups()
	for _, g := range groups {
		if g.Name == name {
			return &admin.GroupInfo{
				Name:            g.Name,
				Scopes:          g.Scopes,
				Source:          g.Source,
				BackendPolicies: g.BackendPolicies,
			}
		}
	}
	return nil
}

func (m *authServerGroupManager) SetGroup(name string, scopes []string) error {
	// Reject edits to config-defined groups.
	if m.srv.IsConfigGroup(name) {
		return fmt.Errorf("cannot modify config-defined group %q", name)
	}
	// Preserve existing BackendPolicies so the scope endpoint doesn't reset them.
	existing, _ := m.srv.GetGroup(name)
	cfg := &authserver.GroupConfig{Scopes: scopes}
	if existing != nil {
		cfg.BackendPolicies = existing.BackendPolicies
	}
	return m.srv.SetGroup(name, cfg)
}

func (m *authServerGroupManager) SetGroupBackendPolicies(name string, policies map[string]auth.BackendPolicy) error {
	if m.srv.IsConfigGroup(name) {
		return fmt.Errorf("cannot modify config-defined group %q", name)
	}
	existing, _ := m.srv.GetGroup(name)
	if existing == nil {
		// Creating a group via backend-policies-only is fine; start with empty scopes.
		existing = &authserver.GroupConfig{}
	}
	if len(policies) == 0 {
		existing.BackendPolicies = nil
	} else {
		existing.BackendPolicies = policies
	}
	return m.srv.SetGroup(name, existing)
}

func (m *authServerGroupManager) DeleteGroup(name string) error {
	// Reject deletion of config-defined groups.
	if m.srv.IsConfigGroup(name) {
		return fmt.Errorf("cannot delete config-defined group %q", name)
	}
	return m.srv.DeleteGroup(name)
}

func (m *authServerGroupManager) DefaultScopes() []string {
	return m.srv.DefaultScopes()
}

func (m *authServerGroupManager) SetDefaultScopes(scopes []string) error {
	return m.srv.SetDefaultScopes(scopes)
}

func (m *authServerGroupManager) DefaultBackendPolicies() map[string]auth.BackendPolicy {
	return m.srv.DefaultBackendPolicies()
}

func (m *authServerGroupManager) SetDefaultBackendPolicies(policies map[string]auth.BackendPolicy) error {
	return m.srv.SetDefaultBackendPolicies(policies)
}

// buildHandler wraps the gateway handler with auth and rate-limit middleware.
func buildHandler(cfg *config.Loaded, gw *gateway.Gateway, authJWKS []byte, authSrv *authserver.Server, logger *slog.Logger, resourceURI string) http.Handler {
	var middlewares []middleware.Middleware

	ea := cfg.EmbeddedAuth
	genChecker := auth.NewCachedGenerationChecker(authSrv, 5*time.Second)
	validator := auth.NewTokenValidator(&auth.TokenValidatorConfig{
		IssuerURL:         ea.Issuer,
		Audience:          ea.Issuer,
		StaticJWKS:        authJWKS,
		RequiredScopes:    ea.RequiredScopes,
		GenerationChecker: genChecker,
	})

	logger.Info("OAuth 2.1 token validation enabled", "issuer", ea.Issuer)
	middlewares = append(middlewares, auth.Middleware(validator, resourceURI))

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

// buildMux creates the HTTP mux with the MCP handler and auth endpoints.
func buildMux(cfg *config.Loaded, handler http.Handler, authSrv *authserver.Server, logger *slog.Logger, resourceURI string, workspaceHandler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()

	// Mount embedded auth endpoints if present.
	if authSrv != nil {
		authRoutes := authSrv.Routes()
		mux.Handle("POST /token", authRoutes)
		mux.Handle("GET /authorize", authRoutes)
		mux.Handle("POST /authorize", authRoutes)
		mux.Handle("POST /register", authRoutes)
		mux.Handle("GET /.well-known/jwks.json", authRoutes)
		mux.Handle("GET /.well-known/oauth-authorization-server", authRoutes)
		logger.Info("mounted auth endpoints", "paths", "/token, /authorize, /register, /.well-known/*")
	}

	// Protected Resource Metadata (RFC 9728) — tells MCP clients where to authenticate.
	meta := &auth.ProtectedResourceMetadata{
		Resource:               resourceURI,
		AuthorizationServers:   []string{cfg.EmbeddedAuth.Issuer},
		ScopesSupported:        cfg.EmbeddedAuth.ScopesSupported,
		BearerMethodsSupported: []string{"header"},
	}
	resourceMetadataHandler := auth.DiscoveryHandler(meta)
	mux.Handle("/.well-known/oauth-protected-resource", resourceMetadataHandler)
	mux.Handle("/.well-known/oauth-protected-resource/mcp", resourceMetadataHandler)
	mux.Handle("/.well-known/oauth-protected-resource/mcp/", resourceMetadataHandler)

	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)
	if workspaceHandler != nil {
		mux.Handle("/workspace/", workspaceHandler)
	}

	// Catch-all: return JSON 404 for any unmatched path.
	// MCP clients (Claude Code) expect JSON responses and fail to parse plain-text 404s.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	})

	return mux
}

func mcpResourceURL(publicURL string) string {
	base := strings.TrimRight(publicURL, "/")
	if base == "" {
		return ""
	}
	return base + "/mcp"
}

// startServers launches the main and admin HTTP servers.
func startServers(cfg *config.Loaded, mainSrv, adminSrv *http.Server, logger *slog.Logger, errCh chan<- error) {
	lc := net.ListenConfig{}
	ctx := context.Background()

	go func() {
		ln, err := lc.Listen(ctx, "tcp", cfg.Listen)
		if err != nil {
			errCh <- fmt.Errorf("listen %s: %w", cfg.Listen, err)
			return
		}
		if cfg.TLS != nil {
			logger.Info("MCP gateway listening (TLS)", "addr", ln.Addr().String())
			errCh <- mainSrv.ServeTLS(ln, cfg.TLS.Cert, cfg.TLS.Key)
		} else {
			logger.Info("MCP gateway listening", "addr", ln.Addr().String())
			errCh <- mainSrv.Serve(ln)
		}
	}()

	go func() {
		ln, err := lc.Listen(ctx, "tcp", cfg.Admin)
		if err != nil {
			errCh <- fmt.Errorf("listen %s: %w", cfg.Admin, err)
			return
		}
		logger.Info("admin API listening", "addr", ln.Addr().String())
		errCh <- adminSrv.Serve(ln)
	}()
}

// printStartupBanner prints the ready message with connection instructions.
func printStartupBanner(cfg *config.Loaded, logger *slog.Logger) {
	// Build listen URL for display.
	scheme := "http"
	if cfg.TLS != nil {
		scheme = "https"
	}
	host := "localhost"
	port := strings.TrimPrefix(cfg.Listen, ":")
	url := fmt.Sprintf("%s://%s:%s/mcp", scheme, host, port)
	tokenURL := fmt.Sprintf("%s://%s:%s/token", scheme, host, port)

	logger.Info("prism ready",
		"backends", len(cfg.Servers),
		"url", url,
	)

	if cfg.EmbeddedAuth != nil {
		fmt.Fprintf(os.Stderr, "\n  Get a token:  curl -s -X POST %s -d \"grant_type=client_credentials&client_id=AGENT&client_secret=SECRET\"\n", tokenURL)
		fmt.Fprintf(os.Stderr, "\n  Claude Code (~/.claude/mcp_servers.json):\n")
		fmt.Fprintf(os.Stderr, "  { \"prism\": { \"type\": \"streamable-http\", \"url\": \"%s\", \"headers\": { \"Authorization\": \"Bearer TOKEN\" } } }\n\n", url)
	} else {
		fmt.Fprintf(os.Stderr, "\n  Claude Code (~/.claude/mcp_servers.json):\n")
		fmt.Fprintf(os.Stderr, "  { \"prism\": { \"type\": \"streamable-http\", \"url\": \"%s\" } }\n\n", url)
	}
}

// waitForShutdown blocks until a signal or server error, then gracefully shuts down.
// On SIGHUP, reloads config and hot-adds/removes backends.
func waitForShutdown(cfg *config.Loaded, configPath string, mainServer, adminServer *http.Server, gw *gateway.Gateway, authSrv *authserver.Server, logger *slog.Logger, errCh <-chan error) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGHUP {
				logger.Info("SIGHUP received — reloading config")
				reloadConfig(configPath, gw, authSrv, logger)
				continue
			}
			logger.Info("received signal", "signal", sig)
		case err := <-errCh:
			logger.Error("server error", "error", err)
		}
		break
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

// reloadConfig re-reads the config file, reloads policy (static clients, groups,
// default_scopes) on the auth server, and diffs mcpServers for backend add/remove.
// Dynamic (DCR) clients and refresh tokens are preserved across reloads.
func reloadConfig(configPath string, gw *gateway.Gateway, authSrv *authserver.Server, logger *slog.Logger) { //nolint:gocyclo // reload intentionally keeps policy and backend diffing together.
	newCfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("reload failed — keeping current config", "error", err)
		return
	}

	// --- Reload policy on the auth server ---
	if newCfg.EmbeddedAuth != nil && authSrv != nil {
		ea := newCfg.EmbeddedAuth

		// Convert embedded clients to authserver clients (same as setupEmbeddedAuth).
		staticClients := make([]authserver.ClientConfig, len(ea.Clients))
		for i, c := range ea.Clients {
			staticClients[i] = authserver.ClientConfig{
				ClientID:      c.ClientID,
				ClientSecret:  c.ClientSecret,
				AllowedScopes: c.AllowedScopes,
			}
		}

		// Convert config group definitions to authserver GroupConfig.
		var groups map[string]authserver.GroupConfig
		if ea.Groups != nil {
			groups = make(map[string]authserver.GroupConfig, len(ea.Groups))
			for name, g := range ea.Groups {
				groups[name] = authserver.GroupConfig{Scopes: g.Scopes}
			}
		}

		authSrv.ReloadPolicy(staticClients, groups, ea.DefaultScopes)
	}

	// --- Diff mcpServers for backend add/remove ---

	// Build sets of current and new backend IDs.
	currentIDs := make(map[string]struct{})
	for _, id := range gw.BackendIDs() {
		currentIDs[id] = struct{}{}
	}

	newIDs := make(map[string]struct{}, len(newCfg.Servers))
	for i := range newCfg.Servers {
		gw.ApplyPersistedBackendSettings(&newCfg.Servers[i])
		if newCfg.Servers[i].Enabled {
			newIDs[newCfg.Servers[i].ID] = struct{}{}
		}
	}

	// Remove backends that no longer exist in config or are now disabled.
	for id := range currentIDs {
		if _, ok := newIDs[id]; !ok {
			if err := gw.DisconnectBackend(id); err != nil {
				logger.Error("reload: failed to remove backend", "id", id, "error", err)
			} else {
				logger.Info("reload: removed backend", "id", id)
			}
		}
	}

	// Add backends that are new in config.
	ctx := context.Background()
	for i := range newCfg.Servers {
		s := &newCfg.Servers[i]
		if !s.Enabled {
			continue
		}
		if _, ok := currentIDs[s.ID]; !ok {
			if err := gw.ConnectBackendViaBridge(ctx, s); err != nil {
				logger.Error("reload: failed to add backend", "id", s.ID, "error", err)
			} else {
				logger.Info("reload: added backend", "id", s.ID)
			}
		}
	}

	logger.Info("config reloaded", "backends", len(newCfg.Servers))
}

// buildAuditLogger creates an audit.Logger from the audit config section.
func buildAuditLogger(cfg *config.Loaded, logger *slog.Logger) *audit.Logger {
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

// openStore initializes the KV store based on config.
// Defaults to bbolt at ~/.prism/prism.db. Falls back to in-memory on error.
func openStore(cfg *config.Loaded, logger *slog.Logger) store.Store {
	sc := cfg.Store
	if sc == nil {
		sc = &config.StoreConfig{}
	}

	switch sc.Type {
	case "redis":
		if sc.URL == "" {
			logger.Error("store.type=redis requires store.url")
			os.Exit(1)
		}
		s, err := store.NewRedisStore(sc.URL)
		if err != nil {
			logger.Error("failed to connect to Redis", "error", err)
			os.Exit(1)
		}
		logger.Info("store: redis", "url", sc.URL)
		return s

	default: // "bbolt" or empty
		path := sc.Path
		if path == "" {
			if dataDir := strings.TrimSpace(os.Getenv("PRISM_DATA_DIR")); dataDir != "" {
				path = filepath.Join(dataDir, "prism.db")
			} else {
				home, err := os.UserHomeDir()
				if err != nil {
					logger.Warn("cannot determine home dir for store — using temp dir", "error", err)
					home = os.TempDir()
				}
				path = filepath.Join(home, ".prism", "prism.db")
			}
		}
		s, err := store.NewBoltStore(path)
		if err != nil {
			logger.Warn("failed to open bbolt store — state will not persist", "error", err, "path", path)
			return store.NewMemoryStore()
		}
		logger.Info("store: bbolt", "path", path)
		return s
	}
}

// ensureSigningKey returns the path to a persistent RSA signing key.
// On first run, generates a key at $PRISM_SIGNING_KEY_FILE or
// ~/.prism/signing-key.pem.
// On subsequent runs, reuses it — tokens survive restarts.
func ensureSigningKey(logger *slog.Logger) string {
	keyPath := strings.TrimSpace(os.Getenv("PRISM_SIGNING_KEY_FILE"))
	if keyPath == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			logger.Warn("cannot determine home dir — using ephemeral signing key", "error", homeErr)
			return ""
		}
		keyPath = filepath.Join(home, ".prism", "signing-key.pem")
	}
	dir := filepath.Dir(keyPath)

	// Already exists — reuse it.
	if _, statErr := os.Stat(keyPath); statErr == nil { //nolint:gosec // operator-configured key path
		return keyPath
	}

	// Create dir and generate key.
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil { //nolint:gosec // operator-configured key path
		logger.Warn("cannot create ~/.prism — using ephemeral signing key", "error", mkErr)
		return ""
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		logger.Warn("cannot generate signing key — using ephemeral key", "error", err)
		return ""
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil { //nolint:gosec // operator-configured key path
		logger.Warn("cannot write signing key — using ephemeral key", "error", err)
		return ""
	}

	logger.Info("generated persistent signing key", "path", keyPath)
	return keyPath
}
