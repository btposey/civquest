package system1

// Bed plan as a frame-local activity (ADR-036, bead cq-390.2).
//
// An agent that owns no bed (BedAvailableFor false while a clan bed registry is wired) pursues
// the plan.GoalBed plan: 3 wool of one colour (gathered by the wool activity, cq-390.3) +
// 3 planks -> bed at a crafting table, then places the bed beside its shelter and owns it. The
// plan is a pure function of the live inventory (plan.Next), so the frame Local only holds the
// act throttle, the craft latch and the placement it is waiting to see.

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/plan"
)

const (
	activityKindBed   = "bed"
	bedActionCooldown = 8  // ticks between repeated melee/harvest/place actions
	bedPlaceVerifyTTL = 80 // ticks to see a placed bed before the claim is released
	bedBlockReach     = 4.5
	// bedShelterSearchRadius: farther shelters are not walked back to just to place a bed.
	bedShelterSearchRadius = 48.0
	bedShelterPlaceRadius  = 4.0
)

// bedLocal is the Bed frame Local.
type bedLocal struct {
	Walk           walkLocal // craft latch (receipt-released like the other waterfall frames)
	LastActionTick int64
	PlacePos       *civquestv1.BlockPosProto // placed bed awaiting confirmation (immutable once set)
	PlaceTick      int64
	Breaks         breakRecord // refused_breaks.go
}

// bedActivity owns its eligibility (Propose) and step; its weight is priorityBed.
type bedActivity struct{ frameBase }

func (a bedActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a bedActivity) Init(htpda.World, any) (any, htpda.Lease) {
	return bedLocal{}, htpda.Lease{}
}
func (a bedActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (bedActivity) fallThrough(*fsmWorld) bool { return true }
func (bedActivity) tick(*fsmWorld) ([]FSMIntent, bool) {
	return nil, true
}

// args: the bed frame is pushed only while its step can progress now, asked of the same chooser
// the step runs, so the offer and the step cannot disagree (cq-3sa.2).
func (a bedActivity) args(lw *fsmWorld) (any, bool) {
	if !a.eligible(lw) {
		return nil, false
	}
	st := lw.ctx.State
	st.mu.Lock()
	defer st.mu.Unlock()
	_, ok := bedMoveLocked(lw.ctx)
	return nil, ok
}

// eligible: awake, daytime, tooled up (an axe: bootstrap is done), and no bed this agent owns
// or may claim while the clan registry is wired.
func (bedActivity) eligible(lw *fsmWorld) bool {
	ctx := lw.ctx
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.State == nil || lw.backedOff(activityKindBed) {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	if ctx.IsDuskOrNight() || ctx.Frame.Status.FoodLevel <= foragingFedFoodLevel {
		return false
	}
	active, available := ctx.State.BedAvailableFor(ctx.AgentID, ctx.Frame)
	if !active || available {
		return false
	}
	return inspectBootstrapInventory(ctx.Frame.Status.Inventory).hasAxe
}

func (a bedActivity) run(lw *fsmWorld, local any) htpda.Step {
	bl, _ := local.(bedLocal)
	in, working := bedStep(lw.ctx, &bl)
	if !working {
		// Nothing to progress (no sheep, no timber, no recipe): yield to lower activities.
		return popNoWork(lw, a.kind)
	}
	var intents []*civquestv1.AgentIntent
	if in != nil {
		intents = []*civquestv1.AgentIntent{in}
	}
	// working with no intent: walking, throttled or waiting on a craft/placement; hold the frame
	// so lower activities do not steer the agent away from the target.
	return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: bl}
}

// bedStep computes the next intent of the bed plan. working=false means there is nothing the
// plan can progress right now; working=true with a nil intent means wait (path, throttle,
// craft or placement pending).
func bedStep(ctx *FSMContext, bl *bedLocal) (intent *civquestv1.AgentIntent, working bool) {
	frame, st := ctx.Frame, ctx.State
	tick := frame.WorldTick

	// A bed was placed: wait for it to show up, else release the claim and re-place.
	if bl.PlacePos != nil {
		if b := FindVisibleBlockAt(frame, bl.PlacePos.X, bl.PlacePos.Y, bl.PlacePos.Z); b != nil && strings.Contains(strings.ToLower(b.BlockId), "bed") {
			bl.PlacePos = nil
			return nil, true // visible: the claim path assigns it and the sleep logic takes over
		}
		if tick-bl.PlaceTick < bedPlaceVerifyTTL {
			return nil, true
		}
		if reg := st.bedReg(); reg != nil {
			_ = reg.ReleaseBed(bl.PlacePos.X, bl.PlacePos.Y, bl.PlacePos.Z, ctx.AgentID)
		}
		bl.PlacePos = nil
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	move, ok := bedMoveLocked(ctx)
	if !ok {
		return nil, false
	}
	return move(bl), true
}

// bedMove carries out what bedMoveLocked chose; a nil intent means wait. st.mu held.
type bedMove func(bl *bedLocal) *civquestv1.AgentIntent

// bedMoveLocked chooses what the bed plan can do with this frame; ok=false means nothing. It
// changes nothing: only calling the move does. st.mu held.
func bedMoveLocked(ctx *FSMContext) (bedMove, bool) {
	frame, st := ctx.Frame, ctx.State
	pos := frame.Status.Position
	if pos == nil {
		return nil, false
	}
	tick := frame.WorldTick
	inv := crafting.InventoryMapFromProtos(frame.Status.Inventory)
	if plan.HasBed(inv) {
		return func(bl *bedLocal) *civquestv1.AgentIntent { return placeBedIntent(ctx, bl, st, inv, pos) }, true
	}
	graph := st.RecipeGraphLocked()
	table, _, tableDistSq := findNearestCraftingTableLocked(frame, pos, st)
	inReach := isCraftingTableWithinReach(table, tableDistSq, pos)
	noNew := st.stationRefusedLocked(craftingTableItem, frame)
	facts := plan.Facts{Tick: tick, Inventory: inv, Table: plan.TableFacts{Near: inReach, NoNew: noNew}, Recipes: graph}
	if table != nil && table.Pos != nil {
		facts.Table.Known = true
		facts.Table.Pos = plan.Pos{X: int(table.Pos.X), Y: int(table.Pos.Y), Z: int(table.Pos.Z)}
		facts.Table.Dist = math.Sqrt(tableDistSq)
	}
	p, ok := plan.New("bed:"+ctx.AgentID, plan.Goal{Kind: plan.GoalBed}, plan.Lease{Owner: ctx.AgentID}, facts)
	if !ok {
		return nil, false
	}

	// Walk the plan in order. Outstanding wool does not stall the timber and planks work; the
	// table and the bed craft wait for it.
	woolMissing := false
	for _, step := range p.Steps {
		switch step.Kind {
		case plan.StepGather:
			switch step.Source {
			case plan.SourceMob:
				// Wool belongs to the wool activity (cq-390.3), which outbids this frame
				// while a sheep or wool drop is in view.
				woolMissing = true
			case plan.SourceTree:
				if log := bedLogLocked(frame, pos, st); log != nil {
					return func(bl *bedLocal) *civquestv1.AgentIntent {
						return logGatherIntent(ctx.AgentID, frame, pos, log, bl)
					}, true
				}
			}
		case plan.StepGoto:
			if !woolMissing && step.Pos != nil {
				return func(*bedLocal) *civquestv1.AgentIntent {
					return bedNavigate(ctx.AgentID, frame, float64(step.Pos.X)+0.5, float64(step.Pos.Y), float64(step.Pos.Z)+0.5)
				}, true
			}
		case plan.StepPlace:
			if woolMissing || inv[step.Item] <= 0 {
				continue
			}
			if st.stationRefusedLocked(step.Item, frame) {
				return nil, false
			}
			return func(bl *bedLocal) *civquestv1.AgentIntent {
				in, _ := st.placeStationLocked(ctx.AgentID, step.Item, frame, &bl.Walk)
				return in
			}, true
		case plan.StepCraft:
			if woolMissing && strings.HasSuffix(step.Item, "_bed") {
				return nil, false
			}
			if step.Requires3x3 && !inReach {
				return nil, false
			}
			// A gather step may have found nothing, leaving the crafts that depend on it
			// without ingredients: only issue what the inventory can actually make.
			if !graph.CanCraftNow(step.RecipeID, inv) {
				continue
			}
			return func(bl *bedLocal) *civquestv1.AgentIntent {
				if last := st.mem.lastCraftTick; last != 0 && tick >= last && tick-last < 20 {
					return nil
				}
				if bl.Walk.craftHeld(graph, step.RecipeID, inv, tick) {
					return nil
				}
				st.mem.lastCraftTick = tick
				bl.Walk.armCraft(graph, step.RecipeID, inv, tick)
				craft := &civquestv1.CraftItemIntent{RecipeId: step.RecipeID, BatchCount: 1}
				if step.Requires3x3 && table != nil {
					craft.WorkstationPos = table.Pos
				}
				return &civquestv1.AgentIntent{AgentId: ctx.AgentID, Action: &civquestv1.AgentIntent_CraftItem{CraftItem: craft}}
			}, true
		}
	}
	return nil, false
}

// placeBedIntent places the held bed beside the shelter when one is known (walking there
// first), else beside the agent, claiming the spot before the block lands so a neighbour cannot
// take the new bed. Assignment follows from the normal claim path once the bed is visible.
// st.mu held.
func placeBedIntent(ctx *FSMContext, bl *bedLocal, st *AgentState, inv map[string]int, pos *civquestv1.Vec3) *civquestv1.AgentIntent {
	frame := ctx.Frame
	var item string
	for id, n := range inv {
		if n > 0 && strings.HasSuffix(id, "_bed") && (item == "" || id < item) {
			item = id
		}
	}
	anchor := pos
	if sp := st.ShelterPos; sp != nil {
		x, y, z := float64(sp.X)+0.5, float64(sp.Y), float64(sp.Z)+0.5
		d := math.Hypot(x-pos.X, z-pos.Z)
		// A shelter the agent stands by is used; one to walk back to must be worth the walk.
		if d <= bedShelterPlaceRadius || d <= bedShelterSearchRadius && !st.IsDestinationShunnedLocked(x, y, z, frame.WorldTick) {
			if d > bedShelterPlaceRadius {
				return bedNavigate(ctx.AgentID, frame, x, y, z)
			}
			anchor = &civquestv1.Vec3{X: x, Y: y, Z: z}
		}
	}
	if bl.LastActionTick != 0 && frame.WorldTick >= bl.LastActionTick && frame.WorldTick-bl.LastActionTick < bedActionCooldown {
		return nil
	}
	target := selectAdjacentCraftingTablePos(frame, anchor)
	if target.X == int32(math.Floor(pos.X)) && target.Z == int32(math.Floor(pos.Z)) {
		target = selectAdjacentCraftingTablePos(frame, pos) // never place on our own feet
	}
	if !st.claimBedNoLock(ctx.AgentID, target, frame.WorldTick) {
		return nil
	}
	bl.PlacePos = &civquestv1.BlockPosProto{X: target.X, Y: target.Y, Z: target.Z}
	bl.PlaceTick = frame.WorldTick
	bl.LastActionTick = frame.WorldTick
	return &civquestv1.AgentIntent{AgentId: ctx.AgentID, Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{
		Pos:             target,
		InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
		BlockOrItemId:   item,
	}}}
}

// bedLogLocked is the nearest log worth harvesting (planks come from logs) among the trees
// worth felling now (tree_choice.go), or nil. st.mu held.
func bedLogLocked(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, st *AgentState) *civquestv1.VisibleBlock {
	var best *civquestv1.VisibleBlock
	bestD := math.MaxFloat64
	for _, t := range preferredTrunks(usableTrunks(frame, pos, st, nil)) {
		for _, b := range t.logs {
			if d := bedBlockDistance(pos, b.Pos); d < bestD {
				best, bestD = b, d
			}
		}
	}
	return best
}

func bedBlockDistance(pos *civquestv1.Vec3, b *civquestv1.BlockPosProto) float64 {
	dx, dy, dz := float64(b.X)+0.5-pos.X, float64(b.Y)+0.5-pos.Y, float64(b.Z)+0.5-pos.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// logGatherIntent harvests the chosen log, walking to it first; nil while the harvest cools down
// or a path is in progress.
func logGatherIntent(agentID string, frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, best *civquestv1.VisibleBlock, bl *bedLocal) *civquestv1.AgentIntent {
	if bedBlockDistance(pos, best.Pos) <= bedBlockReach && withinBreakReach(pos, best.Pos) {
		if bl.LastActionTick != 0 && frame.WorldTick >= bl.LastActionTick && frame.WorldTick-bl.LastActionTick < bedActionCooldown {
			return nil
		}
		bl.LastActionTick = frame.WorldTick
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{
			Pos:             cloneBlockPos(best.Pos),
			InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
			BlockOrItemId:   best.BlockId,
		}}}
	}
	return bedNavigate(agentID, frame, float64(best.Pos.X)+0.5, float64(best.Pos.Y), float64(best.Pos.Z)+0.5)
}

// bedNavigate walks to a target unless a path is already in progress.
func bedNavigate(agentID string, frame *civquestv1.TelemetryFrame, x, y, z float64) *civquestv1.AgentIntent {
	if frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
		return nil
	}
	return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
		Target: &civquestv1.Vec3{X: x, Y: y, Z: z}, SpeedMultiplier: 1.0, ArrivalTolerance: 2,
	}}}
}
