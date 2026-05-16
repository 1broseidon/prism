package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/credentials"
)

type bridgeSpawnResponse struct {
	ID       string   `json:"id"`
	Endpoint string   `json:"endpoint"`
	Tools    []string `json:"tools"`
	Status   string   `json:"status"`
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
		URL:       cfg.URL,
		Env:       cfg.Env,
		Timeout:   config.Duration(30 * time.Second),
	}

	persisted := &persistedBackend{
		Command: cfg.Command,
		Args:    cfg.Args,
		Env:     cfg.Env,
		URL:     cfg.URL,
		Runtime: cfg.Runtime,
	}

	// If command contains spaces and args is empty, split it.
	// Handles "npx @brainfile/cli mcp" from UIs that send a single string.
	if cfg.Command != "" && len(cfg.Args) == 0 && strings.Contains(cfg.Command, " ") {
		parts := strings.Fields(cfg.Command)
		cfg.Command = parts[0]
		cfg.Args = parts[1:]
		persisted.Command = cfg.Command
		persisted.Args = cfg.Args
	}

	if cfg.Command != "" {
		sc.OriginalCommand = append([]string{cfg.Command}, cfg.Args...)
		if g.bridgeURL != "" {
			endpoint, err := g.spawnBridgeBackend(ctx, id, cfg.Command, cfg.Args, cfg.Env, cfg.Runtime)
			if err != nil {
				return err
			}
			sc.URL = endpoint
			sc.BridgeManaged = true
			sc.BridgeRuntime = cfg.Runtime
			persisted.URL = endpoint
			persisted.BridgeManaged = true
		} else {
			sc.Command = append([]string{cfg.Command}, cfg.Args...)
		}
	}

	// Register credential before connecting so the InjectingTransport can use it.
	if cfg.Credential != nil && cfg.Credential.Type != "" && cfg.Credential.Type != "none" {
		cred := buildCredentialFromAdmin(cfg.Credential)
		if cred != nil {
			g.credStore.Register(id, cred)
			g.logger.Info("registered runtime credential for backend",
				"id", id,
				"type", cfg.Credential.Type,
			)

			// Persist credential config in KV for restart survival.
			g.persistCredential(id, &persistedCredential{
				Type:    cfg.Credential.Type,
				Header:  cfg.Credential.Header,
				Value:   cfg.Credential.Value,
				Env:     cfg.Credential.Env,
				Command: cfg.Credential.Command,
			})
		}
	}

	if err := g.ConnectBackend(ctx, sc); err != nil {
		if sc.BridgeManaged {
			_ = g.removeBridgeBackend(id)
		}
		// Clean up credential if connection failed
		g.credStore.Unregister(id)
		g.deletePersistedCredential(id)
		return err
	}

	// Persist backend config for restart survival.
	g.persistBackend(id, persisted)

	return nil
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
	if !ok {
		return fmt.Errorf("backend %q not found", id)
	}
	if backend.Config.BridgeManaged {
		if err := g.removeBridgeBackend(id); err != nil {
			return err
		}
	}
	return g.DisconnectBackend(id)
}

func (g *Gateway) spawnBridgeBackend(ctx context.Context, id, command string, args []string, env map[string]string, runtime string) (string, error) {
	if g.bridgeURL == "" {
		return "", fmt.Errorf("bridge_url is not configured")
	}
	payload := map[string]any{
		"id":      id,
		"command": command,
		"args":    args,
		"env":     env,
	}
	if runtime != "" {
		payload["runtime"] = runtime
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.bridgeURL+"/manage/spawn", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("spawn backend via bridge: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusConflict {
		// 409: backend already exists on the bridge (e.g. gateway restarted but bridge didn't).
		// Treat as success — just connect to the existing endpoint.
		g.logger.Info("bridge backend already exists, reusing", "id", id)
		endpoint := strings.TrimRight(g.bridgeURL, "/") + "/mcp/" + id
		return endpoint, nil
	}
	if resp.StatusCode != http.StatusCreated {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return "", fmt.Errorf("bridge spawn failed: status %d payload %v", resp.StatusCode, payload)
	}
	var result bridgeSpawnResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode bridge spawn response: %w", err)
	}
	endpoint := strings.TrimRight(g.bridgeURL, "/") + result.Endpoint
	return endpoint, nil
}

func (g *Gateway) removeBridgeBackend(id string) error {
	if g.bridgeURL == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, g.bridgeURL+"/manage/"+id, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("remove backend via bridge: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return fmt.Errorf("bridge delete failed: status %d payload %v", resp.StatusCode, payload)
	}
	return nil
}
