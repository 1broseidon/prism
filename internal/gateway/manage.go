package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/credentials"
	"github.com/1broseidon/prism/internal/store"
)

type bridgeSpawnResponse struct {
	ID       string   `json:"id"`
	Endpoint string   `json:"endpoint"`
	Tools    []string `json:"tools"`
	Status   string   `json:"status"`
}

type bridgeSpawnResult struct {
	Endpoint string
	Reused   bool
}

func normalizeBridgeURLs(urls []string) []string {
	seen := make(map[string]bool, len(urls))
	result := make([]string, 0, len(urls))
	for _, raw := range urls {
		u := strings.TrimRight(strings.TrimSpace(raw), "/")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		result = append(result, u)
	}
	return result
}

func (g *Gateway) bridgeURLsForBackend(id string) []string {
	urls := g.BridgeURLs()
	if len(urls) <= 1 {
		return urls
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	start := int(h.Sum32() % uint32(len(urls))) //nolint:gosec // len(urls) is operator config and tiny in practice
	ordered := make([]string, 0, len(urls))
	ordered = append(ordered, urls[start:]...)
	ordered = append(ordered, urls[:start]...)
	return ordered
}

func (g *Gateway) stdioUnavailableError() error {
	if g.stdioDisabled == "" {
		return nil
	}
	return fmt.Errorf("stdio MCP servers are unavailable: %s", g.stdioDisabled)
}

// AddBackend connects a new backend at runtime and registers its tools.
// Connected clients are automatically notified of the tool list change.
func (g *Gateway) AddBackend(ctx context.Context, id string, cfg admin.BackendConfig) error { //nolint:gocritic // intentional value receive — config is mutated locally
	g.mu.RLock()
	_, exists := g.backends[id]
	g.mu.RUnlock()
	if exists {
		return fmt.Errorf("backend %q already exists", id)
	}

	sc := &config.ServerConfig{
		ID:        id,
		Namespace: id,
		Enabled:   true,
		URL:       cfg.URL,
		Env:       cfg.Env,
		Sandbox:   config.NormalizeSandboxConfig(cfg.Sandbox, config.SandboxProfileDefault),
		Workspace: config.NormalizeWorkspaceConfig(cfg.Workspace),
		Timeout:   config.Duration(30 * time.Second),
	}
	if cfg.Enabled != nil {
		sc.Enabled = *cfg.Enabled
	}

	persisted := &persistedBackend{
		Command:   cfg.Command,
		Args:      cfg.Args,
		Env:       cfg.Env,
		URL:       cfg.URL,
		Runtime:   cfg.Runtime,
		Enabled:   boolPtr(sc.Enabled),
		Sandbox:   &sc.Sandbox,
		Workspace: sc.Workspace,
	}
	var bridgeSpawn *bridgeSpawnResult

	// If command contains spaces and args is empty, split it.
	// Handles "npx @brainfile/cli mcp" from UIs that send a single string.
	if cfg.Command != "" && len(cfg.Args) == 0 && strings.Contains(cfg.Command, " ") {
		parts := strings.Fields(cfg.Command)
		cfg.Command = parts[0]
		cfg.Args = parts[1:]
		persisted.Command = cfg.Command
		persisted.Args = cfg.Args
	}

	if !sc.Enabled {
		g.registerAndPersistAdminCredential(id, cfg.Credential)
		g.persistBackend(id, persisted)
		g.logger.Info("persisted disabled backend", "id", id)
		return nil
	}

	if cfg.Command != "" {
		sc.OriginalCommand = append([]string{cfg.Command}, cfg.Args...)
		if g.bridgeURL != "" {
			spawned, err := g.spawnBridgeBackend(ctx, id, cfg.Command, cfg.Args, cfg.Env, cfg.Runtime, &sc.Sandbox, sc.Workspace)
			if err != nil {
				return err
			}
			sc.URL = spawned.Endpoint
			sc.BridgeManaged = true
			sc.BridgeRuntime = cfg.Runtime
			persisted.BridgeManaged = true
			persisted.URL = sc.URL
			bridgeSpawn = &spawned
		} else {
			if err := g.stdioUnavailableError(); err != nil {
				return err
			}
			sc.Command = append([]string{cfg.Command}, cfg.Args...)
		}
	}

	// Register credential before connecting so the InjectingTransport can use it.
	g.registerAndPersistAdminCredential(id, cfg.Credential)

	if err := g.connectBackendWithBridgeRetry(ctx, sc, bridgeSpawn, id, cfg.Command, cfg.Args, cfg.Env, cfg.Runtime, &sc.Sandbox, sc.Workspace); err != nil {
		// Clean up credential if connection failed
		g.credStore.Unregister(id)
		g.deletePersistedCredential(id)
		return err
	}
	persisted.URL = sc.URL

	// Persist backend config for restart survival.
	g.persistBackend(id, persisted)

	return nil
}

func (g *Gateway) registerAndPersistAdminCredential(id string, cfg *admin.CredentialConfig) {
	if cfg != nil && cfg.Type != "" && cfg.Type != "none" {
		cred := buildCredentialFromAdmin(cfg)
		if cred != nil {
			g.credStore.Register(id, cred)
			g.logger.Info("registered runtime credential for backend",
				"id", id,
				"type", cfg.Type,
			)

			// Persist credential config in KV for restart survival.
			g.persistCredential(id, &persistedCredential{
				Type:    cfg.Type,
				Header:  cfg.Header,
				Value:   cfg.Value,
				Env:     cfg.Env,
				Command: cfg.Command,
			})
		}
	}
}

// buildCredentialFromAdmin converts an admin.CredentialConfig into a credentials.Credential.
func buildCredentialFromAdmin(cc *admin.CredentialConfig) credentials.Credential {
	header := cc.Header
	if header == "" {
		header = "Authorization"
	}

	switch cc.Type {
	case "static":
		return &credentials.Static{Header: header, Value: cc.Value}
	case "env":
		return &credentials.Env{Header: header, EnvVar: cc.Env}
	case "command":
		return &credentials.Command{Header: header, Cmd: cc.Command}
	default:
		return nil
	}
}

// RemoveBackend disconnects a backend and removes its tools.
// Connected clients are automatically notified of the tool list change.
func (g *Gateway) RemoveBackend(id string) error {
	g.mu.RLock()
	backend, ok := g.backends[id]
	g.mu.RUnlock()
	if ok {
		if backend.Config.BridgeManaged {
			if err := g.removeBridgeBackend(id); err != nil {
				return err
			}
		}
		return g.DisconnectBackend(id)
	}

	// Not in-memory — check for an orphan KV entry (a backend that was
	// persisted on a previous run but failed to reconnect this run). Without
	// this branch the entry stays in KV forever and keeps logging errors on
	// every restart.
	if g.kvStore == nil {
		return fmt.Errorf("backend %q not found", id)
	}
	data, err := g.kvStore.Get(backendKVPrefix + id)
	if err != nil || data == nil {
		return fmt.Errorf("backend %q not found", id)
	}
	var pb persistedBackend
	if json.Unmarshal(data, &pb) == nil && pb.BridgeManaged {
		if err := g.removeBridgeBackend(id); err != nil {
			g.logger.Warn("failed to remove orphan bridge backend", "id", id, "error", err)
		}
	}
	g.credStore.Unregister(id)
	g.deletePersistedCredential(id)
	g.deletePersistedBackend(id)
	g.cleanupOAuthForBackend(id)
	g.logger.Info("removed orphan persisted backend", "id", id)
	return nil
}

// UpdateBackend updates persisted operational settings without deleting
// credentials or OAuth state. Disabling a backend is a kill switch: tools are
// removed and bridge-managed containers are stopped, but the stored config
// remains available for re-enable.
func (g *Gateway) UpdateBackend(ctx context.Context, id string, update admin.BackendUpdate) error { //nolint:gocyclo // update is the transaction boundary for persist/stop/reconnect/rollback.
	if g.kvStore == nil {
		return fmt.Errorf("backend persistence is not configured")
	}
	pb, connected, err := g.loadOrBuildPersistedBackend(id)
	if err != nil {
		return err
	}
	previous := clonePersistedBackend(pb)
	if live, ok := g.connectedPersistedBackend(id); ok {
		previous = live
	}

	if update.Sandbox != nil {
		sandbox := config.NormalizeSandboxConfig(update.Sandbox, sandboxFallback(pb))
		pb.Sandbox = &sandbox
	}
	if update.Workspace != nil {
		pb.Workspace = config.NormalizeWorkspaceConfig(update.Workspace)
	}
	if update.Enabled != nil {
		pb.Enabled = boolPtr(*update.Enabled)
	}

	enabled := pb.isEnabled()

	if !enabled {
		if connected {
			if err := g.stopConnectedBackendPreservingState(id); err != nil {
				return err
			}
		}
		g.persistBackend(id, pb)
		return nil
	}

	if connected && (update.Sandbox != nil || update.Workspace != nil) && pb.Command != "" {
		if err := g.stopConnectedBackendPreservingState(id); err != nil {
			return err
		}
		connected = false
	}
	if !connected {
		if err := g.connectPersistedBackend(ctx, id, pb); err != nil {
			if previous != nil && previous.isEnabled() {
				g.persistBackend(id, previous)
				rollbackCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				if rollbackErr := g.connectPersistedBackend(rollbackCtx, id, previous); rollbackErr != nil {
					g.logger.Warn("failed to roll back backend settings after reconnect failure",
						"id", id,
						"apply_error", err,
						"error", rollbackErr,
					)
				} else {
					g.logger.Warn("backend settings update failed; restored previous config",
						"id", id,
						"error", err,
					)
				}
				cancel()
			} else {
				g.logger.Warn("backend settings update failed; leaving previous persisted config untouched",
					"id", id,
					"error", err,
				)
			}
			return err
		}
	}
	g.persistBackend(id, pb)
	return nil
}

func clonePersistedBackend(pb *persistedBackend) *persistedBackend {
	if pb == nil {
		return nil
	}
	data, err := json.Marshal(pb)
	if err != nil {
		return nil
	}
	var cloned persistedBackend
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil
	}
	return &cloned
}

func sandboxFallback(pb *persistedBackend) string {
	if pb != nil && pb.Sandbox != nil && pb.Sandbox.Profile != "" {
		return pb.Sandbox.Profile
	}
	return config.SandboxProfileCompat
}

func (g *Gateway) loadOrBuildPersistedBackend(id string) (*persistedBackend, bool, error) {
	var pb persistedBackend
	data, err := g.kvStore.Get(backendKVPrefix + id)
	if err == nil {
		if decodeErr := json.Unmarshal(data, &pb); decodeErr != nil {
			return nil, false, fmt.Errorf("decode persisted backend %q: %w", id, decodeErr)
		}
		connected := g.backendConnected(id)
		return &pb, connected, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, false, fmt.Errorf("read persisted backend %q: %w", id, err)
	}

	g.mu.RLock()
	backend := g.backends[id]
	g.mu.RUnlock()
	if backend == nil {
		return nil, false, fmt.Errorf("backend %q not found", id)
	}
	return persistedFromServerConfig(backend.Config), true, nil
}

func (g *Gateway) backendConnected(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.backends[id]
	return ok
}

func (g *Gateway) connectedPersistedBackend(id string) (*persistedBackend, bool) {
	g.mu.RLock()
	backend := g.backends[id]
	g.mu.RUnlock()
	if backend == nil || backend.Config == nil {
		return nil, false
	}
	return persistedFromServerConfig(backend.Config), true
}

func persistedFromServerConfig(sc *config.ServerConfig) *persistedBackend {
	pb := &persistedBackend{
		URL:           sc.URL,
		Env:           sc.Env,
		BridgeManaged: sc.BridgeManaged,
		Runtime:       sc.BridgeRuntime,
		Enabled:       boolPtr(true),
	}
	sandbox := config.NormalizeSandboxConfig(&sc.Sandbox, config.SandboxProfileDefault)
	pb.Sandbox = &sandbox
	pb.Workspace = config.NormalizeWorkspaceConfig(sc.Workspace)

	command := sc.OriginalCommand
	if len(command) == 0 {
		command = sc.Command
	}
	if len(command) > 0 {
		pb.Command = command[0]
		if len(command) > 1 {
			pb.Args = append([]string(nil), command[1:]...)
		}
	}
	return pb
}

func (g *Gateway) stopConnectedBackendPreservingState(id string) error {
	g.mu.RLock()
	backend := g.backends[id]
	g.mu.RUnlock()
	if backend == nil {
		return nil
	}
	bridgeManaged := backend.Config.BridgeManaged
	if err := g.disconnectBackend(id, true); err != nil {
		return err
	}
	if bridgeManaged {
		return g.removeBridgeBackend(id)
	}
	return nil
}

func (g *Gateway) spawnBridgeBackend(ctx context.Context, id, command string, args []string, env map[string]string, runtime string, sandbox *config.SandboxConfig, workspaceCfg *config.WorkspaceConfig) (bridgeSpawnResult, error) {
	bridgeURLs := g.bridgeURLsForBackend(id)
	if len(bridgeURLs) == 0 {
		return bridgeSpawnResult{}, fmt.Errorf("bridge_url is not configured")
	}
	var firstErr error
	for _, bridgeURL := range bridgeURLs {
		result, err := g.spawnBridgeBackendAt(ctx, bridgeURL, id, command, args, env, runtime, sandbox, workspaceCfg)
		if err == nil {
			return result, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		g.logger.Warn("bridge spawn failed, trying next bridge",
			"id", id,
			"bridge_url", bridgeURL,
			"error", err,
		)
	}
	return bridgeSpawnResult{}, firstErr
}

func (g *Gateway) spawnBridgeBackendAt(ctx context.Context, bridgeURL, id, command string, args []string, env map[string]string, runtime string, sandbox *config.SandboxConfig, workspaceCfg *config.WorkspaceConfig) (bridgeSpawnResult, error) {
	payload := map[string]any{
		"id":      id,
		"command": command,
		"args":    args,
		"env":     env,
	}
	if runtime != "" {
		payload["runtime"] = runtime
	}
	if sandbox != nil {
		payload["sandbox"] = sandbox
	}
	if workspaceCfg != nil {
		snap, err := g.snapshotWorkspaceForBackend(ctx, workspaceCfg)
		if err != nil {
			return bridgeSpawnResult{}, err
		}
		payload["workspace"] = workspaceCfg
		payload["workspace_snapshot"] = snap
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return bridgeSpawnResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bridgeURL+"/manage/spawn", bytes.NewReader(body))
	if err != nil {
		return bridgeSpawnResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return bridgeSpawnResult{}, fmt.Errorf("spawn backend via bridge: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusConflict {
		// 409: backend already exists on the bridge (e.g. gateway restarted but bridge didn't).
		// Treat as success — just connect to the existing endpoint.
		g.logger.Info("bridge backend already exists, reusing", "id", id)
		endpoint := bridgeURL + "/mcp/" + id
		return bridgeSpawnResult{Endpoint: endpoint, Reused: true}, nil
	}
	if resp.StatusCode != http.StatusCreated {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return bridgeSpawnResult{}, fmt.Errorf("bridge spawn failed: status %d payload %v", resp.StatusCode, payload)
	}
	var result bridgeSpawnResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return bridgeSpawnResult{}, fmt.Errorf("decode bridge spawn response: %w", err)
	}
	endpoint := bridgeURL + result.Endpoint
	return bridgeSpawnResult{Endpoint: endpoint}, nil
}

func (g *Gateway) connectBackendWithBridgeRetry(
	ctx context.Context,
	sc *config.ServerConfig,
	spawned *bridgeSpawnResult,
	id, command string,
	args []string,
	env map[string]string,
	runtime string,
	sandbox *config.SandboxConfig,
	workspaceCfg *config.WorkspaceConfig,
) error {
	err := g.ConnectBackend(ctx, sc)
	if err == nil {
		return nil
	}
	if spawned == nil || !sc.BridgeManaged {
		return err
	}
	if !spawned.Reused {
		_ = g.removeBridgeBackend(id)
		return err
	}

	g.logger.Warn("reused bridge backend failed to connect; recreating",
		"id", id,
		"error", err,
	)
	if removeErr := g.removeBridgeBackend(id); removeErr != nil {
		return fmt.Errorf("connect reused bridge backend %q: %w; remove stale bridge backend: %w", id, err, removeErr)
	}

	retry, spawnErr := g.spawnBridgeBackend(ctx, id, command, args, env, runtime, sandbox, workspaceCfg)
	if spawnErr != nil {
		return fmt.Errorf("respawn reused bridge backend %q: %w (original connect: %w)", id, spawnErr, err)
	}
	sc.URL = retry.Endpoint
	if retryErr := g.ConnectBackend(ctx, sc); retryErr != nil {
		_ = g.removeBridgeBackend(id)
		return fmt.Errorf("connect respawned bridge backend %q: %w (original connect: %w)", id, retryErr, err)
	}
	return nil
}

// ConnectBackendViaBridge connects cfg directly, delegating stdio command
// backends to the configured bridge when bridge_url is set. Unlike AddBackend,
// this is for config-defined backends and does not persist runtime state.
func (g *Gateway) ConnectBackendViaBridge(ctx context.Context, cfg *config.ServerConfig) error {
	if cfg == nil || !cfg.IsStdio() || g.bridgeURL == "" {
		if cfg != nil && cfg.IsStdio() {
			if err := g.stdioUnavailableError(); err != nil {
				return err
			}
		}
		return g.ConnectBackend(ctx, cfg)
	}

	bridged := *cfg
	bridged.OriginalCommand = append([]string(nil), cfg.Command...)
	bridged.Command = nil
	bridged.Enabled = true
	bridged.Sandbox = config.NormalizeSandboxConfig(&cfg.Sandbox, config.SandboxProfileDefault)
	bridged.Workspace = config.NormalizeWorkspaceConfig(cfg.Workspace)

	command := cfg.Command[0]
	args := []string(nil)
	if len(cfg.Command) > 1 {
		args = append(args, cfg.Command[1:]...)
	}
	spawned, err := g.spawnBridgeBackend(ctx, cfg.ID, command, args, cfg.Env, cfg.BridgeRuntime, &bridged.Sandbox, bridged.Workspace)
	if err != nil {
		return err
	}
	bridged.URL = spawned.Endpoint
	bridged.BridgeManaged = true

	return g.connectBackendWithBridgeRetry(ctx, &bridged, &spawned, cfg.ID, command, args, cfg.Env, cfg.BridgeRuntime, &bridged.Sandbox, bridged.Workspace)
}

func (g *Gateway) removeBridgeBackend(id string) error {
	bridgeURLs := g.bridgeURLsForBackend(id)
	if len(bridgeURLs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var firstErr error
	for _, bridgeURL := range bridgeURLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, bridgeURL+"/manage/"+id, http.NoBody)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("remove backend via bridge %s: %w", bridgeURL, err)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			var payload map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&payload)
			if firstErr == nil {
				firstErr = fmt.Errorf("bridge delete failed at %s: status %d payload %v", bridgeURL, resp.StatusCode, payload)
			}
		}
		_ = resp.Body.Close()
	}
	return firstErr
}
