package agent_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-390.5: permadeath releases the dead agent's bed claims and nobody else's.
func TestPermadeath_ReleasesBedClaims(t *testing.T) {
	root := t.TempDir()
	civic, err := db.OpenCivicRegistryDB(filepath.Join(root, "civic_registry.db"), "pioneers")
	if err != nil {
		t.Fatal(err)
	}
	defer civic.Close()
	_, _ = civic.ClaimBed(1, 64, 1, "able", 1)
	_, _ = civic.ClaimBed(5, 64, 5, "bert", 1)

	if _, err := agent.ExecutePermadeath(context.Background(), agent.PermadeathRequest{
		AgentID:         "able",
		DeathEvent:      &civquestv1.DeathEvent{DamageSource: "fall", WorldTick: 10},
		Resolver:        xdg.NewResolver(xdg.WithCustomRoot(root)),
		CivicRegistryDB: civic,
	}); err != nil {
		t.Fatal(err)
	}
	if o, _ := civic.BedOwner(1, 64, 1); o != "" {
		t.Fatalf("dead agent keeps its bed: %q", o)
	}
	if o, _ := civic.BedOwner(5, 64, 5); o != "bert" {
		t.Fatalf("living agent's claim = %q", o)
	}
}
