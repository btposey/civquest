package blueprints

// A cabin's fittings (cq-390.8): the door and the windows. They go in once the structure
// stands, filled from whatever is to hand, and one that cannot be had never holds the shelter
// up.

import "strings"

// Window states as a blueprint draws them: facing is the way into the room.
const (
	BlockWindowWest = "minecraft:glass_pane[facing=east]"
	BlockWindowEast = "minecraft:glass_pane[facing=west]"
)

// IsWindow reports a blueprint state that stands for a window opening.
func IsWindow(state string) bool { return NormalizeItemID(state) == "minecraft:glass_pane" }

// IsFitting reports a blueprint state that stands for a door or a window.
func IsFitting(state string) bool {
	return IsWindow(state) || strings.HasSuffix(NormalizeItemID(state), "_door")
}

// CabinFittingAt returns the blueprint state of the fitting at a cabin block index, or "".
func CabinFittingAt(index int) string {
	raw := rawCabinBlocks()
	if index < 0 || index >= len(raw) || !IsFitting(raw[index].State) {
		return ""
	}
	return raw[index].State
}

// FittingFill returns what to place for a fitting from the held item IDs. A door takes any
// wooden door. A window takes a glass pane, else a wooden trapdoor stood upright against the
// outer face of the opening, else a wooden fence. ok is false when nothing held will do.
func FittingFill(state string, held []string) (placed string, ok bool) {
	if !IsWindow(state) {
		return WoodSubstitute(state, held)
	}
	for _, h := range held {
		if h == "minecraft:glass_pane" || strings.HasSuffix(h, "_glass_pane") {
			return h, true
		}
	}
	for _, h := range held {
		if isWooden(h, "trapdoor") {
			facing := "north"
			if i := strings.Index(state, "facing="); i >= 0 {
				facing = strings.TrimSuffix(state[i+len("facing="):], "]")
			}
			return h + "[facing=" + facing + ",half=bottom,open=true]", true
		}
	}
	for _, h := range held {
		if isWooden(h, "fence") {
			return h, true
		}
	}
	return "", false
}

// FittingCrafts lists the items worth crafting for a fitting, best first. No agent can make
// glass, so a window is filled with a trapdoor or a fence.
func FittingCrafts(state string) []string {
	shapes := []string{"door"}
	if IsWindow(state) {
		shapes = []string{"trapdoor", "fence"}
	}
	var out []string
	for _, shape := range shapes {
		for _, w := range woodKinds {
			out = append(out, "minecraft:"+w+"_"+shape)
		}
	}
	return out
}

// Opening is a doorway or window of the cabin template: the state it should hold and the cells
// it spans, bottom first, as offsets from the cabin's centre.
type Opening struct {
	State string
	Cells [][3]int32
}

// CabinOpenings lists the template's openings, the doorway first.
func CabinOpenings() []Opening {
	var out []Opening
	for _, b := range rawCabinBlocks() {
		if !IsFitting(b.State) {
			continue
		}
		o := Opening{State: b.State, Cells: [][3]int32{{b.X, b.Y, b.Z}}}
		if !IsWindow(b.State) {
			o.Cells = append(o.Cells, [3]int32{b.X, b.Y + 1, b.Z}) // a door is two blocks tall
		}
		out = append(out, o)
	}
	return out
}

// FillsOpening reports whether blockID is a fitting for an opening drawn as state.
func FillsOpening(state, blockID string) bool {
	id := NormalizeItemID(blockID)
	if !IsWindow(state) {
		return strings.HasSuffix(id, "_door")
	}
	return strings.HasSuffix(id, "glass_pane") || strings.HasSuffix(id, "_trapdoor") || strings.HasSuffix(id, "_fence")
}
