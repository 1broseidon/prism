package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/1broseidon/prism/internal/config"
	ws "github.com/1broseidon/prism/internal/workspace"
)

type ManagedBackend struct {
	ID          string            `json:"id"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"-"`
	Endpoint    string            `json:"endpoint"`
	Handler     http.Handler      `json:"-"`
	Tools       []string          `json:"tools,omitempty"`
	StartedAt   time.Time         `json:"started_at"`
	ContainerID string            `json:"container_id,omitempty"`
	PID         int               `json:"pid,omitempty"`
	Status      string            `json:"status"`
	Runtime     string            `json:"runtime"`
}

type Manager struct {
	mu          sync.RWMutex
	backends    map[string]*ManagedBackend
	runtime     Runtime
	maxBackends int
	logger      *slog.Logger
}

type WorkspaceRuntime interface {
	WorkspaceChanges(ctx context.Context, id string, refresh bool) (*ws.ChangeSet, error)
	DiscardWorkspaceChanges(ctx context.Context, id string) error
}

func NewManager(runtime Runtime, maxBackends int, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		backends:    make(map[string]*ManagedBackend),
		runtime:     runtime,
		maxBackends: maxBackends,
		logger:      logger,
	}
}

func runManage(logger *slog.Logger, args []string) error {
	flags, leftover := splitAtDashDash(args)
	if len(leftover) > 0 {
		return fmt.Errorf("manage mode does not accept a command after --")
	}

	portStr, flags := parseFlag(flags, "port", "3001")
	host, flags := parseFlag(flags, "host", "0.0.0.0")
	maxStr, flags := parseFlag(flags, "max-backends", "20")
	runtimeName, flags := parseFlag(flags, "runtime", "process")
	defaultImage, flags := parseFlag(flags, "image", strings.TrimSpace(os.Getenv("BRIDGE_IMAGE_FULL")))
	networkName, flags := parseFlag(flags, "network", strings.TrimSpace(os.Getenv("BRIDGE_NETWORK")))
	labelPrefix, flags := parseFlag(flags, "label-prefix", "prism.bridge")
	baseImage, flags := parseFlag(flags, "image-base", strings.TrimSpace(os.Getenv("BRIDGE_IMAGE_BASE")))
	nodeImage, flags := parseFlag(flags, "image-node", strings.TrimSpace(os.Getenv("BRIDGE_IMAGE_NODE")))
	pythonImage, flags := parseFlag(flags, "image-python", strings.TrimSpace(os.Getenv("BRIDGE_IMAGE_PYTHON")))
	fullImage, flags := parseFlag(flags, "image-full", strings.TrimSpace(os.Getenv("BRIDGE_IMAGE_FULL")))
	if len(flags) > 0 {
		return fmt.Errorf("unknown manage flags: %s", strings.Join(flags, " "))
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("invalid port: %s", portStr)
	}
	maxBackends, err := strconv.Atoi(maxStr)
	if err != nil {
		return fmt.Errorf("invalid max-backends: %s", maxStr)
	}

	var runtime Runtime
	switch runtimeName {
	case "process":
		runtime = NewProcessRuntime(logger)
	case "docker":
		dockerRuntime, dockerErr := NewDockerRuntime(logger, &DockerRuntimeOptions{
			DefaultImage: defaultImage,
			BaseImage:    baseImage,
			NodeImage:    nodeImage,
			PythonImage:  pythonImage,
			FullImage:    fullImage,
			Network:      networkName,
			LabelPrefix:  labelPrefix,
			NamePrefix:   "prism-managed-",
		})
		if dockerErr != nil {
			return dockerErr
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = dockerRuntime.RemoveOrphans(cleanupCtx)
		cancel()
		runtime = dockerRuntime
	default:
		return fmt.Errorf("unsupported runtime %q", runtimeName)
	}

	manager := NewManager(runtime, maxBackends, logger)
	mux := http.NewServeMux()
	manager.RegisterRoutes(mux)

	addr := fmt.Sprintf("%s:%d", host, port)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	logger.Info("manage mode listening", "addr", ln.Addr().String(), "runtime", runtimeName)

	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down manage mode", "signal", sig)
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := manager.Close(shutdownCtx); err != nil {
		logger.Warn("failed to stop all managed backends", "error", err)
	}
	return srv.Shutdown(shutdownCtx)
}

func (m *Manager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /manage/spawn", m.handleSpawn)
	mux.HandleFunc("GET /manage", m.handleList)
	mux.HandleFunc("GET /manage/{id}/changes", m.handleChanges)
	mux.HandleFunc("POST /manage/{id}/changes/refresh", m.handleRefreshChanges)
	mux.HandleFunc("POST /manage/{id}/changes/discard", m.handleDiscardChanges)
	mux.HandleFunc("GET /manage/", m.handleGet)
	mux.HandleFunc("DELETE /manage/", m.handleDelete)
	mux.HandleFunc("GET /health", m.handleHealth)
	mux.HandleFunc("/mcp/", m.handleMCPProxy)
}

func (m *Manager) handleSpawn(w http.ResponseWriter, r *http.Request) {
	var req SpawnRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.ID == "" || strings.Contains(req.ID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required and must not contain /"})
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}
	if err := config.ValidateSandboxConfig(req.Sandbox); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := config.ValidateWorkspaceConfig(req.Workspace); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Workspace != nil && req.WorkspaceSnapshot == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_snapshot is required when workspace is set"})
		return
	}

	m.mu.Lock()
	if _, exists := m.backends[req.ID]; exists {
		m.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("backend %q already exists", req.ID)})
		return
	}
	if m.maxBackends > 0 && len(m.backends) >= m.maxBackends {
		m.mu.Unlock()
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "max backends limit reached"})
		return
	}
	m.backends[req.ID] = nil
	m.mu.Unlock()

	result, err := m.runtime.Spawn(r.Context(), req)
	if err != nil {
		m.mu.Lock()
		delete(m.backends, req.ID)
		m.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	backend := &ManagedBackend{
		ID:          req.ID,
		Command:     req.Command,
		Args:        req.Args,
		Env:         req.Env,
		Endpoint:    result.Endpoint,
		Handler:     result.Handler,
		Tools:       result.Tools,
		StartedAt:   time.Now().UTC(),
		ContainerID: result.ContainerID,
		PID:         result.PID,
		Status:      nonEmpty(result.Status, "running"),
		Runtime:     nonEmpty(result.Runtime, "process"),
	}

	m.mu.Lock()
	m.backends[req.ID] = backend
	m.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       backend.ID,
		"endpoint": backend.Endpoint,
		"tools":    backend.Tools,
		"status":   backend.Status,
	})
}

func (m *Manager) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/manage/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backend id is required"})
		return
	}

	m.mu.RLock()
	_, exists := m.backends[id]
	m.mu.RUnlock()
	if !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("backend %q not found", id)})
		return
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.runtime.Stop(stopCtx, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	m.mu.Lock()
	delete(m.backends, id)
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "stopped"})
}

func (m *Manager) handleList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"backends": m.listBackends(),
		"limit":    m.maxBackends,
		"count":    len(m.listBackends()),
	})
}

func (m *Manager) handleGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/manage/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backend id is required"})
		return
	}
	backend := m.getBackend(id)
	if backend == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("backend %q not found", id)})
		return
	}
	writeJSON(w, http.StatusOK, backend)
}

func (m *Manager) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"mode":     "manage",
		"backends": len(m.listBackends()),
	})
}

func (m *Manager) handleChanges(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m.writeWorkspaceChanges(w, r, id, false)
}

func (m *Manager) handleRefreshChanges(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m.writeWorkspaceChanges(w, r, id, true)
}

func (m *Manager) handleDiscardChanges(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	runtime, ok := m.runtime.(WorkspaceRuntime)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace changes are not supported by this runtime"})
		return
	}
	if err := runtime.DiscardWorkspaceChanges(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (m *Manager) writeWorkspaceChanges(w http.ResponseWriter, r *http.Request, id string, refresh bool) {
	runtime, ok := m.runtime.(WorkspaceRuntime)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace changes are not supported by this runtime"})
		return
	}
	changes, err := runtime.WorkspaceChanges(r.Context(), id, refresh)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, changes)
}

func (m *Manager) handleMCPProxy(w http.ResponseWriter, r *http.Request) {
	id := extractBackendID(r.URL.Path)
	if id == "" {
		http.NotFound(w, r)
		return
	}

	m.mu.RLock()
	backend, ok := m.backends[id]
	m.mu.RUnlock()
	if !ok || backend == nil || backend.Handler == nil {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix("/mcp/"+id, backend.Handler).ServeHTTP(w, r)
	if r.Method == http.MethodPost {
		if runtime, ok := m.runtime.(WorkspaceRuntime); ok {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if _, err := runtime.WorkspaceChanges(ctx, id, true); err != nil {
					m.logger.Warn("failed to refresh workspace changes", "id", id, "error", err)
				}
			}()
		}
	}
}

func (m *Manager) getBackend(id string) *ManagedBackend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	backend, ok := m.backends[id]
	if !ok || backend == nil {
		return nil
	}
	clone := *backend
	clone.Handler = nil
	clone.Env = nil
	return &clone
}

func (m *Manager) listBackends() []*ManagedBackend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	backends := make([]*ManagedBackend, 0, len(m.backends))
	for _, backend := range m.backends {
		if backend == nil {
			continue
		}
		clone := *backend
		clone.Handler = nil
		clone.Env = nil
		backends = append(backends, &clone)
	}
	return backends
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.RLock()
	ids := make([]string, 0, len(m.backends))
	for id := range m.backends {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	var firstErr error
	for _, id := range ids {
		if err := m.runtime.Stop(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.mu.Lock()
	m.backends = make(map[string]*ManagedBackend)
	m.mu.Unlock()
	if err := m.runtime.Cleanup(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func extractBackendID(path string) string {
	trimmed := strings.TrimPrefix(path, "/mcp/")
	if trimmed == "" || trimmed == path {
		return ""
	}
	parts := strings.SplitN(strings.TrimPrefix(trimmed, "/"), "/", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
