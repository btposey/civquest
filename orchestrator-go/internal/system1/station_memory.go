package system1

// Setting down a station (cq-qdj). In play test 11 an agent crafted crafting tables and set
// them down for 13 minutes: no activity remembered having asked, or that one already stood
// there. Whoever sets one down asks through placeStationLocked, which reads the agent's memory
// of it, so that none can ask without end:
//   - the item left the pack: one stands there, and no other is made or set down near it;
//   - it did not: after stationAsks, setting it down from that spot is given up, and the
//     activity does something else.
// The memory is a value in Memory (agentMemory.stations), because the job passes between
// activities (plan, bootstrap, bed, armory). Choosers only read it. What a frame asked for is
// collected on its walk cursor and leaves the step as a Memory write (stationStep); no activity
// keeps its own count or has to remember to report one.

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const (
	craftingTableItem = "minecraft:crafting_table"

	// stationCellAsks cells are tried before standing somewhere else.
	stationCellAsks = 8
	// stationAsks is the bound: two rounds of cells and the walk between them.
	stationAsks = 2*stationCellAsks + 1
	// stationAskGap ticks pass between asks, for the pack to show what the last one did.
	stationAskGap = int64(20)
	// stationSpareRadius: within this of a station just set down, one is enough.
	stationSpareRadius = 16.0

	// EventStationGivenUp: a station would not go down and is no longer asked for there.
	EventStationGivenUp = "STATION_GIVEN_UP"
)

// stationAsk is one job of setting a station down.
type stationAsk struct {
	Item  string
	Stand [3]float64 // where the agent stood at the last ask
	Cell  blockCell  // the cell last asked for
	Held  int        // how many were carried then
	Asks  int
	Tick  int64
}

// stationMemory is what the agent remembers of setting stations down. It is a value, changed
// only by a Memory write.
type stationMemory struct {
	Asked stationAsk // under way, or given up once Asks reaches stationAsks
	Down  stationAsk // the last one that left the pack
}

// stationsRemembered is the Memory write of a frame that changed the memory.
type stationsRemembered stationMemory

// rememberStations applies a stationsRemembered write.
func (s *AgentState) rememberStations(m stationMemory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem.stations = m
}

// settled is the memory after seeing the pack: an item asked for that has gone is down.
func (m stationMemory) settled(inv map[string]int) stationMemory {
	if m.Asked.Asks > 0 && inv[m.Asked.Item] < m.Asked.Held {
		m.Down, m.Asked = m.Asked, stationAsk{}
	}
	return m
}

// givenUp reports that setting item down from about pos has been given up.
func (m stationMemory) givenUp(item string, pos *civquestv1.Vec3) bool {
	a := m.Asked
	return a.Item == item && a.Asks >= stationAsks &&
		math.Hypot(pos.X-a.Stand[0], pos.Z-a.Stand[2]) <= placeRelocateDist
}

// stationStandsNearLocked reports that the station last set down stands near enough that another
// is not wanted: it can be used here, or only a danger keeps the agent from walking to it.
func (s *AgentState) stationStandsNearLocked(down stationAsk, item string, frame *civquestv1.TelemetryFrame) bool {
	if down.Item != item {
		return false
	}
	pos, c := frame.Status.Position, down.Cell
	x, y, z := float64(c[0])+0.5, float64(c[1]), float64(c[2])+0.5
	if math.Sqrt((x-pos.X)*(x-pos.X)+(y-pos.Y)*(y-pos.Y)+(z-pos.Z)*(z-pos.Z)) > stationSpareRadius {
		return false
	}
	if b := FindVisibleBlockAt(frame, c[0], c[1], c[2]); b != nil && !strings.Contains(b.BlockId, bareID(item)) {
		return false // something else is there now
	}
	return walkableSlope(c, pos) && !s.IsWorkstationUnreachableLocked(c[0], c[1], c[2], frame.WorldTick) &&
		!s.givenUpLocked(x, y, z, frame.WorldTick)
}

// awaited reports that an ask for item is still out: the pack may yet show what it did.
func (m stationMemory) awaited(item string, tick int64) bool {
	a := m.Asked
	return a.Item == item && a.Asks > 0 && tick >= a.Tick && tick-a.Tick < stationAskGap
}

// stationsLocked is the memory as this frame's pack shows it. s.mu must be held.
func (s *AgentState) stationsLocked(frame *civquestv1.TelemetryFrame) stationMemory {
	return s.mem.stations.settled(crafting.InventoryMapFromProtos(frame.Status.Inventory))
}

// stationRefusedLocked reports that item is not to be made or set down here. s.mu must be held.
func (s *AgentState) stationRefusedLocked(item string, frame *civquestv1.TelemetryFrame) bool {
	m := s.stationsLocked(frame)
	return m.givenUp(item, frame.Status.Position) || s.stationStandsNearLocked(m.Down, item, frame)
}

// placeStationLocked asks for the carried station to be set down beside the agent: a free cell,
// another each time, and half way through a step to somewhere else (cq-6l3.17). refused means
// the owner must do something else; a nil intent without it means the last ask is still out.
// s.mu must be held.
func (s *AgentState) placeStationLocked(agentID, item string, frame *civquestv1.TelemetryFrame, w *walkLocal) (in *civquestv1.AgentIntent, refused bool) {
	if s.stationRefusedLocked(item, frame) {
		return nil, true
	}
	tick, pos := frame.WorldTick, frame.Status.Position
	a := s.stationsLocked(frame).Asked
	// Given up elsewhere, or left off far from here: this is another job.
	if a.Item != item || a.Asks >= stationAsks || math.Hypot(pos.X-a.Stand[0], pos.Z-a.Stand[2]) > stationSpareRadius {
		a = stationAsk{Item: item}
	}
	if s.stationsLocked(frame).awaited(item, tick) {
		return nil, false
	}
	n := a.Asks
	a.Asks, a.Tick, a.Stand = n+1, tick, [3]float64{pos.X, pos.Y, pos.Z}
	a.Held = crafting.InventoryMapFromProtos(frame.Status.Inventory)[item]
	if n == stationCellAsks {
		in = &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target:          &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z + placeRelocateDist},
			SpeedMultiplier: 1.0, ArrivalTolerance: 1,
		}}}
	} else {
		if n > stationCellAsks {
			n-- // the walk used no cell
		}
		cells := openCells(frame, pos)
		cell := cells[n%len(cells)]
		a.Cell = cellAt(cell)
		in = &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{
			Pos: cell, InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK, BlockOrItemId: item,
		}}}
	}
	if w != nil && w.stations != nil {
		w.stations.Asked = a
	}
	return in, false
}

// craftCursor returns the walk cursor of a Local that crafts and sets stations down: where
// its in-flight craft is latched and what it asks of a station is noted.
func craftCursor(local any) (walkLocal, func(walkLocal) any, bool) {
	switch l := local.(type) {
	case taskLocal:
		return l.Walk, func(w walkLocal) any { l.Walk = w; return l }, true
	case shelterLocal:
		return l.Walk, func(w walkLocal) any { l.Walk = w; return l }, true
	}
	return walkCarrier(local)
}

// stationStep gives a frame's walk cursor somewhere to note what it asks for during one step,
// and returns what changed as a Memory write.
func stationStep(w htpda.World, local any, step func(local any) htpda.Step) htpda.Step {
	lw := asFSMWorld(w)
	walk, put, ok := craftCursor(local)
	if !ok || lw == nil || lw.ctx == nil || lw.ctx.State == nil || lw.ctx.Frame.GetStatus() == nil {
		return step(local)
	}
	st := lw.ctx.State
	st.mu.RLock()
	before, now := st.mem.stations, st.stationsLocked(lw.ctx.Frame)
	st.mu.RUnlock()
	walk.stations = &now
	out := step(put(walk))
	if next, put, ok := craftCursor(out.Next); ok {
		next.stations = nil
		out.Next = put(next)
	}
	if now != before {
		if now.Asked.Asks == stationAsks && before.Asked.Asks < stationAsks {
			st.noteDiag(EventStationGivenUp, fmt.Sprintf("%s would not go down in %d asks", now.Asked.Item, stationAsks),
				map[string]any{"item": now.Asked.Item, "cell": now.Asked.Cell[:]})
		}
		out.Memory = append(out.Memory, stationsRemembered(now))
	}
	return out
}
