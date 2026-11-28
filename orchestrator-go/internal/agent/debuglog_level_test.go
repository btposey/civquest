package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/logging"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

func TestDebugLoggerLevels(t *testing.T) {
	for minimum, wantDebug := range map[string]bool{"": true, "info": false} {
		t.Setenv(logging.EnvVar, minimum)
		path := filepath.Join(t.TempDir(), "a.jsonl")
		l := NewDebugLogger(path, "", "a", "c")
		l.Emit(1, "VITALS", "sample", nil)
		l.Emit(2, "DEATH", "died", nil)
		_ = l.Close()
		out, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), `"level":"WARN"`) {
			t.Errorf("minimum %q: a death must be logged as WARN: %s", minimum, out)
		}
		if got := strings.Contains(string(out), `"level":"DEBUG"`); got != wantDebug {
			t.Errorf("minimum %q: debug written = %v, want %v", minimum, got, wantDebug)
		}
	}
}

// A stuck agent is an error, written whatever the minimum level (cq-3sa.1).
func TestDebugLoggerWatchdogIsAnError(t *testing.T) {
	t.Setenv(logging.EnvVar, "error")
	path := filepath.Join(t.TempDir(), "a.jsonl")
	l := NewDebugLogger(path, "", "a", "c")
	l.Emit(1, system1.EventWatchdog, "no_progress: shelter", map[string]any{"condition": "no_progress"})
	_ = l.Close()
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"level":"ERROR"`) || !strings.Contains(string(out), `"category":"WATCHDOG"`) {
		t.Errorf("watchdog event: %s", out)
	}
}
