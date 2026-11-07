package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var (
	// ErrDiscoveryNotFound is returned when the discovery artifact cannot be found.
	ErrDiscoveryNotFound = errors.New("discovery artifact not found")
	// ErrDiscoveryTimeout is returned when polling exceeds the requested duration.
	ErrDiscoveryTimeout = errors.New("timed out waiting for ready server discovery metadata")
	// ErrServerNotReady is returned when metadata is present but status is not READY.
	ErrServerNotReady = errors.New("server status is not READY")
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

// Addr formats the dialable host:port address for the gRPC bridge.
func (m *ServerMeta) Addr() string {
	if m == nil {
		return ""
	}
	host := m.Host
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d", host, m.Port)
}

// IsReady returns true if the discovery metadata reports status "READY".
func (m *ServerMeta) IsReady() bool {
	return m != nil && strings.EqualFold(m.Status, "READY")
}

// ResolveDiscoveryPath returns the candidate path for the discovery artifact.
// It prioritizes explicitPath, then CIVQUEST_DISCOVERY_PATH, followed by known candidate locations.
func ResolveDiscoveryPath(preferredPath string) string {
	if preferredPath != "" {
		return preferredPath
	}
	if env := os.Getenv("CIVQUEST_DISCOVERY_PATH"); env != "" {
		return env
	}

	candidates := []string{
		"civquest-server-meta.json",
		"../../mod-neoforge/build/integration-server/civquest-server-meta.json",
		"../../mod-neoforge/run/civquest-server-meta.json",
		"../mod-neoforge/build/integration-server/civquest-server-meta.json",
		"../mod-neoforge/run/civquest-server-meta.json",
		"mod-neoforge/build/integration-server/civquest-server-meta.json",
		"mod-neoforge/run/civquest-server-meta.json",
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}

	return "civquest-server-meta.json"
}

// ReadDiscovery reads and parses the discovery artifact from disk.
func ReadDiscovery(path string) (*ServerMeta, error) {
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
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		resolved := ResolveDiscoveryPath(path)
		meta, err := ReadDiscovery(resolved)
		if err == nil {
			if meta.IsReady() && meta.Port > 0 {
				return meta, nil
			}
			lastErr = fmt.Errorf("%w (status=%q, port=%d)", ErrServerNotReady, meta.Status, meta.Port)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("%w: %v", ErrDiscoveryTimeout, lastErr)
			}
			return nil, ErrDiscoveryTimeout
		case <-ticker.C:
		}
	}
}
