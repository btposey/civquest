package bridge_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
)

func TestReadDiscoveryValid(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "civquest-server-meta.json")

	content := `{
  "status": "READY",
  "grpc_transport": "tcp",
  "grpc_port": 54321,
  "grpc_host": "127.0.0.1",
  "server_pid": 12345,
  "shared_key": "test-key-abc-123",
  "timestamp_ms": 1731000000000
}`
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatalf("Failed writing test file: %v", err)
	}

	meta, err := bridge.ReadDiscovery(filePath)
	if err != nil {
		t.Fatalf("ReadDiscovery failed: %v", err)
	}

	if meta.Status != "READY" {
		t.Errorf("Expected status READY, got %s", meta.Status)
	}
	if meta.Transport != "tcp" {
		t.Errorf("Expected transport tcp, got %s", meta.Transport)
	}
	if meta.Port != 54321 {
		t.Errorf("Expected port 54321, got %d", meta.Port)
	}
	if meta.Host != "127.0.0.1" {
		t.Errorf("Expected host 127.0.0.1, got %s", meta.Host)
	}
	if meta.ServerPID != 12345 {
		t.Errorf("Expected PID 12345, got %d", meta.ServerPID)
	}
	if meta.SharedKey != "test-key-abc-123" {
		t.Errorf("Expected shared key test-key-abc-123, got %s", meta.SharedKey)
	}
	if meta.TimestampMs != 1731000000000 {
		t.Errorf("Expected timestamp_ms 1731000000000, got %d", meta.TimestampMs)
	}
	if !meta.IsReady() {
		t.Errorf("IsReady() should be true")
	}
	if meta.Addr() != "127.0.0.1:54321" {
		t.Errorf("Expected addr 127.0.0.1:54321, got %s", meta.Addr())
	}
}

func TestReadDiscoveryNotFound(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "nonexistent-meta.json")

	_, err := bridge.ReadDiscovery(filePath)
	if err == nil {
		t.Fatalf("Expected error for non-existent file, got nil")
	}
	if !errors.Is(err, bridge.ErrDiscoveryNotFound) {
		t.Errorf("Expected ErrDiscoveryNotFound, got: %v", err)
	}
}

func TestReadDiscoveryInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "corrupt.json")
	if err := os.WriteFile(filePath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("Failed writing file: %v", err)
	}

	_, err := bridge.ReadDiscovery(filePath)
	if err == nil {
		t.Fatalf("Expected error for invalid JSON, got nil")
	}
}

func TestServerMetaAddr(t *testing.T) {
	tests := []struct {
		name     string
		meta     *bridge.ServerMeta
		expected string
	}{
		{
			name:     "nil receiver",
			meta:     nil,
			expected: "",
		},
		{
			name: "standard host",
			meta: &bridge.ServerMeta{
				Host: "192.168.1.100",
				Port: 50051,
			},
			expected: "192.168.1.100:50051",
		},
		{
			name: "wildcard 0.0.0.0 defaults to 127.0.0.1",
			meta: &bridge.ServerMeta{
				Host: "0.0.0.0",
				Port: 61234,
			},
			expected: "127.0.0.1:61234",
		},
		{
			name: "empty host defaults to 127.0.0.1",
			meta: &bridge.ServerMeta{
				Host: "",
				Port: 61234,
			},
			expected: "127.0.0.1:61234",
		},
		{
			name: "ipv6 wildcard defaults to 127.0.0.1",
			meta: &bridge.ServerMeta{
				Host: "::",
				Port: 61234,
			},
			expected: "127.0.0.1:61234",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.meta.Addr()
			if actual != tc.expected {
				t.Errorf("Addr() = %q, expected %q", actual, tc.expected)
			}
		})
	}
}

func TestServerMetaIsReady(t *testing.T) {
	if (*bridge.ServerMeta)(nil).IsReady() {
		t.Errorf("nil ServerMeta.IsReady() should be false")
	}
	if !(&bridge.ServerMeta{Status: "READY"}).IsReady() {
		t.Errorf("READY should be true")
	}
	if !(&bridge.ServerMeta{Status: "ready"}).IsReady() {
		t.Errorf("ready (case insensitive) should be true")
	}
	if (&bridge.ServerMeta{Status: "STOPPED"}).IsReady() {
		t.Errorf("STOPPED should be false")
	}
	if (&bridge.ServerMeta{Status: "STARTING"}).IsReady() {
		t.Errorf("STARTING should be false")
	}
}

func TestResolveDiscoveryPath(t *testing.T) {
	// 1. Explicit path
	explicit := "/tmp/explicit-path.json"
	if p := bridge.ResolveDiscoveryPath(explicit); p != explicit {
		t.Errorf("Expected explicit path %s, got %s", explicit, p)
	}

	// 2. Env variable
	envPath := "/tmp/env-path.json"
	t.Setenv("CIVQUEST_DISCOVERY_PATH", envPath)
	if p := bridge.ResolveDiscoveryPath(""); p != envPath {
		t.Errorf("Expected env path %s, got %s", envPath, p)
	}

	// 3. Fallback when env unset and no candidates exist
	t.Setenv("CIVQUEST_DISCOVERY_PATH", "")
	p := bridge.ResolveDiscoveryPath("")
	if p == "" {
		t.Errorf("Fallback path should not be empty")
	}
}

func TestWaitForDiscoverySuccess(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "civquest-server-meta.json")

	// Initially write STOPPED
	stoppedJSON := `{"status": "STARTING", "grpc_port": 0, "shared_key": "wait-key"}`
	if err := os.WriteFile(filePath, []byte(stoppedJSON), 0600); err != nil {
		t.Fatalf("Failed writing initial file: %v", err)
	}

	// Launch async update to READY after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		readyJSON := `{
  "status": "READY",
  "grpc_transport": "tcp",
  "grpc_port": 50051,
  "grpc_host": "127.0.0.1",
  "server_pid": 9999,
  "shared_key": "ready-key-123",
  "timestamp_ms": 1731000005000
}`
		_ = os.WriteFile(filePath, []byte(readyJSON), 0600)
	}()

	meta, err := bridge.WaitForDiscovery(filePath, 2*time.Second)
	if err != nil {
		t.Fatalf("WaitForDiscovery failed: %v", err)
	}

	if !meta.IsReady() {
		t.Errorf("Expected ready meta, got status: %s", meta.Status)
	}
	if meta.Port != 50051 {
		t.Errorf("Expected port 50051, got %d", meta.Port)
	}
	if meta.SharedKey != "ready-key-123" {
		t.Errorf("Expected shared key ready-key-123, got %s", meta.SharedKey)
	}
}

func TestWaitForDiscoveryTimeout(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "nonexistent.json")

	_, err := bridge.WaitForDiscovery(filePath, 150*time.Millisecond)
	if err == nil {
		t.Fatalf("Expected timeout error, got nil")
	}
	if !errors.Is(err, bridge.ErrDiscoveryTimeout) {
		t.Errorf("Expected ErrDiscoveryTimeout, got: %v", err)
	}
}

func TestWaitForDiscoveryContextCancelled(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "nonexistent.json")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := bridge.WaitForDiscoveryContext(ctx, filePath)
	if err == nil {
		t.Fatalf("Expected error on cancelled context, got nil")
	}
	if !errors.Is(err, bridge.ErrDiscoveryTimeout) {
		t.Errorf("Expected ErrDiscoveryTimeout, got: %v", err)
	}
}
