package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

var (
	ErrServerNotRunning = errors.New("server is not running")
	ErrServerAlreadyUp  = errors.New("server is already running")
)

// Manager manages the execution and lifecycle of the CivQuest NeoForge server.
type Manager struct {
	resolver *xdg.Resolver
}

// NewManager creates a new server lifecycle Manager.
func NewManager(r *xdg.Resolver) *Manager {
	if r == nil {
		r = xdg.DefaultResolver()
	}
	return &Manager{resolver: r}
}

// Resolver returns the underlying XDG resolver.
func (m *Manager) Resolver() *xdg.Resolver {
	return m.resolver
}

// Status inspects the server domain and returns its runtime status.
func (m *Manager) Status() (domain.ServerStatus, error) {
	return domain.DetectServer(m.resolver)
}

// Start launches the CivQuest server runner in the background and writes its PID.
func (m *Manager) Start() (int, error) {
	status, err := m.Status()
	if err != nil {
		return 0, fmt.Errorf("failed checking server status: %w", err)
	}

	if !status.Installed {
		return 0, domain.ErrServerNotInstalled
	}

	if status.Running {
		return status.PID, fmt.Errorf("%w (PID %d)", ErrServerAlreadyUp, status.PID)
	}

	// Clean up stale PID file if present
	_ = os.Remove(m.resolver.ServerPIDPath())

	// Ensure state directory exists
	if err := os.MkdirAll(m.resolver.StateDir(), 0750); err != nil {
		return 0, fmt.Errorf("failed creating state dir: %w", err)
	}

	// Determine runner script path
	runnerScript := m.resolver.ServerRunScriptPath()
	if status.Manifest != nil && status.Manifest.RunnerScript != "" {
		runnerScript = status.Manifest.RunnerScript
	}

	if _, err := os.Stat(runnerScript); err != nil {
		return 0, fmt.Errorf("server runner script not found at %s: %w", runnerScript, err)
	}

	// Open server log file for appending
	logPath := m.resolver.ServerLogPath()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return 0, fmt.Errorf("failed opening server log file %s: %w", logPath, err)
	}
	defer logFile.Close()

	// Launch runner script in a new session / process group
	cmd := exec.Command("bash", runnerScript)
	cmd.Dir = m.resolver.ServerDir()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Write start banner to log
	fmt.Fprintf(logFile, "=== [%s] Starting CivQuest NeoForge Server ===\n", time.Now().Format(time.RFC3339))

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed starting server process: %w", err)
	}

	pid := cmd.Process.Pid

	// Write PID file
	if err := os.WriteFile(m.resolver.ServerPIDPath(), []byte(fmt.Sprintf("%d\n", pid)), 0644); err != nil {
		// Attempt to kill process if writing PID file failed
		_ = cmd.Process.Kill()
		return 0, fmt.Errorf("failed writing server PID file: %w", err)
	}

	fmt.Fprintf(logFile, "[%s] Server process spawned with PID %d\n", time.Now().Format(time.RFC3339), pid)

	// Detach process
	_ = cmd.Process.Release()

	return pid, nil
}

// Stop gracefully stops the server via SIGTERM, waiting up to timeout before sending SIGKILL.
func (m *Manager) Stop(ctx context.Context, timeout time.Duration) error {
	pidPath := m.resolver.ServerPIDPath()
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrServerNotRunning
		}
		return fmt.Errorf("failed reading PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil || pid <= 0 {
		_ = os.Remove(pidPath)
		return errors.New("invalid PID file content; removed stale file")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return ErrServerNotRunning
	}

	// Verify if process is actually alive
	if !domain.IsProcessRunning(pid) {
		_ = os.Remove(pidPath)
		return fmt.Errorf("%w (cleaned up stale PID %d)", ErrServerNotRunning, pid)
	}

	// Send SIGTERM to process group or process
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// Fallback to sending to process directly
		_ = process.Signal(syscall.SIGTERM)
	}

	// Log stop request to server log if accessible
	if logFile, err := os.OpenFile(m.resolver.ServerLogPath(), os.O_WRONLY|os.O_APPEND, 0640); err == nil {
		fmt.Fprintf(logFile, "[%s] Received stop request (SIGTERM sent to PID %d)\n", time.Now().Format(time.RFC3339), pid)
		_ = logFile.Close()
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = process.Signal(syscall.SIGKILL)
			_ = os.Remove(pidPath)
			return ctx.Err()
		case <-ticker.C:
			// Check if process has terminated
			if !domain.IsProcessRunning(pid) {
				// Process is gone
				_ = os.Remove(pidPath)
				return nil
			}
			if time.Now().After(deadline) {
				// Force kill
				if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
					_ = process.Signal(syscall.SIGKILL)
				}
				_ = os.Remove(pidPath)
				return nil
			}
		}
	}
}

// Logs reads the last nLines from the server log and streams new lines if follow is true.
func (m *Manager) Logs(ctx context.Context, nLines int, follow bool, out io.Writer) error {
	logPath := m.resolver.ServerLogPath()
	file, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("server log file does not exist at %s (has the server ever run?)", logPath)
		}
		return fmt.Errorf("failed opening log file: %w", err)
	}
	defer file.Close()

	if nLines <= 0 {
		nLines = 50
	}

	// Read lines
	scanner := bufio.NewScanner(file)
	var ring []string
	for scanner.Scan() {
		ring = append(ring, scanner.Text())
		if len(ring) > nLines {
			ring = ring[1:]
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("error reading log file: %w", err)
	}

	for _, line := range ring {
		fmt.Fprintln(out, line)
	}

	if !follow {
		return nil
	}

	// Follow mode
	reader := bufio.NewReader(file)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for {
				line, err := reader.ReadString('\n')
				if len(line) > 0 {
					fmt.Fprint(out, line)
				}
				if err != nil {
					break
				}
			}
		}
	}
}
