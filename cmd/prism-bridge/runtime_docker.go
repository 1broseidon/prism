package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	networktypes "github.com/docker/docker/api/types/network"
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
	containerID string
	endpoint    string
	status      string
}

type dockerSpec struct {
	name         string
	image        string
	endpoint     string
	labels       map[string]string
	containerCfg *container.Config
	hostCfg      *container.HostConfig
	networking   *networktypes.NetworkingConfig
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

func (d *DockerRuntime) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error) { //nolint:gocritic // Runtime interface fixes signature
	spec := d.buildSpec(&req)
	resp, err := d.client.ContainerCreate(ctx, spec.containerCfg, spec.hostCfg, spec.networking, nil, spec.name)
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}

	if startErr := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); startErr != nil {
		_ = d.client.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("start container: %w", startErr)
	}

	endpoint, err := d.resolveEndpoint(ctx, resp.ID, spec.name)
	if err != nil {
		_ = d.stopAndRemove(context.Background(), resp.ID)
		return nil, err
	}

	if waitErr := d.waitForHealthy(ctx, endpoint); waitErr != nil {
		_ = d.stopAndRemove(context.Background(), resp.ID)
		return nil, waitErr
	}

	tools, err := d.discoverTools(ctx, endpoint)
	if err != nil {
		_ = d.stopAndRemove(context.Background(), resp.ID)
		return nil, err
	}

	handler, err := d.newProxyHandler(endpoint)
	if err != nil {
		_ = d.stopAndRemove(context.Background(), resp.ID)
		return nil, err
	}

	backend := &dockerBackend{containerID: resp.ID, endpoint: endpoint, status: "running"}
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
	hostCfg := &container.HostConfig{
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
	}
	var networking *networktypes.NetworkingConfig
	if d.network != "" {
		networking = &networktypes.NetworkingConfig{EndpointsConfig: map[string]*networktypes.EndpointSettings{
			d.network: {},
		}}
	}
	return &dockerSpec{
		name:         name,
		image:        image,
		endpoint:     endpoint,
		labels:       labels,
		containerCfg: cfg,
		hostCfg:      hostCfg,
		networking:   networking,
	}
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
	timeoutSeconds := 10
	stopErr := d.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeoutSeconds})
	removeErr := d.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	if stopErr != nil && !client.IsErrNotFound(stopErr) {
		return stopErr
	}
	if removeErr != nil && !client.IsErrNotFound(removeErr) {
		return removeErr
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
