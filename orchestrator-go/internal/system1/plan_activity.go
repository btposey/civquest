package system1

// PlanActivity: committed multi-step plans executed frame-locally (ADR-039, bead cq-dcx.4).
//
// The plan frame owns a plan.Plan (goal plan.GoalToolProgression for now) in its Local and
// executes plan.Next steps by delegating to the existing machinery:
//
//	Gather(tree)        -> a harvest_tree task (pushed as a task_subroutine child frame), or a walk
//	                       to a recalled timber POI
//	Gather(stone/ore)   -> navigate + HARVEST_BREAK on a visible block the held tool can drop
//	Craft               -> CraftItem with the pending-craft latch (cq-dcx.2)
//	Place               -> PLACE_BLOCK of the crafting table beside the agent (station_memory.go)
//	Goto                -> Navigate to the known table
//
// Target commitment: a gather target is chosen once and held until it is reached (felled),
// invalid (cooldown, unreachable, gone) or its lease expires; while it is valid a new candidate
// replaces it only when it is planSwitchRatio cheaper (hysteresis), so a nearer trunk coming into
// view does not re-target the agent every tick. Candidates are scored by distance plus the
// spatial-memory revisit cost of their cell (plan_memory.go).
//
// The plan frame bids (by self-push preemption over bootstrap/exploration, and by an expedition
// yielding to it) only while its next step is actionable now; otherwise the legacy bootstrap /
// exploration paths run (discovery of timber and stone is still theirs). On exit the frame banks
// its plan, committed target and craft latch to the Store; the next plan frame resumes them.

import (
	"math"
	"slices"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/plan"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/spatialmem"
)

const activityKindPlan = "plan"

// Planning budgets (tag: design-budget; tuning values, not measurements).
const (
	// planLeaseTicks: a plan older than this is replanned from scratch (~10 min).
	planLeaseTicks int64 = 12000
	// planTargetLeaseTicks: a committed gather target is dropped after this long (~30 s).
	planTargetLeaseTicks int64 = 600
	// planFailedTargets: consecutive trunks whose harvest ended with the trunk still standing
	// (unreachable, obstructed) before tree gathering backs off for planFailBackoffTicks, so the
	// discovery paths (survey, expedition) get to move the agent on.
	planFailedTargets          = 1
	planFailBackoffTicks int64 = 400
	// planSwitchRatio: a valid committed target is replaced only by a candidate whose score is
	// below this fraction of the committed one (hysteresis).
	planSwitchRatio = 0.5
	// planRevisitWeight: blocks of extra distance per unit of spatialmem.RevisitCost.
	planRevisitWeight = 6.0
	// planPeerPenalty: blocks added to a trunk a visible peer already stands at.
	planPeerPenalty = 16.0
	// planDepletedRadius: within this distance a target column with no reachable log left is
	// recorded as a felled trunk.
	planDepletedRadius = 6.0
	planActionTicks    = int64(8)
	planNavReissue     = int64(40)
	planCraftThrottle  = int64(20)
)

// planTarget is a committed gather/goto target.
type planTarget struct {
	Kind     string // "trunk", "poi", "mine", "table"
	Pos      [3]int32
	Chosen   int64
	Attempts int
}

func (t planTarget) set() bool { return t.Kind != "" }

// planLocal is the plan frame Local.
type planLocal struct {
	Plan       plan.Plan
	Target     planTarget
	Walk       walkLocal // inline-started task, craft latch, nav throttles
	LastAction int64
	LastNav    int64
	NavKey     [3]int32
	Replans    int
	Invalid    plan.Invalidation // last invalidation that forced a replan
	Fails      int               // consecutive failed gather targets
	Backoff    int64             // tree gathering is not attempted before this tick
	Breaks     breakRecord       // refused_breaks.go
}

type planActivity struct{ frameBase }

func newPlanActivity() planActivity { return planActivity{frameBase{activityKindPlan}} }

func (a planActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a planActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (planActivity) fallThrough(*fsmWorld) bool         { return true }
func (planActivity) tick(*fsmWorld) ([]FSMIntent, bool) { return nil, true }

// Init resumes the banked plan (Store) when it is still live.
func (planActivity) Init(w htpda.World, _ any) (any, htpda.Lease) {
	l := planLocal{}
	if lw := asFSMWorld(w); lw != nil && lw.fsm != nil {
		if b := lw.fsm.planning.Bank; b.live(w.Tick()) {
			l.Plan, l.Target, l.Backoff = b.Plan, b.Target, b.Backoff
			l.Walk.craftLatch = b.Latch
		}
	}
	return l, htpda.Lease{}
}

// eligible: the bootstrap window (Bootstrap / Colonize), a recipe graph, and an open goal. A
// settled agent has the window too, in which the plan prepares only the ladder reserve (cq-htv.4).
func (planActivity) eligible(lw *fsmWorld) bool {
	ctx := lw.ctx
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.Frame.Status.Position == nil || ctx.State == nil {
		return false
	}
	stage := lw.fsm.currentStage
	var open bool
	switch stage {
	case StageBootstrap, StageColonize:
		open = isBootstrapActivityEligible(stage, lw.fsm.currentRoutine, ctx)
	case StageSettlement:
		open = isBootstrapWindowOpen(lw.fsm.currentRoutine, ctx)
	}
	if !open || hasSurvivalPreemption(ctx.Frame) {
		return false
	}
	p := lw.probePlan()
	return p != nil && p.status == plan.NextReady
}

// args: the plan frame is pushed only while its next step can be acted on now.
func (a planActivity) args(lw *fsmWorld) (any, bool) {
	if lw.planOnStack() {
		return nil, false // one plan frame per agent: a suspended one resumes, it is not duplicated
	}
	p := lw.probePlan()
	if p == nil || p.status != plan.NextReady || !p.actionable {
		return nil, false
	}
	return nil, true
}

func (planActivity) store(local any, done bool) htpda.StoreWrite {
	l, _ := local.(planLocal)
	return planBank{Plan: l.Plan, Target: l.Target, Latch: l.Walk.craftLatch, Backoff: l.Backoff, Done: done}
}

func (a planActivity) cancel(local any) htpda.Step {
	return htpda.Step{Op: htpda.OpPop, Store: []htpda.StoreWrite{a.store(local, false)}}
}

func (a planActivity) exit(lw *fsmWorld, local any, intents []*civquestv1.AgentIntent, halt bool) htpda.Step {
	l, _ := local.(planLocal)
	st := withCraftLatch(popFrame(lw, a.kind, intents, halt), l.Walk.craftLatch)
	st.Store = []htpda.StoreWrite{a.store(local, false)}
	return st
}

// run is one plan step: (re)plan, pick the next step, act on it.
func (a planActivity) run(lw *fsmWorld, local any) htpda.Step {
	l, _ := local.(planLocal)
	ctx := lw.ctx
	st, frame := ctx.State, ctx.Frame
	tick := frame.WorldTick
	pos := frame.Status.Position
	mem := lw.spatial()

	st.mu.Lock()
	facts, table := planFactsLocked(frame, pos, st)
	if l.Plan.ID == "" {
		l.Plan = plan.NewToolProgression("tool_progression:"+ctx.AgentID, plan.Lease{Owner: ctx.AgentID, ExpiresTick: tick + planLeaseTicks}, facts)
	} else if inv := plan.Check(l.Plan, facts); inv != plan.InvalidNone {
		l.Plan = plan.Replan(l.Plan, facts)
		l.Plan.Lease.ExpiresTick = tick + planLeaseTicks
		l.Replans++
		l.Invalid = inv
		if inv == plan.InvalidSiteLost && l.Target.Kind == "table" {
			l.Target = planTarget{}
		}
	}
	step, status := plan.Next(l.Plan, facts)
	if status != plan.NextReady {
		st.mu.Unlock()
		done := status == plan.NextComplete
		out := withCraftLatch(popFrame(lw, a.kind, nil, false), l.Walk.craftLatch)
		out.Store = []htpda.StoreWrite{a.store(l, done)}
		return out
	}
	x := planExec{lw: lw, l: &l, facts: facts, table: table, mem: mem, tick: tick}
	l.Walk.deferTask = !l.Walk.hasTask()
	in, working := x.act(step)
	l.Walk.deferTask = false
	st.mu.Unlock()

	if !working {
		if x.moveOn {
			// The trunks in view failed: explore (expedition child) instead of handing the
			// agent back to a waterfall that would retry the same obstructed stand.
			if args, ok := lw.expeditionStart(activityKindBootstrap); ok {
				return htpda.Step{Op: htpda.OpPush, Child: activityKindExpedition, Args: args, Next: l, Memory: x.memory}
			}
		}
		// Nothing actionable now (no target in view): yield to the discovery paths below.
		st := a.exit(lw, l, nil, false)
		st.Memory = x.memory
		return st
	}
	var intents []*civquestv1.AgentIntent
	if in != nil {
		intents = []*civquestv1.AgentIntent{in}
	}
	return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: l, Memory: x.memory}
}

// planOnStack reports whether a plan frame is already on the stack (only valid inside Dispatch).
func (w *fsmWorld) planOnStack() bool {
	if w == nil || w.fsm == nil || w.fsm.kernel == nil {
		return false
	}
	for _, f := range w.activitiesInStep() {
		if f.Kind() == activityKindPlan {
			return true
		}
	}
	return false
}

// ---- facts ----

// planToolGate adapts the cq-gpa.10 block->tool model (RequirementForBlock /
// CraftTargetForRequirement) to plan.Facts.ToolGate (raw item -> minimum tool item).
func planToolGate(item string) string {
	return CraftTargetForRequirement(RequirementForBlock(planSourceBlock(item)))
}

// planSourceBlock maps a harvested raw item to the block it is mined from.
func planSourceBlock(item string) string {
	id := bareID(item)
	switch {
	case id == "cobblestone":
		return "minecraft:stone"
	case id == "coal":
		return "minecraft:coal_ore"
	case strings.HasPrefix(id, "raw_"):
		return "minecraft:" + strings.TrimPrefix(id, "raw_") + "_ore"
	case id == "diamond" || id == "emerald" || id == "redstone" || id == "lapis_lazuli":
		return "minecraft:" + strings.TrimSuffix(id, "_lazuli") + "_ore"
	}
	return item
}

// planFactsLocked builds the planner's read-only facts (state.mu held).
func planFactsLocked(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, st *AgentState) (plan.Facts, *civquestv1.VisibleBlock) {
	f := plan.Facts{
		Tick:      frame.WorldTick,
		Inventory: crafting.InventoryMapFromProtos(frame.Status.Inventory),
		Recipes:   st.RecipeGraphLocked(),
		ToolGate:  planToolGate,
		// A settled agent's plan prepares the reserve and nothing else.
		ReserveOnly: st.stage == StageSettlement,
	}
	table, _, dSq := findNearestCraftingTableLocked(frame, pos, st)
	f.Table.Near = isCraftingTableWithinReach(table, dSq, pos)
	f.Table.NoNew = st.stationRefusedLocked(craftingTableItem, frame)
	if table != nil && table.Pos != nil {
		f.Table.Known = true
		f.Table.Pos = plan.Pos{X: int(table.Pos.X), Y: int(table.Pos.Y), Z: int(table.Pos.Z)}
		f.Table.Dist = math.Sqrt(dSq)
	}
	return f, table
}

// planProbe is the per-tick, read-only evaluation of the plan's next step (cached on the
// fsmWorld: several frames ask for it in one tick while searching preemptors).
type planProbe struct {
	status     plan.NextStatus
	step       plan.Step
	actionable bool
}

func (w *fsmWorld) probePlan() *planProbe {
	if w.planProbe != nil {
		return w.planProbe
	}
	ctx := w.ctx
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.Frame.Status.Position == nil {
		return nil
	}
	st, frame := ctx.State, ctx.Frame
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.RecipeGraphLocked().IsEmpty() {
		w.planProbe = &planProbe{status: plan.NextBlocked}
		return w.planProbe
	}
	facts, table := planFactsLocked(frame, frame.Status.Position, st)
	// Next derives the steps itself; building a Plan first would derive them twice per tick.
	step, status := plan.Next(plan.Plan{Goal: plan.Goal{Kind: plan.GoalToolProgression}}, facts)
	pr := &planProbe{status: status, step: step}
	if status == plan.NextReady {
		l := planLocal{}
		if b := w.fsm.planning.Bank; b.live(frame.WorldTick) {
			l.Target, l.Walk.craftLatch, l.Backoff = b.Target, b.Latch, b.Backoff
		}
		x := planExec{lw: w, l: &l, facts: facts, table: table, mem: w.spatial(), tick: frame.WorldTick, dry: true}
		_, pr.actionable = x.act(step)
	}
	w.planProbe = pr
	return pr
}

// ---- step execution ----

// planExec executes one plan step against the live frame (state.mu held). dry=true only
// decides whether the step is actionable (no intents, no state writes).
type planExec struct {
	lw     *fsmWorld
	l      *planLocal
	facts  plan.Facts
	table  *civquestv1.VisibleBlock
	mem    spatialmem.Snapshot
	tick   int64
	dry    bool
	memory []htpda.MemoryWrite
	moveOn bool // gather targets failed: the agent should explore elsewhere
}

func (x *planExec) frame() *civquestv1.TelemetryFrame { return x.lw.ctx.Frame }
func (x *planExec) pos() *civquestv1.Vec3             { return x.lw.ctx.Frame.Status.Position }
func (x *planExec) st() *AgentState                   { return x.lw.ctx.State }
func (x *planExec) agent() string                     { return x.lw.ctx.AgentID }

// act returns the step's intent (nil = wait) and whether the step is being worked on.
func (x *planExec) act(step plan.Step) (*civquestv1.AgentIntent, bool) {
	switch step.Kind {
	case plan.StepCraft:
		return x.craft(step)
	case plan.StepPlace:
		return x.place(step)
	case plan.StepGoto:
		if step.Pos == nil {
			return nil, false
		}
		return x.gotoTable(*step.Pos)
	case plan.StepGather:
		switch step.Source {
		case plan.SourceTree:
			return x.gatherTree()
		case plan.SourceStone, plan.SourceOre:
			return x.gatherMine(step)
		}
	}
	return nil, false
}

func (x *planExec) craft(step plan.Step) (*civquestv1.AgentIntent, bool) {
	graph, inv := x.facts.Recipes, x.facts.Inventory
	if step.Requires3x3 && !x.facts.Table.Near {
		return nil, false
	}
	if x.l.Walk.craftHeld(graph, step.RecipeID, inv, x.tick) {
		return nil, true // in flight: wait for the inventory
	}
	ps, ok := graph.PlanNextStep(step.Item, inv, x.facts.Table.Near)
	if !ok || !ps.CanExecuteNow {
		return nil, false
	}
	if x.dry {
		return nil, true
	}
	st := x.st()
	if last := st.mem.lastCraftTick; last != 0 && x.tick >= last && x.tick-last < planCraftThrottle {
		return nil, true
	}
	st.mem.lastCraftTick = x.tick
	x.l.Walk.armCraft(graph, ps.RecipeID, inv, x.tick)
	c := &civquestv1.CraftItemIntent{RecipeId: ps.RecipeID, BatchCount: 1}
	if ps.Requires3x3 && x.table != nil {
		c.WorkstationPos = cloneBlockPos(x.table.Pos)
	}
	return &civquestv1.AgentIntent{AgentId: x.agent(), Action: &civquestv1.AgentIntent_CraftItem{CraftItem: c}}, true
}

func (x *planExec) place(step plan.Step) (*civquestv1.AgentIntent, bool) {
	if x.facts.Inventory[step.Item] <= 0 {
		return nil, false
	}
	if x.dry {
		return nil, true
	}
	in, refused := x.st().placeStationLocked(x.agent(), step.Item, x.frame(), &x.l.Walk)
	return in, !refused
}

func (x *planExec) gotoTable(p plan.Pos) (*civquestv1.AgentIntent, bool) {
	if !x.facts.Table.Known {
		return nil, false
	}
	if x.dry {
		return nil, true
	}
	tgt := [3]int32{int32(p.X), int32(p.Y), int32(p.Z)}
	if x.l.Target.Kind != "table" || x.l.Target.Pos != tgt {
		x.l.Target = planTarget{Kind: "table", Pos: tgt, Chosen: x.tick}
	}
	if x.frame().Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
		x.st().MarkWorkstationUnreachableLocked(tgt[0], tgt[1], tgt[2], x.tick)
		x.l.Target = planTarget{}
		return nil, true
	}
	return x.navigate(tgt), true
}

// navigate emits a Navigate to the block cell unless a path to it is in flight.
func (x *planExec) navigate(c [3]int32) *civquestv1.AgentIntent {
	f := x.frame()
	if f.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && x.l.NavKey == c {
		return nil
	}
	if x.l.NavKey == c && x.l.LastNav != 0 && x.tick >= x.l.LastNav && x.tick-x.l.LastNav < planNavReissue {
		return nil
	}
	x.l.NavKey, x.l.LastNav = c, x.tick
	return &civquestv1.AgentIntent{AgentId: x.agent(), Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
		Target: &civquestv1.Vec3{X: float64(c[0]) + 0.5, Y: float64(c[1]), Z: float64(c[2]) + 0.5}, SpeedMultiplier: 1.0, ArrivalTolerance: 2,
	}}}
}

// ---- gather: trees ----

type planTrunk struct {
	pos   [3]int32 // lowest reachable log of the column
	score float64
	large bool // chosen only when no small tree will do (tree_choice.go)
}

// trunkCandidates are the visible, usable trunk columns scored by distance + revisit cost.
func (x *planExec) trunkCandidates() []planTrunk {
	trunks := usableTrunks(x.frame(), x.pos(), x.st(), func(b *civquestv1.VisibleBlock) bool {
		return !x.mem.Depleted(trunkMark([2]int32{b.Pos.X, b.Pos.Z}), spatialmem.KindTrunk, x.tick)
	})
	out := make([]planTrunk, 0, len(trunks))
	for _, t := range trunks {
		c := planTrunk{pos: [3]int32{t.base.Pos.X, t.base.Pos.Y, t.base.Pos.Z}, large: t.large}
		c.score = x.trunkScore(c.pos)
		out = append(out, c)
	}
	return out
}

func trunkMark(col [2]int32) spatialmem.Pos { return spatialmem.Pos{X: col[0], Z: col[1]} }

// trunkScore: blocks of distance + revisit cost of the trunk's cell + a peer penalty.
func (x *planExec) trunkScore(p [3]int32) float64 {
	pos := x.pos()
	cx, cz := float64(p[0])+0.5, float64(p[2])+0.5
	s := math.Sqrt((cx-pos.X)*(cx-pos.X)+(float64(p[1])+0.5-pos.Y)*(float64(p[1])+0.5-pos.Y)+(cz-pos.Z)*(cz-pos.Z)) +
		spatialPenalty(x.mem, cx, cz, x.tick, planRevisitWeight)
	for _, e := range x.frame().VisibleEntities {
		if e == nil || e.Position == nil || IsHostile(e) || e.IsTamed || strings.EqualFold(e.DisplayName, x.agent()) {
			continue
		}
		et := strings.ToLower(e.EntityType)
		if (strings.Contains(et, "player") || strings.Contains(et, "agent")) && math.Hypot(e.Position.X-cx, e.Position.Z-cz) <= 2.5 {
			s += planPeerPenalty
			break
		}
	}
	return s
}

// commitTrunk keeps the committed trunk while valid; a candidate replaces it only past the
// hysteresis ratio. ok=false: no usable trunk in view.
func (x *planExec) commitTrunk() (planTarget, bool) {
	cands := x.trunkCandidates()
	small := slices.ContainsFunc(cands, func(c planTrunk) bool { return !c.large })
	best := -1
	for i, c := range cands {
		if c.large && small {
			continue
		}
		if best < 0 || c.score < cands[best].score ||
			(c.score == cands[best].score && (c.pos[0] < cands[best].pos[0] || (c.pos[0] == cands[best].pos[0] && c.pos[2] < cands[best].pos[2]))) {
			best = i
		}
	}
	// The legacy pick (nearest columns, spread across agents by an agent-id hash) is kept while
	// its cell carries no revisit cost, so agents keep fanning out across a stand; once the agent
	// has worked that cell (or it holds depleted trunks) the spatially scored best wins.
	if legacy, _ := findNearestTreeTrunk(x.agent(), x.frame(), x.pos(), x.st()); legacy != nil && legacy.Pos != nil {
		for i, c := range cands {
			if c.pos[0] == legacy.Pos.X && c.pos[2] == legacy.Pos.Z &&
				spatialPenalty(x.mem, float64(c.pos[0])+0.5, float64(c.pos[2])+0.5, x.tick, planRevisitWeight) == 0 {
				best = i
				break
			}
		}
	}
	cur := x.l.Target
	if cur.Kind == "trunk" && x.tick-cur.Chosen < planTargetLeaseTicks {
		for _, c := range cands {
			if c.pos[0] != cur.Pos[0] || c.pos[2] != cur.Pos[2] {
				continue
			}
			// still valid: hold it unless the best candidate is far cheaper, or it proves large
			// before it is tried while a small tree will do. A tried one is held so that its
			// failure is recorded.
			if best >= 0 && cands[best].pos != c.pos && (cands[best].score < planSwitchRatio*c.score ||
				cur.Attempts == 0 && c.large && !cands[best].large) {
				break
			}
			cur.Pos = c.pos
			return cur, true
		}
		if !x.dry && x.trunkGone(cur) {
			x.memory = append(x.memory, spatialDepleted{Pos: trunkMark([2]int32{cur.Pos[0], cur.Pos[2]}), Kind: spatialmem.KindTrunk, Tick: x.tick})
			x.l.Fails = 0
		}
	}
	if best < 0 {
		return planTarget{}, false
	}
	return planTarget{Kind: "trunk", Pos: cands[best].pos, Chosen: x.tick}, true
}

// trunkGone: the agent is next to the committed column and no reachable log is left in it.
func (x *planExec) trunkGone(t planTarget) bool {
	pos, f := x.pos(), x.frame()
	if math.Hypot(float64(t.Pos[0])+0.5-pos.X, float64(t.Pos[2])+0.5-pos.Z) > planDepletedRadius {
		return false
	}
	for _, b := range f.InterestingBlocks {
		if b != nil && b.Pos != nil && b.Pos.X == t.Pos[0] && b.Pos.Z == t.Pos[2] && isLogBlockID(b.BlockId) && !isElevatedCanopyOnlyLog(f, pos, b) {
			return false
		}
	}
	return true
}

func (x *planExec) gatherTree() (*civquestv1.AgentIntent, bool) {
	if x.l.Walk.hasTask() {
		if x.dry {
			return nil, true
		}
		res := x.l.Walk.stepTaskLocked(x.agent(), x.st(), x.frame())
		return res.Intent, true
	}
	if x.tick < x.l.Backoff {
		return nil, false
	}
	t, ok := x.commitTrunk()
	if !ok {
		return x.gatherRecalledTree()
	}
	if x.dry {
		return nil, true
	}
	st := x.st()
	if x.l.Target.Kind == "trunk" && x.l.Target.Pos[0] == t.Pos[0] && x.l.Target.Pos[2] == t.Pos[2] && t.Attempts > 0 {
		// The harvest task on this trunk ended and the trunk still stands: it failed.
		st.MarkTreeFailedLocked(t.Pos[0], t.Pos[2], x.tick)
		x.l.Target = planTarget{}
		if x.l.Fails++; x.l.Fails >= planFailedTargets {
			x.l.Fails, x.l.Backoff = 0, x.tick+planFailBackoffTicks
			x.moveOn = true
			return nil, false
		}
		return nil, true
	}
	t.Attempts = 1
	x.l.Target = t
	res := x.l.Walk.launchTaskLocked(x.agent(), st, x.frame(), NewHarvestTreeTask(t.Pos[0], t.Pos[1], t.Pos[2], st.stage, x.tick))
	return res.Intent, true
}

// gatherRecalledTree walks to a remembered timber stand; arriving at an empty one marks it.
func (x *planExec) gatherRecalledTree() (*civquestv1.AgentIntent, bool) {
	st := x.st()
	_, poi := findTreeTrunk(x.agent(), x.frame(), x.pos(), st, func(p *db.AgentPOI) bool {
		bp := poiBlockPos(p)
		return x.mem.Depleted(trunkMark([2]int32{bp.X, bp.Z}), spatialmem.KindTrunk, x.tick)
	})
	if poi == nil {
		return nil, false
	}
	bp := poiBlockPos(poi)
	if x.dry {
		return nil, true
	}
	x.l.Target = planTarget{Kind: "poi", Pos: [3]int32{bp.X, bp.Y, bp.Z}, Chosen: x.tick}
	in, handled := navigateToRecalledTreePOILocked(x.agent(), x.frame(), x.pos(), x.frame().Status.NavigationState, st, poi, &x.l.Walk)
	if !handled {
		// arrived at an empty stand (pruned) or stuck: an empty site is spatial knowledge.
		x.memory = append(x.memory, spatialDepleted{Pos: trunkMark([2]int32{bp.X, bp.Z}), Kind: spatialmem.KindTrunk, Tick: x.tick})
		x.l.Target = planTarget{}
	}
	return in, true
}

// ---- gather: stone / ore ----

// planMineable reports whether block id yields the gathered item.
func planMineable(blockID, item string) bool {
	if !isMineableStoneBlockID(blockID) {
		return false
	}
	b, it := bareID(blockID), bareID(item)
	switch it {
	case "cobblestone":
		return !strings.HasSuffix(b, "_ore")
	case "cobbled_deepslate":
		return b == "deepslate" || b == "cobbled_deepslate"
	case "coal":
		return strings.HasSuffix(b, "coal_ore")
	}
	return strings.Contains(b, strings.TrimPrefix(it, "raw_"))
}

func (x *planExec) gatherMine(step plan.Step) (*civquestv1.AgentIntent, bool) {
	f, pos := x.frame(), x.pos()
	inv := f.Status.Inventory
	// What was mined and left lying is picked up before more is dug.
	if drop := nearbyDrop(x.st(), f, func(item string) bool { return item != "" && item == bareID(step.Item) }); drop != nil {
		if x.dry {
			return nil, true
		}
		if f.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING ||
			(x.l.LastAction != 0 && x.tick >= x.l.LastAction && x.tick-x.l.LastAction < planActionTicks) {
			return nil, true
		}
		x.l.LastAction = x.tick
		return walkToDrop(x.agent(), drop), true
	}
	var best *civquestv1.VisibleBlock
	bestScore := math.MaxFloat64
	for _, b := range f.InterestingBlocks {
		if b == nil || b.Pos == nil || !planMineable(b.BlockId, step.Item) || !HasSuitableTool(inv, RequirementForBlock(b.BlockId)) {
			continue
		}
		if !dropWouldBeInReach(pos, b.Pos) {
			continue // buried, or underfoot: its drop would be left in the hole
		}
		if x.st().givenUpLocked(float64(b.Pos.X)+0.5, float64(b.Pos.Y), float64(b.Pos.Z)+0.5, x.tick) ||
			x.st().unbreakableLocked(b, x.tick) {
			continue // no way to it was found, or it will not break
		}
		c := [3]int32{b.Pos.X, b.Pos.Y, b.Pos.Z}
		d := math.Sqrt(math.Pow(float64(c[0])+0.5-pos.X, 2) + math.Pow(float64(c[1])+0.5-pos.Y, 2) + math.Pow(float64(c[2])+0.5-pos.Z, 2))
		if x.l.Target.Kind == "mine" && x.l.Target.Pos == c {
			d *= planSwitchRatio // committed block: hysteresis
		}
		if d < bestScore {
			best, bestScore = b, d
		}
	}
	if best == nil {
		return nil, false
	}
	if x.dry {
		return nil, true
	}
	c := [3]int32{best.Pos.X, best.Pos.Y, best.Pos.Z}
	if x.l.Target.Kind != "mine" || x.l.Target.Pos != c {
		x.l.Target = planTarget{Kind: "mine", Pos: c, Chosen: x.tick}
	}
	dx, dy, dz := float64(c[0])+0.5-pos.X, float64(c[1])+0.5-pos.Y, float64(c[2])+0.5-pos.Z
	now, spent := breakOrWalk(pos, best.Pos,
		dx*dx+dy*dy+dz*dz <= directiveReachRadius*directiveReachRadius && math.Abs(dy) <= 3.0)
	if spent {
		x.st().MarkWorkstationUnreachableLocked(c[0], c[1], c[2], x.tick)
		return nil, true // another block next step
	}
	if now {
		if x.l.LastAction != 0 && x.tick >= x.l.LastAction && x.tick-x.l.LastAction < planActionTicks {
			return nil, true
		}
		x.l.LastAction = x.tick
		return &civquestv1.AgentIntent{AgentId: x.agent(), Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{
			Pos: cloneBlockPos(best.Pos), InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK, BlockOrItemId: best.BlockId,
		}}}, true
	}
	return x.navigate(c), true
}
