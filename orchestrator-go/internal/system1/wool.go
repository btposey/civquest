package system1

// Wool harvesting as a frame-local activity (ADR-036, bead cq-390.3). It is the wool source of
// the bed plan (cq-390.2, bed_activity.go): shear sheep when shears are carried (the fleece
// regrows and the flock survives), otherwise kill sheep in melee and collect the wool drop. Any
// colour is harvested. Babies and already-sheared sheep are never targeted: a baby drops nothing,
// a sheared sheep drops no wool and shearing it is a no-op.
//
// The demand is one bed: 3 wool of ONE colour while the agent owns and can claim no bed. The
// activity bids only while a viable sheep or wool drop is in view (opportunistic: it never
// scouts), by day, for a tooled-up agent (axe or shears).

import (
	"fmt"
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const (
	activityKindWool = "wool"

	// woolTarget is the same-colour wool one bed costs (3 wool + 3 planks).
	woolTarget = int32(3)
	// DESIGN BUDGET (cq-390.3): seek radius, reach and cooldown are tuned, not measured.
	woolSeekRadius          = 24.0 // sheep farther than this are ignored (the sensory entity radius)
	woolDropRadius          = 12.0 // dropped wool is walked to within this radius
	woolShearReach          = 3.5  // Java shear reach is 4.5; stay inside it
	woolMeleeReach          = 3.5
	woolActionCooldownTicks = int64(10)
)

// woolLocal is the Wool frame Local.
type woolLocal struct {
	Breaks         breakRecord // refused_breaks.go
	LastActionTick int64
	LastTargetKey  string
}

// maxWoolOfOneColour is the largest carried stack total of any single wool colour.
func maxWoolOfOneColour(inv []*civquestv1.ItemStackProto) int32 {
	byColour := map[string]int32{}
	var best int32
	for _, it := range inv {
		if it == nil || it.Count <= 0 || !strings.HasSuffix(bareID(it.ItemId), "_wool") {
			continue
		}
		id := bareID(it.ItemId)
		byColour[id] += it.Count
		best = max(best, byColour[id])
	}
	return best
}

func countInventoryItem(inv []*civquestv1.ItemStackProto, bare string) int32 {
	var n int32
	for _, it := range inv {
		if it != nil && it.Count > 0 && bareID(it.ItemId) == bare {
			n += it.Count
		}
	}
	return n
}

func hasInventoryItem(inv []*civquestv1.ItemStackProto, bare string) bool {
	return countInventoryItem(inv, bare) > 0
}

// isWoolSheep: an adult, untamed sheep that still carries wool (valid to shear or to kill).
func isWoolSheep(e *civquestv1.VisibleEntity) bool {
	return e != nil && e.Position != nil && bareID(e.EntityType) == "sheep" &&
		!e.IsBaby && !e.IsSheared && !e.IsTamed && !IsHostile(e)
}

func isWoolDrop(e *civquestv1.VisibleEntity) bool {
	return isDroppedItemEntity(e) && e.Position != nil && strings.Contains(strings.ToLower(e.DisplayName), "wool")
}

// NeedsWool reports whether the agent wants wool: no bed carried, none owned or claimable, and
// fewer than three wool of any one colour.
func NeedsWool(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState) bool {
	if frame == nil || frame.Status == nil {
		return false
	}
	inv := frame.Status.Inventory
	for _, it := range inv {
		if it != nil && it.Count > 0 && strings.HasSuffix(bareID(it.ItemId), "_bed") {
			return false
		}
	}
	if maxWoolOfOneColour(inv) >= woolTarget {
		return false
	}
	if state == nil {
		return true
	}
	if active, available := state.BedAvailableFor(agentID, frame); active {
		return !available
	}
	return state.GetAssignedBed() == nil
}

// woolTargets returns the nearest viable sheep and wool drop in range.
func woolTargets(frame *civquestv1.TelemetryFrame, state *AgentState) (sheep, drop *civquestv1.VisibleEntity, sheepDist, dropDist float64) {
	pos := frame.Status.Position
	sheepDist, dropDist = math.MaxFloat64, math.MaxFloat64
	for _, e := range frame.VisibleEntities {
		if e != nil && e.Position != nil && state.IsDestinationShunned(e.Position.X, e.Position.Y, e.Position.Z, frame.WorldTick) {
			continue
		}
		switch {
		case isWoolSheep(e):
			if d := entityDistance(pos, e); d <= woolSeekRadius && d < sheepDist {
				sheep, sheepDist = e, d
			}
		case isWoolDrop(e):
			if d := entityDistance(pos, e); d <= woolDropRadius && d < dropDist {
				drop, dropDist = e, d
			}
		}
	}
	return
}

// woolSafe is the yield-to-threat gate (hostiles in view, combat damage, sleep).
func woolSafe(frame *civquestv1.TelemetryFrame) bool {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || frame.Status.IsSleeping {
		return false
	}
	for _, e := range frame.VisibleEntities {
		if e != nil && IsHostile(e) && entityDistance(frame.Status.Position, e) <= MaxPerceptionRadius {
			return false
		}
	}
	return frame.LastDamageTaken == nil || IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken)
}

// EvaluateWool is one deterministic wool-gathering step: collect a nearby wool drop, else shear
// the nearest woolly sheep (shears carried) or attack it (no shears).
func EvaluateWool(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState, mem *woolLocal) *civquestv1.AgentIntent {
	if !woolSafe(frame) || !NeedsWool(agentID, frame, state) {
		return nil
	}
	if mem == nil {
		mem = &woolLocal{}
	}
	navState := frame.Status.NavigationState
	sheep, drop, sheepDist, dropDist := woolTargets(frame, state)

	navigate := func(key string, p *civquestv1.Vec3, tolerance int32) *civquestv1.AgentIntent {
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && mem.LastTargetKey == key {
			return nil
		}
		mem.LastTargetKey = key
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target: &civquestv1.Vec3{X: p.X, Y: p.Y, Z: p.Z}, SpeedMultiplier: 1.05, ArrivalTolerance: tolerance,
		}}}
	}

	// Wool already on the ground (a shear or a kill) is picked up before the next sheep.
	if drop != nil && dropDist > forageDropPickupReach {
		return navigate(fmt.Sprintf("wool_drop:%d", drop.EntityId), drop.Position, 1)
	}
	if sheep == nil {
		return nil
	}
	shears := hasInventoryItem(frame.Status.Inventory, "shears")
	reach := woolMeleeReach
	if shears {
		reach = woolShearReach
	}
	key := fmt.Sprintf("sheep:%d", sheep.EntityId)
	if sheepDist > reach {
		return navigate("nav_"+key, sheep.Position, 2)
	}
	if mem.LastTargetKey == key && mem.LastActionTick > 0 && frame.WorldTick >= mem.LastActionTick &&
		frame.WorldTick-mem.LastActionTick < woolActionCooldownTicks {
		return nil
	}
	mem.LastTargetKey, mem.LastActionTick = key, frame.WorldTick
	if shears {
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_UseItem{UseItem: &civquestv1.UseItemIntent{
			Mode: civquestv1.UseItemIntent_SHEAR_ENTITY, TargetEntityId: sheep.EntityId,
		}}}
	}
	return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Attack{Attack: &civquestv1.AttackIntent{
		TargetEntityId: sheep.EntityId,
	}}}
}

// waiting reports a walk this frame sent that is still under way, or an action cooling down.
func (l woolLocal) waiting(frame *civquestv1.TelemetryFrame) bool {
	if l.LastTargetKey != "" && frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
		return true
	}
	return l.LastActionTick > 0 && frame.WorldTick >= l.LastActionTick && frame.WorldTick-l.LastActionTick < woolActionCooldownTicks
}

// woolActivity bids while wool is wanted and a sheep or wool drop is in view.
type woolActivity struct{ frameBase }

func (a woolActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a woolActivity) Init(htpda.World, any) (any, htpda.Lease) {
	return woolLocal{}, htpda.Lease{}
}
func (a woolActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (woolActivity) fallThrough(*fsmWorld) bool         { return true }
func (woolActivity) tick(*fsmWorld) ([]FSMIntent, bool) { return nil, true }

// eligible: awake, fed enough, daytime, nothing pending with others, tooled up, wool wanted and
// something to harvest in view.
func (woolActivity) eligible(lw *fsmWorld) bool {
	ctx := lw.ctx
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.IsDuskOrNight() ||
		ctx.Frame.Status.FoodLevel <= foragingFedFoodLevel {
		return false
	}
	if !woolSafe(ctx.Frame) || hasSurvivalPreemption(ctx.Frame) {
		return false
	}
	inv := ctx.Frame.Status.Inventory
	if !inspectBootstrapInventory(inv).hasAxe && !hasInventoryItem(inv, "shears") {
		return false
	}
	sheep, drop, _, _ := woolTargets(ctx.Frame, ctx.State)
	return (sheep != nil || drop != nil) && NeedsWool(ctx.AgentID, ctx.Frame, ctx.State)
}

func (a woolActivity) run(lw *fsmWorld, local any) htpda.Step {
	wl, _ := local.(woolLocal)
	var intents []*civquestv1.AgentIntent
	if intent := EvaluateWool(lw.ctx.AgentID, lw.ctx.Frame, lw.ctx.State, &wl); intent != nil {
		intents = []*civquestv1.AgentIntent{intent}
	}
	// Waiting for its own walk or blow is not having nothing to do: leaving then, and being
	// pushed again a frame later, was the pattern of every approach to a sheep.
	if len(intents) == 0 && !lw.holds() && !wl.waiting(lw.ctx.Frame) {
		return popFrame(lw, a.kind, nil, true)
	}
	return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: wl}
}
