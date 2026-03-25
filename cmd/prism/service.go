package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const pidFile = "/tmp/prism.pid"

// runService dispatches service management subcommands.
func runService(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: prism service <start|stop|restart|reload|status>")
		os.Exit(1)
	}

	switch args[0] {
	case "start":
		serviceStart(args[1:])
	case "stop":
		serviceStop()
	case "restart":
		serviceStop()
		serviceStart(args[1:])
	case "reload":
		serviceReload()
	case "status":
		serviceStatus()
	default:
		fmt.Fprintf(os.Stderr, "unknown service command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: prism service <start|stop|restart|reload|status>")
		os.Exit(1)
	}
}

func serviceStart(args []string) {
	if pid := readPID(); pid > 0 {
		if processRunning(pid) {
			fmt.Fprintf(os.Stderr, "prism is already running (pid %d)\n", pid)
			os.Exit(1)
		}
		// Stale pid file
		_ = os.Remove(pidFile)
	}

	// Build the command: re-exec ourselves with "serve" and forward remaining args.
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to find executable: %v\n", err)
		os.Exit(1)
	}

	cmdArgs := append([]string{"serve"}, args...)
	cmd := exec.CommandContext(context.Background(), exe, cmdArgs...) //nolint:gosec // re-exec ourselves
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from terminal

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start prism: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil { //nolint:gosec // pid file is not sensitive
		fmt.Fprintf(os.Stderr, "warning: failed to write pid file: %v\n", err)
	}

	fmt.Printf("prism started (pid %d)\n", cmd.Process.Pid)

	// Detach — don't wait for the child.
	_ = cmd.Process.Release()
}

func serviceStop() {
	pid := readPID()
	if pid <= 0 || !processRunning(pid) {
		fmt.Fprintln(os.Stderr, "prism is not running")
		_ = os.Remove(pidFile)
		os.Exit(1)
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "failed to stop prism (pid %d): %v\n", pid, err)
		os.Exit(1)
	}

	_ = os.Remove(pidFile)
	fmt.Printf("prism stopped (pid %d)\n", pid)
}

func serviceReload() {
	pid := readPID()
	if pid <= 0 || !processRunning(pid) {
		fmt.Fprintln(os.Stderr, "prism is not running")
		os.Exit(1)
	}

	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		fmt.Fprintf(os.Stderr, "failed to reload prism (pid %d): %v\n", pid, err)
		os.Exit(1)
	}

	fmt.Printf("prism reloaded (pid %d)\n", pid)
}

func serviceStatus() {
	pid := readPID()
	if pid <= 0 || !processRunning(pid) {
		fmt.Println("prism is not running")
		_ = os.Remove(pidFile)
		return
	}

	// Try the admin API for richer info.
	fmt.Printf("prism is running (pid %d)\n", pid)

	// Read config to find admin port.
	configPath := findConfig()
	if configPath == "" {
		return
	}
	fmt.Printf("config: %s\n", configPath)
}

// --- helpers ---

func readPID() int {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

func processRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks if the process exists without actually sending a signal.
	return proc.Signal(syscall.Signal(0)) == nil
}

func findConfig() string {
	for _, p := range []string{"config.json", "/etc/prism/config.json"} {
		if abs, err := filepath.Abs(p); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	return ""
}
