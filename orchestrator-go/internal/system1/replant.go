package system1

import (
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Regenerative harvest (bead cq-3a0.2): wood is a renewable crop. After a tree is felled the
// harvest task replants a matching sapling at the trunk base, unless the site is being cleared
// for structures. Everything here is pure and frame-local; no AgentState fields are added.

// clearingMargin is the horizontal margin (blocks) kept clear around the 5x5 cabin footprint.
const clearingMargin = 1

// ClearingSite is a rectangular horizontal region reserved for construction.
type ClearingSite struct {
	Center *civquestv1.BlockPosProto
	Radius int32 // inclusive horizontal half-extent, margin included
}

// ClearingContext is the read-only input of IsClearingSite.
type ClearingContext struct {
	Sites           []ClearingSite // shelter/cabin footprint, blueprint and studio sites
	DirectiveAction string         // active strategic directive action ("" when none)
}

// ClearingContextFor derives the clearing context from the agent state: the cabin footprint
// around ShelterPos and the active strategic directive.
func ClearingContextFor(state *AgentState, worldTick int64) ClearingContext {
	var cc ClearingContext
	if state == nil {
		return cc
	}
	if sp := state.GetShelterPos(); sp != nil {
		cc.Sites = append(cc.Sites, ClearingSite{Center: sp, Radius: 2 + clearingMargin})
	}
	if dir, ok := state.ActiveStrategicDirective(worldTick); ok {
		cc.DirectiveAction = dir.Action
	}
	return cc
}

// IsClearingSite reports whether the cell lies where the plan is to clear land for structures:
// any BUILD_* directive is active (the whole work area is being cleared), or the cell is inside
// a reserved shelter/blueprint/studio footprint.
func IsClearingSite(cell *civquestv1.BlockPosProto, ctx ClearingContext) bool {
	if strings.HasPrefix(strings.ToUpper(ctx.DirectiveAction), "BUILD_") {
		return true
	}
	if cell == nil {
		return false
	}
	for _, s := range ctx.Sites {
		if s.Center == nil {
			continue
		}
		dx, dz := cell.X-s.Center.X, cell.Z-s.Center.Z
		if dx < 0 {
			dx = -dx
		}
		if dz < 0 {
			dz = -dz
		}
		if dx <= s.Radius && dz <= s.Radius {
			return true
		}
	}
	return false
}

// saplingForFamily maps a log family ("oak", "dark_oak", "mangrove", ...) to its sapling id.
func saplingForFamily(family string) string {
	switch family {
	case "":
		return ""
	case "mangrove":
		return "mangrove_propagule"
	case "oak", "spruce", "birch", "jungle", "acacia", "dark_oak", "cherry":
		return family + "_sapling"
	}
	return ""
}

// pickReplantSapling chooses the sapling to plant: the matching species when the log family is
// a known tree; any sapling when the family is unknown. "" when none qualifies.
func pickReplantSapling(frame *civquestv1.TelemetryFrame, family string) string {
	if frame == nil || frame.Status == nil {
		return ""
	}
	want := saplingForFamily(family)
	for _, it := range frame.Status.Inventory {
		if it == nil || it.Count <= 0 || !isSaplingItemID(it.ItemId) {
			continue
		}
		id := strings.ToLower(it.ItemId)
		if idx := strings.LastIndex(id, ":"); idx >= 0 {
			id = id[idx+1:]
		}
		if want == "" || id == want {
			return it.ItemId
		}
	}
	return ""
}

// replantSaplingFor returns the sapling id to plant at the trunk base (tx,ty,tz), or "" when
// replanting is not wanted: no (matching) sapling, invalid soil/space, or a clearing site.
func replantSaplingFor(state *AgentState, frame *civquestv1.TelemetryFrame, tx, ty, tz int32, family string) string {
	id := pickReplantSapling(frame, family)
	if id == "" || !canPlantSaplingAt(frame, tx, ty, tz) {
		return ""
	}
	var tick int64
	if frame != nil {
		tick = frame.WorldTick
	}
	if IsClearingSite(&civquestv1.BlockPosProto{X: tx, Y: ty, Z: tz}, ClearingContextFor(state, tick)) {
		return ""
	}
	return id
}
