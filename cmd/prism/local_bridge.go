package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultLocalBridgeURL = "http://127.0.0.1:3001"

type localBridgeProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	url    string
}

func startLocalDockerBridge(parent context.Context, logger *slog.Logger) (*localBridgeProcess, error) {
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		return nil, fmt.Errorf("docker socket is not mounted at /var/run/docker.sock")
	}
	bin, err := exec.LookPath("prism-bridge")
	if err != nil {
		return nil, fmt.Errorf("prism-bridge binary is not available in PATH")
	}

	ctx, cancel := context.WithCancel(parent)
	args := []string{
		"manage",
		"--host", "127.0.0.1",
		"--port", "3001",
		"--runtime", "docker",
		"--image-full", sandboxImage(),
		"--image-node", sandboxImageFor("PRISM_SANDBOX_IMAGE_NODE"),
		"--image-python", sandboxImageFor("PRISM_SANDBOX_IMAGE_PYTHON"),
	}
	if network := strings.TrimSpace(os.Getenv("PRISM_BRIDGE_NETWORK")); network != "" {
		args = append(args, "--network", network)
	}

	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // binary path is resolved from PATH inside Prism runtime
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start local bridge: %w", err)
	}

	proc := &localBridgeProcess{cmd: cmd, cancel: cancel, url: defaultLocalBridgeURL}
	go func() {
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			logger.Error("local bridge exited", "error", err)
		}
	}()

	if err := waitForLocalBridge(ctx, proc.url); err != nil {
		proc.Stop()
		return nil, err
	}
	logger.Info("local Docker bridge started", "url", proc.url, "sandbox_image", sandboxImage())
	return proc, nil
}

func (p *localBridgeProcess) Stop() {
	if p == nil {
		return
	}
	p.cancel()
	done := make(chan struct{})
	go func() {
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Signal(os.Interrupt)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func waitForLocalBridge(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", http.NoBody)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("local bridge did not become healthy at %s", baseURL)
}

func sandboxImage() string {
	if image := strings.TrimSpace(os.Getenv("PRISM_SANDBOX_IMAGE")); image != "" {
		return image
	}
	if image := strings.TrimSpace(os.Getenv("BRIDGE_IMAGE_FULL")); image != "" {
		return image
	}
	return "ghcr.io/1broseidon/prism:latest"
}

func sandboxImageFor(env string) string {
	if image := strings.TrimSpace(os.Getenv(env)); image != "" {
		return image
	}
	return sandboxImage()
}
