package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestUpsertProfilePreservesOthersAndIsIdempotent(t *testing.T) {
	mc := t.TempDir()
	orig := `{"profiles":{"NeoForge":{"name":"NeoForge","icon":"x"}},"settings":{"crashAssistance":true},"version":3}`
	if err := os.WriteFile(filepath.Join(mc, "launcher_profiles.json"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	game := filepath.Join(t.TempDir(), "client")

	for i := 0; i < 2; i++ {
		if err := UpsertProfile(mc, game, "neoforge-21.1.265", "-Xmx4G"); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	data, _ := os.ReadFile(filepath.Join(mc, "launcher_profiles.json"))
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["settings"]; !ok {
		t.Error("settings key lost")
	}
	var profiles map[string]map[string]any
	_ = json.Unmarshal(top["profiles"], &profiles)
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	p := profiles[ProfileKey]
	if p["gameDir"] != game || p["lastVersionId"] != "neoforge-21.1.265" || p["name"] != ProfileName {
		t.Errorf("unexpected profile: %v", p)
	}
	if _, err := os.Stat(filepath.Join(mc, "launcher_profiles.json.civquest.bak")); err != nil {
		t.Error("backup missing")
	}
	bak, _ := os.ReadFile(filepath.Join(mc, "launcher_profiles.json.civquest.bak"))
	if string(bak) != orig {
		t.Error("backup must hold the original content, not a later version")
	}
}

func TestStageModReplacesStaleJars(t *testing.T) {
	game := t.TempDir()
	mods := filepath.Join(game, "mods")
	_ = os.MkdirAll(mods, 0o755)
	_ = os.WriteFile(filepath.Join(mods, "mod-neoforge-0.0.1.jar"), []byte("old"), 0o644)
	_ = os.WriteFile(filepath.Join(mods, "other.jar"), []byte("keep"), 0o644)
	src := filepath.Join(t.TempDir(), "mod-neoforge-0.1.0-SNAPSHOT.jar")
	_ = os.WriteFile(src, []byte("new"), 0o644)

	if _, err := StageMod(game, src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mods, "mod-neoforge-0.0.1.jar")); !os.IsNotExist(err) {
		t.Error("stale jar not removed")
	}
	if _, err := os.Stat(filepath.Join(mods, "other.jar")); err != nil {
		t.Error("unrelated jar removed")
	}
	if b, _ := os.ReadFile(filepath.Join(mods, filepath.Base(src))); string(b) != "new" {
		t.Error("new jar not staged")
	}
}

func TestHasNeoForgeClient(t *testing.T) {
	mc := t.TempDir()
	if HasNeoForgeClient(mc, "21.1.265") {
		t.Error("unexpected true")
	}
	d := filepath.Join(mc, "versions", "neoforge-21.1.265")
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "neoforge-21.1.265.json"), []byte("{}"), 0o644)
	if !HasNeoForgeClient(mc, "21.1.265") {
		t.Error("expected true")
	}
}

func TestManagedProcess_ProcessExited_CleanExit(t *testing.T) {
	proc, err := LaunchCommand("bash", "-c", "exit 0")
	if err != nil {
		t.Fatalf("failed launching command: %v", err)
	}

	if proc.PID() <= 0 {
		t.Errorf("expected positive PID, got %d", proc.PID())
	}

	select {
	case code := <-proc.ProcessExited():
		if code != 0 {
			t.Errorf("expected exit code 0, got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for process exit channel")
	}

	if proc.ExitCode() != 0 {
		t.Errorf("expected ExitCode() = 0, got %d", proc.ExitCode())
	}
	if proc.IsRunning() {
		t.Error("expected IsRunning() to be false")
	}
}

func TestManagedProcess_ProcessExited_Crash(t *testing.T) {
	proc, err := LaunchCommand("bash", "-c", "exit 42")
	if err != nil {
		t.Fatalf("failed launching command: %v", err)
	}

	select {
	case code := <-proc.ProcessExited():
		if code != 42 {
			t.Errorf("expected exit code 42, got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for process exit channel")
	}

	if proc.ExitCode() != 42 {
		t.Errorf("expected ExitCode() = 42, got %d", proc.ExitCode())
	}
}

func TestManagedProcess_Signal_And_Kill(t *testing.T) {
	proc, err := LaunchCommand("sleep", "10")
	if err != nil {
		t.Fatalf("failed launching sleep: %v", err)
	}

	if !proc.IsRunning() {
		t.Error("expected process to be running")
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed signaling process: %v", err)
	}

	select {
	case code := <-proc.ProcessExited():
		if code == 0 {
			t.Errorf("expected non-zero exit code on SIGTERM, got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for process exit channel after SIGTERM")
	}
}

func TestManagedProcess_LaunchProcess_Options(t *testing.T) {
	dir := t.TempDir()
	opts := ProcessOptions{
		Command: "bash",
		Args:    []string{"-c", "pwd"},
		Dir:     dir,
	}

	proc, err := LaunchProcess(opts)
	if err != nil {
		t.Fatalf("failed LaunchProcess: %v", err)
	}

	code := <-proc.ProcessExited()
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestDefaultWatchdogConfig(t *testing.T) {
	cfg := DefaultWatchdogConfig()
	if cfg.CleanExitBackoff != 3*time.Second {
		t.Errorf("expected 3s clean exit backoff, got %v", cfg.CleanExitBackoff)
	}
	if len(cfg.CrashBackoffs) != 3 {
		t.Fatalf("expected 3 crash backoff steps, got %d", len(cfg.CrashBackoffs))
	}
	if cfg.CrashBackoffs[0] != 3*time.Second || cfg.CrashBackoffs[1] != 6*time.Second || cfg.CrashBackoffs[2] != 12*time.Second {
		t.Errorf("expected [3s, 6s, 12s] crash backoffs, got %v", cfg.CrashBackoffs)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("expected MaxRetries = 3, got %d", cfg.MaxRetries)
	}
}
