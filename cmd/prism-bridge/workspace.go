package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	iofs "io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	ws "github.com/1broseidon/prism/internal/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const workspaceBridgeVersion = "0.1.0"

var workspaceServiceIDRE = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

type workspaceOptions struct {
	gateway   string
	token     string
	id        string
	backendID string
	namespace string
	root      string
	filesOnly bool
	command   []string
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

func runWorkspace(logger *slog.Logger, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "--help", "-h", "help":
			printUsage()
			return nil
		case "run":
			return runWorkspaceBridge(logger, args[1:])
		case "install":
			return installWorkspaceBridge(logger, args[1:])
		case "uninstall":
			return uninstallWorkspaceBridge(logger, args[1:])
		}
	}
	return runWorkspaceBridge(logger, args)
}

func parseWorkspaceOptions(args []string) (*workspaceOptions, error) {
	flags, command := splitAtDashDash(args)
	fs := flag.NewFlagSet("workspace", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	hostname, _ := os.Hostname()
	defaultID := sanitizeWorkspaceServiceID(hostname)
	if defaultID == "" {
		defaultID = "local"
	}
	defaultRoot, _ := os.Getwd()

	opts := &workspaceOptions{}
	fs.StringVar(&opts.gateway, "gateway", envOrDefault("PRISM_GATEWAY_URL", ""), "Prism gateway base URL")
	fs.StringVar(&opts.token, "token", envOrDefault("PRISM_WORKSPACE_TOKEN", ""), "workspace bridge token")
	fs.StringVar(&opts.id, "id", envOrDefault("PRISM_WORKSPACE_ID", defaultID), "workspace id")
	fs.StringVar(&opts.backendID, "backend", envOrDefault("PRISM_WORKSPACE_BACKEND", "Brainfile"), "backend id")
	fs.StringVar(&opts.namespace, "namespace", envOrDefault("PRISM_WORKSPACE_NAMESPACE", ""), "tool namespace")
	fs.StringVar(&opts.root, "root", envOrDefault("PRISM_WORKSPACE_ROOT", defaultRoot), "working directory for stdio server")
	fs.BoolVar(&opts.filesOnly, "files-only", false, "only expose workspace snapshot/apply operations; do not start a local stdio server")
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	if len(fs.Args()) > 0 {
		return nil, fmt.Errorf("unknown workspace flags: %s", strings.Join(fs.Args(), " "))
	}

	if len(command) == 0 && !opts.filesOnly {
		command = []string{"npx", "@brainfile/cli", "mcp"}
	}
	opts.command = command
	opts.gateway = strings.TrimRight(strings.TrimSpace(opts.gateway), "/")
	opts.token = strings.TrimSpace(opts.token)
	opts.id = sanitizeWorkspaceServiceID(opts.id)
	opts.backendID = sanitizeWorkspaceServiceID(opts.backendID)
	opts.root = strings.TrimSpace(opts.root)
	if opts.namespace == "" {
		opts.namespace = opts.backendID + "-" + opts.id
	}
	opts.namespace = sanitizeWorkspaceServiceID(opts.namespace)

	switch {
	case opts.gateway == "":
		return nil, errors.New("--gateway or PRISM_GATEWAY_URL is required")
	case opts.token == "":
		return nil, errors.New("--token or PRISM_WORKSPACE_TOKEN is required")
	case opts.id == "":
		return nil, errors.New("--id is invalid")
	case opts.backendID == "":
		return nil, errors.New("--backend is invalid")
	case opts.namespace == "":
		return nil, errors.New("--namespace is invalid")
	case opts.root == "":
		return nil, errors.New("--root is required")
	}
	return opts, nil
}

func runWorkspaceBridge(logger *slog.Logger, args []string) error { //nolint:gocyclo // main loop handles protocol operation dispatch
	opts, err := parseWorkspaceOptions(args)
	if err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()

	var session *mcp.ClientSession
	var toolList []*mcp.Tool
	if !opts.filesOnly {
		session, err = connectWorkspaceStdio(ctx, opts, logger)
		if err != nil {
			return err
		}
		defer func() { _ = session.Close() }()

		tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
		if err != nil {
			return fmt.Errorf("list tools from workspace backend: %w", err)
		}
		if len(tools.Tools) == 0 {
			return fmt.Errorf("workspace backend %q reported no tools", opts.backendID)
		}
		toolList = tools.Tools
	}

	client := &http.Client{}
	if err := registerWorkspace(ctx, client, opts, toolList); err != nil {
		return err
	}
	defer func() {
		unregCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = unregisterWorkspace(unregCtx, client, opts)
	}()

	logger.Info("workspace bridge connected", "gateway", opts.gateway, "workspace", opts.id, "namespace", opts.namespace, "root", opts.root, "tools", len(toolList))
	backoff := 500 * time.Millisecond
	var (
		cachedUsage    int64
		usageRefreshed time.Time
	)
	const usageRefreshInterval = 60 * time.Second
	for ctx.Err() == nil {
		if time.Since(usageRefreshed) > usageRefreshInterval {
			if used, err := computeWorkspaceUsage(opts.root); err != nil {
				logger.Debug("workspace usage walk failed", "root", opts.root, "error", err)
			} else {
				cachedUsage = used
			}
			usageRefreshed = time.Now()
		}
		req, err := pollWorkspace(ctx, client, opts, cachedUsage)
		if err != nil {
			logger.Warn("workspace poll failed", "error", err)
			if registerErr := registerWorkspace(ctx, client, opts, toolList); registerErr != nil {
				logger.Warn("workspace re-register failed", "error", registerErr)
			} else {
				logger.Info("workspace bridge re-registered", "gateway", opts.gateway, "workspace", opts.id, "namespace", opts.namespace)
				backoff = 500 * time.Millisecond
				continue
			}
			sleepContext(ctx, backoff)
			backoff = minDuration(10*time.Second, backoff*2)
			continue
		}
		backoff = 500 * time.Millisecond
		if req == nil {
			continue
		}
		result := workspaceCallResult{
			WorkspaceID: opts.id,
			RequestID:   req.RequestID,
		}
		switch req.Kind {
		case "snapshot":
			policy := ws.SnapshotPolicy{}
			if req.Snapshot != nil {
				policy = *req.Snapshot
			}
			snap, err := ws.CreateSnapshot(opts.root, policy)
			if err != nil {
				result.Error = err.Error()
			} else {
				result.Snapshot = snap
			}
		case "apply":
			if req.Apply == nil {
				result.Error = "apply request is missing payload"
			} else {
				applied, err := ws.ApplyChangeSet(opts.root, req.Apply.Changes, req.Apply.Policy)
				if err != nil {
					result.Error = err.Error()
				} else {
					result.Apply = applied
				}
			}
		default:
			if session == nil {
				result.Error = "workspace bridge was started with --files-only"
				break
			}
			callCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
			res, err := session.CallTool(callCtx, &mcp.CallToolParams{
				Name:      req.ToolName,
				Arguments: req.Arguments,
			})
			cancel()
			if err != nil {
				result.Error = err.Error()
			} else {
				result.Result = res
			}
		}
		if err := postWorkspaceResult(ctx, client, opts, &result); err != nil {
			logger.Warn("workspace result post failed", "request", req.RequestID, "error", err)
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil
	}
	return ctx.Err()
}

func connectWorkspaceStdio(ctx context.Context, opts *workspaceOptions, logger *slog.Logger) (*mcp.ClientSession, error) {
	cmd := exec.CommandContext(ctx, opts.command[0], opts.command[1:]...) //nolint:gosec // command is explicit operator input
	cmd.Dir = opts.root
	transport := &mcp.CommandTransport{
		Command:           cmd,
		TerminateDuration: 5 * time.Second,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "prism-bridge-workspace", Version: workspaceBridgeVersion}, nil)
	logger.Info("connecting to workspace stdio server", "command", opts.command, "root", opts.root)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to workspace stdio server: %w", err)
	}
	return session, nil
}

func registerWorkspace(ctx context.Context, client *http.Client, opts *workspaceOptions, tools []*mcp.Tool) error {
	hostname, _ := os.Hostname()
	payload := workspaceRegisterRequest{
		WorkspaceID: opts.id,
		Hostname:    hostname,
		Root:        opts.root,
		Version:     workspaceBridgeVersion,
	}
	if len(tools) > 0 {
		payload.Backends = []workspaceRegisterBackend{{
			ID:        opts.backendID,
			Namespace: opts.namespace,
			Tools:     tools,
		}}
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return workspaceJSON(reqCtx, client, http.MethodPost, opts.gateway+"/workspace/register", opts.token, payload, nil)
}

func unregisterWorkspace(ctx context.Context, client *http.Client, opts *workspaceOptions) error {
	return workspaceJSON(ctx, client, http.MethodPost, opts.gateway+"/workspace/unregister", opts.token, map[string]string{
		"workspace_id": opts.id,
	}, nil)
}

func pollWorkspace(ctx context.Context, client *http.Client, opts *workspaceOptions, usedBytes int64) (*workspaceCallRequest, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	url := opts.gateway + "/workspace/poll?workspace_id=" + opts.id
	if usedBytes > 0 {
		url += "&used_bytes=" + strconv.FormatInt(usedBytes, 10)
	}
	var response workspacePollResponse
	if err := workspaceJSON(reqCtx, client, http.MethodGet, url, opts.token, nil, &response); err != nil {
		return nil, err
	}
	return response.Request, nil
}

// computeWorkspaceUsage walks the workspace root and returns the total file
// size in bytes. Errors are logged by the caller; this returns 0 on failure so
// the bridge can keep polling.
func computeWorkspaceUsage(root string) (int64, error) {
	if strings.TrimSpace(root) == "" {
		return 0, nil
	}
	var total int64
	err := filepath.WalkDir(root, func(_ string, d iofs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries (e.g. permission denied on a subtree)
			// rather than aborting the whole walk.
			if d != nil && d.IsDir() {
				return iofs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func postWorkspaceResult(ctx context.Context, client *http.Client, opts *workspaceOptions, result *workspaceCallResult) error {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return workspaceJSON(reqCtx, client, http.MethodPost, opts.gateway+"/workspace/result", opts.token, result, nil)
}

func workspaceJSON(ctx context.Context, client *http.Client, method, url, token string, body, out any) error {
	var r io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
		r = &buf
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r) //nolint:gosec // URL is the operator-configured Prism gateway
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req) //nolint:gosec // request goes to the operator-configured Prism gateway
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 96<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errorBody struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &errorBody); err == nil && errorBody.Error != "" {
			return fmt.Errorf("%s: %s", resp.Status, errorBody.Error)
		}
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return err
		}
	}
	return nil
}

func installWorkspaceBridge(logger *slog.Logger, args []string) error {
	opts, err := parseWorkspaceOptions(args)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate prism-bridge binary: %w", err)
	}
	exe, _ = filepath.Abs(exe)
	runArgs := make([]string, 0, 14+len(opts.command))
	runArgs = append(runArgs, exe, "workspace", "run", "--gateway", opts.gateway, "--id", opts.id, "--backend", opts.backendID, "--namespace", opts.namespace, "--root", opts.root)
	if opts.filesOnly {
		runArgs = append(runArgs, "--files-only")
	}
	if len(opts.command) > 0 {
		runArgs = append(runArgs, "--")
		runArgs = append(runArgs, opts.command...)
	}

	switch runtime.GOOS {
	case "linux":
		return installWorkspaceSystemd(logger, opts, runArgs)
	case "darwin":
		return installWorkspaceLaunchd(logger, opts, runArgs)
	case "windows":
		return installWorkspaceScheduledTask(logger, opts, runArgs)
	default:
		return fmt.Errorf("workspace install is not supported on %s", runtime.GOOS)
	}
}

func uninstallWorkspaceBridge(logger *slog.Logger, args []string) error {
	opts, err := parseWorkspaceOptions(append(args, "--"))
	if err != nil {
		return err
	}
	name := workspaceServiceName(opts.id)
	switch runtime.GOOS {
	case "linux":
		unit := name + ".service"
		_ = runServiceCommand("systemctl", "--user", "disable", "--now", unit)
		path := filepath.Join(userHomeDir(), ".config", "systemd", "user", unit)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		envPath := filepath.Join(userHomeDir(), ".config", "prism", name+".env")
		if err := os.Remove(envPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = runServiceCommand("systemctl", "--user", "daemon-reload")
		logger.Info("removed workspace bridge service", "service", unit)
		return nil
	case "darwin":
		plist := "com.prism." + name + ".plist"
		path := filepath.Join(userHomeDir(), "Library", "LaunchAgents", plist)
		_ = runServiceCommand("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid()), path)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		logger.Info("removed workspace bridge launch agent", "plist", plist)
		return nil
	case "windows":
		task := `Prism\` + name
		_ = runServiceCommand("schtasks", "/Delete", "/TN", task, "/F")
		logger.Info("removed workspace bridge scheduled task", "task", task)
		return nil
	default:
		return fmt.Errorf("workspace uninstall is not supported on %s", runtime.GOOS)
	}
}

func installWorkspaceSystemd(logger *slog.Logger, opts *workspaceOptions, runArgs []string) error {
	unit := workspaceServiceName(opts.id) + ".service"
	configDir := filepath.Join(userHomeDir(), ".config", "prism")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	envPath := filepath.Join(configDir, workspaceServiceName(opts.id)+".env")
	if err := os.WriteFile(envPath, []byte("PRISM_WORKSPACE_TOKEN="+opts.token+"\n"), 0o600); err != nil { //nolint:gosec // path is under ~/.config/prism with sanitized service id
		return err
	}
	dir := filepath.Join(userHomeDir(), ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(dir, unit)
	content := fmt.Sprintf(`[Unit]
Description=Prism workspace bridge (%s)
After=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
Environment=%s
EnvironmentFile=%s
ExecStart=%s
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, opts.id, systemdPath(opts.root), systemdQuote("PATH="+os.Getenv("PATH")), systemdPath(envPath), quoteArgs(runArgs))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil { //nolint:gosec // path is under ~/.config/systemd/user with sanitized service id
		return err
	}
	if err := runServiceCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("service file written to %s, but systemctl daemon-reload failed: %w", path, err)
	}
	if err := runServiceCommand("systemctl", "--user", "enable", unit); err != nil {
		return fmt.Errorf("service file written to %s, but systemctl enable failed: %w", path, err)
	}
	if err := runServiceCommand("systemctl", "--user", "restart", unit); err != nil {
		return fmt.Errorf("service file written to %s, but systemctl restart failed: %w", path, err)
	}
	logger.Info("installed workspace bridge service", "service", unit, "path", path)
	return nil
}

func installWorkspaceLaunchd(logger *slog.Logger, opts *workspaceOptions, runArgs []string) error {
	label := "com.prism." + workspaceServiceName(opts.id)
	dir := filepath.Join(userHomeDir(), "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(dir, label+".plist")
	argsXML := strings.Builder{}
	for _, arg := range runArgs {
		argsXML.WriteString("    <string>")
		argsXML.WriteString(html.EscapeString(arg))
		argsXML.WriteString("</string>\n")
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>WorkingDirectory</key><string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PRISM_WORKSPACE_TOKEN</key><string>%s</string>
  </dict>
  <key>ProgramArguments</key>
  <array>
%s  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
`, label, html.EscapeString(opts.root), html.EscapeString(opts.token), argsXML.String())
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil { //nolint:gosec // path is under ~/Library/LaunchAgents with sanitized service id
		return err
	}
	_ = runServiceCommand("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid()), path)
	if err := runServiceCommand("launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), path); err != nil {
		return fmt.Errorf("launch agent written to %s, but launchctl bootstrap failed: %w", path, err)
	}
	logger.Info("installed workspace bridge launch agent", "label", label, "path", path)
	return nil
}

func installWorkspaceScheduledTask(logger *slog.Logger, opts *workspaceOptions, runArgs []string) error {
	task := `Prism\` + workspaceServiceName(opts.id)
	runArgs = withWorkspaceTokenArg(runArgs, opts.token)
	tr := quoteArgs(runArgs)
	if err := runServiceCommand("schtasks", "/Create", "/TN", task, "/SC", "ONLOGON", "/TR", tr, "/F"); err != nil {
		return fmt.Errorf("create scheduled task: %w", err)
	}
	if err := runServiceCommand("schtasks", "/Run", "/TN", task); err != nil {
		return fmt.Errorf("scheduled task created, but start failed: %w", err)
	}
	logger.Info("installed workspace bridge scheduled task", "task", task)
	return nil
}

func withWorkspaceTokenArg(runArgs []string, token string) []string {
	out := make([]string, 0, len(runArgs)+2)
	inserted := false
	for _, arg := range runArgs {
		if arg == "--" && !inserted {
			out = append(out, "--token", token)
			inserted = true
		}
		out = append(out, arg)
	}
	if !inserted {
		out = append(out, "--token", token)
	}
	return out
}

func runServiceCommand(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run() //nolint:gosec // service commands and args are fixed or sanitized by the caller
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		cancel()
		signal.Stop(ch)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func sanitizeWorkspaceServiceID(s string) string {
	s = strings.TrimSpace(s)
	s = workspaceServiceIDRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-_.")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func workspaceServiceName(id string) string {
	return "prism-bridge-workspace-" + sanitizeWorkspaceServiceID(id)
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	return "."
}

func quoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = systemdQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func systemdQuote(s string) string {
	return strconv.Quote(s)
}

func systemdPath(s string) string {
	replacer := strings.NewReplacer(
		"\\", "\\x5c",
		" ", "\\x20",
		"\t", "\\x09",
		"\n", "",
	)
	return replacer.Replace(s)
}

func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
