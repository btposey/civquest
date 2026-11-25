package system1

// Shelter and Construction as frame-local activities (ADR-036, bead cq-2sr.8).
//
// Build progress is the Shelter frame's typed value Local; it survives ticks and
// suspend/resume (an emergency frame pushed over Shelter leaves the Local untouched).
// Durable knowledge (ShelterPos, banked cabin block count) reaches the store only
// through the Store effect of the frame's pop/cancel/expiry. "Shelter complete" is
// derived: Local while the frame runs, the banked store count afterwards.

import (
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// shelterLocal is the Shelter frame Local: the frontier-cabin build progress.
type shelterLocal struct {
	BuildIndex int // next cabin blueprint block to place
	Phase      int // construction phase of the last placed block
	Pos        *civquestv1.BlockPosProto
	// Walk is the night-outpost search/exploration cursor used while sourcing materials.
	Walk walkLocal
	// TreeLocked/TreeX/TreeZ: the trunk this frame's timber sourcing is focused on (private to
	// the Shelter frame; the frame's existence is the lock).
	TreeLocked   bool
	TreeX, TreeZ int32
}

// targetTree returns the focused trunk (nil-safe: no frame bound means no focus).
func (l *shelterLocal) targetTree() (locked bool, x, z int32) {
	if l == nil {
		return false, 0, 0
	}
	return l.TreeLocked, l.TreeX, l.TreeZ
}

func (l *shelterLocal) focusTree(x, z int32) {
	if l != nil {
		l.TreeLocked, l.TreeX, l.TreeZ = true, x, z
	}
}

func (l *shelterLocal) clearTreeFocus() {
	if l != nil {
		l.TreeLocked, l.TreeX, l.TreeZ = false, 0, 0
	}
}

// shelterWalk is the walk cursor of a Shelter frame Local (a fresh one when no frame is bound).
func shelterWalk(prog *shelterLocal) *walkLocal {
	if prog == nil {
		return &walkLocal{}
	}
	return &prog.Walk
}

// complete reports whether every cabin block has been placed.
func (l shelterLocal) complete() bool { return l.BuildIndex >= blueprints.CabinTotalBlocks }

// cabinPhase is the phase of the last placed block (0 before any placement).
func cabinPhase(built int) int {
	if built <= 0 {
		return 0
	}
	return blueprints.CabinBlockPhase(built - 1)
}

// shelterStore is the Store effect a Shelter frame emits when it ends.
type shelterStore struct {
	Pos   *civquestv1.BlockPosProto
	Built int
}

// newShelterLocal seeds a Local from the durable store (call without s.mu held).
func newShelterLocal(s *AgentState) shelterLocal {
	if s == nil {
		return shelterLocal{}
	}
	built := s.GetCabinBuilt()
	return shelterLocal{BuildIndex: built, Phase: cabinPhase(built), Pos: s.GetShelterPos()}
}

// seedShelterLocalLocked is newShelterLocal for callers already holding s.mu.
func seedShelterLocalLocked(s *AgentState) shelterLocal {
	l := shelterLocal{BuildIndex: s.CabinBuilt, Phase: cabinPhase(s.CabinBuilt)}
	if s.ShelterPos != nil {
		l.Pos = &civquestv1.BlockPosProto{X: s.ShelterPos.X, Y: s.ShelterPos.Y, Z: s.ShelterPos.Z}
	}
	return l
}

func (l shelterLocal) storeWrite() htpda.StoreWrite {
	return shelterStore{Pos: l.Pos, Built: l.BuildIndex}
}

// applyShelterStore applies a Shelter frame's Store effect to the durable store.
func (s *AgentState) applyShelterStore(w shelterStore) {
	if w.Pos != nil {
		s.SetShelterPos(w.Pos)
	}
	s.SetCabinBuilt(w.Built)
}

// applyStoreWrites applies kernel Store effects (applied only on frame exit).
func (s *AgentState) applyStoreWrites(ws []htpda.StoreWrite) {
	if s == nil {
		return
	}
	for _, w := range ws {
		if v, ok := w.(shelterStore); ok {
			s.applyShelterStore(v)
		}
	}
}

// shelterActivity owns its eligibility (Propose) and waterfall; its weight is priorityShelter.
type shelterActivity struct{ frameBase }

func (a shelterActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a shelterActivity) Init(w htpda.World, _ any) (any, htpda.Lease) {
	if lw := asFSMWorld(w); lw != nil && lw.ctx != nil {
		return newShelterLocal(lw.ctx.State), htpda.Lease{}
	}
	return shelterLocal{}, htpda.Lease{}
}
func (a shelterActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (shelterActivity) fallThrough(lw *fsmWorld) bool { return lw.fsm.currentStage != StageColonize }

// cancel banks the Local into the store.
func (shelterActivity) cancel(local any) htpda.Step {
	sl, _ := local.(shelterLocal)
	return htpda.Step{Op: htpda.OpPop, Store: []htpda.StoreWrite{sl.storeWrite()}}
}

// exit ends the frame, banking the Local into the store.
func (a shelterActivity) exit(lw *fsmWorld, local any, intents []*civquestv1.AgentIntent, halt bool) htpda.Step {
	sl, _ := local.(shelterLocal)
	st := popFrame(lw, a.kind, intents, halt)
	st.Store = []htpda.StoreWrite{sl.storeWrite()}
	return st
}

// eligible: awake, no conversation/trade; a BUILD_SHELTER directive biases toward Shelter
// (which places the blocks); otherwise only without a bed/finished shelter, at dusk/night
// (bootstrap defers while a harvest still needs the agent's materials).
func (shelterActivity) eligible(lw *fsmWorld) bool {
	ctx, skillCtx := lw.ctx, lw.skillCtx
	stage, routine := lw.fsm.currentStage, lw.fsm.currentRoutine
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	if directiveBuildsShelter(stage, routine, ctx) && !skillIsShelterComplete(skillCtx, ctx.Facts) && !skillIsShelterOnCooldown(skillCtx, ctx.Facts) {
		return true // a BUILD_SHELTER directive biases toward Shelter, which places the blocks
	}
	if skillHasAssignedBed(skillCtx, ctx.Facts) || skillIsShelterComplete(skillCtx, ctx.Facts) || skillIsShelterOnCooldown(skillCtx, ctx.Facts) {
		return false
	}
	if stage == StageBootstrap {
		if !skillIsDuskOrNight(skillCtx, ctx.Facts) {
			return false
		}
		return !shouldDeferShelterForHarvest(ctx)
	}
	return skillIsDuskOrNight(skillCtx, ctx.Facts) || routine == RoutineSleep
}

func shouldDeferShelterForHarvest(ctx *FSMContext) bool {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if !ctx.State.HasActiveTask() {
		return false
	}
	_, kind, _, _ := ctx.State.ActiveTaskInfo()
	if kind != "harvest_tree" && kind != "collect_items" {
		return false
	}
	inv := inspectBootstrapInventory(ctx.Frame.Status.Inventory)
	return inv.buildingBlockCount == 0 && !inv.hasLight
}

// hasShelterWorkOrMaterials reports whether a (not yet running) Shelter frame has anything to
// do: banked cabin progress in the store or build materials in reach.
func hasShelterWorkOrMaterials(ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if ctx.State != nil && ctx.State.GetCabinBuilt() > 0 {
		return true
	}
	return hasShelterMaterials(ctx)
}

// hasShelterWork is hasShelterWorkOrMaterials for a running frame: its Local holds the progress.
func hasShelterWork(ctx *FSMContext, l shelterLocal) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	return l.BuildIndex > 0 || hasShelterMaterials(ctx)
}

func hasShelterMaterials(ctx *FSMContext) bool {
	inv := inspectBootstrapInventory(ctx.Frame.Status.Inventory)
	if inv.logCount > 0 || inv.planksCount > 0 || inv.buildingBlockCount > 0 || inv.hasLight {
		return true
	}
	for _, b := range ctx.Frame.InterestingBlocks {
		if b == nil {
			continue
		}
		id := strings.ToLower(b.BlockId)
		if strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem") {
			return true
		}
	}
	return false
}

// tick: the bootstrap shelter skill, then shelter_construction, then the goal evaluator.
func (shelterActivity) tick(lw *fsmWorld) ([]FSMIntent, bool) {
	ctx, skillCtx, reg := lw.ctx, lw.skillCtx, lw.reg
	var si []*civquestv1.AgentIntent
	if lw.fsm.currentStage == StageBootstrap {
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "shelter"); ok && len(skillIntents) > 0 {
			si = appendNonNilIntents(nil, skillIntents)
		}
	}
	if len(si) == 0 {
		if skill, ok := reg.GetSkill("shelter_construction"); ok && skill != nil && skill.Preconditions(ctx.Facts) {
			if out, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
				si = appendNonNilIntents(nil, out)
			}
		}
	}
	if len(si) == 0 {
		if intent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, skillCtx.Shelter); intent != nil {
			si = []*civquestv1.AgentIntent{intent}
		}
	}
	return wrapOrDone(si)
}

// run is one step of the Shelter frame. The waterfall runs with the frame's Local bound to the
// skill context and the updated Local becomes the next Local.
func (a shelterActivity) run(lw *fsmWorld, local any) htpda.Step {
	sl, _ := local.(shelterLocal)
	if sl.complete() {
		return a.exit(lw, sl, nil, true)
	}
	prog := sl
	lw.skillCtx.Shelter = &prog
	fi, done := a.tick(lw)
	lw.skillCtx.Shelter = nil
	intents := unwrapFSMIntents(fi)
	if !done {
		return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: prog}
	}
	// Nothing to emit this tick. With progress or materials in hand the build is merely
	// waiting (place/craft throttle, pathing): hold the frame instead of dropping progress.
	if hasShelterWork(lw.ctx, prog) {
		return htpda.Step{Op: htpda.OpStay, Next: prog}
	}
	return a.exit(lw, prog, intents, len(intents) > 0 || !a.fallThrough(lw))
}

// Construction progress (blueprint quadrant block index) is a value-type Local owned by whoever
// drives ConstructionSkill; the skill itself holds no per-agent state.
type ConstructionLocal struct{ Index int }
