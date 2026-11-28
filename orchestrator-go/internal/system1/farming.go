package system1

// Farming comes before any trade (cq-vuu). Until an agent could water a first field from its
// settlement (its cabin stands, water is within reach and it holds a bucket) it stays an
// explorer and gatherer: it does not enter the PROFESSION strategy, and goes on exploring and
// gathering wherever that takes it.

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// farmWaterReach: water this near the settlement can be brought to its fields. DESIGN BUDGET:
// a first guess, to be revisited with the farm chain (cq-390.10).
const farmWaterReach = 48.0

// FarmingNeeds is what an agent has of what a first field needs, as one frame shows it.
type FarmingNeeds struct {
	Shelter bool // its cabin stands
	Water   bool // water in view near the settlement
	Bucket  bool // a bucket, empty or full of water, in its pack
}

// Met reports that farming is possible.
func (n FarmingNeeds) Met() bool { return n.Shelter && n.Water && n.Bucket }

func farmingNeeds(state *AgentState, frame *civquestv1.TelemetryFrame) FarmingNeeds {
	n := FarmingNeeds{Shelter: state.GetCabinBuilt() >= blueprints.CabinTotalBlocks}
	for _, item := range frame.GetStatus().GetInventory() {
		id := strings.ToLower(item.GetItemId())
		if item.GetCount() > 0 && (bareID(id) == "bucket" || bareID(id) == "water_bucket") {
			n.Bucket = true
		}
	}
	if home, ok := state.Settlement(); ok {
		for _, b := range frame.GetInterestingBlocks() {
			if isSurfaceWaterBlock(b) &&
				math.Hypot(float64(b.Pos.X)+0.5-home.Pos[0], float64(b.Pos.Z)+0.5-home.Pos[2]) <= farmWaterReach {
				n.Water = true
				break
			}
		}
	}
	return n
}

// ObserveFarming records what the latest frame showed of farming's needs.
func (s *AgentState) ObserveFarming(n FarmingNeeds) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.farming = n
}
