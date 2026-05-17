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
	"strings"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/metrics"
	"github.com/1broseidon/prism/internal/store"
	ws "github.com/1broseidon/prism/internal/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	workspaceSettingsKVKey = "gateway/workspace_bridge/v1"
	workspacePollTimeout   = 25 * time.Second
	workspaceCallTimeout   = 60 * time.Second
)

var workspaceIDRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type workspaceBridgeSettings struct {
	Enabled   bool   `json:"enabled"`
	TokenHash string `json:"token_hash,omitempty"`
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
	lastSeen  time.Time
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
	if !m.authorized(r) {
		writeWorkspaceError(w, http.StatusUnauthorized, "workspace bridge is disabled or token is invalid")
		return
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

func (m *workspaceBridgeManager) authorized(r *http.Request) bool {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}

	m.mu.Lock()
	settings := m.settings
	m.mu.Unlock()
	if !settings.Enabled || settings.TokenHash == "" {
		return false
	}
	return verifyWorkspaceToken(token, settings.TokenHash)
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

	conn := &workspaceConnection{
		id:       req.WorkspaceID,
		hostname: strings.TrimSpace(req.Hostname),
		root:     strings.TrimSpace(req.Root),
		version:  strings.TrimSpace(req.Version),
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
	if g.workspace == nil {
		return nil
	}
	return g.workspace.list()
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
			Hostname:  conn.hostname,
			Root:      conn.root,
			Version:   conn.version,
			LastSeen:  conn.lastSeen,
			Connected: now.Sub(conn.lastSeen) < 2*workspacePollTimeout,
			Backends:  backends,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// DisconnectWorkspace removes a workspace bridge and its registered tools.
func (g *Gateway) DisconnectWorkspace(id string) bool {
	if g.workspace == nil {
		return false
	}
	return g.workspace.disconnectWorkspace(id)
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
