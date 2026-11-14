// Package launcher configures the official Minecraft launcher for CivQuest play:
// it ensures the NeoForge client version exists, stages the mod into an isolated
// game directory, and upserts a "CivQuest" launcher profile.
package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	// ProfileKey is the stable launcher_profiles.json key for the CivQuest profile.
	ProfileKey = "civquest"
	// ProfileName is the display name shown in the Minecraft launcher.
	ProfileName = "CivQuest"
)

// DefaultMinecraftDir returns the standard launcher directory (~/.minecraft).
func DefaultMinecraftDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".minecraft"), nil
}

// NeoForgeVersionID returns the launcher version id for a NeoForge release.
func NeoForgeVersionID(neoForgeVersion string) string {
	return "neoforge-" + neoForgeVersion
}

// InstallerURL returns the Maven URL of the official NeoForge installer.
func InstallerURL(neoForgeVersion string) string {
	return fmt.Sprintf("https://maven.neoforged.net/releases/net/neoforged/neoforge/%[1]s/neoforge-%[1]s-installer.jar", neoForgeVersion)
}

// HasNeoForgeClient reports whether the NeoForge client version is installed in mcDir.
func HasNeoForgeClient(mcDir, neoForgeVersion string) bool {
	id := NeoForgeVersionID(neoForgeVersion)
	_, err := os.Stat(filepath.Join(mcDir, "versions", id, id+".json"))
	return err == nil
}

// EnsureNeoForgeClient installs the NeoForge client into mcDir when missing,
// using installerPath, or downloading the official installer into cacheDir.
func EnsureNeoForgeClient(ctx context.Context, mcDir, neoForgeVersion, installerPath, cacheDir string, out io.Writer) error {
	if HasNeoForgeClient(mcDir, neoForgeVersion) {
		return nil
	}
	if _, err := exec.LookPath("java"); err != nil {
		return errors.New("java (21+) is required on PATH to install the NeoForge client")
	}
	if _, err := os.Stat(filepath.Join(mcDir, "launcher_profiles.json")); err != nil {
		return fmt.Errorf("%s does not look like a Minecraft launcher directory (run the launcher once first): %w", mcDir, err)
	}

	if installerPath == "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return err
		}
		installerPath = filepath.Join(cacheDir, fmt.Sprintf("neoforge-%s-installer.jar", neoForgeVersion))
		if _, err := os.Stat(installerPath); err != nil {
			fmt.Fprintf(out, "Downloading %s ...\n", InstallerURL(neoForgeVersion))
			if err := download(ctx, InstallerURL(neoForgeVersion), installerPath); err != nil {
				return err
			}
		}
	}

	fmt.Fprintf(out, "Installing NeoForge %s client into %s ...\n", neoForgeVersion, mcDir)
	cmd := exec.CommandContext(ctx, "java", "-jar", installerPath, "--installClient", mcDir)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("NeoForge installer failed: %w", err)
	}
	if !HasNeoForgeClient(mcDir, neoForgeVersion) {
		return fmt.Errorf("NeoForge installer finished but version %s was not found in %s/versions", NeoForgeVersionID(neoForgeVersion), mcDir)
	}
	return nil
}

func download(ctx context.Context, url, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// StageMod copies modJar into gameDir/mods, replacing any previous CivQuest jars.
func StageMod(gameDir, modJar string) (string, error) {
	modsDir := filepath.Join(gameDir, "mods")
	if err := os.MkdirAll(modsDir, 0o755); err != nil {
		return "", err
	}
	stale, _ := filepath.Glob(filepath.Join(modsDir, "mod-neoforge-*.jar"))
	for _, s := range stale {
		_ = os.Remove(s)
	}
	data, err := os.ReadFile(modJar)
	if err != nil {
		return "", fmt.Errorf("reading mod jar: %w", err)
	}
	dest := filepath.Join(modsDir, filepath.Base(modJar))
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// UpsertProfile adds or updates the CivQuest profile in launcher_profiles.json,
// preserving every other key and profile. A one-time .civquest.bak backup is kept.
func UpsertProfile(mcDir, gameDir, versionID string, javaArgs string) error {
	path := filepath.Join(mcDir, "launcher_profiles.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	profiles := map[string]json.RawMessage{}
	if p, ok := top["profiles"]; ok {
		if err := json.Unmarshal(p, &profiles); err != nil {
			return fmt.Errorf("parsing profiles in %s: %w", path, err)
		}
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	profile := map[string]any{
		"name":          ProfileName,
		"type":          "custom",
		"icon":          "Furnace",
		"lastVersionId": versionID,
		"gameDir":       gameDir,
		"created":       now,
		"lastUsed":      now,
	}
	if javaArgs != "" {
		profile["javaArgs"] = javaArgs
	}
	if existing, ok := profiles[ProfileKey]; ok {
		var prev map[string]any
		if json.Unmarshal(existing, &prev) == nil {
			if c, ok := prev["created"]; ok {
				profile["created"] = c
			}
			if _, ok := profile["javaArgs"]; !ok {
				if ja, ok := prev["javaArgs"]; ok {
					profile["javaArgs"] = ja
				}
			}
		}
	}
	pb, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	profiles[ProfileKey] = pb
	profilesJSON, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	top["profiles"] = profilesJSON

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}

	bak := path + ".civquest.bak"
	if _, err := os.Stat(bak); os.IsNotExist(err) {
		if err := os.WriteFile(bak, raw, 0o644); err != nil {
			return fmt.Errorf("writing backup: %w", err)
		}
	}
	tmp := path + ".civquest.tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ProcessOptions specifies options for launching an OS child process.
type ProcessOptions struct {
	Command     string
	Args        []string
	Dir         string
	Env         []string
	Stdout      io.Writer
	Stderr      io.Writer
	Stdin       io.Reader
	SysProcAttr *syscall.SysProcAttr
}

// ManagedProcess coordinates the execution and exit observation of an OS process.
type ManagedProcess struct {
	cmd      *exec.Cmd
	pid      int
	exitChan chan int
	done     chan struct{}
	mu       sync.Mutex
	exited   bool
	exitCode int
}

// Process is an alias for ManagedProcess.
type Process = ManagedProcess

// LaunchProcess spawns an OS process with the given options and begins monitoring it.
func LaunchProcess(opts ProcessOptions) (*ManagedProcess, error) {
	cmd := exec.Command(opts.Command, opts.Args...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	cmd.Stdin = opts.Stdin
	cmd.SysProcAttr = opts.SysProcAttr

	return StartProcess(cmd)
}

// LaunchCommand starts an executable with arguments and begins monitoring it.
func LaunchCommand(name string, args ...string) (*ManagedProcess, error) {
	return StartProcess(exec.Command(name, args...))
}

// StartProcess starts the provided exec.Cmd and begins monitoring its exit status.
func StartProcess(cmd *exec.Cmd) (*ManagedProcess, error) {
	if cmd == nil {
		return nil, errors.New("cmd cannot be nil")
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p := &ManagedProcess{
		cmd:      cmd,
		pid:      cmd.Process.Pid,
		exitChan: make(chan int, 1),
		done:     make(chan struct{}),
	}

	go p.monitor()

	return p, nil
}

// MonitorProcess attaches an exit observation monitor to an already-started exec.Cmd.
func MonitorProcess(cmd *exec.Cmd) (*ManagedProcess, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("cmd or cmd.Process cannot be nil")
	}

	p := &ManagedProcess{
		cmd:      cmd,
		pid:      cmd.Process.Pid,
		exitChan: make(chan int, 1),
		done:     make(chan struct{}),
	}

	go p.monitor()

	return p, nil
}

func (p *ManagedProcess) monitor() {
	var exitCode int
	err := p.cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if exitCode < 0 {
				if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					exitCode = 128 + int(ws.Signal())
				}
			}
		} else {
			exitCode = 1
		}
	} else {
		exitCode = 0
	}

	p.mu.Lock()
	p.exited = true
	p.exitCode = exitCode
	p.mu.Unlock()

	p.exitChan <- exitCode
	close(p.done)
}

// PID returns the OS process ID.
func (p *ManagedProcess) PID() int {
	return p.pid
}

// Cmd returns the underlying exec.Cmd.
func (p *ManagedProcess) Cmd() *exec.Cmd {
	return p.cmd
}

// ProcessExited returns a channel that receives the process exit code when it terminates.
func (p *ManagedProcess) ProcessExited() <-chan int {
	return p.exitChan
}

// Wait blocks until the process exits and returns its exit code.
func (p *ManagedProcess) Wait() int {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode
}

// ExitCode returns the exit code if exited, or -1 if still running.
func (p *ManagedProcess) ExitCode() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.exited {
		return -1
	}
	return p.exitCode
}

// IsRunning reports whether the process is still actively running.
func (p *ManagedProcess) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.exited
}

// Signal delivers an OS signal to the child process.
func (p *ManagedProcess) Signal(sig os.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exited || p.cmd.Process == nil {
		return errors.New("process already exited")
	}
	return p.cmd.Process.Signal(sig)
}

// Kill forcefully terminates the process.
func (p *ManagedProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exited || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// WatchdogConfig specifies retry, backoff, and lifecycle hooks for supervisor keep-alive.
type WatchdogConfig struct {
	CleanExitBackoff time.Duration
	CrashBackoffs    []time.Duration
	MaxRetries       int
	CheckDeath       func() bool
	OnRestart        func(attempt int, delay time.Duration, isCrash bool)
	OnPermadeath     func()
	OnMaxRetries     func(exitCode int)
}

// DefaultWatchdogConfig returns standard supervisor watchdog configuration parameters:
// 3s backoff on clean exit without death, exponential backoff (3s, 6s, 12s) with max 3 retries on crash.
func DefaultWatchdogConfig() WatchdogConfig {
	return WatchdogConfig{
		CleanExitBackoff: 3 * time.Second,
		CrashBackoffs:    []time.Duration{3 * time.Second, 6 * time.Second, 12 * time.Second},
		MaxRetries:       3,
	}
}
