package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/config"
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

	return g.ConnectBackend(ctx, sc)
}

// RemoveBackend disconnects a backend and removes its tools.
// Connected clients are automatically notified of the tool list change.
func (g *Gateway) RemoveBackend(id string) error {
	return g.DisconnectBackend(id)
}
