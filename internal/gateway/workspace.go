package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/metrics"
	"github.com/1broseidon/prism/internal/store"
	ws "github.com/1broseidon/prism/internal/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	workspaceSettingsKVKey  = "gateway/workspace_bridge/v1"
	workspaceRegistryPrefix = "gateway/workspaces/"
	workspacePollTimeout    = 25 * time.Second
	workspaceCallTimeout    = 60 * time.Second
)

var workspaceIDRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var workspacePolicyValueRE = regexp.MustCompile(`^[A-Za-z0-9_.@:/-]{1,128}$`)

type workspaceBridgeSettings struct {
	Enabled   bool   `json:"enabled"`
	TokenHash string `json:"token_hash,omitempty"`
}

type workspaceRegistryEntry struct {
	ID               string    `json:"id"`
	Type             string    `json:"type"`
	Owner            string    `json:"owner,omitempty"`
	AllowedAgents    []string  `json:"allowed_agents,omitempty"`
	AllowedTemplates []string  `json:"allowed_templates,omitempty"`
	QuotaBytes       int64     `json:"quota_bytes,omitempty"`
	RetentionSeconds int64     `json:"retention_seconds,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type workspaceBridgeManager struct {
	gateway *Gateway
	store   store.Store

	mu         sync.Mutex
	settings   workspaceBridgeSettings
	workspaces map[string]*workspaceConnection
}

type workspaceConnection struct {
	id        string
	hostname  string
	root      string
	version   string
	owner     string // OAuth-stamped owner identifier (PrismID / ClientID / Subject); empty for ops bridges
	lastSeen  time.Time
	usedBytes int64
	backends  []admin.WorkspaceBackendStatus
	toolNames []string
	queue     chan workspaceCallRequest
	pending   map[string]chan workspaceCallResult
}

type workspaceRegisterRequest struct {
	WorkspaceID string                     `json:"workspace_id"`
	Hostname    string                     `json:"hostname,omitempty"`
	Root        string                     `json:"root,omitempty"`
	Version     string                     `json:"version,omitempty"`
	Backends    []workspaceRegisterBackend `json:"backends"`
}

type workspaceRegisterBackend struct {
	ID        string      `json:"id"`
	Namespace string      `json:"namespace"`
	Tools     []*mcp.Tool `json:"tools"`
}

type workspaceRegisterResponse struct {
	Status string   `json:"status"`
	Tools  []string `json:"tools"`
}

type workspacePollResponse struct {
	Request *workspaceCallRequest `json:"request,omitempty"`
}

type workspaceCallRequest struct {
	RequestID string                 `json:"request_id"`
	Kind      string                 `json:"kind,omitempty"`
	BackendID string                 `json:"backend_id"`
	ToolName  string                 `json:"tool_name"`
	Arguments any                    `json:"arguments,omitempty"`
	Snapshot  *ws.SnapshotPolicy     `json:"snapshot,omitempty"`
	Apply     *workspaceApplyRequest `json:"apply,omitempty"`
}

type workspaceCallResult struct {
	WorkspaceID string              `json:"workspace_id"`
	RequestID   string              `json:"request_id"`
	Result      *mcp.CallToolResult `json:"result,omitempty"`
	Snapshot    *ws.Snapshot        `json:"snapshot,omitempty"`
	Apply       *ws.ApplyResult     `json:"apply,omitempty"`
	Error       string              `json:"error,omitempty"`
}

type workspaceApplyRequest struct {
	Policy  ws.SnapshotPolicy `json:"policy"`
	Changes *ws.ChangeSet     `json:"changes"`
}

func newWorkspaceBridgeManager(g *Gateway) *workspaceBridgeManager {
	return &workspaceBridgeManager{
		gateway:    g,
		workspaces: make(map[string]*workspaceConnection),
	}
}

// InitWorkspaceBridge loads persisted workspace bridge settings and applies an
// optional token supplied through the process environment.
func (g *Gateway) InitWorkspaceBridge(kv store.Store, envToken string) error {
	if g.workspace == nil {
		g.workspace = newWorkspaceBridgeManager(g)
	}
	return g.workspace.init(kv, envToken)
}

func (m *workspaceBridgeManager) init(kv store.Store, envToken string) error {
	m.store = kv
	settings, err := loadWorkspaceBridgeSettings(kv)
	if err != nil {
		return err
	}
	if token := strings.TrimSpace(envToken); token != "" {
		settings.Enabled = true
		settings.TokenHash = hashWorkspaceToken(token)
	}
	m.mu.Lock()
	m.settings = settings
	m.mu.Unlock()
	return nil
}

func loadWorkspaceBridgeSettings(kv store.Store) (workspaceBridgeSettings, error) {
	if kv == nil {
		return workspaceBridgeSettings{}, nil
	}
	data, err := kv.Get(workspaceSettingsKVKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return workspaceBridgeSettings{}, nil
		}
		return workspaceBridgeSettings{}, fmt.Errorf("load workspace bridge settings: %w", err)
	}
	var s workspaceBridgeSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return workspaceBridgeSettings{}, fmt.Errorf("parse workspace bridge settings: %w", err)
	}
	return s, nil
}

func saveWorkspaceBridgeSettings(kv store.Store, s workspaceBridgeSettings) error {
	if kv == nil {
		return errors.New("kv store is required to persist workspace bridge settings")
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal workspace bridge settings: %w", err)
	}
	return kv.Set(workspaceSettingsKVKey, data)
}

// WorkspaceBridgeConfig returns the current operator-facing workspace bridge settings.
func (g *Gateway) WorkspaceBridgeConfig() admin.WorkspaceBridgeConfigView {
	if g.workspace == nil {
		return admin.WorkspaceBridgeConfigView{}
	}
	return g.workspace.configView()
}

func (m *workspaceBridgeManager) configView() admin.WorkspaceBridgeConfigView {
	m.mu.Lock()
	defer m.mu.Unlock()
	return admin.WorkspaceBridgeConfigView{
		Enabled:  m.settings.Enabled,
		TokenSet: m.settings.TokenHash != "",
	}
}

// SetWorkspaceBridgeConfig persists and applies runtime workspace bridge settings.
func (g *Gateway) SetWorkspaceBridgeConfig(update admin.WorkspaceBridgeUpdate) (admin.WorkspaceBridgeConfigView, error) {
	if g.workspace == nil {
		g.workspace = newWorkspaceBridgeManager(g)
	}
	return g.workspace.setConfig(update)
}

func (m *workspaceBridgeManager) setConfig(update admin.WorkspaceBridgeUpdate) (admin.WorkspaceBridgeConfigView, error) {
	token := strings.TrimSpace(update.Token)
	if token != "" && len(token) < 24 {
		return admin.WorkspaceBridgeConfigView{}, errors.New("workspace token must be at least 24 characters")
	}

	m.mu.Lock()
	next := m.settings
	next.Enabled = update.Enabled
	if token != "" {
		next.TokenHash = hashWorkspaceToken(token)
	}
	m.mu.Unlock()

	if next.Enabled && next.TokenHash == "" {
		return admin.WorkspaceBridgeConfigView{}, errors.New("workspace bridge token is required before enabling")
	}
	if err := saveWorkspaceBridgeSettings(m.store, next); err != nil {
		return admin.WorkspaceBridgeConfigView{}, err
	}

	m.mu.Lock()
	m.settings = next
	disabled := !next.Enabled
	m.mu.Unlock()
	if disabled {
		m.disconnectAll()
	}
	return m.configView(), nil
}

// WorkspaceBridgeHandler exposes the public, token-authenticated workspace
// bridge control plane used by local prism-bridge workspace services.
func (g *Gateway) WorkspaceBridgeHandler() http.Handler {
	if g.workspace == nil {
		g.workspace = newWorkspaceBridgeManager(g)
	}
	return http.HandlerFunc(g.workspace.serveHTTP)
}

func (m *workspaceBridgeManager) serveHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := m.authorize(r)
	if !ok {
		writeWorkspaceError(w, http.StatusUnauthorized, "workspace bridge is disabled or token is invalid")
		return
	}
	// Stash claims so handleRegister can stamp owner on the registration.
	if claims != nil {
		r = r.WithContext(auth.ContextWithClaims(r.Context(), claims))
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/workspace/register":
		m.handleRegister(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/workspace/poll":
		m.handlePoll(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/workspace/result":
		m.handleResult(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/workspace/unregister":
		m.handleUnregister(w, r)
	default:
		writeWorkspaceError(w, http.StatusNotFound, "not found")
	}
}

// authorize verifies the bearer on a workspace-bridge request. It accepts
// EITHER a valid agent OAuth token (preferred — caller is an identifiable
// agent, claims returned) OR the shared workspace token (legacy ops bridge,
// claims nil). The second bool is the auth verdict.
func (m *workspaceBridgeManager) authorize(r *http.Request) (*auth.Claims, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, false
	}

	// Prefer OAuth: any valid agent token authenticates the bridge as that
	// agent and lets the gateway stamp owner on the registered workspace.
	if v := m.gateway.getTokenValidator(); v != nil {
		if claims, _, err := v.Validate(r.Context(), token); err == nil {
			return claims, true
		}
	}

	// Fall back to the shared workspace token (ops-managed bridge).
	m.mu.Lock()
	settings := m.settings
	m.mu.Unlock()
	if !settings.Enabled || settings.TokenHash == "" {
		return nil, false
	}
	if verifyWorkspaceToken(token, settings.TokenHash) {
		return nil, true
	}
	return nil, false
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func (m *workspaceBridgeManager) handleRegister(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var req workspaceRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWorkspaceError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	registered, err := m.register(r.Context(), &req)
	if err != nil {
		writeWorkspaceError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, workspaceRegisterResponse{Status: "ok", Tools: registered})
}

func (m *workspaceBridgeManager) register(ctx context.Context, req *workspaceRegisterRequest) ([]string, error) {
	if !workspaceIDRE.MatchString(req.WorkspaceID) {
		return nil, errors.New("workspace_id must match ^[A-Za-z0-9_.-]{1,64}$")
	}
	if len(req.Backends) > 8 {
		return nil, errors.New("too many workspace backends")
	}

	owner := agentOwnerFromContext(ctx)
	conn := &workspaceConnection{
		id:       req.WorkspaceID,
		hostname: strings.TrimSpace(req.Hostname),
		root:     strings.TrimSpace(req.Root),
		version:  strings.TrimSpace(req.Version),
		owner:    owner,
		lastSeen: time.Now(),
		queue:    make(chan workspaceCallRequest, 64),
		pending:  make(map[string]chan workspaceCallResult),
	}

	type toolRegistration struct {
		tool        *mcp.Tool
		workspaceID string
		backendID   string
		namespace   string
		original    string
	}
	registered := make([]string, 0)
	toolRegs := make([]toolRegistration, 0)
	for _, backend := range req.Backends {
		if !workspaceIDRE.MatchString(backend.ID) {
			return nil, fmt.Errorf("backend id %q is invalid", backend.ID)
		}
		if !workspaceIDRE.MatchString(backend.Namespace) {
			return nil, fmt.Errorf("namespace %q is invalid", backend.Namespace)
		}
		if len(backend.Tools) == 0 {
			return nil, fmt.Errorf("backend %q has no tools", backend.ID)
		}
		if len(backend.Tools) > 256 {
			return nil, fmt.Errorf("backend %q has too many tools", backend.ID)
		}

		status := admin.WorkspaceBackendStatus{
			ID:        backend.ID,
			Namespace: backend.Namespace,
			Tools:     make([]admin.WorkspaceToolStatus, 0, len(backend.Tools)),
		}
		for _, tool := range backend.Tools {
			if tool == nil || strings.TrimSpace(tool.Name) == "" {
				return nil, fmt.Errorf("backend %q has an invalid tool", backend.ID)
			}
			originalName := strings.TrimSpace(tool.Name)
			namespacedName := backend.Namespace + namespaceSeparator + originalName
			if tool.InputSchema == nil {
				tool.InputSchema = map[string]any{"type": "object"}
			}
			toolRegs = append(toolRegs, toolRegistration{
				tool: &mcp.Tool{
					Name:        namespacedName,
					Description: fmt.Sprintf("[%s workspace] %s", backend.Namespace, tool.Description),
					InputSchema: tool.InputSchema,
				},
				workspaceID: req.WorkspaceID,
				backendID:   backend.ID,
				namespace:   backend.Namespace,
				original:    originalName,
			})
			conn.toolNames = append(conn.toolNames, namespacedName)
			registered = append(registered, namespacedName)
			status.Tools = append(status.Tools, admin.WorkspaceToolStatus{
				Name:        namespacedName,
				Description: tool.Description,
			})
		}
		conn.backends = append(conn.backends, status)
	}

	m.mu.Lock()
	if existing := m.workspaces[req.WorkspaceID]; existing != nil {
		m.removeConnectionLocked(existing)
	}
	m.workspaces[req.WorkspaceID] = conn
	m.mu.Unlock()

	m.persistOwnerIfPresent(req.WorkspaceID, owner)

	for _, reg := range toolRegs {
		workspaceID := reg.workspaceID
		backendID := reg.backendID
		namespace := reg.namespace
		originalName := reg.original
		m.gateway.server.AddTool(reg.tool, func(ctx context.Context, call *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return m.callTool(ctx, workspaceID, backendID, namespace, originalName, call)
		})
	}

	sort.Strings(registered)
	m.gateway.logger.Info("workspace bridge registered", "id", req.WorkspaceID, "tools", len(registered), "root", req.Root)
	reconnectBase := context.WithoutCancel(ctx)
	go func(workspaceID string) {
		ctx, cancel := context.WithTimeout(reconnectBase, 2*time.Minute)
		defer cancel()
		m.gateway.reconnectPersistedBackendsForWorkspace(ctx, workspaceID)
	}(req.WorkspaceID)
	return registered, nil
}

func (m *workspaceBridgeManager) callTool(ctx context.Context, workspaceID, backendID, namespace, toolName string, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if policy := auth.LivePolicy(ctx, m.gateway.getPolicyResolver()); policy != nil {
		if !policy.CanAccessTool(namespace, toolName) {
			m.gateway.auditor.LogCall(ctx, namespace, toolName, workspaceID+"/"+backendID, false, false, 0, nil)
			metrics.RecordScopeDenial(namespace, toolName)
			metrics.RecordToolCall(namespace, toolName, workspaceID+"/"+backendID, false, 0)
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("access denied: scope %q:%q not granted", namespace, toolName)},
				},
			}, nil
		}
	}

	callID, err := randomWorkspaceID()
	if err != nil {
		return nil, err
	}
	resultCh := make(chan workspaceCallResult, 1)
	call := workspaceCallRequest{
		RequestID: callID,
		BackendID: backendID,
		ToolName:  toolName,
	}
	if req != nil {
		call.Arguments = req.Params.Arguments
	}

	m.mu.Lock()
	conn := m.workspaces[workspaceID]
	if conn == nil {
		m.mu.Unlock()
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("workspace %q is not connected", workspaceID)}},
		}, nil
	}
	conn.pending[callID] = resultCh
	select {
	case conn.queue <- call:
	default:
		delete(conn.pending, callID)
		m.mu.Unlock()
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("workspace %q is busy", workspaceID)}},
		}, nil
	}
	m.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, workspaceCallTimeout)
	defer cancel()
	start := time.Now()
	select {
	case result := <-resultCh:
		elapsed := time.Since(start)
		m.gateway.auditor.LogCall(ctx, namespace, toolName, workspaceID+"/"+backendID, true, false, elapsed.Milliseconds(), nil)
		metrics.RecordToolCall(namespace, toolName, workspaceID+"/"+backendID, true, elapsed)
		if result.Error != "" {
			return nil, errors.New(result.Error)
		}
		if result.Result == nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "workspace bridge returned no result"}},
			}, nil
		}
		return result.Result, nil
	case <-callCtx.Done():
		m.mu.Lock()
		if conn := m.workspaces[workspaceID]; conn != nil {
			delete(conn.pending, callID)
		}
		m.mu.Unlock()
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("workspace %q call timed out", workspaceID)}},
		}, nil
	}
}

// SnapshotWorkspace requests a policy-filtered snapshot from a connected workspace agent.
func (g *Gateway) SnapshotWorkspace(ctx context.Context, workspaceID string, policy ws.SnapshotPolicy) (*ws.Snapshot, error) {
	if g.workspace == nil {
		return nil, errors.New("workspace bridge is not initialized")
	}
	return g.workspace.snapshot(ctx, workspaceID, policy)
}

// ApplyWorkspaceChanges asks a connected workspace agent to apply a staged change set.
func (g *Gateway) ApplyWorkspaceChanges(ctx context.Context, workspaceID string, policy ws.SnapshotPolicy, changes *ws.ChangeSet) (*ws.ApplyResult, error) {
	if g.workspace == nil {
		return nil, errors.New("workspace bridge is not initialized")
	}
	return g.workspace.apply(ctx, workspaceID, policy, changes)
}

func (m *workspaceBridgeManager) snapshot(ctx context.Context, workspaceID string, policy ws.SnapshotPolicy) (*ws.Snapshot, error) {
	result, err := m.requestWorkspaceOperation(ctx, workspaceID, &workspaceCallRequest{
		Kind:     "snapshot",
		Snapshot: &policy,
	})
	if err != nil {
		return nil, err
	}
	if result.Snapshot == nil {
		return nil, errors.New("workspace bridge returned no snapshot")
	}
	return result.Snapshot, nil
}

func (m *workspaceBridgeManager) apply(ctx context.Context, workspaceID string, policy ws.SnapshotPolicy, changes *ws.ChangeSet) (*ws.ApplyResult, error) {
	result, err := m.requestWorkspaceOperation(ctx, workspaceID, &workspaceCallRequest{
		Kind: "apply",
		Apply: &workspaceApplyRequest{
			Policy:  policy,
			Changes: changes,
		},
	})
	if err != nil {
		return nil, err
	}
	if result.Apply == nil {
		return nil, errors.New("workspace bridge returned no apply result")
	}
	return result.Apply, nil
}

func (m *workspaceBridgeManager) requestWorkspaceOperation(ctx context.Context, workspaceID string, call *workspaceCallRequest) (workspaceCallResult, error) {
	if !workspaceIDRE.MatchString(workspaceID) {
		return workspaceCallResult{}, errors.New("invalid workspace id")
	}
	callID, err := randomWorkspaceID()
	if err != nil {
		return workspaceCallResult{}, err
	}
	call.RequestID = callID
	resultCh := make(chan workspaceCallResult, 1)

	m.mu.Lock()
	conn := m.workspaces[workspaceID]
	if conn == nil {
		m.mu.Unlock()
		return workspaceCallResult{}, fmt.Errorf("workspace %q is not connected", workspaceID)
	}
	conn.pending[callID] = resultCh
	select {
	case conn.queue <- *call:
	default:
		delete(conn.pending, callID)
		m.mu.Unlock()
		return workspaceCallResult{}, fmt.Errorf("workspace %q is busy", workspaceID)
	}
	m.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, workspaceCallTimeout)
	defer cancel()
	select {
	case result := <-resultCh:
		if result.Error != "" {
			return workspaceCallResult{}, errors.New(result.Error)
		}
		return result, nil
	case <-callCtx.Done():
		m.mu.Lock()
		if conn := m.workspaces[workspaceID]; conn != nil {
			delete(conn.pending, callID)
		}
		m.mu.Unlock()
		return workspaceCallResult{}, callCtx.Err()
	}
}

func (m *workspaceBridgeManager) handlePoll(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("workspace_id")
	if !workspaceIDRE.MatchString(workspaceID) {
		writeWorkspaceError(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}

	m.mu.Lock()
	conn := m.workspaces[workspaceID]
	if conn == nil {
		m.mu.Unlock()
		writeWorkspaceError(w, http.StatusNotFound, "workspace is not registered")
		return
	}
	conn.lastSeen = time.Now()
	if raw := strings.TrimSpace(r.URL.Query().Get("used_bytes")); raw != "" {
		if used, err := strconv.ParseInt(raw, 10, 64); err == nil && used >= 0 {
			conn.usedBytes = used
		}
	}
	queue := conn.queue
	m.mu.Unlock()

	timer := time.NewTimer(workspacePollTimeout)
	defer timer.Stop()

	select {
	case call := <-queue:
		writeWorkspaceJSON(w, http.StatusOK, workspacePollResponse{Request: &call})
	case <-timer.C:
		writeWorkspaceJSON(w, http.StatusOK, workspacePollResponse{})
	case <-r.Context().Done():
		return
	}
}

func (m *workspaceBridgeManager) handleResult(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 96<<20)
	var result workspaceCallResult
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		writeWorkspaceError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if !workspaceIDRE.MatchString(result.WorkspaceID) || result.RequestID == "" {
		writeWorkspaceError(w, http.StatusBadRequest, "invalid result")
		return
	}

	m.mu.Lock()
	conn := m.workspaces[result.WorkspaceID]
	if conn == nil {
		m.mu.Unlock()
		writeWorkspaceError(w, http.StatusNotFound, "workspace is not registered")
		return
	}
	ch := conn.pending[result.RequestID]
	if ch == nil {
		m.mu.Unlock()
		writeWorkspaceError(w, http.StatusNotFound, "request is not pending")
		return
	}
	delete(conn.pending, result.RequestID)
	conn.lastSeen = time.Now()
	m.mu.Unlock()

	ch <- result
	writeWorkspaceJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (m *workspaceBridgeManager) handleUnregister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWorkspaceError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if !m.disconnectWorkspace(req.WorkspaceID) {
		writeWorkspaceError(w, http.StatusNotFound, "workspace is not registered")
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ListWorkspaces returns connected workspace bridge status for the admin API.
func (g *Gateway) ListWorkspaces() []admin.WorkspaceStatus {
	statuses := make([]admin.WorkspaceStatus, 0)
	if g.workspace != nil {
		statuses = append(statuses, g.workspace.list()...)
	}
	if g.kvStore != nil {
		statuses = mergeWorkspaceRegistryStatuses(statuses, g.loadRegisteredWorkspaces())
	}
	for i := range statuses {
		statuses[i].HealthStatus = workspaceHealth(
			statuses[i].Connected,
			statuses[i].UsedBytes,
			statuses[i].QuotaBytes,
		)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	return statuses
}

// CreateWorkspace persists a remote-only workspace. Proxied workspaces are
// created by running a local workspace bridge.
func (g *Gateway) CreateWorkspace(_ context.Context, req admin.WorkspaceCreateRequest) (admin.WorkspaceStatus, error) { //nolint:gocritic // admin.WorkspaceBridgeManager interface uses value DTOs
	if g.kvStore == nil {
		return admin.WorkspaceStatus{}, errors.New("workspace persistence is not configured")
	}
	id := strings.TrimSpace(req.ID)
	if !workspaceIDRE.MatchString(id) {
		return admin.WorkspaceStatus{}, errors.New("workspace id must match ^[A-Za-z0-9_.-]{1,64}$")
	}
	typ := strings.TrimSpace(req.Type)
	switch typ {
	case config.WorkspaceTypeVirtual, config.WorkspaceTypeEphemeral:
	case "", config.WorkspaceTypeProxied:
		return admin.WorkspaceStatus{}, errors.New("remote workspaces must be virtual or ephemeral")
	default:
		return admin.WorkspaceStatus{}, fmt.Errorf("workspace type must be %q or %q", config.WorkspaceTypeVirtual, config.WorkspaceTypeEphemeral)
	}
	allowedAgents, err := cleanWorkspacePolicyValues(req.AllowedAgents)
	if err != nil {
		return admin.WorkspaceStatus{}, fmt.Errorf("allowed_agents: %w", err)
	}
	allowedTemplates, err := cleanWorkspacePolicyValues(req.AllowedTemplates)
	if err != nil {
		return admin.WorkspaceStatus{}, fmt.Errorf("allowed_templates: %w", err)
	}
	if req.QuotaBytes < 0 {
		return admin.WorkspaceStatus{}, errors.New("quota_bytes must be >= 0")
	}
	if req.RetentionSeconds < 0 {
		return admin.WorkspaceStatus{}, errors.New("retention_seconds must be >= 0")
	}
	entry := workspaceRegistryEntry{
		ID:               id,
		Type:             typ,
		Owner:            strings.TrimSpace(req.Owner),
		AllowedAgents:    allowedAgents,
		AllowedTemplates: allowedTemplates,
		QuotaBytes:       req.QuotaBytes,
		RetentionSeconds: req.RetentionSeconds,
		CreatedAt:        time.Now().UTC(),
	}
	data, err := json.Marshal(&entry)
	if err != nil {
		return admin.WorkspaceStatus{}, err
	}
	if err := g.kvStore.Set(workspaceRegistryPrefix+id, data); err != nil {
		return admin.WorkspaceStatus{}, err
	}
	return workspaceStatusFromRegistry(&entry), nil
}

func cleanWorkspacePolicyValues(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if value != "*" && !workspacePolicyValueRE.MatchString(value) {
			return nil, fmt.Errorf("%q must be 1-128 chars of [A-Za-z0-9_.@:/-] or *", value)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func (g *Gateway) loadRegisteredWorkspaces() []admin.WorkspaceStatus {
	keys, err := g.kvStore.List(workspaceRegistryPrefix)
	if err != nil {
		g.logger.Warn("failed to list workspace registry", "error", err)
		return nil
	}
	out := make([]admin.WorkspaceStatus, 0, len(keys))
	for _, key := range keys {
		data, err := g.kvStore.Get(key)
		if err != nil {
			g.logger.Warn("failed to read workspace registry entry", "key", key, "error", err)
			continue
		}
		var entry workspaceRegistryEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			g.logger.Warn("failed to decode workspace registry entry", "key", key, "error", err)
			continue
		}
		out = append(out, workspaceStatusFromRegistry(&entry))
	}
	return out
}

func (g *Gateway) registeredWorkspace(id string) (*workspaceRegistryEntry, bool) {
	if g.kvStore == nil || !workspaceIDRE.MatchString(id) {
		return nil, false
	}
	data, err := g.kvStore.Get(workspaceRegistryPrefix + id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false
	}
	if err != nil {
		g.logger.Warn("failed to read workspace registry entry", "workspace", id, "error", err)
		return nil, false
	}
	var entry workspaceRegistryEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		g.logger.Warn("failed to decode workspace registry entry", "workspace", id, "error", err)
		return nil, false
	}
	if entry.ID == "" {
		entry.ID = id
	}
	return &entry, true
}

func mergeWorkspaceRegistryStatuses(live, registered []admin.WorkspaceStatus) []admin.WorkspaceStatus {
	seen := make(map[string]int, len(live))
	for i := range live {
		seen[live[i].ID] = i
	}
	for i := range registered {
		status := &registered[i]
		if idx, ok := seen[status.ID]; ok {
			if live[idx].Type == "" {
				live[idx].Type = status.Type
			}
			if live[idx].CreatedAt.IsZero() {
				live[idx].CreatedAt = status.CreatedAt
			}
			if live[idx].Owner == "" {
				live[idx].Owner = status.Owner
			}
			if len(live[idx].AllowedAgents) == 0 {
				live[idx].AllowedAgents = append([]string(nil), status.AllowedAgents...)
			}
			if len(live[idx].AllowedTemplates) == 0 {
				live[idx].AllowedTemplates = append([]string(nil), status.AllowedTemplates...)
			}
			if live[idx].QuotaBytes == 0 {
				live[idx].QuotaBytes = status.QuotaBytes
			}
			if live[idx].RetentionSeconds == 0 {
				live[idx].RetentionSeconds = status.RetentionSeconds
			}
			continue
		}
		live = append(live, *status)
	}
	return live
}

// persistOwnerIfPresent stamps the workspace's owner into the registry when
// the bridge authenticated as an agent. Failures are logged but do not abort
// registration — the in-memory connection still works; the owner just won't
// survive a gateway restart.
func (m *workspaceBridgeManager) persistOwnerIfPresent(workspaceID, owner string) {
	if owner == "" {
		return
	}
	if err := m.gateway.persistProxiedRegistryEntry(workspaceID, owner); err != nil {
		m.gateway.logger.Warn("failed to persist proxied workspace ownership",
			"workspace", workspaceID, "owner", owner, "error", err)
	}
}

// agentOwnerFromContext returns the most-stable identifier on the validated
// claims, preferring PrismID, then ClientID, then Subject. Empty if no
// claims are attached (workspace-token / ops-bridge path).
func agentOwnerFromContext(ctx context.Context) string {
	claims := auth.ClaimsFromContext(ctx)
	if claims == nil {
		return ""
	}
	switch {
	case claims.PrismID != "":
		return claims.PrismID
	case claims.ClientID != "":
		return claims.ClientID
	default:
		return claims.Subject
	}
}

// persistProxiedRegistryEntry stores ownership metadata for a proxied
// workspace so resolveAgentWorkspace can look it up by owner. Existing
// entries are upgraded with the owner if it's missing; type/quota/etc.
// are preserved.
func (g *Gateway) persistProxiedRegistryEntry(id, owner string) error {
	if g.kvStore == nil || !workspaceIDRE.MatchString(id) || strings.TrimSpace(owner) == "" {
		return nil
	}
	key := workspaceRegistryPrefix + id
	entry := workspaceRegistryEntry{
		ID:        id,
		Type:      config.WorkspaceTypeProxied,
		Owner:     owner,
		CreatedAt: time.Now().UTC(),
	}
	if existing, err := g.kvStore.Get(key); err == nil {
		var prev workspaceRegistryEntry
		if jsonErr := json.Unmarshal(existing, &prev); jsonErr == nil {
			entry = prev
			if entry.Type == "" {
				entry.Type = config.WorkspaceTypeProxied
			}
			entry.Owner = owner
		}
	}
	data, err := json.Marshal(&entry)
	if err != nil {
		return err
	}
	return g.kvStore.Set(key, data)
}

func workspaceStatusFromRegistry(entry *workspaceRegistryEntry) admin.WorkspaceStatus {
	// Virtual workspaces are considered "connected" (gateway-resident storage
	// that exists whether or not a bridge is actively polling). Ephemeral
	// workspaces only exist while a bridge is attached.
	connected := entry.Type == config.WorkspaceTypeVirtual
	return admin.WorkspaceStatus{
		ID:               entry.ID,
		Type:             entry.Type,
		Owner:            entry.Owner,
		AllowedAgents:    append([]string(nil), entry.AllowedAgents...),
		AllowedTemplates: append([]string(nil), entry.AllowedTemplates...),
		QuotaBytes:       entry.QuotaBytes,
		RetentionSeconds: entry.RetentionSeconds,
		CreatedAt:        entry.CreatedAt,
		Connected:        connected,
		// UsedBytes for virtual/ephemeral is a follow-up — requires docker
		// volume inspection via the server-side bridge runtime.
	}
}

func (m *workspaceBridgeManager) list() []admin.WorkspaceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]admin.WorkspaceStatus, 0, len(m.workspaces))
	now := time.Now()
	for _, conn := range m.workspaces {
		backends := make([]admin.WorkspaceBackendStatus, len(conn.backends))
		copy(backends, conn.backends)
		out = append(out, admin.WorkspaceStatus{
			ID:        conn.id,
			Type:      config.WorkspaceTypeProxied,
			Owner:     conn.owner,
			Hostname:  conn.hostname,
			Root:      conn.root,
			Version:   conn.version,
			LastSeen:  conn.lastSeen,
			Connected: now.Sub(conn.lastSeen) < 2*workspacePollTimeout,
			UsedBytes: conn.usedBytes,
			Backends:  backends,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// workspaceHealth maps a workspace's runtime state to one of the documented
// health values: ok / quota_warn / quota_exceeded / stale.
func workspaceHealth(connected bool, used, quota int64) string {
	if !connected {
		return admin.WorkspaceHealthStale
	}
	if quota > 0 {
		if used >= quota {
			return admin.WorkspaceHealthQuotaExceeded
		}
		if used*10 >= quota*9 { // >= 90% of quota
			return admin.WorkspaceHealthQuotaWarn
		}
	}
	return admin.WorkspaceHealthOK
}

// DisconnectWorkspace removes a workspace bridge and its registered tools.
func (g *Gateway) DisconnectWorkspace(id string) bool {
	disconnected := false
	if g.workspace != nil {
		disconnected = g.workspace.disconnectWorkspace(id)
	}
	deleted := false
	if g.kvStore != nil && workspaceIDRE.MatchString(id) {
		key := workspaceRegistryPrefix + id
		if _, err := g.kvStore.Get(key); err == nil {
			_ = g.kvStore.Delete(key)
			deleted = true
		}
	}
	return disconnected || deleted
}

func (m *workspaceBridgeManager) disconnectWorkspace(id string) bool {
	if !workspaceIDRE.MatchString(id) {
		return false
	}
	m.mu.Lock()
	conn := m.workspaces[id]
	if conn == nil {
		m.mu.Unlock()
		return false
	}
	delete(m.workspaces, id)
	toolNames := append([]string(nil), conn.toolNames...)
	m.removeConnectionLocked(conn)
	m.mu.Unlock()
	m.restoreBackendTools(toolNames)
	m.gateway.logger.Info("workspace bridge disconnected", "id", id)
	return true
}

func (m *workspaceBridgeManager) disconnectAll() {
	var restore []string
	m.mu.Lock()
	for id, conn := range m.workspaces {
		delete(m.workspaces, id)
		restore = append(restore, conn.toolNames...)
		m.removeConnectionLocked(conn)
		m.gateway.logger.Info("workspace bridge disconnected", "id", id)
	}
	m.mu.Unlock()
	m.restoreBackendTools(restore)
}

func (m *workspaceBridgeManager) removeConnectionLocked(conn *workspaceConnection) {
	if len(conn.toolNames) > 0 {
		m.gateway.server.RemoveTools(conn.toolNames...)
	}
	for requestID, ch := range conn.pending {
		delete(conn.pending, requestID)
		ch <- workspaceCallResult{WorkspaceID: conn.id, RequestID: requestID, Error: "workspace disconnected"}
	}
}

func (m *workspaceBridgeManager) restoreBackendTools(toolNames []string) {
	if len(toolNames) == 0 {
		return
	}
	removed := make(map[string]struct{}, len(toolNames))
	for _, name := range toolNames {
		removed[name] = struct{}{}
	}

	m.gateway.mu.RLock()
	backends := make([]*Backend, 0)
	for _, backend := range m.gateway.backends {
		for _, name := range backend.ToolNames {
			if _, ok := removed[name]; ok {
				backends = append(backends, backend)
				break
			}
		}
	}
	m.gateway.mu.RUnlock()

	for _, backend := range backends {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := m.gateway.registerBackendTools(ctx, backend)
		cancel()
		if err != nil {
			m.gateway.logger.Warn("failed to restore backend tools after workspace disconnect", "backend", backend.Config.ID, "error", err)
		}
	}
}

func hashWorkspaceToken(token string) string {
	sum := sha256.Sum256([]byte("prism workspace bridge v1\x00" + token))
	return hex.EncodeToString(sum[:])
}

func verifyWorkspaceToken(token, hash string) bool {
	got := hashWorkspaceToken(token)
	return subtle.ConstantTimeCompare([]byte(got), []byte(hash)) == 1
}

func randomWorkspaceID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func writeWorkspaceJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeWorkspaceError(w http.ResponseWriter, status int, message string) {
	writeWorkspaceJSON(w, status, map[string]string{"error": message})
}
