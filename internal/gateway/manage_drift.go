package gateway

import (
	"context"
	"fmt"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/config"
)

// checkGrantWorkspaceDrift compares the workspace pinned by the issued
// grant on ctx against the live workspace configuration the spawn path is
// about to use. When the two diverge, the helper emits a structured
// workspace-drift event (with a GrantHash/LiveHash diff) and returns an
// error so the caller refuses the spawn.
//
// When no pinned grant workspace is present on ctx, the check is a no-op
// — legacy scope-only callers don't have a pinned workspace and must be
// allowed to spawn under the live config.
func (g *Gateway) checkGrantWorkspaceDrift(ctx context.Context, backendID string, live *config.WorkspaceConfig) error {
	pinned := grantWorkspaceFromContext(ctx)
	if pinned == nil {
		return nil
	}
	liveInst := workspaceConfigInstance(live)
	ok, diff := auth.CompareWorkspace(pinned, liveInst)
	if ok {
		return nil
	}
	event := auth.GrantEvent{
		Timestamp:     g.now(),
		Backend:       backendID,
		Outcome:       "denied",
		WorkspaceID:   instanceID(liveInst),
		WorkspaceType: instanceType(liveInst),
		MatchedIndex:  -1,
		Trace: auth.GrantTrace{
			DenyDim: auth.GrantDenyWorkspaceDrift,
			What:    auth.AxisResult{Verdict: "pass"},
			Context: auth.AxisResult{Verdict: "fail", Layer: "live_config", Detail: "workspace drift"},
			When:    auth.AxisResult{Verdict: "pass"},
			How:     auth.AxisResult{Verdict: "pass"},
			Drift:   diff,
		},
	}
	if emitter := g.getGrantEmitter(); emitter != nil {
		emitter.Emit(ctx, event)
	}
	return fmt.Errorf("workspace drift: backend %q live workspace differs from pinned grant", backendID)
}

// workspaceConfigInstance derives the auth.WorkspaceInstance shape the
// drift helpers compare against from a config.WorkspaceConfig.
func workspaceConfigInstance(cfg *config.WorkspaceConfig) *auth.WorkspaceInstance {
	if cfg == nil {
		return nil
	}
	return &auth.WorkspaceInstance{
		ID:        cfg.ID,
		Type:      cfg.Type,
		WriteMode: cfg.WriteMode,
	}
}

func instanceID(ws *auth.WorkspaceInstance) string {
	if ws == nil {
		return ""
	}
	return ws.ID
}

func instanceType(ws *auth.WorkspaceInstance) string {
	if ws == nil {
		return ""
	}
	return ws.Type
}
