package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/config"
	ws "github.com/1broseidon/prism/internal/workspace"
	"github.com/docker/docker/api/types/container"
	mounttypes "github.com/docker/docker/api/types/mount"
	networktypes "github.com/docker/docker/api/types/network"
	volumetypes "github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const managedBackendPort = "3001"

type DockerRuntime struct {
	client       *client.Client
	defaultImage string
	images       map[string]string
	network      string
	labelPrefix  string
	namePrefix   string
	logger       *slog.Logger

	mu       sync.RWMutex
	backends map[string]*dockerBackend
}

type dockerBackend struct {
	containerID  string
	endpoint     string
	status       string
	volumeName   string
	cacheVolume  string
	workspace    *config.WorkspaceConfig
	baseSnapshot *ws.Snapshot
	changes      *ws.ChangeSet
}

type dockerSpec struct {
	name          string
	image         string
	endpoint      string
	labels        map[string]string
	containerCfg  *container.Config
	hostCfg       *container.HostConfig
	networking    *networktypes.NetworkingConfig
	volumeName    string
	cacheVolume   string
	installScript string
}

func NewDockerRuntime(logger *slog.Logger, opts *DockerRuntimeOptions) (*DockerRuntime, error) {
	if logger == nil {
		logger = slog.Default()
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	images := map[string]string{
		"base":   strings.TrimSpace(opts.BaseImage),
		"node":   strings.TrimSpace(opts.NodeImage),
		"python": strings.TrimSpace(opts.PythonImage),
		"full":   strings.TrimSpace(opts.FullImage),
	}
	return &DockerRuntime{
		client:       cli,
		defaultImage: strings.TrimSpace(opts.DefaultImage),
		images:       images,
		network:      strings.TrimSpace(opts.Network),
		labelPrefix:  nonEmpty(opts.LabelPrefix, "prism.bridge"),
		namePrefix:   nonEmpty(opts.NamePrefix, "prism-managed-"),
		logger:       logger,
		backends:     make(map[string]*dockerBackend),
	}, nil
}

type DockerRuntimeOptions struct {
	DefaultImage string
	BaseImage    string
	NodeImage    string
	PythonImage  string
	FullImage    string
	Network      string
	LabelPrefix  string
	NamePrefix   string
}

func (d *DockerRuntime) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error) { //nolint:gocritic,gocyclo // Runtime interface fixes signature; spawn owns cleanup branches
	spec := d.buildSpec(&req)
	if spec.volumeName != "" {
		if _, err := d.client.VolumeCreate(ctx, volumetypes.CreateOptions{
			Name:   spec.volumeName,
			Labels: spec.labels,
		}); err != nil {
			return nil, fmt.Errorf("create workspace volume: %w", err)
		}
	}
	if spec.cacheVolume != "" {
		if _, err := d.client.VolumeCreate(ctx, volumetypes.CreateOptions{
			Name:   spec.cacheVolume,
			Labels: spec.labels,
		}); err != nil {
			_ = d.removeVolume(context.Background(), spec.volumeName)
			return nil, fmt.Errorf("create package cache volume: %w", err)
		}
	}
	if spec.installScript != "" {
		if err := d.runInstallStep(ctx, spec); err != nil {
			_ = d.removeVolume(context.Background(), spec.volumeName)
			_ = d.removeVolume(context.Background(), spec.cacheVolume)
			return nil, err
		}
	}
	if spec.volumeName != "" {
		if err := d.runWorkspaceVolumeInit(ctx, spec); err != nil {
			_ = d.removeVolume(context.Background(), spec.volumeName)
			_ = d.removeVolume(context.Background(), spec.cacheVolume)
			return nil, err
		}
	}
	resp, err := d.client.ContainerCreate(ctx, spec.containerCfg, spec.hostCfg, spec.networking, nil, spec.name)
	if err != nil {
		_ = d.removeVolume(context.Background(), spec.volumeName)
		_ = d.removeVolume(context.Background(), spec.cacheVolume)
		return nil, fmt.Errorf("create container: %w", err)
	}

	if req.WorkspaceSnapshot != nil {
		if copyErr := d.copyWorkspaceSnapshot(ctx, resp.ID, &req); copyErr != nil {
			_ = d.stopAndRemove(context.Background(), resp.ID)
			_ = d.removeVolume(context.Background(), spec.volumeName)
			_ = d.removeVolume(context.Background(), spec.cacheVolume)
			return nil, copyErr
		}
	}

	if startErr := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); startErr != nil {
		_ = d.client.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
		_ = d.removeVolume(context.Background(), spec.volumeName)
		_ = d.removeVolume(context.Background(), spec.cacheVolume)
		return nil, fmt.Errorf("start container: %w", startErr)
	}

	endpoint, err := d.resolveEndpoint(ctx, resp.ID, spec.name)
	if err != nil {
		_ = d.stopAndRemove(context.Background(), resp.ID)
		_ = d.removeVolume(context.Background(), spec.volumeName)
		_ = d.removeVolume(context.Background(), spec.cacheVolume)
		return nil, err
	}

	if waitErr := d.waitForHealthy(ctx, endpoint); waitErr != nil {
		logs := d.containerLogs(ctx, resp.ID)
		_ = d.stopAndRemove(context.Background(), resp.ID)
		_ = d.removeVolume(context.Background(), spec.volumeName)
		_ = d.removeVolume(context.Background(), spec.cacheVolume)
		return nil, fmt.Errorf("%w: %s", waitErr, logs)
	}

	tools, err := d.discoverTools(ctx, endpoint)
	if err != nil {
		_ = d.stopAndRemove(context.Background(), resp.ID)
		_ = d.removeVolume(context.Background(), spec.volumeName)
		_ = d.removeVolume(context.Background(), spec.cacheVolume)
		return nil, err
	}

	handler, err := d.newProxyHandler(endpoint)
	if err != nil {
		_ = d.stopAndRemove(context.Background(), resp.ID)
		_ = d.removeVolume(context.Background(), spec.volumeName)
		_ = d.removeVolume(context.Background(), spec.cacheVolume)
		return nil, err
	}

	backend := &dockerBackend{
		containerID:  resp.ID,
		endpoint:     endpoint,
		status:       "running",
		volumeName:   spec.volumeName,
		cacheVolume:  spec.cacheVolume,
		workspace:    config.NormalizeWorkspaceConfig(req.Workspace),
		baseSnapshot: req.WorkspaceSnapshot,
	}
	d.mu.Lock()
	d.backends[req.ID] = backend
	d.mu.Unlock()

	return &SpawnResult{
		Endpoint:    "/mcp/" + req.ID,
		Handler:     handler,
		Tools:       tools,
		ContainerID: resp.ID,
		Status:      "running",
		Runtime:     "docker",
	}, nil
}

func (d *DockerRuntime) Stop(ctx context.Context, id string) error {
	d.mu.RLock()
	backend, ok := d.backends[id]
	d.mu.RUnlock()
	if !ok {
		return fmt.Errorf("backend %q not found", id)
	}
	if err := d.stopAndRemove(ctx, backend.containerID); err != nil {
		return err
	}
	if err := d.removeVolume(ctx, backend.volumeName); err != nil {
		return err
	}
	if err := d.removeVolume(ctx, backend.cacheVolume); err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.backends, id)
	d.mu.Unlock()
	backend.status = "stopped"
	return nil
}

func (d *DockerRuntime) Status(ctx context.Context, id string) (*RuntimeStatus, error) {
	d.mu.RLock()
	backend, ok := d.backends[id]
	d.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("backend %q not found", id)
	}
	inspect, err := d.client.ContainerInspect(ctx, backend.containerID)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	status := backend.status
	if inspect.State != nil && inspect.State.Status != "" {
		status = inspect.State.Status
	}
	pid := 0
	if inspect.State != nil {
		pid = inspect.State.Pid
	}
	return &RuntimeStatus{ContainerID: backend.containerID, PID: pid, Status: status}, nil
}

func (d *DockerRuntime) Cleanup(ctx context.Context) error {
	d.mu.RLock()
	ids := make([]string, 0, len(d.backends))
	for id := range d.backends {
		ids = append(ids, id)
	}
	d.mu.RUnlock()

	var firstErr error
	for _, id := range ids {
		if err := d.Stop(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (d *DockerRuntime) buildSpec(req *SpawnRequest) *dockerSpec {
	name := d.containerName(req.ID)
	image := d.imageForRequest(req)
	sandbox := config.NormalizeSandboxConfig(req.Sandbox, config.SandboxProfileCompat)
	workspaceCfg := config.NormalizeWorkspaceConfig(req.Workspace)
	labels := map[string]string{
		d.labelPrefix + ".id":      req.ID,
		d.labelPrefix + ".managed": "true",
	}
	env := envFromMap(req.Env)
	endpoint := fmt.Sprintf("http://%s:%s/mcp", name, managedBackendPort)
	cfg := &container.Config{
		Image:      image,
		Entrypoint: []string{"prism-bridge"},
		Cmd:        append([]string{"serve", "--port", managedBackendPort, "--"}, append([]string{req.Command}, req.Args...)...),
		Env:        env,
		Labels:     labels,
	}
	var volumeName string
	var cacheVolume string
	var installScript string
	if workspaceCfg != nil && req.WorkspaceSnapshot != nil {
		cfg.WorkingDir = "/workspace"
		cfg.Env = upsertEnv(cfg.Env, "PWD", "/workspace")
		volumeName = d.containerName(req.ID) + "-workspace"
		cacheVolume = d.containerName(req.ID) + "-cache"
		cfg.Env = upsertEnv(cfg.Env, "NPM_CONFIG_CACHE", "/cache/npm")
		cfg.Env = upsertEnv(cfg.Env, "NPM_CONFIG_IGNORE_SCRIPTS", "true")
		cfg.Env = upsertEnv(cfg.Env, "NPM_CONFIG_PREFER_OFFLINE", "true")
		cfg.Env = upsertEnv(cfg.Env, "UV_CACHE_DIR", "/cache/uv")
		installScript = installScriptForRequest(req, &sandbox)
	}
	hostCfg := &container.HostConfig{
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
	}
	applySandboxToDockerSpec(&sandbox, cfg, hostCfg)
	if volumeName != "" {
		hostCfg.Mounts = append(hostCfg.Mounts, mounttypes.Mount{
			Type:   mounttypes.TypeVolume,
			Source: volumeName,
			Target: "/workspace",
		})
	}
	if cacheVolume != "" {
		hostCfg.Mounts = append(hostCfg.Mounts, mounttypes.Mount{
			Type:   mounttypes.TypeVolume,
			Source: cacheVolume,
			Target: "/cache",
		})
	}
	if volumeName != "" && !sandbox.RunsAsRoot() {
		wrapWorkspaceEntrypoint(req, &sandbox, cfg, hostCfg)
	}
	var networking *networktypes.NetworkingConfig
	if d.network != "" {
		networking = &networktypes.NetworkingConfig{EndpointsConfig: map[string]*networktypes.EndpointSettings{
			d.network: {},
		}}
	}
	return &dockerSpec{
		name:          name,
		image:         image,
		endpoint:      endpoint,
		labels:        labels,
		containerCfg:  cfg,
		hostCfg:       hostCfg,
		networking:    networking,
		volumeName:    volumeName,
		cacheVolume:   cacheVolume,
		installScript: installScript,
	}
}

func wrapWorkspaceEntrypoint(req *SpawnRequest, sandbox *config.SandboxConfig, cfg *container.Config, hostCfg *container.HostConfig) {
	uid := sandbox.UID
	gid := sandbox.GID
	if uid == 0 {
		uid = config.DefaultSandboxUID
	}
	if gid == 0 {
		gid = config.DefaultSandboxGID
	}
	serveArgs := append([]string{"prism-bridge", "serve", "--port", managedBackendPort, "--", req.Command}, req.Args...)
	script := fmt.Sprintf("chown -R %d:%d /workspace /cache && exec su-exec %d:%d %s", uid, gid, uid, gid, shellJoin(serveArgs))
	cfg.User = ""
	cfg.Entrypoint = []string{"sh", "-c"}
	cfg.Cmd = []string{script}
	hostCfg.CapAdd = append(hostCfg.CapAdd, "CHOWN", "SETGID", "SETUID")
}

func (d *DockerRuntime) runInstallStep(ctx context.Context, spec *dockerSpec) error {
	if spec.installScript == "" {
		return nil
	}
	cfg := &container.Config{
		Image:      spec.image,
		Entrypoint: []string{"sh", "-c"},
		Cmd:        []string{spec.installScript},
		Env: []string{
			"NPM_CONFIG_CACHE=/cache/npm",
			"NPM_CONFIG_IGNORE_SCRIPTS=true",
			"UV_CACHE_DIR=/cache/uv",
		},
		Labels: spec.labels,
	}
	hostCfg := &container.HostConfig{
		SecurityOpt: []string{"no-new-privileges:true"},
		Mounts: []mounttypes.Mount{{
			Type:   mounttypes.TypeVolume,
			Source: spec.cacheVolume,
			Target: "/cache",
		}},
	}
	resp, err := d.client.ContainerCreate(ctx, cfg, hostCfg, spec.networking, nil, spec.name+"-install")
	if err != nil {
		return fmt.Errorf("create install container: %w", err)
	}
	defer func() { _ = d.stopAndRemove(context.Background(), resp.ID) }()
	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start install container: %w", err)
	}
	statusCh, errCh := d.client.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("wait install container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			logs := d.containerLogs(ctx, resp.ID)
			return fmt.Errorf("install container exited with status %d: %s", status.StatusCode, logs)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (d *DockerRuntime) runWorkspaceVolumeInit(ctx context.Context, spec *dockerSpec) error {
	uid, gid := sandboxUserForSpec(spec.containerCfg.User)
	script := fmt.Sprintf("mkdir -p /workspace && chown -R %d:%d /workspace", uid, gid)
	cfg := &container.Config{
		Image:      spec.image,
		Entrypoint: []string{"sh", "-c"},
		Cmd:        []string{script},
		Labels:     spec.labels,
	}
	hostCfg := &container.HostConfig{
		SecurityOpt: []string{"no-new-privileges:true"},
		Mounts: []mounttypes.Mount{{
			Type:   mounttypes.TypeVolume,
			Source: spec.volumeName,
			Target: "/workspace",
		}},
	}
	resp, err := d.client.ContainerCreate(ctx, cfg, hostCfg, spec.networking, nil, spec.name+"-workspace-init")
	if err != nil {
		return fmt.Errorf("create workspace init container: %w", err)
	}
	defer func() { _ = d.stopAndRemove(context.Background(), resp.ID) }()
	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start workspace init container: %w", err)
	}
	statusCh, errCh := d.client.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("wait workspace init container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			logs := d.containerLogs(ctx, resp.ID)
			return fmt.Errorf("workspace init container exited with status %d: %s", status.StatusCode, logs)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func sandboxUserForSpec(user string) (uid, gid int) {
	if _, err := fmt.Sscanf(user, "%d:%d", &uid, &gid); err == nil {
		return uid, gid
	}
	return 0, 0
}

func (d *DockerRuntime) containerLogs(ctx context.Context, containerID string) string {
	reader, err := d.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "80",
	})
	if err != nil {
		return err.Error()
	}
	defer func() { _ = reader.Close() }()
	data, _ := io.ReadAll(io.LimitReader(reader, 64<<10))
	return strings.TrimSpace(string(data))
}

func installScriptForRequest(req *SpawnRequest, sandbox *config.SandboxConfig) string {
	uid := config.DefaultSandboxUID
	gid := config.DefaultSandboxGID
	if sandbox != nil {
		if sandbox.RunsAsRoot() {
			uid = 0
			gid = 0
		} else {
			if sandbox.UID != 0 {
				uid = sandbox.UID
			}
			if sandbox.GID != 0 {
				gid = sandbox.GID
			}
		}
	}
	switch detectRuntimeProfile(req.Command) {
	case "node":
		if pkg := npxPackage(req.Command, req.Args); pkg != "" {
			return fmt.Sprintf("mkdir -p /cache/npm /cache/uv && chown -R %d:%d /cache && npm cache add %s", uid, gid, shellQuote(pkg))
		}
	case "python":
		if pkg := uvxPackage(req.Command, req.Args); pkg != "" {
			return fmt.Sprintf("mkdir -p /cache/npm /cache/uv && chown -R %d:%d /cache && uvx %s --help >/dev/null", uid, gid, shellQuote(pkg))
		}
	}
	return fmt.Sprintf("mkdir -p /cache/npm /cache/uv && chown -R %d:%d /cache", uid, gid)
}

func npxPackage(command string, args []string) string {
	if filepath.Base(strings.TrimSpace(command)) != "npx" {
		return ""
	}
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "--package" || arg == "-p" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}

func uvxPackage(command string, args []string) string {
	if filepath.Base(strings.TrimSpace(command)) != "uvx" {
		return ""
	}
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func (d *DockerRuntime) copyWorkspaceSnapshot(ctx context.Context, containerID string, req *SpawnRequest) error {
	if req.WorkspaceSnapshot == nil {
		return nil
	}
	sandbox := config.NormalizeSandboxConfig(req.Sandbox, config.SandboxProfileCompat)
	uid := sandbox.UID
	gid := sandbox.GID
	if sandbox.RunsAsRoot() {
		uid = 0
		gid = 0
	}
	if uid == 0 && !sandbox.RunsAsRoot() {
		uid = config.DefaultSandboxUID
	}
	if gid == 0 && !sandbox.RunsAsRoot() {
		gid = config.DefaultSandboxGID
	}
	reader, err := ws.TarForContainer(req.WorkspaceSnapshot.Archive, uid, gid)
	if err != nil {
		return fmt.Errorf("prepare workspace snapshot: %w", err)
	}
	if err := d.client.CopyToContainer(ctx, containerID, "/workspace", reader, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copy workspace snapshot into container: %w", err)
	}
	return nil
}

func (d *DockerRuntime) WorkspaceChanges(ctx context.Context, id string, refresh bool) (*ws.ChangeSet, error) {
	d.mu.RLock()
	backend, ok := d.backends[id]
	d.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("backend %q not found", id)
	}
	if backend.baseSnapshot == nil {
		return &ws.ChangeSet{}, nil
	}
	if !refresh && backend.changes != nil {
		return backend.changes, nil
	}
	current, err := d.snapshotContainerWorkspace(ctx, backend.containerID)
	if err != nil {
		return nil, err
	}
	changes, err := ws.ChangeSetFromArchives(backend.baseSnapshot.BaseID, backend.baseSnapshot.Archive, current)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if currentBackend := d.backends[id]; currentBackend != nil {
		currentBackend.changes = changes
	}
	d.mu.Unlock()
	return changes, nil
}

func (d *DockerRuntime) DiscardWorkspaceChanges(ctx context.Context, id string) error {
	d.mu.RLock()
	backend, ok := d.backends[id]
	d.mu.RUnlock()
	if !ok {
		return fmt.Errorf("backend %q not found", id)
	}
	if backend.baseSnapshot == nil {
		return nil
	}
	current, err := d.snapshotContainerWorkspace(ctx, backend.containerID)
	if err != nil {
		return err
	}
	next, err := ws.SnapshotFromArchive(current)
	if err != nil {
		return err
	}
	d.mu.Lock()
	if currentBackend := d.backends[id]; currentBackend != nil {
		currentBackend.baseSnapshot = next
		currentBackend.changes = &ws.ChangeSet{BaseID: next.BaseID}
	}
	d.mu.Unlock()
	return nil
}

func (d *DockerRuntime) snapshotContainerWorkspace(ctx context.Context, containerID string) ([]byte, error) {
	reader, _, err := d.client.CopyFromContainer(ctx, containerID, "/workspace/.")
	if err != nil {
		return nil, fmt.Errorf("copy workspace from container: %w", err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return ws.ArchiveFromReader(bytes.NewReader(data))
}

func applySandboxToDockerSpec(sandbox *config.SandboxConfig, cfg *container.Config, hostCfg *container.HostConfig) {
	if sandbox == nil {
		return
	}
	if !sandbox.RunsAsRoot() {
		uid := sandbox.UID
		gid := sandbox.GID
		if uid == 0 {
			uid = config.DefaultSandboxUID
		}
		if gid == 0 {
			gid = config.DefaultSandboxGID
		}
		cfg.User = fmt.Sprintf("%d:%d", uid, gid)
		cfg.Env = upsertEnv(cfg.Env, "HOME", "/home/sandbox")
	}
	if sandbox.ReadOnlyRoot() {
		hostCfg.ReadonlyRootfs = true
		hostCfg.Tmpfs = map[string]string{
			"/tmp":          "rw,nosuid,nodev,exec,size=256m",
			"/home/sandbox": "rw,nosuid,nodev,exec,size=256m",
		}
		cfg.Env = upsertEnv(cfg.Env, "HOME", "/home/sandbox")
		cfg.Env = upsertEnv(cfg.Env, "XDG_CACHE_HOME", "/tmp/.cache")
		cfg.Env = upsertEnv(cfg.Env, "NPM_CONFIG_CACHE", "/tmp/.npm")
		cfg.Env = upsertEnv(cfg.Env, "UV_CACHE_DIR", "/tmp/uv")
	}
	if memory, err := config.ParseMemoryBytes(sandbox.Memory); err == nil && memory > 0 {
		hostCfg.Memory = memory
	}
	if sandbox.CPUs > 0 {
		hostCfg.NanoCPUs = int64(sandbox.CPUs * 1_000_000_000)
	}
	if sandbox.PidsLimit > 0 {
		limit := sandbox.PidsLimit
		hostCfg.PidsLimit = &limit
	}
	if len(sandbox.Mounts) > 0 {
		hostCfg.Mounts = make([]mounttypes.Mount, 0, len(sandbox.Mounts))
		for _, m := range sandbox.Mounts {
			hostCfg.Mounts = append(hostCfg.Mounts, mounttypes.Mount{
				Type:     mounttypes.TypeBind,
				Source:   m.Source,
				Target:   m.Target,
				ReadOnly: m.IsReadOnly(),
			})
		}
	}
}

func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func (d *DockerRuntime) imageForRequest(req *SpawnRequest) string {
	if override := strings.TrimSpace(req.Runtime); override != "" && override != "auto" {
		if image := d.images[override]; image != "" {
			return image
		}
	}

	switch detectRuntimeProfile(req.Command) {
	case "node":
		if d.images["node"] != "" {
			return d.images["node"]
		}
	case "python":
		if d.images["python"] != "" {
			return d.images["python"]
		}
	case "base":
		if d.images["base"] != "" {
			return d.images["base"]
		}
	}

	if d.images["full"] != "" {
		return d.images["full"]
	}
	if d.defaultImage != "" {
		return d.defaultImage
	}
	if d.images["node"] != "" {
		return d.images["node"]
	}
	if d.images["python"] != "" {
		return d.images["python"]
	}
	if d.images["base"] != "" {
		return d.images["base"]
	}
	return "prism-bridge:full"
}

func detectRuntimeProfile(command string) string {
	base := filepath.Base(strings.TrimSpace(command))
	switch base {
	case "npx", "node", "npm", "yarn", "pnpm", "bunx", "bun":
		return "node"
	case "uvx", "uv", "python", "python3", "pip", "pip3":
		return "python"
	default:
		return "full"
	}
}

func (d *DockerRuntime) containerName(id string) string {
	return d.namePrefix + sanitizeContainerName(id)
}

func sanitizeContainerName(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-._")
	if name == "" {
		return "backend"
	}
	return name
}

func envFromMap(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		result = append(result, k+"="+env[k])
	}
	return result
}

func (d *DockerRuntime) resolveEndpoint(ctx context.Context, containerID, containerName string) (string, error) {
	if d.network != "" {
		return fmt.Sprintf("http://%s:%s/mcp", containerName, managedBackendPort), nil
	}
	inspect, err := d.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("inspect container: %w", err)
	}
	for _, netCfg := range inspect.NetworkSettings.Networks {
		if netCfg.IPAddress != "" {
			return fmt.Sprintf("http://%s:%s/mcp", netCfg.IPAddress, managedBackendPort), nil
		}
	}
	return "", fmt.Errorf("container %s has no reachable network address", containerID)
}

func (d *DockerRuntime) waitForHealthy(ctx context.Context, endpoint string) error {
	healthURL := strings.TrimSuffix(endpoint, "/mcp") + "/health"
	httpClient := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	backoff := 100 * time.Millisecond
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("backend did not become healthy within 30s")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, http.NoBody) //nolint:gosec // endpoint is bridge-managed container address
		if err != nil {
			return err
		}
		resp, err := httpClient.Do(req) //nolint:gosec // same — controlled endpoint
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func (d *DockerRuntime) discoverTools(ctx context.Context, endpoint string) ([]string, error) {
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "prism-bridge-manage", Version: "0.1.0"}, nil)
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to spawned backend: %w", err)
	}
	defer func() { _ = session.Close() }()
	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("list tools from spawned backend: %w", err)
	}
	tools := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		tools = append(tools, tool.Name)
	}
	sort.Strings(tools)
	return tools, nil
}

func (d *DockerRuntime) newProxyHandler(endpoint string) (http.Handler, error) {
	target, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target) //nolint:gosec // target is bridge-managed container address
	originalDirector := proxy.Director
	basePath := target.Path
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = joinURLPath(basePath, req.URL.Path)
		if target.RawQuery == "" || req.URL.RawQuery == "" {
			req.URL.RawQuery = strings.Trim(strings.TrimSpace(target.RawQuery)+"&"+strings.TrimSpace(req.URL.RawQuery), "&")
		} else {
			req.URL.RawQuery = target.RawQuery + "&" + req.URL.RawQuery
		}
	}
	return proxy, nil
}

func joinURLPath(basePath, reqPath string) string {
	basePath = strings.TrimRight(basePath, "/")
	if reqPath == "" || reqPath == "/" {
		if basePath == "" {
			return "/"
		}
		return basePath
	}
	if strings.HasPrefix(reqPath, "/") {
		return basePath + reqPath
	}
	return basePath + "/" + reqPath
}

func (d *DockerRuntime) stopAndRemove(ctx context.Context, containerID string) error {
	timeoutSeconds := 2
	stopErr := d.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeoutSeconds})
	removeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	removeErr := d.client.ContainerRemove(removeCtx, containerID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	if stopErr != nil && !client.IsErrNotFound(stopErr) {
		if removeErr == nil || client.IsErrNotFound(removeErr) {
			return nil
		}
		return fmt.Errorf("stop container: %w; force remove: %w", stopErr, removeErr)
	}
	if removeErr != nil && !client.IsErrNotFound(removeErr) {
		return removeErr
	}
	return nil
}

func (d *DockerRuntime) removeVolume(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	if err := d.client.VolumeRemove(ctx, name, true); err != nil && !client.IsErrNotFound(err) {
		return fmt.Errorf("remove workspace volume: %w", err)
	}
	return nil
}

func (d *DockerRuntime) RemoveOrphans(ctx context.Context) error {
	containers, err := d.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	var firstErr error
	for i := range containers {
		c := &containers[i]
		if c.Labels[d.labelPrefix+".managed"] != "true" {
			continue
		}
		if err := d.stopAndRemove(ctx, c.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
