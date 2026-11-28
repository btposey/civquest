package system1

// cq-qdj: a danger shapes where an agent goes, not what it can use from where it stands; and no
// activity sets a station down without end.

import (
	"math"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting/craftingtest"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// holding is a frame in which the agent carries a table and the timber to make more.
func holding(tick int64, blocks ...*civquestv1.VisibleBlock) *civquestv1.TelemetryFrame {
	f := rf(tick, 4000, withBlocks(blocks...))
	f.Status.Inventory = []*civquestv1.ItemStackProto{
		item(0, craftingTableItem, 1), item(1, "minecraft:oak_log", 12), item(2, "minecraft:oak_planks", 12), item(3, "minecraft:stick", 8),
	}
	return f
}

func tableState() *AgentState {
	st := NewAgentState()
	st.SetRecipeGraph(newTestRecipeGraph())
	st.mem.telemetryFramesSeen = 5
	return st
}

// asksWhileHeld drives one placer for far longer than a placement takes, with the table never
// leaving the pack, and returns what it asked to set down and whether it made another table.
func asksWhileHeld(st *AgentState, ask func(frame *civquestv1.TelemetryFrame, walk *walkLocal) *civquestv1.AgentIntent) (places, crafts int) {
	walk := walkLocal{}
	for tick := int64(100); tick < 100+stationAskGap*100; tick += stationAskGap {
		box := st.mem.stations
		walk.stations = &box
		in := ask(holding(tick), &walk)
		st.rememberStations(box)
		if in.GetInteractBlock().GetBlockOrItemId() == craftingTableItem {
			places++
		}
		if in.GetCraftItem().GetRecipeId() == craftingTableItem {
			crafts++
		}
	}
	return places, crafts
}

func TestStations_NoPlacerAsksWithoutEnd(t *testing.T) {
	for name, ask := range map[string]func(st *AgentState) func(*civquestv1.TelemetryFrame, *walkLocal) *civquestv1.AgentIntent{
		"bootstrap": func(st *AgentState) func(*civquestv1.TelemetryFrame, *walkLocal) *civquestv1.AgentIntent {
			return func(f *civquestv1.TelemetryFrame, w *walkLocal) *civquestv1.AgentIntent {
				return EvaluateBootstrapGoalsDirect("a", f, f.Status.Position, f.Status.NavigationState, st, w)
			}
		},
		"bed": func(st *AgentState) func(*civquestv1.TelemetryFrame, *walkLocal) *civquestv1.AgentIntent {
			st.SetRecipeGraph(crafting.NewRecipeGraph(craftingtest.Nodes())) // it has beds
			return func(f *civquestv1.TelemetryFrame, w *walkLocal) *civquestv1.AgentIntent {
				f.Status.Inventory = append(f.Status.Inventory, item(4, "minecraft:white_wool", 3))
				bl := bedLocal{Walk: *w}
				in, _ := bedStep(&FSMContext{AgentID: "a", Frame: f, State: st}, &bl)
				*w = bl.Walk
				return in
			}
		},
		"armory": func(st *AgentState) func(*civquestv1.TelemetryFrame, *walkLocal) *civquestv1.AgentIntent {
			return func(f *civquestv1.TelemetryFrame, w *walkLocal) *civquestv1.AgentIntent {
				f.Status.Inventory = append(f.Status.Inventory, item(4, "minecraft:iron_ingot", 12))
				return EvaluateArmoryMaintenance("a", f, nil, st, nil, w)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			st := tableState()
			places, crafts := asksWhileHeld(st, ask(st))
			if places != stationAsks-1 || crafts != 0 {
				t.Fatalf("asked to set the table down %d times (want %d, then no more) and made %d tables", places, stationAsks-1, crafts)
			}
		})
	}
}

func TestStations_OneSetDownIsEnoughNearby(t *testing.T) {
	st := tableState()
	empty := func(tick int64, x float64) *civquestv1.TelemetryFrame {
		f := rf(tick, 4000, withPos(x, 64, 0.5))
		return f
	}
	st.rememberStations(stationMemory{Asked: stationAsk{Item: craftingTableItem, Cell: blockCell{1, 64, 0}, Held: 1, Asks: 1, Tick: 100}})

	// The pack no longer holds it: it is down, in view or not.
	if !st.stationRefusedLocked(craftingTableItem, empty(120, 0.5)) {
		t.Fatal("another table wanted beside the one just set down")
	}
	st.rememberStations(st.stationsLocked(empty(120, 0.5)))
	if !st.stationRefusedLocked(craftingTableItem, empty(140, 8.5)) {
		t.Fatal("another table wanted a few steps from the one set down")
	}
	if st.stationRefusedLocked(craftingTableItem, empty(160, 1+stationSpareRadius+2)) {
		t.Fatal("no table may be made far from the one set down")
	}
	gone := empty(180, 0.5)
	gone.InterestingBlocks = []*civquestv1.VisibleBlock{blk(1, 64, 0, "minecraft:dirt")}
	if st.stationRefusedLocked(craftingTableItem, gone) {
		t.Fatal("the table is gone, yet no other may be made")
	}
	st.MarkWorkstationUnreachable(1, 64, 0, 200)
	if st.stationRefusedLocked(craftingTableItem, empty(200, 8.5)) {
		t.Fatal("a table no walk reaches must not stand in the way of another")
	}
}

func TestStations_GivenUpOnlyAboutTheSpot(t *testing.T) {
	st := tableState()
	st.rememberStations(stationMemory{Asked: stationAsk{Item: craftingTableItem, Stand: [3]float64{0.5, 64, 0.5}, Held: 1, Asks: stationAsks, Tick: 100}})
	if !st.stationRefusedLocked(craftingTableItem, holding(200)) {
		t.Fatal("asked again where it was given up")
	}
	away := holding(200)
	away.Status.Position.X = 0.5 + placeRelocateDist + 1
	if in, refused := st.placeStationLocked("a", craftingTableItem, away, &walkLocal{}); refused || in == nil {
		t.Fatalf("somewhere else it must be tried again, got %v refused=%t", in, refused)
	}
}

func TestCraftingTable_DangerHidesOnlyWhatMustBeWalkedTo(t *testing.T) {
	st := tableState()
	st.NoteDanger(Danger{Kind: "drowned", X: -8.5, Y: 63, Z: 0.5, Seen: 90})
	near, far := blk(2, 64, 0, craftingTableItem), blk(7, 64, 0, craftingTableItem)
	frame := rf(100, 4000, withBlocks(far, near))
	if got, _, d := findNearestCraftingTableLocked(frame, frame.Status.Position, st); got != near || !isCraftingTableWithinReach(got, d, frame.Status.Position) {
		t.Fatalf("the table in reach must be used, got %v", got)
	}
	frame = rf(100, 4000, withBlocks(far))
	if got, _, _ := findNearestCraftingTableLocked(frame, frame.Status.Position, st); got != nil {
		t.Fatalf("a table by a danger must not be walked to, got %v", got)
	}

	// Remembered tables: the nearest is by the danger, the next one is taken.
	si := st.SpatialIndex()
	for _, x := range []float64{-9.5, 30.5} {
		si.UpsertPOI(db.AgentPOI{
			POIID: FormatPOIID(POICategoryWorkstation, int32(math.Floor(x)), 64, 0), Category: POICategoryWorkstation, BlockID: craftingTableItem,
			X: x, Y: 64, Z: 0.5, AccessX: x, AccessY: 64, AccessZ: 1.5, ElevationLayer: ElevationSurface, Reliability: 1, IsActive: true,
		})
	}
	frame = rf(100, 4000)
	if _, poi, _ := findNearestCraftingTableLocked(frame, frame.Status.Position, st); poi == nil || poi.X != 30.5 {
		t.Fatalf("want the remembered table away from the danger, got %+v", poi)
	}
}

func TestBed_DangerHidesOnlyWhatMustBeWalkedTo(t *testing.T) {
	st := NewAgentState()
	st.NoteDanger(Danger{Kind: "drowned", X: -8.5, Y: 63, Z: 0.5, Seen: 90})
	at := &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}
	beside, off := &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, &civquestv1.BlockPosProto{X: 7, Y: 64, Z: 0}
	if st.useRefused(at, beside, bedUseReach, 100) || !st.useRefused(at, off, bedUseReach, 100) {
		t.Fatal("a danger must hide the bed to be walked to, and not the one in reach")
	}
	st.SetAssignedBed(beside)
	EvaluateSleep("a", rf(100, 14000, withBlocks(blk(2, 64, 0, "minecraft:red_bed"))), st)
	if st.GetAssignedBed() == nil {
		t.Fatal("the bed beside the agent was abandoned for a danger")
	}
	st.MarkTargetUnreachable(2.5, 64, 0.5, 100, DefaultUnreachableTargetTTL)
	if !st.useRefused(at, beside, bedUseReach, 101) {
		t.Fatal("a bed given up stays given up, in reach or not")
	}
}
