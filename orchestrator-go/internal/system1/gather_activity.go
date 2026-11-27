package system1

// Gathering along the way (cq-6l3.15). Until it has a settlement an agent is an explorer that
// stocks up on what it passes: rare finds (ore, gems), food (fish first, a renewable protein,
// then livestock), and the wood, stone and coal that make tools and later shelter. Berries and
// other wild plants are chance finds, picked up after everything else. It only takes what is in view and close by, up to a stock
// target for each, and never leaves its course to search; needs that are pressing (bootstrap,
// the plan, shelter, hunger) outrank it. An expedition leg is interrupted for a find and resumes.

import (
	"fmt"
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const (
	activityKindGather = "gather"
	gatherFishKey      = "fishing"
	gatherNoFelling    = "felling" // asked of skipped: a task below already owns the motor

	// DESIGN BUDGET (cq-6l3.15): radius, reach and stock targets are first guesses to tune in play.
	gatherRadius        = 12.0
	gatherMineReach     = 4.0
	gatherMeleeReach    = 3.0
	gatherActionTicks   = int64(20)
	gatherTargetTicks   = int64(200)  // give up on one find after this long
	gatherSkipTicks     = int64(6000) // and ignore it for this long
	gatherPackStacks    = 34          // a pack this full takes only what it already carries
	gatherLogStock      = int32(24)
	gatherStoneStock    = int32(32)
	gatherCoalStock     = int32(8)
	gatherFoodStock     = int32(12)
	gatherForageStock   = int32(16)   // of each wild plant food
	gatherFishTicks     = int64(1200) // one stop to fish, then the explorer moves on
	gatherRareStock     = int32(16)
	gatherMaxClimbUp    = 3 // blocks above the feet that can still be reached
	gatherMaxReachBelow = 1
)

// gatherGood is the stock a find counts towards.
type gatherGood string

const (
	goodNone   gatherGood = ""
	goodRare   gatherGood = "rare"
	goodFish   gatherGood = "fish"
	goodFood   gatherGood = "food"
	goodForage gatherGood = "forage"
	goodLog    gatherGood = "log"
	goodCoal   gatherGood = "coal"
	goodStone  gatherGood = "stone"
)

// gatherRank orders finds: rare ones first, chance plant food last.
var gatherRank = map[gatherGood]int{goodRare: 0, goodFish: 1, goodFood: 2, goodLog: 3, goodCoal: 4, goodStone: 5, goodForage: 6}

// rareOreItems maps an ore block (without the deepslate_ prefix) to the item it yields.
var rareOreItems = map[string]string{
	"iron_ore": "raw_iron", "copper_ore": "raw_copper", "gold_ore": "raw_gold", "lapis_ore": "lapis_lazuli",
	"redstone_ore": "redstone", "diamond_ore": "diamond", "emerald_ore": "emerald",
	"amethyst_cluster": "amethyst_shard",
}

// rareDropNames are dropped items worth picking up whatever the stock of anything else.
var rareDropNames = map[string]string{
	"raw_iron": "raw_iron", "raw_copper": "raw_copper", "raw_gold": "raw_gold", "lapis_lazuli": "lapis_lazuli",
	"redstone_dust": "redstone", "diamond": "diamond", "emerald": "emerald", "amethyst_shard": "amethyst_shard",
	"iron_ingot": "iron_ingot", "gold_ingot": "gold_ingot", "saddle": "saddle", "name_tag": "name_tag",
	"ender_pearl": "ender_pearl", "golden_apple": "golden_apple",
}

// wildFoodBlocks maps plants that grow untended to what they yield; crops are left alone,
// since someone planted them.
var wildFoodBlocks = map[string]string{
	"sweet_berry_bush": "sweet_berries", "brown_mushroom": "brown_mushroom", "red_mushroom": "red_mushroom",
	"melon": "melon_slice", "pumpkin": "pumpkin",
}

// isWildFoodItem reports whether item is what a wild plant yields.
func isWildFoodItem(item string) bool {
	for _, yield := range wildFoodBlocks {
		if yield == item {
			return true
		}
	}
	return false
}

// gatherStock is what the pack holds of each good.
type gatherStock struct {
	items map[string]int32
	full  bool
}

func stockOf(inv []*civquestv1.ItemStackProto) gatherStock {
	s := gatherStock{items: map[string]int32{}}
	stacks := 0
	for _, it := range inv {
		if it != nil && it.Count > 0 {
			stacks++
			s.items[bareID(it.ItemId)] += it.Count
		}
	}
	s.full = stacks >= gatherPackStacks
	return s
}

// wants reports whether more of item (counting towards good) fits the stock targets.
func (s gatherStock) wants(good gatherGood, item string) bool {
	if s.full && s.items[item] == 0 {
		return false
	}
	sum := func(match func(string) bool) (n int32) {
		for id, c := range s.items {
			if match(id) {
				n += c
			}
		}
		return n
	}
	switch good {
	case goodRare:
		return s.items[item] < gatherRareStock
	case goodFood, goodFish:
		// Meat and fish: plant food carried does not stop the search for protein.
		return sum(func(id string) bool { return CalculateFoodScore(id) > 0 && !isWildFoodItem(id) }) < gatherFoodStock
	case goodForage:
		return s.items[item] < gatherForageStock
	case goodLog:
		return sum(isLogBlockID) < gatherLogStock
	case goodCoal:
		return s.items["coal"]+s.items["charcoal"] < gatherCoalStock
	case goodStone:
		return s.items["cobblestone"]+s.items["cobbled_deepslate"] < gatherStoneStock
	}
	return false
}

// gatherBlockGood classifies a visible block; item is what breaking it yields.
func gatherBlockGood(b *civquestv1.VisibleBlock) (good gatherGood, item string) {
	id := bareID(b.BlockId)
	ore := strings.TrimPrefix(id, "deepslate_")
	switch {
	case rareOreItems[ore] != "":
		return goodRare, rareOreItems[ore]
	case ore == "coal_ore":
		return goodCoal, "coal"
	case id == "stone":
		return goodStone, "cobblestone"
	case id == "deepslate":
		return goodStone, "cobbled_deepslate"
	case wildFoodBlocks[id] != "" && isEdiblePlantBlock(b) > 0 && isCropMatureHint(b):
		return goodForage, wildFoodBlocks[id]
	}
	return goodNone, ""
}

// gatherDropGood classifies a dropped item by its display name.
func gatherDropGood(e *civquestv1.VisibleEntity) (gatherGood, string) {
	name := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(e.DisplayName)), " ", "_")
	switch {
	case rareDropNames[name] != "":
		return goodRare, rareDropNames[name]
	case name == "coal":
		return goodCoal, name
	case name == "cobblestone" || name == "cobbled_deepslate":
		return goodStone, name
	case isLogBlockID(name):
		return goodLog, name
	case isWildFoodItem(name):
		return goodForage, name
	case isFoodDropEntity(e):
		return goodFood, name
	}
	return goodNone, ""
}

// gatherFind is one thing worth taking.
type gatherFind struct {
	Key    string
	Good   gatherGood
	Block  *civquestv1.VisibleBlock  // a block to break
	Entity *civquestv1.VisibleEntity // a drop to walk onto, or an animal to hunt
	Hunt   bool
	Fell   bool // a tree: the harvest task takes the whole trunk
	Fish   bool // a spell of fishing, driven by evaluateFishing
	Pos    [3]float64
	Dist   float64
}

func (f *gatherFind) better(o *gatherFind) bool {
	if o == nil {
		return true
	}
	// A drop lies there to be taken before anything is broken or hunted.
	fd, od := f.Entity != nil && !f.Hunt, o.Entity != nil && !o.Hunt
	if fd != od {
		return fd
	}
	if a, b := gatherRank[f.Good], gatherRank[o.Good]; a != b {
		return a < b
	}
	return f.Dist < o.Dist
}

// bestGatherFind returns the most worthwhile find in view, or nil. state.mu must not be held.
func bestGatherFind(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState, skipped func(string) bool) *gatherFind {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return nil
	}
	pos := frame.Status.Position
	stock := stockOf(frame.Status.Inventory)
	feet := int32(math.Floor(pos.Y))
	var best *gatherFind

	state.mu.Lock()
	defer state.mu.Unlock()
	offer := func(f *gatherFind) {
		if f.Dist <= gatherRadius && !skipped(f.Key) &&
			!state.IsTargetUnreachableLocked(f.Pos[0], f.Pos[1], f.Pos[2], frame.WorldTick) && f.better(best) {
			best = f
		}
	}
	if stock.wants(goodFish, "") {
		if d, ok := fishingSpot(frame); ok {
			offer(&gatherFind{Key: gatherFishKey, Good: goodFish, Fish: true, Pos: [3]float64{pos.X, pos.Y, pos.Z}, Dist: d})
		}
	}
	for _, e := range frame.VisibleEntities {
		if e == nil || e.Position == nil {
			continue
		}
		at := [3]float64{e.Position.X, e.Position.Y, e.Position.Z}
		if isDroppedItemEntity(e) {
			d := entityDistance(pos, e)
			if good, item := gatherDropGood(e); good != goodNone && d > forageDropPickupReach && stock.wants(good, item) {
				offer(&gatherFind{Key: fmt.Sprintf("drop:%d", e.EntityId), Good: good, Entity: e, Pos: at, Dist: d})
			}
			continue
		}
		// Sheep are left for their wool, young animals to grow.
		if isPassiveFoodAnimal(e) && !e.IsBaby && bareID(e.EntityType) != "sheep" &&
			inspectBootstrapInventory(frame.Status.Inventory).hasSword && stock.wants(goodFood, "") {
			offer(&gatherFind{Key: fmt.Sprintf("animal:%d", e.EntityId), Good: goodFood, Entity: e, Hunt: true, Pos: at, Dist: entityDistance(pos, e)})
		}
	}
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		good, item := gatherBlockGood(b)
		if good == goodNone || !stock.wants(good, item) || !HasSuitableTool(frame.Status.Inventory, RequirementForBlock(b.BlockId)) {
			continue
		}
		dy := b.Pos.Y - feet
		if dy < -gatherMaxReachBelow || dy > gatherMaxClimbUp {
			continue
		}
		// Never dig the ground out from underfoot, and take stone only from outcrops: a floor
		// of stone mined away leaves pits.
		if dy < 0 && (good == goodStone || (b.Pos.X == int32(math.Floor(pos.X)) && b.Pos.Z == int32(math.Floor(pos.Z)))) {
			continue
		}
		offer(&gatherFind{Key: fmt.Sprintf("block:%d,%d,%d", b.Pos.X, b.Pos.Y, b.Pos.Z), Good: good, Block: b,
			Pos: [3]float64{float64(b.Pos.X) + 0.5, float64(b.Pos.Y), float64(b.Pos.Z) + 0.5}, Dist: blockDistance(pos, b)})
	}
	if !skipped(gatherNoFelling) && stock.wants(goodLog, "") {
		if trunk, _ := findNearestTreeTrunk(agentID, frame, pos, state); trunk != nil {
			offer(&gatherFind{Key: fmt.Sprintf("trunk:%d,%d", trunk.Pos.X, trunk.Pos.Z), Good: goodLog, Block: trunk, Fell: true,
				Pos: [3]float64{float64(trunk.Pos.X) + 0.5, float64(trunk.Pos.Y), float64(trunk.Pos.Z) + 0.5}, Dist: blockDistance(pos, trunk)})
		}
	}
	return best
}

// fishingSpot reports whether the agent can fish here: a line already out, water in view for
// a rod it carries or can make at hand, or a fish close enough to strike.
func fishingSpot(frame *civquestv1.TelemetryFrame) (float64, bool) {
	if fishingLineCast(frame) {
		return 0, true
	}
	inv := frame.Status.Inventory
	if water, d := nearestSurfaceWater(frame); water != nil && d <= gatherRadius &&
		(hasInventoryItem(inv, "fishing_rod") || countInventoryItem(inv, "string") >= 2 && craftTableNear(frame) &&
			(countInventoryItem(inv, "stick") >= 3 || planksCarried(inv) >= 2)) {
		return d, true
	}
	if fish, d := nearestFish(frame); fish != nil && d <= gatherRadius {
		return d, true
	}
	return 0, false
}

// gatherGaveUp is the Memory write for a find that took too long.
type gatherGaveUp struct {
	Key   string
	Until int64
}

func (w *fsmWorld) gatherSkipped(key string) bool {
	return w.fsm != nil && w.Tick() < w.fsm.gatherSkips[key]
}

// gatherLocal is the Gather frame Local.
type gatherLocal struct {
	Key        string
	Since      int64
	LastAction int64
	Fishing    forageLocal // the fishing cursor while the find is a spell of fishing
}

// gatherActivity bids while the agent has no settlement and something worth taking is in view.
type gatherActivity struct{ frameBase }

func (a gatherActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a gatherActivity) Init(htpda.World, any) (any, htpda.Lease) {
	return gatherLocal{}, htpda.Lease{}
}
func (a gatherActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (gatherActivity) fallThrough(*fsmWorld) bool         { return true }
func (gatherActivity) tick(*fsmWorld) ([]FSMIntent, bool) { return nil, true }

// eligible: unsettled, awake, fed, daytime, safe, nothing pending with others, and a find in view.
func (a gatherActivity) eligible(lw *fsmWorld) bool { return a.find(lw) != nil }

func (gatherActivity) find(lw *fsmWorld) *gatherFind {
	ctx := lw.ctx
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil
	}
	f := ctx.Frame
	if len(f.IncomingChats) > 0 || f.PendingTrade != nil || ctx.IsDuskOrNight() ||
		f.Status.FoodLevel <= foragingFedFoodLevel || !woolSafe(f) || hasSurvivalPreemption(f) ||
		lw.backedOff(activityKindGather) || ctx.State.SettlementEstablished() {
		return nil
	}
	return bestGatherFind(ctx.AgentID, f, ctx.State, func(key string) bool {
		if key == gatherNoFelling {
			return lw.onStack(activityKindTaskSubroutine)
		}
		return lw.gatherSkipped(key)
	})
}

func (a gatherActivity) run(lw *fsmWorld, local any) htpda.Step {
	l, _ := local.(gatherLocal)
	find := a.find(lw)
	if find == nil {
		return popNoWork(lw, a.kind)
	}
	frame := lw.ctx.Frame
	tick := frame.WorldTick
	if l.Key != find.Key {
		l.Key, l.Since, l.LastAction, l.Fishing = find.Key, tick, 0, forageLocal{}
	}
	if find.Fish {
		return fishAlongTheWay(lw.ctx.AgentID, frame, l)
	}
	if tick-l.Since >= gatherTargetTicks {
		return htpda.Step{Op: htpda.OpStay, Next: gatherLocal{},
			Memory: []htpda.MemoryWrite{gatherGaveUp{Key: find.Key, Until: tick + gatherSkipTicks}}}
	}
	if find.Fell && lw.skipped[activityKindTaskSubroutine] {
		return htpda.Step{Op: htpda.OpStay, Next: l} // a task ended this tick: start the next one later
	}
	if find.Fell {
		// Felling is this frame's child. A trunk is tried once: however the task ends, it
		// is not started again straight away.
		st := lw.ctx.State
		trunk := find.Block.Pos
		task := NewHarvestTreeTask(trunk.X, trunk.Y, trunk.Z, st.Stage(), tick)
		task.Spec.Standalone = true
		st.publishTask(task)
		return htpda.Step{Op: htpda.OpPush, Child: activityKindTaskSubroutine, Args: taskStart{Task: task}, Next: gatherLocal{},
			Memory: []htpda.MemoryWrite{gatherGaveUp{Key: find.Key, Until: tick + gatherSkipTicks}}}
	}
	intent, next := gatherIntent(lw.ctx.AgentID, frame, find, l)
	var intents []*civquestv1.AgentIntent
	if intent != nil {
		intents = []*civquestv1.AgentIntent{intent}
	}
	return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: next}
}

// fishAlongTheWay runs one step of a spell of fishing. It ends when the casts keep missing or
// the spell has run its time (a line still out gets as long again before it is reeled in).
func fishAlongTheWay(agentID string, frame *civquestv1.TelemetryFrame, l gatherLocal) htpda.Step {
	tick := frame.WorldTick
	cast := fishingLineCast(frame)
	over := tick-l.Since >= gatherFishTicks
	if l.Fishing.Fish.DryCasts >= fishMaxDryCasts && !cast || over && !cast || tick-l.Since >= 2*gatherFishTicks {
		st := htpda.Step{Op: htpda.OpStay, Next: gatherLocal{},
			Memory: []htpda.MemoryWrite{gatherGaveUp{Key: gatherFishKey, Until: tick + gatherSkipTicks}}}
		if cast {
			st.Intents = []*civquestv1.AgentIntent{{AgentId: agentID, Action: &civquestv1.AgentIntent_UseItem{
				UseItem: &civquestv1.UseItemIntent{Mode: civquestv1.UseItemIntent_REEL_FISHING_ROD}}}}
		}
		return st
	}
	cursor := l.Fishing.clone()
	opt, _ := evaluateFishing(agentID, frame, &cursor, 1.0, false)
	l.Fishing = cursor
	st := htpda.Step{Op: htpda.OpStay, Next: l}
	if opt.intent != nil {
		st.Intents = []*civquestv1.AgentIntent{opt.intent}
	}
	return st
}

// gatherIntent walks to the find or takes it (nil: wait for the walk or the cooldown).
func gatherIntent(agentID string, frame *civquestv1.TelemetryFrame, find *gatherFind, l gatherLocal) (*civquestv1.AgentIntent, gatherLocal) {
	tick := frame.WorldTick
	walking := frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	ready := l.LastAction == 0 || tick-l.LastAction >= gatherActionTicks
	walkTo := func(tolerance int32) (*civquestv1.AgentIntent, gatherLocal) {
		if walking && l.LastAction != 0 || !ready {
			return nil, l
		}
		l.LastAction = tick
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target:          &civquestv1.Vec3{X: find.Pos[0], Y: find.Pos[1], Z: find.Pos[2]},
			SpeedMultiplier: 1.0, ArrivalTolerance: tolerance,
		}}}, l
	}
	switch {
	case find.Hunt:
		if find.Dist > gatherMeleeReach {
			return walkTo(2)
		}
		if !ready {
			return nil, l
		}
		l.LastAction = tick
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Attack{Attack: &civquestv1.AttackIntent{
			TargetEntityId: find.Entity.EntityId,
		}}}, l
	case find.Entity != nil:
		return walkTo(1)
	case find.Dist > gatherMineReach:
		return walkTo(2)
	}
	if !ready {
		return nil, l
	}
	l.LastAction = tick
	return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{
		Pos: cloneBlockPos(find.Block.Pos), InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK, BlockOrItemId: find.Block.BlockId,
	}}}, l
}
