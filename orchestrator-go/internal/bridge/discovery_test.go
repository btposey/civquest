package bridge_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
)

func TestReadDiscoveryValid(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "civquest-server-meta.json")

	pid := os.Getpid()
	content := fmt.Sprintf(`{
  "status": "READY",
  "grpc_transport": "tcp",
  "grpc_port": 54321,
  "grpc_host": "127.0.0.1",
  "server_pid": %d,
  "shared_key": "test-key-abc-123",
  "timestamp_ms": 1731000000000
}`, pid)
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
	if meta.ServerPID != int64(pid) {
		t.Errorf("Expected PID %d, got %d", pid, meta.ServerPID)
	}
	if meta.SharedKey != "test-key-abc-123" {
		t.Errorf("Expected shared key test-key-abc-123, got %s", meta.SharedKey)
	}
	if meta.TimestampMs != 1731000000000 {
		t.Errorf("Expected timestamp_ms 1731000000000, got %d", meta.TimestampMs)
	}
	if meta.ServerID != "" {
		t.Errorf("Expected raw server_id to be empty when omitted from JSON, got %s", meta.ServerID)
	}
	if meta.GetServerID() != "default" {
		t.Errorf("Expected GetServerID() == 'default', got %s", meta.GetServerID())
	}
	if meta.WorldName != "default" {
		t.Errorf("Expected default world_name, got %s", meta.WorldName)
	}
	if !meta.IsReady() {
		t.Errorf("IsReady() should be true")
	}
	if meta.Addr() != "127.0.0.1:54321" {
		t.Errorf("Expected addr 127.0.0.1:54321, got %s", meta.Addr())
	}
}

func TestReadDiscoveryServerIDAndWorldName(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "civquest-server-meta.json")

	pid := os.Getpid()
	content := fmt.Sprintf(`{
  "status": "READY",
  "grpc_transport": "tcp",
  "grpc_port": 54321,
  "grpc_host": "127.0.0.1",
  "server_pid": %d,
  "shared_key": "test-key-abc-123",
  "server_id": "test-server-42",
  "world_name": "Overworld_Prime",
  "timestamp_ms": 1731000000000
}`, pid)
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatalf("Failed writing test file: %v", err)
	}

	meta, err := bridge.ReadDiscovery(filePath)
	if err != nil {
		t.Fatalf("ReadDiscovery failed: %v", err)
	}

	if meta.ServerID != "test-server-42" {
		t.Errorf("Expected server_id 'test-server-42', got %s", meta.ServerID)
	}
	if meta.GetServerID() != "test-server-42" {
		t.Errorf("Expected GetServerID() 'test-server-42', got %s", meta.GetServerID())
	}
	if meta.WorldName != "Overworld_Prime" {
		t.Errorf("Expected world_name 'Overworld_Prime', got %s", meta.WorldName)
	}
	if meta.GetWorldName() != "Overworld_Prime" {
		t.Errorf("Expected GetWorldName() 'Overworld_Prime', got %s", meta.GetWorldName())
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

func TestReadDiscoveryDeadPID(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "stale-server-meta.json")

	// Use an extremely unlikely PID for a running process
	deadPID := 99999999
	content := fmt.Sprintf(`{
  "status": "READY",
  "grpc_transport": "tcp",
  "grpc_port": 54321,
  "grpc_host": "127.0.0.1",
  "server_pid": %d,
  "shared_key": "test-key-abc-123",
  "timestamp_ms": 1731000000000
}`, deadPID)
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatalf("Failed writing test file: %v", err)
	}

	_, err := bridge.ReadDiscovery(filePath)
	if err == nil {
		t.Fatalf("Expected error for dead PID, got nil")
	}
	if !errors.Is(err, bridge.ErrServerProcessDead) {
		t.Errorf("Expected ErrServerProcessDead, got: %v", err)
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
		{
			name: "bracketed ipv6 wildcard defaults to 127.0.0.1",
			meta: &bridge.ServerMeta{
				Host: "[::]",
				Port: 61234,
			},
			expected: "127.0.0.1:61234",
		},
		{
			name: "ipv6 loopback ::1 formatted with brackets",
			meta: &bridge.ServerMeta{
				Host: "::1",
				Port: 50051,
			},
			expected: "[::1]:50051",
		},
		{
			name: "already bracketed ipv6 [::1] formatted without double brackets",
			meta: &bridge.ServerMeta{
				Host: "[::1]",
				Port: 50051,
			},
			expected: "[::1]:50051",
		},
		{
			name: "global ipv6 2001:db8::1 formatted with brackets",
			meta: &bridge.ServerMeta{
				Host: "2001:db8::1",
				Port: 50051,
			},
			expected: "[2001:db8::1]:50051",
		},
		{
			name: "zero port returns empty",
			meta: &bridge.ServerMeta{
				Host: "127.0.0.1",
				Port: 0,
			},
			expected: "",
		},
		{
			name: "negative port returns empty",
			meta: &bridge.ServerMeta{
				Host: "127.0.0.1",
				Port: -1,
			},
			expected: "",
		},
		{
			name: "out-of-bounds port > 65535 returns empty",
			meta: &bridge.ServerMeta{
				Host: "127.0.0.1",
				Port: 65536,
			},
			expected: "",
		},
		{
			name: "max valid port 65535",
			meta: &bridge.ServerMeta{
				Host: "127.0.0.1",
				Port: 65535,
			},
			expected: "127.0.0.1:65535",
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
	validMeta := &bridge.ServerMeta{
		Status:    "READY",
		Port:      50051,
		SharedKey: "valid-key-123",
	}
	if !validMeta.IsReady() {
		t.Errorf("Valid READY metadata should be true")
	}

	caseInsensitive := &bridge.ServerMeta{
		Status:    "ready",
		Port:      50051,
		SharedKey: "valid-key-123",
	}
	if !caseInsensitive.IsReady() {
		t.Errorf("ready (case insensitive) should be true")
	}

	// Missing or empty shared key
	emptyKey := &bridge.ServerMeta{
		Status:    "READY",
		Port:      50051,
		SharedKey: "",
	}
	if emptyKey.IsReady() {
		t.Errorf("READY with empty SharedKey should be false")
	}

	// Invalid port bounds
	zeroPort := &bridge.ServerMeta{
		Status:    "READY",
		Port:      0,
		SharedKey: "valid-key-123",
	}
	if zeroPort.IsReady() {
		t.Errorf("READY with port 0 should be false")
	}

	highPort := &bridge.ServerMeta{
		Status:    "READY",
		Port:      70000,
		SharedKey: "valid-key-123",
	}
	if highPort.IsReady() {
		t.Errorf("READY with port 70000 should be false")
	}

	// Non-ready status
	stopped := &bridge.ServerMeta{
		Status:    "STOPPED",
		Port:      50051,
		SharedKey: "valid-key-123",
	}
	if stopped.IsReady() {
		t.Errorf("STOPPED should be false")
	}

	starting := &bridge.ServerMeta{
		Status:    "STARTING",
		Port:      50051,
		SharedKey: "valid-key-123",
	}
	if starting.IsReady() {
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

	// 3. Fallback when env unset
	t.Setenv("CIVQUEST_DISCOVERY_PATH", "")
	p := bridge.ResolveDiscoveryPath("")
	if p == "" {
		t.Errorf("Fallback path should not be empty")
	}

	// 4. Invariant 2: Dynamic XDG resolution to $XDG_DATA_HOME/civquest/server/civquest-server-meta.json
	tempDataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tempDataDir)
	xdgServerDir := filepath.Join(tempDataDir, "civquest", "server")
	if err := os.MkdirAll(xdgServerDir, 0755); err != nil {
		t.Fatalf("Failed creating test XDG server dir: %v", err)
	}
	xdgMetaPath := filepath.Join(xdgServerDir, "civquest-server-meta.json")
	if err := os.WriteFile(xdgMetaPath, []byte(`{"status":"READY"}`), 0600); err != nil {
		t.Fatalf("Failed writing XDG server meta: %v", err)
	}

	resolvedXDG := bridge.ResolveDiscoveryPath("")
	if resolvedXDG != xdgMetaPath {
		t.Errorf("Expected XDG resolution %s, got %s", xdgMetaPath, resolvedXDG)
	}
}

func TestWaitForDiscoverySuccess(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "civquest-server-meta.json")
	pid := os.Getpid()

	// Initially write STOPPED
	stoppedJSON := `{"status": "STARTING", "grpc_port": 0, "shared_key": "wait-key"}`
	if err := os.WriteFile(filePath, []byte(stoppedJSON), 0600); err != nil {
		t.Fatalf("Failed writing initial file: %v", err)
	}

	// Launch async update to READY after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		readyJSON := fmt.Sprintf(`{
  "status": "READY",
  "grpc_transport": "tcp",
  "grpc_port": 50051,
  "grpc_host": "127.0.0.1",
  "server_pid": %d,
  "shared_key": "ready-key-123",
  "timestamp_ms": 1731000005000
}`, pid)
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
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
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
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got: %v", err)
	}
}

func TestWaitForDiscoveryDeadPIDRefusal(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "civquest-server-meta.json")

	// Meta says READY, but PID is dead
	content := `{
  "status": "READY",
  "grpc_transport": "tcp",
  "grpc_port": 50051,
  "grpc_host": "127.0.0.1",
  "server_pid": 99999999,
  "shared_key": "ready-key-123",
  "timestamp_ms": 1731000005000
}`
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatalf("Failed writing initial file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := bridge.WaitForDiscoveryContext(ctx, filePath)
	if err == nil {
		t.Fatalf("Expected timeout refusing to latch onto dead PID, got nil")
	}
	if !errors.Is(err, bridge.ErrDiscoveryTimeout) {
		t.Errorf("Expected ErrDiscoveryTimeout, got: %v", err)
	}
	if !errors.Is(err, bridge.ErrServerProcessDead) {
		t.Errorf("Expected error to wrap ErrServerProcessDead, got: %v", err)
	}
}

func TestServerMetaStringMasking(t *testing.T) {
	metaWithKey := &bridge.ServerMeta{
		Status:    "READY",
		Host:      "127.0.0.1",
		Port:      50051,
		ServerPID: 1234,
		SharedKey: "super-secret-probe-key",
	}

	str := metaWithKey.String()
	if strings.Contains(str, "super-secret-probe-key") {
		t.Errorf("ServerMeta.String() leaked secret key: %s", str)
	}
	if !strings.Contains(str, "[REDACTED]") {
		t.Errorf("ServerMeta.String() should mask key with [REDACTED], got: %s", str)
	}

	metaWithoutKey := &bridge.ServerMeta{
		Status: "STARTING",
		Port:   50051,
	}
	if !strings.Contains(metaWithoutKey.String(), "[NONE]") {
		t.Errorf("ServerMeta.String() should report [NONE] when empty, got: %s", metaWithoutKey.String())
	}

	var nilMeta *bridge.ServerMeta
	if nilMeta.String() != "<nil>" {
		t.Errorf("nil ServerMeta.String() = %q, expected <nil>", nilMeta.String())
	}
}

func TestReadDiscoveryFiltersStaleCandidates(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create a stale metadata file with status STOPPED and a dead PID
	stalePath := filepath.Join(tempDir, "civquest-server-stale.json")
	staleContent := `{
  "status": "STOPPED",
  "grpc_transport": "tcp",
  "grpc_port": 37623,
  "grpc_host": "127.0.0.1",
  "server_pid": 999999999,
  "shared_key": "stale-key",
  "timestamp_ms": 1700000000000
}`
	if err := os.WriteFile(stalePath, []byte(staleContent), 0600); err != nil {
		t.Fatalf("Failed writing stale discovery file: %v", err)
	}

	// 2. Reading the explicit stale file should fail process liveness check
	_, err := bridge.ReadDiscovery(stalePath)
	if err == nil {
		t.Fatalf("Expected ReadDiscovery on stale file to fail, got nil")
	}

	// 3. Create a live metadata file with status READY and current PID
	livePath := filepath.Join(tempDir, "civquest-server-live.json")
	pid := os.Getpid()
	liveContent := fmt.Sprintf(`{
  "status": "READY",
  "grpc_transport": "tcp",
  "grpc_port": 32971,
  "grpc_host": "127.0.0.1",
  "server_pid": %d,
  "shared_key": "live-key-xyz",
  "timestamp_ms": 1700000001000
}`, pid)
	if err := os.WriteFile(livePath, []byte(liveContent), 0600); err != nil {
		t.Fatalf("Failed writing live discovery file: %v", err)
	}

	meta, err := bridge.ReadDiscovery(livePath)
	if err != nil {
		t.Fatalf("Failed reading live discovery file: %v", err)
	}
	if !meta.IsReady() || meta.Port != 32971 || meta.SharedKey != "live-key-xyz" {
		t.Errorf("Unexpected live metadata parsed: %+v", meta)
	}
}
