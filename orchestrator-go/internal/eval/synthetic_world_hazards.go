package eval

// Scenario-world builders for the playtest-2 fix scenarios (cq-gpa.13).
//
// All builders use the same convention as NewSyntheticStartingWorld: the top ground layer is
// Y=63 (so an agent stands with feet at Y=64) and blocks are keyed by integer cell.

import (
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// NewBlankWorld returns an empty synthetic world (no village, no terrain proving grounds).
func NewBlankWorld() *SyntheticStartingWorld {
	return &SyntheticStartingWorld{
		Blocks:       make(map[BlockPosKey]string, 2048),
		BlockStates:  make(map[BlockPosKey]string, 16),
		AssignedBeds: map[string]*civquestv1.BlockPosProto{},
		Workstations: map[string]*civquestv1.BlockPosProto{},
	}
}

// perimeterHeight is the height of the wall around filled ground: too tall to step or jump.
const perimeterHeight = 4

// FillGround lays a flat ground layer (top surface Y=63) over [x0..x1] x [z0..z1] and walls it
// in just outside that area, so agents cannot reach the drop at the edge of the world.
func (w *SyntheticStartingWorld) FillGround(x0, z0, x1, z1 int32, blockID string) {
	if blockID == "" {
		blockID = "minecraft:grass_block"
	}
	for x := x0; x <= x1; x++ {
		for z := z0; z <= z1; z++ {
			w.Blocks[BlockPosKey{X: x, Y: 63, Z: z}] = blockID
		}
	}
	for x := x0 - 1; x <= x1+1; x++ {
		for z := z0 - 1; z <= z1+1; z++ {
			if x >= x0 && x <= x1 && z >= z0 && z <= z1 {
				continue
			}
			for y := int32(63); y <= 63+perimeterHeight; y++ {
				w.Blocks[BlockPosKey{X: x, Y: y, Z: z}] = "minecraft:bedrock"
			}
		}
	}
}

// AddPond carves a circular pond of the given radius centred on (cx, cz): sand bed at Y=62 and
// water at Y=63..65 (3 deep, so an agent standing on the bed has its eyes submerged). The
// surrounding ground (Y=63) is left intact as the dry shore.
func (w *SyntheticStartingWorld) AddPond(cx, cz int32, radius float64) {
	r := int32(math.Ceil(radius))
	for x := cx - r; x <= cx+r; x++ {
		for z := cz - r; z <= cz+r; z++ {
			if math.Hypot(float64(x-cx), float64(z-cz)) > radius {
				continue
			}
			w.Blocks[BlockPosKey{X: x, Y: 62, Z: z}] = "minecraft:sand"
			for y := int32(63); y <= 65; y++ {
				w.Blocks[BlockPosKey{X: x, Y: y, Z: z}] = "minecraft:water"
			}
		}
	}
}

// AddLitCampfire places a lit campfire occupying cell (x, 64, z).
func (w *SyntheticStartingWorld) AddLitCampfire(x, z int32) {
	k := BlockPosKey{X: x, Y: 64, Z: z}
	w.Blocks[k] = "minecraft:campfire"
	w.BlockStates[k] = "lit=true"
}

// AddTrunk places a vertical log column of the given height from Y=64 (a 9-log trunk is the
// playtest-2 case: reach covers y+0..y+6 so the top logs must be left standing).
func (w *SyntheticStartingWorld) AddTrunk(x, z, height int32, logID string) {
	if logID == "" {
		logID = "minecraft:oak_log"
	}
	for y := int32(64); y < 64+height; y++ {
		w.Blocks[BlockPosKey{X: x, Y: y, Z: z}] = logID
	}
}

// AddStoneOutcrop places a solid size x size x height block of stone starting at (x, 64, z).
func (w *SyntheticStartingWorld) AddStoneOutcrop(x, z, size, height int32) {
	for dx := int32(0); dx < size; dx++ {
		for dz := int32(0); dz < size; dz++ {
			for y := int32(64); y < 64+height; y++ {
				w.Blocks[BlockPosKey{X: x + dx, Y: y, Z: z + dz}] = "minecraft:stone"
			}
		}
	}
}
