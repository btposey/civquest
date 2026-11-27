package blueprints

// Any wood will do (user, play test 7): a blueprint names oak blocks, and a builder places
// whatever wood of the same shape it holds. A roof with no wooden slab to hand is laid in
// planks.

import "strings"

var woodKinds = []string{"oak", "spruce", "birch", "jungle", "acacia", "dark_oak", "mangrove",
	"cherry", "bamboo", "crimson", "warped", "pale_oak"}

// woodShapes are the blueprint shapes any wood can fill, with the shapes that may stand in.
var woodShapes = []struct {
	shape     string
	standIns  []string
	keepProps bool
}{
	{"slab", []string{"slab", "planks"}, true},
	{"planks", []string{"planks"}, false},
	{"log", []string{"log", "planks"}, true},
	{"door", []string{"door"}, true},
}

func isWooden(item, shape string) bool {
	name := strings.TrimPrefix(item, "minecraft:")
	name = strings.TrimPrefix(name, "stripped_")
	for _, w := range woodKinds {
		if name == w+"_"+shape || (shape == "log" && name == w+"_stem") {
			return true
		}
	}
	return false
}

// WoodSubstitute returns the block state to place for a blueprint's state given the held item
// IDs: the state itself if its item is held, else the same shape in another wood, else a
// stand-in shape. ok is false when the state is a wooden one and nothing held can fill it;
// states that are not wooden are returned unchanged.
func WoodSubstitute(state string, held []string) (placed string, ok bool) {
	base, props := state, ""
	if i := strings.IndexByte(state, '['); i >= 0 {
		base, props = state[:i], state[i:]
	}
	for _, s := range woodShapes {
		if !isWooden(base, s.shape) {
			continue
		}
		for _, h := range held {
			if h == base {
				return state, true
			}
		}
		for _, shape := range s.standIns {
			for _, h := range held {
				if isWooden(h, shape) {
					if shape == s.shape && s.keepProps {
						return h + props, true
					}
					return h, true
				}
			}
		}
		return "", false
	}
	return state, true
}
