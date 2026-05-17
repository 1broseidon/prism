package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/1broseidon/prism/internal/config"
	ws "github.com/1broseidon/prism/internal/workspace"
)

func workspaceSnapshotPolicy(cfg *config.WorkspaceConfig) ws.SnapshotPolicy {
	if cfg == nil {
		return ws.SnapshotPolicy{}
	}
	return ws.SnapshotPolicy{
		Include:  cfg.Include,
		Exclude:  cfg.Exclude,
		MaxBytes: cfg.MaxBytes,
	}
}

func (g *Gateway) snapshotWorkspaceForBackend(ctx context.Context, cfg *config.WorkspaceConfig) (*ws.Snapshot, error) {
	cfg = config.NormalizeWorkspaceConfig(cfg)
	if cfg == nil {
		return nil, nil
	}
	snap, err := g.SnapshotWorkspace(ctx, cfg.ID, workspaceSnapshotPolicy(cfg))
	if err != nil {
		return nil, fmt.Errorf("snapshot workspace %q: %w", cfg.ID, err)
	}
	return snap, nil
}

// BackendWorkspaceChanges returns staged sandbox workspace changes for a backend.
func (g *Gateway) BackendWorkspaceChanges(ctx context.Context, id string, refresh bool) (*ws.ChangeSet, error) {
	bridgeURL, err := g.bridgeURLForExistingBackend(id)
	if err != nil {
		return nil, err
	}
	method := http.MethodGet
	path := "/manage/" + id + "/changes"
	if refresh {
		method = http.MethodPost
		path += "/refresh"
	}
	req, err := http.NewRequestWithContext(ctx, method, bridgeURL+path, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("workspace changes via bridge: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return nil, fmt.Errorf("workspace changes failed: status %d payload %v", resp.StatusCode, payload)
	}
	var changes ws.ChangeSet
	if err := json.NewDecoder(resp.Body).Decode(&changes); err != nil {
		return nil, err
	}
	return &changes, nil
}

// ApplyBackendWorkspaceChanges applies staged sandbox changes through the workspace agent.
func (g *Gateway) ApplyBackendWorkspaceChanges(ctx context.Context, id string) (*ws.ApplyResult, error) {
	pb, _, err := g.loadOrBuildPersistedBackend(id)
	if err != nil {
		return nil, err
	}
	cfg := pb.workspaceConfig()
	if cfg == nil {
		return nil, fmt.Errorf("backend %q has no workspace configured", id)
	}
	changes, err := g.BackendWorkspaceChanges(ctx, id, true)
	if err != nil {
		return nil, err
	}
	result, err := g.ApplyWorkspaceChanges(ctx, cfg.ID, workspaceSnapshotPolicy(cfg), changes)
	if err != nil {
		return nil, err
	}
	if len(result.Conflicts) == 0 {
		if err := g.acceptBridgeWorkspaceChanges(ctx, id); err != nil {
			g.logger.Warn("workspace apply succeeded but bridge baseline reset failed", "id", id, "error", err)
		}
	}
	return result, nil
}

// DiscardBackendWorkspaceChanges throws away sandbox changes by rebuilding from a fresh snapshot.
func (g *Gateway) DiscardBackendWorkspaceChanges(ctx context.Context, id string) error {
	pb, connected, err := g.loadOrBuildPersistedBackend(id)
	if err != nil {
		return err
	}
	if connected {
		if err := g.stopConnectedBackendPreservingState(id); err != nil {
			return err
		}
	}
	return g.connectPersistedBackend(ctx, id, pb)
}

func (g *Gateway) acceptBridgeWorkspaceChanges(ctx context.Context, id string) error {
	bridgeURL, err := g.bridgeURLForExistingBackend(id)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bridgeURL+"/manage/"+id+"/changes/discard", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("discard workspace changes via bridge: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return fmt.Errorf("discard workspace changes failed: status %d payload %v", resp.StatusCode, payload)
	}
	return nil
}

func (g *Gateway) bridgeURLForExistingBackend(id string) (string, error) {
	urls := g.bridgeURLsForBackend(id)
	if len(urls) == 0 {
		return "", fmt.Errorf("bridge_url is not configured")
	}
	return urls[0], nil
}

func (g *Gateway) syncWorkspaceAfterToolCall(ctx context.Context, backendID string, cfg *config.ServerConfig) {
	if cfg == nil || cfg.Workspace == nil || !cfg.BridgeManaged {
		return
	}
	workspaceCfg := config.NormalizeWorkspaceConfig(cfg.Workspace)
	if workspaceCfg == nil || workspaceCfg.WriteMode == config.WorkspaceWriteSandboxOnly {
		return
	}
	changes, err := g.BackendWorkspaceChanges(ctx, backendID, true)
	if err != nil {
		g.logger.Warn("failed to refresh workspace changes", "id", backendID, "error", err)
		return
	}
	if len(changes.Files) == 0 || workspaceCfg.WriteMode != config.WorkspaceWriteAutoApply {
		return
	}
	result, err := g.ApplyWorkspaceChanges(ctx, workspaceCfg.ID, workspaceSnapshotPolicy(workspaceCfg), changes)
	if err != nil {
		g.logger.Warn("failed to auto-apply workspace changes", "id", backendID, "error", err)
		return
	}
	if len(result.Conflicts) > 0 {
		g.logger.Warn("workspace auto-apply had conflicts", "id", backendID, "conflicts", result.Conflicts)
		return
	}
	if err := g.acceptBridgeWorkspaceChanges(ctx, backendID); err != nil {
		g.logger.Warn("workspace auto-apply baseline reset failed", "id", backendID, "error", err)
	}
}
