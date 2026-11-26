package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-6l3.7: a dead agent was reported on every health check, every two seconds.
func TestSupervisor_ReportsADeathOnce(t *testing.T) {
	r := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
	if err := os.MkdirAll(filepath.Join(r.GraveyardDir(), "mortal_20261114T120000Z"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.LogsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cfg := DefaultSupervisorConfig(r)
	cfg.Out = &out
	sup, err := NewDaemonSupervisor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sup.workers["mortal"] = &AgentWorker{}
	for i := 0; i < 5; i++ {
		sup.healthCheck()
	}
	if n := strings.Count(out.String(), "is deceased"); n != 1 {
		t.Fatalf("death reported %d times, want once:\n%s", n, out.String())
	}
}
