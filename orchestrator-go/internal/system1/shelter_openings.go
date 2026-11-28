package system1

// The finished cabin's doorway and windows (cq-8jf; user: favour protection). An opening that
// cannot be fitted is blocked with a temporary block. What is temporary is read off the world
// against the cabin template, the only record of it: a solid block standing where the template
// draws a fitting. It is replaced when the fitting is held or can be made; the doorway is
// sealed from inside for the night and opened when the night is over. A shelter sealed this way
// is no trap: recovery leaves by the doorway and never through the walls.

import (
	"math"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

type openingStands uint8

const (
	standsUnseen openingStands = iota
	standsOpen
	standsTemporary
	standsFitting
)

const (
	// tendRange is how near the cabin its openings are looked after.
	tendRange = 8.0
	// tendReach is the distance within which a cell of an opening is broken or filled.
	tendReach = 4.5
	// tendPace separates two asks about the openings.
	tendPace = int64(20)
	// tendAsks bounds the asks about one cell before the owner does otherwise.
	tendAsks = 6
	// doorstepOut is how far south of the centre one stands outside the doorway.
	doorstepOut = int32(3)
	// doorwayWalks is how often recovery walks out through an unblocked doorway before it
	// breaks a door that will not let it through.
	doorwayWalks = 3
)

// openingCell is one cell of an opening as one frame shows it.
type openingCell struct {
	At    *civquestv1.BlockPosProto
	Stand openingStands
	Block string
}

type openingView struct {
	State string // as the template draws it
	Cells []openingCell
}

func (o openingView) any(s openingStands) bool {
	for _, c := range o.Cells {
		if c.Stand == s {
			return true
		}
	}
	return false
}

// lookAtOpenings diffs the frame against the template at site, the doorway first.
func lookAtOpenings(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, site *civquestv1.BlockPosProto) []openingView {
	var out []openingView
	for _, o := range blueprints.CabinOpenings() {
		v := openingView{State: o.State}
		for _, d := range o.Cells {
			c := openingCell{At: &civquestv1.BlockPosProto{X: site.X + d[0], Y: site.Y + d[1], Z: site.Z + d[2]}}
			b := FindVisibleBlockAt(frame, c.At.X, c.At.Y, c.At.Z)
			switch {
			case b != nil && blueprints.FillsOpening(o.State, b.BlockId):
				c.Stand, c.Block = standsFitting, b.BlockId
			case b != nil && isSuffocatingBlock(b.BlockId):
				c.Stand, c.Block = standsTemporary, b.BlockId
			case b != nil || sampledAround(pos, c.At):
				c.Stand = standsOpen
			}
			v.Cells = append(v.Cells, c)
		}
		out = append(out, v)
	}
	return out
}

// sampledAround reports that the mod lists whatever stands at c for an agent at pos, so a cell
// there with nothing listed is empty: one or two cells off along an axis, feet to two up.
func sampledAround(pos *civquestv1.Vec3, c *civquestv1.BlockPosProto) bool {
	f := ladderCell(pos)
	dx, dy, dz := int(c.X)-f.X, int(c.Y)-f.Y, int(c.Z)-f.Z
	return dy >= 0 && dy <= 2 && (dx == 0) != (dz == 0) && intAbs(dx)+intAbs(dz) <= 2
}

// insideCabin reports that pos is in the room of the cabin centred at site.
func insideCabin(pos *civquestv1.Vec3, site *civquestv1.BlockPosProto) bool {
	if pos == nil || site == nil {
		return false
	}
	c := ladderCell(pos)
	return intAbs(c.X-int(site.X)) <= 1 && intAbs(c.Z-int(site.Z)) <= 1 && c.Y >= int(site.Y) && c.Y <= int(site.Y)+2
}

func cabinCentre(site *civquestv1.BlockPosProto) *civquestv1.Vec3 {
	return &civquestv1.Vec3{X: float64(site.X) + 0.5, Y: float64(site.Y + 1), Z: float64(site.Z) + 0.5}
}

func cabinDoorstep(site *civquestv1.BlockPosProto) *civquestv1.Vec3 {
	return &civquestv1.Vec3{X: float64(site.X) + 0.5, Y: float64(site.Y), Z: float64(site.Z+doorstepOut) + 0.5}
}

// tendMark is an ask about an opening that failed: it is not made again until what it depends on
// has changed, the time of day for a block and the timber held for a craft.
type tendMark struct {
	Cell       lightPos3
	Sheltering bool
	Timber     int32 // one more than the timber held when a craft failed, 0 none
}

// cabinTend is the Shelter frame's work on the openings.
type cabinTend struct {
	Cell   lightPos3 // the cell last asked about
	Asks   int
	Walks  int
	Sent   int64
	Walked int64
	Failed tendMark
}

// tendMove is the next thing to do about the openings. At most one of Break, Place, Craft and
// Walk is set; hold keeps the frame with nothing to ask for.
type tendMove struct {
	Break, Place *civquestv1.BlockPosProto
	Block        string
	Craft        string
	Walk         *civquestv1.Vec3
	Door         bool // the cell is the doorway's: leaving depends on it
	Hold         bool
}

func (m tendMove) none() bool {
	return m.Break == nil && m.Place == nil && m.Craft == "" && m.Walk == nil && !m.Hold
}

// cabinMoveLocked chooses what to do about the openings of the finished cabin. state.mu held.
func (lw *fsmWorld) cabinMoveLocked(t cabinTend) tendMove {
	s, frame := lw.ctx.State, lw.Frame()
	pos, site := lw.pos(), s.ShelterPos
	if frame == nil || pos == nil || site == nil ||
		math.Hypot(pos.X-float64(site.X)-0.5, pos.Z-float64(site.Z)-0.5) > tendRange || math.Abs(pos.Y-float64(site.Y)) > 4 {
		return tendMove{}
	}
	sheltering := skillIsDuskOrNight(lw.skillCtx, lw.ctx.Facts) || lw.fsm.currentRoutine == RoutineSleep
	// With a bed elsewhere the night is not spent here, so the way is not shut.
	keepIn := sheltering && (s.AssignedBed == nil || insideCabin(&civquestv1.Vec3{X: float64(s.AssignedBed.X), Y: float64(s.AssignedBed.Y), Z: float64(s.AssignedBed.Z)}, site))
	inside := insideCabin(pos, site)
	openings := lookAtOpenings(frame, pos, site)
	held := heldItemIDs(frame.Status.Inventory)
	inv := inspectBootstrapInventory(frame.Status.Inventory)
	block := ""
	if inv.buildingBlockCount > 0 {
		block = cabinBlock(blueprints.BlockOakPlanks, frame.Status.Inventory, inv.placeableBlockID)
	}
	failed := func(at *civquestv1.BlockPosProto) bool {
		return t.Failed.Cell == lightSite(at) && t.Failed.Sheltering == sheltering
	}
	reach := func(at *civquestv1.BlockPosProto) bool { return withinReachOf(pos, at, tendReach) }

	var door openingView
	for _, o := range openings {
		isDoor := !blueprints.IsWindow(o.State)
		if isDoor {
			door = o
		}
		if o.any(standsFitting) {
			continue
		}
		fill, have := blueprints.FittingFill(o.State, held)
		// A temporary block comes out for the fitting; the doorway's also to leave, to let others
		// in, and to get in for the night.
		if have || isDoor && (!keepIn || !inside) {
			for i := len(o.Cells) - 1; i >= 0; i-- {
				if c := o.Cells[i]; c.Stand == standsTemporary && reach(c.At) && (isDoor || !failed(c.At)) {
					return tendMove{Break: c.At, Block: c.Block, Door: isDoor}
				}
			}
		}
		for _, c := range o.Cells {
			if c.Stand != standsOpen || !reach(c.At) || failed(c.At) || blueprints.IsCollidingWithVoxel(pos, c.At) {
				continue
			}
			switch {
			case have && !o.any(standsTemporary) && !o.any(standsUnseen):
				return tendMove{Place: o.Cells[0].At, Block: fill, Door: isDoor}
			case !have && block != "" && (!isDoor || keepIn && inside):
				return tendMove{Place: c.At, Block: block, Door: isDoor}
			}
		}
	}
	if len(door.Cells) == 0 {
		return tendMove{}
	}
	hung := door.any(standsFitting)
	if !hung && t.Walks < tendAsks {
		_, have := blueprints.FittingFill(door.State, held)
		switch {
		case inside && door.any(standsUnseen):
			return tendMove{Walk: cabinCentre(site)} // look from where the whole doorway shows
		case keepIn && !inside && (block != "" || have) && !failed(door.Cells[0].At):
			if door.any(standsUnseen) {
				return tendMove{Walk: cabinDoorstep(site)}
			}
			return tendMove{Walk: cabinCentre(site)}
		}
	}
	if craft := lw.cabinCraftLocked(openings, held, inv, t, keepIn); craft != "" {
		return tendMove{Craft: craft}
	}
	return tendMove{Hold: keepIn && inside && door.any(standsTemporary)}
}

// cabinCraftLocked is the first unfitted opening whose fitting can be made without walking off.
func (lw *fsmWorld) cabinCraftLocked(openings []openingView, held []string, inv bootstrapInventorySummary,
	t cabinTend, keepIn bool) string {
	s, frame, pos := lw.ctx.State, lw.Frame(), lw.pos()
	if t.Failed.Timber != 0 && inv.logCount*4+inv.planksCount < t.Failed.Timber {
		return ""
	}
	for _, o := range openings {
		if o.any(standsFitting) || !o.any(standsOpen) && !o.any(standsTemporary) || keepIn && !insideCabin(pos, s.ShelterPos) {
			continue
		}
		if _, have := blueprints.FittingFill(o.State, held); have {
			continue
		}
		table, distSq := s.fittingTableLocked(frame, pos, s.ShelterPos)
		if table != nil && !isCraftingTableWithinReach(table, distSq, pos) && !hasItemID(held, craftingTableItem) {
			continue
		}
		if !(fittingView{state: o.State}).timberShort(s, frame, pos, s.ShelterPos, inv) && !s.RecipeGraphLocked().IsEmpty() {
			return o.State
		}
	}
	return ""
}

func hasItemID(held []string, id string) bool {
	for _, h := range held {
		if h == id {
			return true
		}
	}
	return false
}

func withinReachOf(pos *civquestv1.Vec3, b *civquestv1.BlockPosProto, reach float64) bool {
	dx, dy, dz := float64(b.X)+0.5-pos.X, float64(b.Y)+0.5-pos.Y, float64(b.Z)+0.5-pos.Z
	return dx*dx+dy*dy+dz*dz <= reach*reach
}

// tendsCabin reports that the finished cabin's openings want something done now.
func (lw *fsmWorld) tendsCabin() bool {
	if lw.ctx == nil || lw.ctx.State == nil || lw.fsm == nil {
		return false
	}
	s := lw.ctx.State
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.CabinBuilt < blueprints.CabinTotalBlocks {
		return false
	}
	return !lw.cabinMoveLocked(cabinTend{Failed: s.construction.site.Tend}).none()
}

// tendCabin is one step of the Shelter frame on a finished cabin; ok is false when the openings
// want nothing.
func (lw *fsmWorld) tendCabin(l *shelterLocal) (intents []*civquestv1.AgentIntent, ok bool) {
	s := lw.ctx.State
	s.mu.Lock()
	defer s.mu.Unlock()
	m := lw.cabinMoveLocked(l.Tend)
	if m.none() {
		return nil, false
	}
	frame, tick, t := lw.Frame(), lw.Tick(), &l.Tend
	navState := frame.Status.GetNavigationState()
	pathing := navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	send := func(in *civquestv1.AgentIntent) []*civquestv1.AgentIntent {
		if in == nil {
			return nil
		}
		return []*civquestv1.AgentIntent{in}
	}
	switch {
	case m.Hold:
		if !pathing && !frame.Status.GetIsInWater() {
			l.Rest = "sheltering in the cabin behind a blocked doorway until morning"
		}
	case m.Craft != "":
		if t.Cell != (lightPos3{}) || l.Fit.Index != -1 {
			t.Cell, l.Fit = lightPos3{}, fittingJob{Index: -1, Since: tick}
		}
		in, spent := l.workFitting(s, lw.agentID(), m.Craft, frame, lw.pos(), navState)
		if spent || tick-l.Fit.Since >= fittingWait {
			inv := inspectBootstrapInventory(frame.Status.Inventory)
			t.Failed.Timber = inv.logCount*4 + inv.planksCount + 1
			l.Fit = fittingJob{}
		}
		return send(in), true
	case m.Walk != nil:
		if pathing || t.Walked != 0 && tick >= t.Walked && tick-t.Walked < fittingWalk {
			break
		}
		t.Walked = tick
		t.Walks++
		if t.Walks >= tendAsks {
			// No way in or to a view of the doorway was found: leave it until the day turns.
			t.Failed.Cell = lightSite(blueprintDoorway(s.ShelterPos))
			t.Failed.Sheltering = skillIsDuskOrNight(lw.skillCtx, lw.ctx.Facts) || lw.fsm.currentRoutine == RoutineSleep
		}
		return send(recoverStep(lw.agentID(), int32(math.Floor(m.Walk.X)), int32(m.Walk.Y), int32(math.Floor(m.Walk.Z)))), true
	default:
		if t.Sent != 0 && tick >= t.Sent && tick-t.Sent < tendPace {
			break
		}
		at, kind := m.Break, civquestv1.InteractBlockIntent_HARVEST_BREAK
		if at == nil {
			at, kind = m.Place, civquestv1.InteractBlockIntent_PLACE_BLOCK
		}
		if t.Cell != lightSite(at) {
			t.Cell, t.Asks = lightSite(at), 0
		}
		t.Sent = tick
		if t.Asks++; t.Asks >= tendAsks {
			t.Asks = 0
			if m.Door && m.Break != nil {
				// Never given up, or the agent is shut in: ask again from the middle of the room.
				return send(recoverStep(lw.agentID(), s.ShelterPos.X, s.ShelterPos.Y+1, s.ShelterPos.Z)), true
			}
			t.Failed.Cell = lightSite(at)
			t.Failed.Sheltering = skillIsDuskOrNight(lw.skillCtx, lw.ctx.Facts) || lw.fsm.currentRoutine == RoutineSleep
		}
		return send(&civquestv1.AgentIntent{AgentId: lw.agentID(), Action: &civquestv1.AgentIntent_InteractBlock{
			InteractBlock: &civquestv1.InteractBlockIntent{Pos: at, InteractionType: kind, BlockOrItemId: m.Block},
		}}), true
	}
	return nil, true
}

// blueprintDoorway is the lower cell of the template's doorway at site.
func blueprintDoorway(site *civquestv1.BlockPosProto) *civquestv1.BlockPosProto {
	d := blueprints.CabinOpenings()[0].Cells[0]
	return &civquestv1.BlockPosProto{X: site.X + d[0], Y: site.Y + d[1], Z: site.Z + d[2]}
}

// ownCabin is the site of the agent's finished cabin, or nil.
func (s *AgentState) ownCabin() *civquestv1.BlockPosProto {
	if s.GetCabinBuilt() < blueprints.CabinTotalBlocks {
		return nil
	}
	return s.GetShelterPos()
}

// doorwayStep is recovery in or into the agent's own cabin, which is no trap: the temporary
// blocks of the doorway are broken, and from inside the way out is walked. handled is false when
// the doorway is not what stands in the way, and ordinary recovery may go on.
func (loc *recoverLocal) doorwayStep(lw *fsmWorld, f *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) (in *civquestv1.AgentIntent, handled bool) {
	site := lw.ctx.State.ownCabin()
	if site == nil || loc.Cause == CauseCongestion {
		return nil, false
	}
	inside := insideCabin(pos, site)
	bound := loc.HasTarget && insideCabin(&civquestv1.Vec3{X: loc.Target[0], Y: loc.Target[1], Z: loc.Target[2]}, site)
	if !inside && !bound {
		return nil, false
	}
	door := lookAtOpenings(f, pos, site)[0]
	breakCell := func(c openingCell) *civquestv1.AgentIntent {
		return recoverBreak(lw.agentID(), &civquestv1.VisibleBlock{Pos: c.At, BlockId: c.Block})
	}
	for i := len(door.Cells) - 1; i >= 0; i-- {
		if c := door.Cells[i]; c.Stand == standsTemporary && withinReachOf(pos, c.At, tendReach) {
			return breakCell(c), true
		}
	}
	if !inside {
		return nil, false
	}
	walk := func(to *civquestv1.Vec3) (*civquestv1.AgentIntent, bool) {
		loc.Attempts++
		return recoverStep(lw.agentID(), int32(math.Floor(to.X)), int32(to.Y), int32(math.Floor(to.Z))), true
	}
	switch {
	case door.any(standsUnseen) || door.any(standsTemporary):
		return walk(cabinCentre(site))
	case loc.Attempts < doorwayWalks:
		return walk(cabinDoorstep(site))
	case door.any(standsFitting) && withinReachOf(pos, door.Cells[0].At, tendReach):
		return breakCell(door.Cells[0]), true
	}
	return nil, false
}

func intAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
