package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/credentials"
)

// AddBackend connects a new backend at runtime and registers its tools.
// Connected clients are automatically notified of the tool list change.
func (g *Gateway) AddBackend(ctx context.Context, id string, cfg admin.BackendConfig) error {
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

	if cfg.Command != "" {
		sc.Command = append([]string{cfg.Command}, cfg.Args...)
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
		// Clean up credential if connection failed
		g.credStore.Unregister(id)
		g.deletePersistedCredential(id)
		return err
	}
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
	return g.DisconnectBackend(id)
}
