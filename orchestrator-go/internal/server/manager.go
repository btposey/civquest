package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// SetupOptions provides configuration flags for server setup.
type SetupOptions struct {
	Port       int
	OnlineMode bool
	MaxPlayers int
	Difficulty string
	Gamemode   string
	Motd       string
	Force      bool
}

// DefaultSetupOptions returns standard defaults for playtesting.
func DefaultSetupOptions() SetupOptions {
	return SetupOptions{
		Port:       25565,
		OnlineMode: false,
		MaxPlayers: 20,
		Difficulty: "normal",
		Gamemode:   "survival",
		Motd:       "CivQuest Autonomous Settlement Server",
		Force:      false,
	}
}

// Setup initializes the server environment for playtesting:
// 1. Initializes server directories ($XDG_DATA_HOME/civquest/server, mods, state).
// 2. Writes eula.txt with eula=true.
// 3. Writes server.properties with the specified port, online-mode, difficulty, etc.
// 4. Writes a default runner script run.sh if absent.
// 5. Writes server manifest if absent.
func (m *Manager) Setup(opts SetupOptions) error {
	if opts.Port < 1 || opts.Port > 65535 {
		return fmt.Errorf("invalid port %d: must be between 1 and 65535", opts.Port)
	}
	if opts.MaxPlayers < 1 || opts.MaxPlayers > 10000 {
		opts.MaxPlayers = 20
	}
	if opts.Difficulty == "" {
		opts.Difficulty = "normal"
	}
	if opts.Gamemode == "" {
		opts.Gamemode = "survival"
	}
	if opts.Motd == "" {
		opts.Motd = "CivQuest Autonomous Settlement Server"
	}

	for _, field := range []struct{ name, val string }{
		{"motd", opts.Motd},
		{"difficulty", opts.Difficulty},
		{"gamemode", opts.Gamemode},
	} {
		if strings.ContainsAny(field.val, "\r\n") {
			return fmt.Errorf("%s cannot contain newline characters", field.name)
		}
	}

	// 1. Initialize directories and acquire exclusive lock
	if err := os.MkdirAll(m.resolver.StateDir(), 0750); err != nil {
		return fmt.Errorf("failed initializing state directory: %w", err)
	}

	lockPath := filepath.Join(m.resolver.StateDir(), "server.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("failed opening server lock file: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another server process holds lock: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	status, err := m.Status()
	if err == nil && status.Running {
		return fmt.Errorf("cannot setup server: server is currently running (PID %d)", status.PID)
	}

	if err := m.resolver.InitServerDirectories(); err != nil {
		return fmt.Errorf("failed initializing server directories: %w", err)
	}

	// 2. Write eula.txt
	eulaPath := m.resolver.ServerEULAPath()
	if _, err := os.Stat(eulaPath); os.IsNotExist(err) || opts.Force {
		eulaContent := "# By changing the setting below to TRUE you are indicating your agreement to the Mojang EULA (https://aka.ms/MinecraftEULA).\neula=true\n"
		if err := os.WriteFile(eulaPath, []byte(eulaContent), 0640); err != nil {
			return fmt.Errorf("failed writing eula.txt: %w", err)
		}
	}

	// 3. Write server.properties
	propsPath := m.resolver.ServerPropertiesPath()
	if _, err := os.Stat(propsPath); os.IsNotExist(err) || opts.Force {
		propsContent := fmt.Sprintf(`# CivQuest Dedicated Server Properties
level-name=world
pause-when-empty-seconds=-1
online-mode=%t
server-port=%d
enable-rcon=false
difficulty=%s
gamemode=%s
max-players=%d
sync-chunk-writes=true
view-distance=10
simulation-distance=10
motd=%s
`, opts.OnlineMode, opts.Port, opts.Difficulty, opts.Gamemode, opts.MaxPlayers, opts.Motd)
		if err := os.WriteFile(propsPath, []byte(propsContent), 0640); err != nil {
			return fmt.Errorf("failed writing server.properties: %w", err)
		}
	}

	// 4. Write runner script if absent
	runScriptPath := m.resolver.ServerRunScriptPath()
	if _, err := os.Stat(runScriptPath); os.IsNotExist(err) || opts.Force {
		scriptContent := `#!/usr/bin/env bash
set -euo pipefail
umask 0027

SERVER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SERVER_DIR}"

JAVA_OPTS="${JAVA_OPTS:--Xms2G -Xmx4G}"

cleanup() {
    echo "[civquest-server] Received shutdown signal. Gracefully exiting..."
    exit 0
}
trap cleanup SIGTERM SIGINT

if compgen -G "${SERVER_DIR}/libraries/net/neoforged/neoforge/*/unix_args.txt" > /dev/null 2>&1; then
    UNIX_ARGS="$(compgen -G "${SERVER_DIR}/libraries/net/neoforged/neoforge/*/unix_args.txt" | head -n 1)"
    JVM_ARGS=""
    if [ -f "${SERVER_DIR}/user_jvm_args.txt" ]; then
        JVM_ARGS="@user_jvm_args.txt"
    fi
    exec java ${JAVA_OPTS} ${JVM_ARGS} @"${UNIX_ARGS}" nogui "$@"
elif compgen -G "${SERVER_DIR}/neoforge-*.jar" > /dev/null 2>&1; then
    NEOFORGE_JAR="$(compgen -G "${SERVER_DIR}/neoforge-*.jar" | head -n 1)"
    exec java ${JAVA_OPTS} -jar "${NEOFORGE_JAR}" nogui "$@"
elif [ -f "${SERVER_DIR}/server.jar" ]; then
    exec java ${JAVA_OPTS} -jar "${SERVER_DIR}/server.jar" nogui "$@"
else
    echo "[civquest-server] CivQuest Server Runner Active (staging mode)"
    while true; do
        sleep 1
    done
fi
`
		if err := os.WriteFile(runScriptPath, []byte(scriptContent), 0750); err != nil {
			return fmt.Errorf("failed writing runner script: %w", err)
		}
	}

	// 5. Ensure manifest exists if absent
	manifestPath := m.resolver.ServerManifestPath()
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) || opts.Force {
		manifest := domain.ServerManifest{
			Version:          "0.1.0",
			MinecraftVersion: "1.21.1",
			NeoForgeVersion:  "21.1.265",
			InstalledAt:      time.Now().UTC().Format(time.RFC3339),
			ServerDir:        m.resolver.ServerDir(),
			ModsDir:          m.resolver.ServerModsDir(),
			RunnerScript:     runScriptPath,
		}
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return fmt.Errorf("failed marshaling server manifest: %w", err)
		}
		if err := os.WriteFile(manifestPath, data, 0640); err != nil {
			return fmt.Errorf("failed writing server manifest: %w", err)
		}
	}

	return nil
}

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
	// Ensure state directory exists
	if err := os.MkdirAll(m.resolver.StateDir(), 0750); err != nil {
		return 0, fmt.Errorf("failed creating state dir: %w", err)
	}

	// Guard against concurrent invocations using a file lock
	lockPath := filepath.Join(m.resolver.StateDir(), "server.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return 0, fmt.Errorf("failed opening server lock file: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return 0, fmt.Errorf("another server startup process holds lock: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

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

	// Determine runner script path
	runnerScript := m.resolver.ServerRunScriptPath()
	if status.Manifest != nil && status.Manifest.RunnerScript != "" {
		runnerScript = status.Manifest.RunnerScript
	}

	// Validate runner script path: must be clean and reside strictly within ServerDir
	cleanServerDir, err := filepath.Abs(m.resolver.ServerDir())
	if err != nil {
		return 0, fmt.Errorf("failed resolving canonical server directory: %w", err)
	}
	if resolvedDir, err := filepath.EvalSymlinks(cleanServerDir); err == nil {
		cleanServerDir = resolvedDir
	}

	var candidatePath string
	if filepath.IsAbs(runnerScript) {
		candidatePath = filepath.Clean(runnerScript)
	} else {
		candidatePath = filepath.Clean(filepath.Join(cleanServerDir, runnerScript))
	}

	fi, err := os.Stat(candidatePath)
	if err != nil {
		return 0, fmt.Errorf("server runner script not found at %s: %w", candidatePath, err)
	}
	if fi.IsDir() {
		return 0, fmt.Errorf("server runner script path is a directory: %s", candidatePath)
	}

	resolvedRunner, err := filepath.EvalSymlinks(candidatePath)
	if err != nil {
		return 0, fmt.Errorf("failed resolving symlinks for runner script %s: %w", candidatePath, err)
	}

	rel, err := filepath.Rel(cleanServerDir, resolvedRunner)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return 0, fmt.Errorf("security violation: runner script %s traverses outside server directory %s", runnerScript, cleanServerDir)
	}

	runnerScript = resolvedRunner

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

// isServerProcess inspects /proc/<pid>/cmdline on Linux to verify the process is a CivQuest/Minecraft server.
func isServerProcess(pid int, serverDir, runnerScript string) bool {
	cmdlineBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		// If /proc does not exist (non-Linux or procfs not mounted), assume valid if IsProcessRunning passed
		return true
	}
	if len(cmdlineBytes) == 0 {
		return false
	}
	cmdline := strings.ToLower(string(cmdlineBytes))
	if strings.Contains(cmdline, "civquest") ||
		strings.Contains(cmdline, "run.sh") ||
		strings.Contains(cmdline, "neoforge") ||
		strings.Contains(cmdline, "minecraft") ||
		strings.Contains(cmdline, "server.jar") ||
		(serverDir != "" && strings.Contains(cmdline, strings.ToLower(serverDir))) ||
		(runnerScript != "" && strings.Contains(cmdline, strings.ToLower(filepath.Base(runnerScript)))) {
		return true
	}
	return false
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
	if err != nil || pid <= 1 {
		_ = os.Remove(pidPath)
		return fmt.Errorf("%w: invalid PID (%d) in file; removed stale file", ErrServerNotRunning, pid)
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

	// Verify process cmdline to prevent signaling recycled PID
	runnerScript := m.resolver.ServerRunScriptPath()
	if !isServerProcess(pid, m.resolver.ServerDir(), runnerScript) {
		_ = os.Remove(pidPath)
		return fmt.Errorf("%w (PID %d is running but cmdline does not match server; removed stale PID file)", ErrServerNotRunning, pid)
	}

	// Send SIGTERM: only use process group signal (-pid) if pid > 1 and process is group leader
	pgid, pgidErr := syscall.Getpgid(pid)
	isLeader := (pgidErr == nil && pgid == pid && pid > 1)

	if isLeader {
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
			_ = process.Signal(syscall.SIGTERM)
		}
	} else {
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
			// Context was cancelled (e.g. user pressed Ctrl+C during cq server stop)
			// Do NOT send SIGKILL and do NOT remove server.pid!
			// The server is continuing graceful shutdown in the background.
			if logFile, err := os.OpenFile(m.resolver.ServerLogPath(), os.O_WRONLY|os.O_APPEND, 0640); err == nil {
				fmt.Fprintf(logFile, "[%s] Stop wait cancelled by context; server PID %d continuing shutdown in background\n",
					time.Now().Format(time.RFC3339), pid)
				_ = logFile.Close()
			}
			return fmt.Errorf("server stop wait cancelled; server (PID %d) is continuing graceful shutdown in background: %w", pid, ctx.Err())

		case <-ticker.C:
			// Check if process has terminated
			if !domain.IsProcessRunning(pid) {
				// Process is gone
				_ = os.Remove(pidPath)
				return nil
			}
			if time.Now().After(deadline) {
				// Internal graceful wait deadline expired: force kill
				if isLeader {
					if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
						_ = process.Signal(syscall.SIGKILL)
					}
				} else {
					_ = process.Signal(syscall.SIGKILL)
				}
				_ = os.Remove(pidPath)
				if logFile, err := os.OpenFile(m.resolver.ServerLogPath(), os.O_WRONLY|os.O_APPEND, 0640); err == nil {
					fmt.Fprintf(logFile, "[%s] Graceful stop timeout expired; SIGKILL sent to PID %d\n", time.Now().Format(time.RFC3339), pid)
					_ = logFile.Close()
				}
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

	// Read lines safely without 64KB bufio.Scanner limit
	reader := bufio.NewReader(file)
	var ring []string
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			ring = append(ring, line)
			if len(ring) > nLines {
				ring = ring[1:]
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("error reading log file: %w", err)
		}
	}

	for _, line := range ring {
		fmt.Fprintln(out, line)
	}

	if !follow {
		return nil
	}

	// Follow mode: continue reading with reader
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
