package system1

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/vertical"
)

// Ladder carry policy (cq-3a0.5). Agents CARRY ladders, use them as needed,
// always pick them up after use, craft more when below a reserve, and never
// discard them as junk. Everything here is a pure function of the telemetry
// frame, the inventory and the vertical planner; no AgentState fields.

const (
	// LadderItemID is the canonical ladder item.
	LadderItemID = "minecraft:ladder"
	// MaxLadderReserve caps the reserve so one tall tree cannot starve other crafting.
	MaxLadderReserve = 12
	// ladderPickupRadius is the horizontal radius around the use site within which
	// dropped ladders are collected.
	ladderPickupRadius = 8.0
	ladderPickupDY     = 6.0
)

// IsLadderItem reports whether an item id is a ladder.
func IsLadderItem(itemID string) bool {
	id := strings.ToLower(strings.TrimSpace(itemID))
	return id == "ladder" || id == LadderItemID || strings.HasSuffix(id, ":ladder")
}

// LaddersAvailable returns how many ladders the inventory carries.
func LaddersAvailable(inv []*civquestv1.ItemStackProto) int {
	n := 0
	for _, it := range inv {
		if it != nil && it.Count > 0 && IsLadderItem(it.ItemId) {
			n += int(it.Count)
		}
	}
	return n
}

// ReserveContext carries the world view needed to size the reserve.
type ReserveContext struct {
	Agent     vertical.Vec3
	Solid     vertical.SolidFunc
	Breakable vertical.SolidFunc
	EyeHeight float64
	Reach     float64
}

// LadderReserve sizes the ladder reserve to the tallest visible/targeted tree:
// the max LaddersNeeded over every tree's PlanClimb (inventory-independent),
// capped at MaxLadderReserve. Trees reachable from the ground, and trees with
// no valid plan, contribute 0.
func LadderReserve(trees [][]vertical.Cell, ctx ReserveContext) int {
	reserve := 0
	for _, logs := range trees {
		if len(logs) == 0 {
			continue
		}
		plan := vertical.PlanClimb(vertical.ClimbInput{
			Targets: logs, Agent: ctx.Agent, Solid: ctx.Solid, Breakable: ctx.Breakable,
			HaveLadders: 0, EyeHeight: ctx.EyeHeight, Reach: ctx.Reach,
		})
		if !plan.OK() || plan.FromGround {
			continue
		}
		if plan.LaddersNeeded > reserve {
			reserve = plan.LaddersNeeded
		}
	}
	if reserve > MaxLadderReserve {
		reserve = MaxLadderReserve
	}
	return reserve
}

// NeedsMoreLadders reports whether the climb plan needs more ladders than carried.
func NeedsMoreLadders(plan vertical.ClimbPlan, inv []*civquestv1.ItemStackProto) bool {
	return plan.OK() && !plan.FromGround && plan.LaddersNeeded > LaddersAvailable(inv)
}

// BelowLadderReserve reports whether carried ladders are under the reserve.
func BelowLadderReserve(reserve int, inv []*civquestv1.ItemStackProto) bool {
	return reserve > 0 && LaddersAvailable(inv) < reserve
}

// LadderCraftStep is the craft trigger: when carried ladders are below the
// reserve it asks the recipe graph for the next step toward a ladder
// (sticks, planks->sticks, logs->planks...). ok is false when no craft is
// needed or the recipe graph cannot make progress from the inventory.
func LadderCraftStep(graph *crafting.RecipeGraph, inv []*civquestv1.ItemStackProto, reserve int, hasTable bool) (crafting.Step, bool) {
	if graph.IsEmpty() || !BelowLadderReserve(reserve, inv) {
		return crafting.Step{}, false
	}
	step, ok := graph.PlanNextStep(LadderItemID, crafting.InventoryMapFromProtos(inv), hasTable)
	if !ok || step.MissingRaw {
		return step, false
	}
	return step, true
}

// LadderCraftIntent converts an executable LadderCraftStep into a craft intent
// (nil when nothing is craftable right now). workstation may be nil.
func LadderCraftIntent(agentID string, graph *crafting.RecipeGraph, inv []*civquestv1.ItemStackProto, reserve int, hasTable bool, workstation *civquestv1.BlockPosProto) *civquestv1.AgentIntent {
	step, ok := LadderCraftStep(graph, inv, reserve, hasTable)
	if !ok || !step.CanExecuteNow {
		return nil
	}
	ci := &civquestv1.CraftItemIntent{RecipeId: step.RecipeID, BatchCount: 1}
	if step.Requires3x3 && workstation != nil {
		ci.WorkstationPos = workstation
	}
	return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_CraftItem{CraftItem: ci}}
}

// IsProtectedItem reports items that must never be discarded as junk or
// dropped by inventory-full handling. Ladders are protected.
func IsProtectedItem(itemID string) bool { return IsLadderItem(itemID) }

// DiscardCandidates filters an inventory down to stacks that may be discarded
// (inventory-full / junk handling), removing every protected item.
func DiscardCandidates(inv []*civquestv1.ItemStackProto) []*civquestv1.ItemStackProto {
	var out []*civquestv1.ItemStackProto
	for _, it := range inv {
		if it == nil || it.Count <= 0 || IsProtectedItem(it.ItemId) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// isLadderDropEntity matches a dropped ladder item entity.
func isLadderDropEntity(e *civquestv1.VisibleEntity) bool {
	if !isDroppedItemEntity(e) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(e.DisplayName))
	return strings.Contains(name, "ladder")
}

// FindNearestDroppedLadder returns the nearest dropped ladder within range of
// the use site (anchorX, anchorZ), or nil.
func FindNearestDroppedLadder(frame *civquestv1.TelemetryFrame, anchorX, anchorZ int32) *civquestv1.VisibleEntity {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil {
		return nil
	}
	pos := frame.Status.Position
	var best *civquestv1.VisibleEntity
	bestSq := math.MaxFloat64
	for _, e := range frame.VisibleEntities {
		if !isLadderDropEntity(e) || e.Position == nil || math.Abs(e.Position.Y-pos.Y) > ladderPickupDY {
			continue
		}
		if math.Hypot(e.Position.X-(float64(anchorX)+0.5), e.Position.Z-(float64(anchorZ)+0.5)) > ladderPickupRadius {
			continue
		}
		dx, dy, dz := e.Position.X-pos.X, e.Position.Y-pos.Y, e.Position.Z-pos.Z
		if d := dx*dx + dy*dy + dz*dz; d < bestSq {
			bestSq, best = d, e
		}
	}
	return best
}

// CollectLaddersIntent is the pickup rule: after use, walk to the nearest
// dropped ladder near the use site; the server vacuums items within ~1.5m.
// Returns nil when no dropped ladder is in range.
func CollectLaddersIntent(agentID string, frame *civquestv1.TelemetryFrame, anchorX, anchorZ int32) *civquestv1.AgentIntent {
	drop := FindNearestDroppedLadder(frame, anchorX, anchorZ)
	if drop == nil {
		return nil
	}
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target:           &civquestv1.Vec3{X: drop.Position.X, Y: drop.Position.Y, Z: drop.Position.Z},
			SpeedMultiplier:  1.0,
			ArrivalTolerance: 1,
		}},
	}
}
