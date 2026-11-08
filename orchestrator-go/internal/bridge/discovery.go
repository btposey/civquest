package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

var (
	// ErrDiscoveryNotFound is returned when the discovery artifact cannot be found.
	ErrDiscoveryNotFound = errors.New("discovery artifact not found")
	// ErrDiscoveryTimeout is returned when polling exceeds the requested duration.
	ErrDiscoveryTimeout = errors.New("timed out waiting for ready server discovery metadata")
	// ErrServerNotReady is returned when metadata is present but status is not READY.
	ErrServerNotReady = errors.New("server status is not READY")
	// ErrServerProcessDead is returned when the server PID in discovery metadata does not exist.
	ErrServerProcessDead = errors.New("server process is not running (stale discovery artifact)")
)

// ServerMeta represents the serialized runtime discovery artifact emitted by the CivQuest server mod.
type ServerMeta struct {
	Status      string `json:"status"`
	Transport   string `json:"grpc_transport"`
	Port        int    `json:"grpc_port"`
	Host        string `json:"grpc_host"`
	ServerPID   int64  `json:"server_pid"`
	SharedKey   string `json:"shared_key"`
	TimestampMs int64  `json:"timestamp_ms"`
}

// String implements fmt.Stringer and masks credentials to prevent secret leakage in logs.
func (m *ServerMeta) String() string {
	if m == nil {
		return "<nil>"
	}
	maskedKey := "[NONE]"
	if m.SharedKey != "" {
		maskedKey = "[REDACTED]"
	}
	return fmt.Sprintf("ServerMeta{status: %q, host: %q, port: %d, server_pid: %d, shared_key: %s}",
		m.Status, m.Host, m.Port, m.ServerPID, maskedKey)
}

// Addr formats the dialable host:port address for the gRPC bridge.
func (m *ServerMeta) Addr() string {
	if m == nil {
		return ""
	}
	if m.Port <= 0 || m.Port > 65535 {
		return ""
	}
	host := strings.Trim(strings.TrimSpace(m.Host), "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(m.Port))
}

// IsReady returns true if the discovery metadata reports status "READY", has a valid port, and a non-empty shared key.
func (m *ServerMeta) IsReady() bool {
	return m != nil &&
		strings.EqualFold(m.Status, "READY") &&
		m.Port > 0 &&
		m.Port <= 65535 &&
		m.SharedKey != ""
}

// isLocalHost checks if the given host string targets the local machine.
func isLocalHost(host string) bool {
	h := strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	return h == "" || h == "127.0.0.1" || h == "localhost" || h == "::1" || h == "0.0.0.0" || h == "::"
}

// isProcessAlive checks whether the process is alive using signal 0.
func isProcessAlive(pid int64) bool {
	if pid <= 1 {
		return true
	}
	err := syscall.Kill(int(pid), 0)
	// err == nil: process exists and can be signaled
	// err == EPERM: process exists but caller lacks permission to signal it
	// err == ESRCH: process does not exist
	return err == nil || errors.Is(err, syscall.EPERM)
}

// IsProcessAlive reports whether the ServerPID corresponds to a running process if local.
func (m *ServerMeta) IsProcessAlive() bool {
	if m == nil || m.ServerPID <= 1 || !isLocalHost(m.Host) {
		return true
	}
	return isProcessAlive(m.ServerPID)
}

func getCandidateDiscoveryPaths() []string {
	paths := xdg.NewPaths()
	return []string{
		filepath.Join(paths.ServerDir(), "civquest-server-meta.json"),
		filepath.Join(paths.RuntimeDir(), "civquest-server-meta.json"),
		"civquest-server-meta.json",
		"mod-neoforge/run/civquest-server-meta.json",
		"../mod-neoforge/run/civquest-server-meta.json",
		"../../mod-neoforge/run/civquest-server-meta.json",
		"../../../mod-neoforge/run/civquest-server-meta.json",
		"mod-neoforge/build/integration-server/civquest-server-meta.json",
		"../mod-neoforge/build/integration-server/civquest-server-meta.json",
		"../../mod-neoforge/build/integration-server/civquest-server-meta.json",
		"../../../mod-neoforge/build/integration-server/civquest-server-meta.json",
	}
}

// ResolveDiscoveryPath returns the candidate path for the discovery artifact.
// It prioritizes explicitPath, then CIVQUEST_DISCOVERY_PATH, followed by dynamic XDG locations,
// and finally development fallbacks.
func ResolveDiscoveryPath(preferredPath string) string {
	if preferredPath != "" {
		return filepath.Clean(preferredPath)
	}
	if env := os.Getenv("CIVQUEST_DISCOVERY_PATH"); env != "" {
		return filepath.Clean(env)
	}

	candidates := getCandidateDiscoveryPaths()

	for _, cand := range candidates {
		cleaned := filepath.Clean(cand)
		if fi, err := os.Stat(cleaned); err == nil && !fi.IsDir() {
			return cleaned
		}
	}

	paths := xdg.NewPaths()
	return filepath.Clean(filepath.Join(paths.ServerDir(), "civquest-server-meta.json"))
}

// ReadDiscovery reads and parses the discovery artifact from disk.
// If path is empty, it searches candidates for an active server instance with status READY and alive process.
// If the metadata references a local server and ServerPID > 1, it verifies process liveness.
func ReadDiscovery(path string) (*ServerMeta, error) {
	if path == "" {
		candidates := getCandidateDiscoveryPaths()
		for _, cand := range candidates {
			cleaned := filepath.Clean(cand)
			data, err := os.ReadFile(cleaned)
			if err != nil {
				continue
			}
			var meta ServerMeta
			if err := json.Unmarshal(data, &meta); err != nil {
				continue
			}
			if meta.IsReady() && meta.IsProcessAlive() {
				return &meta, nil
			}
		}
	}

	resolved := ResolveDiscoveryPath(path)
	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrDiscoveryNotFound, resolved)
		}
		return nil, fmt.Errorf("failed reading discovery file %s: %w", resolved, err)
	}

	var meta ServerMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed parsing discovery JSON from %s: %w", resolved, err)
	}

	if meta.ServerPID > 1 && isLocalHost(meta.Host) {
		if !isProcessAlive(meta.ServerPID) {
			return nil, fmt.Errorf("%w (pid=%d): process does not exist", ErrServerProcessDead, meta.ServerPID)
		}
	}

	return &meta, nil
}

// WaitForDiscovery polls for discovery metadata until status is "READY" and credentials are valid,
// or until the timeout elapses.
func WaitForDiscovery(path string, timeout time.Duration) (*ServerMeta, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return WaitForDiscoveryContext(ctx, path)
}

// WaitForDiscoveryContext polls for discovery metadata with a caller-provided context.
func WaitForDiscoveryContext(ctx context.Context, path string) (*ServerMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDiscoveryTimeout, err)
	}

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		meta, err := ReadDiscovery(path)
		if err == nil {
			if meta.IsReady() {
				return meta, nil
			}
			lastErr = fmt.Errorf("%w (status=%q, port=%d, key_set=%t)", ErrServerNotReady, meta.Status, meta.Port, meta.SharedKey != "")
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("%w: %w (last error: %w)", ErrDiscoveryTimeout, ctx.Err(), lastErr)
			}
			return nil, fmt.Errorf("%w: %w", ErrDiscoveryTimeout, ctx.Err())
		case <-ticker.C:
		}
	}
}
