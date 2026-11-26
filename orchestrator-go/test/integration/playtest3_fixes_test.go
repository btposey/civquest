package integration

// Playtest-3 fix scenarios (epic cq-yn1, session of 2026-11-26).

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting/craftingtest"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// A live frame arrives every few ticks. These bounds are generous so a loaded machine does
// not flake; the failure they guard against never returned.
const (
	tickBudget = 500 * time.Millisecond
	tickHang   = 10 * time.Second
)

// stepWithin runs one simulation step and fails the test if the agent does not answer.
func stepWithin(t *testing.T, s *eval.PlaytestSim) time.Duration {
	t.Helper()
	done := make(chan struct{})
	start := time.Now()
	go func() { defer close(done); s.Step() }()
	select {
	case <-done:
	case <-time.After(tickHang):
		t.Fatalf("agent brain hung: tick did not return within %v (inventory %s)", tickHang, s.InventorySummary())
	}
	return time.Since(start)
}

// (1) Brain freeze (cq-yn1.1, cq-yn1.2).
//
// UNFIXED BEHAVIOUR: five of six agents went silent 25-75 s after spawning. As soon as one
// held an axe the bed planner searched the recipe catalogue's dye cycles, never returned, and
// held the agent's state lock, so movement, chat and reflexes all stopped.
func TestPlaytest3_01_BootstrapToBedStaysResponsiveOnCyclicCatalog(t *testing.T) {
	catalogs := map[string][]*civquestv1.RecipeNode{"synthetic": craftingtest.Nodes()}
	if jar := craftingtest.FindServerJar(); jar != "" {
		nodes, err := craftingtest.FromServerJar(jar)
		if err != nil {
			t.Fatalf("reading %s: %v", jar, err)
		}
		catalogs["server"] = nodes
	}
	for name, nodes := range catalogs {
		t.Run(name, func(t *testing.T) {
			w := groundWorld(40)
			for _, x := range []int32{4, 7, 10} {
				w.AddTrunk(x, 0, 6, "")
				w.AddTrunk(x, 6, 6, "")
			}
			w.AddStoneOutcrop(-10, -4, 4, 2)
			// The clan bed registry is wired in production and is what makes agents want a bed.
			civic, err := db.OpenCivicRegistryDB(filepath.Join(t.TempDir(), "civic_registry.db"), "pioneers")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = civic.Close() })
			s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
				Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w, Recipes: nodes,
				Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5},
				Setup: func(st *system1.AgentState) { st.SetBedRegistry(civic) },
			})
			for i := 0; i < 3; i++ {
				s.AddSheep(-3+float64(i)*2, 9, "white")
			}
			bedCrafted := func() bool {
				for _, c := range s.Crafts {
					if c.OK && strings.HasSuffix(c.Recipe, "_bed") {
						return true
					}
				}
				return false
			}
			var worst time.Duration
			for elapsed := int64(0); elapsed < 8*simTicksPerMinute && !bedCrafted(); elapsed += 4 {
				if d := stepWithin(t, s); d > worst {
					worst = d
				}
			}
			t.Logf("worst tick %v, inventory %s", worst, s.InventorySummary())
			if worst > tickBudget {
				t.Errorf("worst tick took %v, budget %v", worst, tickBudget)
			}
			if !bedCrafted() {
				t.Errorf("no bed crafted: crafts=%+v inventory=%s\n%s", s.Crafts, s.InventorySummary(), traceTail(s, len(s.Steps)-15, len(s.Steps)))
			}
		})
	}
}
