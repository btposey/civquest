package blueprints

import (
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Cabin block state constants.
const (
	BlockOakPlanks  = "minecraft:oak_planks"
	BlockOakLogAxis = "minecraft:oak_log[axis=y]"
	BlockOakDoorLow = "minecraft:oak_door[facing=south,half=lower]"
	BlockOakSlabBtm = "minecraft:oak_slab[type=bottom]"
	BlockWallTorchS = "minecraft:wall_torch[facing=south]"

	CabinTotalBlocks = 82
)

// Phase identifier constants for Frontier Cabin construction.
const (
	CabinPhaseFloor        = 1
	CabinPhaseCornerPillar = 2
	CabinPhaseWallFaces    = 3
	CabinPhaseDoorway      = 4
	CabinPhaseRoof         = 5
	CabinPhaseTorch        = 6
)

// BlueprintSpec defines a declarative architectural blueprint schema suitable for YAML serialization.
type BlueprintSpec struct {
	Name       string           `yaml:"name" json:"name"`
	Version    int              `yaml:"version" json:"version"`
	Type       string           `yaml:"type" json:"type"`
	Style      string           `yaml:"style" json:"style"`
	Dimensions Dimensions       `yaml:"dimensions" json:"dimensions"`
	Phases     []string         `yaml:"phases,omitempty" json:"phases,omitempty"`
	Blocks     []BlockPlacement `yaml:"blocks" json:"blocks"`
}

// CabinBlockIntent describes a single placed block with its logical construction phase.
type CabinBlockIntent struct {
	Phase int
	X     int32
	Y     int32
	Z     int32
	State string
}

// rawCabinBlocks returns the complete set of block placements for the 5x5 Frontier Cabin,
// strictly ordered bottom-up (Y ascending: Y=0 floor before Y=1..3 walls/doors before Y=4 roof).
func rawCabinBlocks() []CabinBlockIntent {
	var blocks []CabinBlockIntent

	// Phase 1: 5x5 floor of oak_planks at Y=0 (25 blocks: X in [-2, 2], Z in [-2, 2])
	for z := int32(-2); z <= 2; z++ {
		for x := int32(-2); x <= 2; x++ {
			blocks = append(blocks, CabinBlockIntent{
				Phase: CabinPhaseFloor,
				X:     x,
				Y:     0,
				Z:     z,
				State: BlockOakPlanks,
			})
		}
	}

	// Y = 1: Corners (Phase 2), Wall faces (Phase 3), Doorway lower (Phase 4)
	corners := [][2]int32{
		{-2, -2}, {2, -2}, {-2, 2}, {2, 2},
	}
	for _, c := range corners {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseCornerPillar,
			X:     c[0],
			Y:     1,
			Z:     c[1],
			State: BlockOakLogAxis,
		})
	}
	// North wall Y=1 (Z=-2, X in [-1, 1])
	for x := int32(-1); x <= 1; x++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     x,
			Y:     1,
			Z:     -2,
			State: BlockOakPlanks,
		})
	}
	// South wall Y=1: doorway at X=0; wall planks at X=-1, 1
	blocks = append(blocks, CabinBlockIntent{
		Phase: CabinPhaseWallFaces,
		X:     -1,
		Y:     1,
		Z:     2,
		State: BlockOakPlanks,
	})
	blocks = append(blocks, CabinBlockIntent{
		Phase: CabinPhaseDoorway,
		X:     0,
		Y:     1,
		Z:     2,
		State: BlockOakDoorLow,
	})
	blocks = append(blocks, CabinBlockIntent{
		Phase: CabinPhaseWallFaces,
		X:     1,
		Y:     1,
		Z:     2,
		State: BlockOakPlanks,
	})
	// West wall Y=1 (X=-2, Z in [-1, 1])
	for z := int32(-1); z <= 1; z++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     -2,
			Y:     1,
			Z:     z,
			State: BlockOakPlanks,
		})
	}
	// East wall Y=1 (X=2, Z in [-1, 1])
	for z := int32(-1); z <= 1; z++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     2,
			Y:     1,
			Z:     z,
			State: BlockOakPlanks,
		})
	}

	// Y = 2: Corners (Phase 2), Wall faces (Phase 3), Torch (Phase 6). The door placed at Y=1
	// fills (0, 2, 2) too: one item hangs both halves.
	for _, c := range corners {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseCornerPillar,
			X:     c[0],
			Y:     2,
			Z:     c[1],
			State: BlockOakLogAxis,
		})
	}
	// North wall Y=2
	for x := int32(-1); x <= 1; x++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     x,
			Y:     2,
			Z:     -2,
			State: BlockOakPlanks,
		})
	}
	// South wall Y=2
	blocks = append(blocks, CabinBlockIntent{
		Phase: CabinPhaseWallFaces,
		X:     -1,
		Y:     2,
		Z:     2,
		State: BlockOakPlanks,
	})
	blocks = append(blocks, CabinBlockIntent{
		Phase: CabinPhaseWallFaces,
		X:     1,
		Y:     2,
		Z:     2,
		State: BlockOakPlanks,
	})
	// West wall Y=2
	for z := int32(-1); z <= 1; z++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     -2,
			Y:     2,
			Z:     z,
			State: BlockOakPlanks,
		})
	}
	// East wall Y=2
	for z := int32(-1); z <= 1; z++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     2,
			Y:     2,
			Z:     z,
			State: BlockOakPlanks,
		})
	}
	// Phase 6: Torch placed inside on north wall at (X=0, Y=2, Z=-1)
	blocks = append(blocks, CabinBlockIntent{
		Phase: CabinPhaseTorch,
		X:     0,
		Y:     2,
		Z:     -1,
		State: BlockWallTorchS,
	})

	// Y = 3: Corners (Phase 2), Wall faces (Phase 3, including lintel above doorway at X=0, Z=2)
	for _, c := range corners {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseCornerPillar,
			X:     c[0],
			Y:     3,
			Z:     c[1],
			State: BlockOakLogAxis,
		})
	}
	// North wall Y=3
	for x := int32(-1); x <= 1; x++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     x,
			Y:     3,
			Z:     -2,
			State: BlockOakPlanks,
		})
	}
	// South wall Y=3 (includes lintel above door at X=0)
	for x := int32(-1); x <= 1; x++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     x,
			Y:     3,
			Z:     2,
			State: BlockOakPlanks,
		})
	}
	// West wall Y=3
	for z := int32(-1); z <= 1; z++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     -2,
			Y:     3,
			Z:     z,
			State: BlockOakPlanks,
		})
	}
	// East wall Y=3
	for z := int32(-1); z <= 1; z++ {
		blocks = append(blocks, CabinBlockIntent{
			Phase: CabinPhaseWallFaces,
			X:     2,
			Y:     3,
			Z:     z,
			State: BlockOakPlanks,
		})
	}

	// Y = 4: Phase 5: Roof over 3x3 interior at Y=4 with oak_slab[type=bottom] (9 slabs)
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			blocks = append(blocks, CabinBlockIntent{
				Phase: CabinPhaseRoof,
				X:     x,
				Y:     4,
				Z:     z,
				State: BlockOakSlabBtm,
			})
		}
	}

	return blocks
}

// FrontierCabinBlueprint returns the complete slice of PlaceBlockIntents forming a 5x5 exterior /
// 3x3 interior Frontier Cabin centered at (0, 0, 0) in strict bottom-up order (Y ascending: floor before walls before roof).
func FrontierCabinBlueprint() []*civquestv1.PlaceBlockIntent {
	raw := rawCabinBlocks()
	intents := make([]*civquestv1.PlaceBlockIntent, len(raw))
	for i, b := range raw {
		intents[i] = &civquestv1.PlaceBlockIntent{
			TargetVoxel: &civquestv1.BlockPosProto{
				X: b.X,
				Y: b.Y,
				Z: b.Z,
			},
			BlockStateString: b.State,
		}
	}
	return intents
}

// FrontierCabinIntents returns the Frontier Cabin PlaceBlockIntents translated by the specified center coordinate.
func FrontierCabinIntents(center *civquestv1.Vec3) []*civquestv1.PlaceBlockIntent {
	base := FrontierCabinBlueprint()
	if center == nil {
		return base
	}
	cx := int32(center.X)
	cy := int32(center.Y)
	cz := int32(center.Z)

	for _, intent := range base {
		if intent.TargetVoxel != nil {
			intent.TargetVoxel.X += cx
			intent.TargetVoxel.Y += cy
			intent.TargetVoxel.Z += cz
		}
	}
	return base
}

// FrontierCabinBlueprintByPhase returns the cabin block placements grouped strictly by phases 1 through 6:
// Phase 1: 5x5 Floor (Y=0)
// Phase 2: 4 Corner Pillars (Y=1..3)
// Phase 3: Perimeter Wall Faces (Y=1..3)
// Phase 4: Doorway (Y=1..2)
// Phase 5: Roof (Y=4)
// Phase 6: Inside Torch (Y=2)
func FrontierCabinBlueprintByPhase() []*civquestv1.PlaceBlockIntent {
	raw := rawCabinBlocks()
	var sorted []CabinBlockIntent
	for phase := 1; phase <= 6; phase++ {
		for _, b := range raw {
			if b.Phase == phase {
				sorted = append(sorted, b)
			}
		}
	}

	intents := make([]*civquestv1.PlaceBlockIntent, len(sorted))
	for i, b := range sorted {
		intents[i] = &civquestv1.PlaceBlockIntent{
			TargetVoxel: &civquestv1.BlockPosProto{
				X: b.X,
				Y: b.Y,
				Z: b.Z,
			},
			BlockStateString: b.State,
		}
	}
	return intents
}

// CabinBlockPhase returns the construction phase (1-6) corresponding to a block intent at the given index.
func CabinBlockPhase(index int) int {
	raw := rawCabinBlocks()
	if index < 0 || index >= len(raw) {
		return CabinPhaseTorch
	}
	return raw[index].Phase
}

// FrontierCabinSpec returns a reusable BlueprintSpec struct suitable for serialization or YAML storage.
func FrontierCabinSpec() *BlueprintSpec {
	raw := rawCabinBlocks()
	blocks := make([]BlockPlacement, len(raw))
	for i, b := range raw {
		blocks[i] = BlockPlacement{
			X:     int(b.X + 2), // normalized to [0, 4]
			Y:     int(b.Y),     // [0, 4]
			Z:     int(b.Z + 2), // normalized to [0, 4]
			State: b.State,
		}
	}

	return &BlueprintSpec{
		Name:    "Frontier Cabin",
		Version: 1,
		Type:    "cabin",
		Style:   "frontier",
		Dimensions: Dimensions{
			Width:  5,
			Height: 5,
			Length: 5,
		},
		Phases: []string{
			"1: Foundation Floor (oak_planks)",
			"2: Corner Pillars (oak_log)",
			"3: Wall Faces (oak_planks)",
			"4: Doorway (oak_door)",
			"5: Roof Slabs (oak_slab)",
			"6: Interior Lighting (torch)",
		},
		Blocks: blocks,
	}
}

// IsCollidingWithVoxel reports whether the agent's position is within 1.0 block horizontally
// and vertically of targetVoxel, indicating that placing a block at targetVoxel would collide
// with or entomb the agent.
func IsCollidingWithVoxel(pos *civquestv1.Vec3, targetVoxel *civquestv1.BlockPosProto) bool {
	if pos == nil || targetVoxel == nil {
		return false
	}
	targetCenterX := float64(targetVoxel.X) + 0.5
	targetCenterZ := float64(targetVoxel.Z) + 0.5
	hDist := math.Hypot(pos.X-targetCenterX, pos.Z-targetCenterZ)
	vDist := math.Abs(pos.Y - float64(targetVoxel.Y))
	return hDist <= 1.0 && vDist <= 1.0
}

// CabinStepBackMinDist is the shortest step back worth sending: past the arrival tolerance of 1
// with room to clear the voxel.
const CabinStepBackMinDist = 2.5

// CalculateCabinStepBack computes a micro-step waypoint positioned outside the 5x5 construction boundary
// when the agent is within collision range of targetVoxel during shelter construction.
func CalculateCabinStepBack(pos *civquestv1.Vec3, targetVoxel *civquestv1.BlockPosProto, shelterCenter *civquestv1.BlockPosProto) *civquestv1.Vec3 {
	if pos == nil {
		return &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
	}
	cx := float64(0)
	cy := pos.Y
	cz := float64(0)
	if shelterCenter != nil {
		cx = float64(shelterCenter.X) + 0.5
		cy = float64(shelterCenter.Y)
		cz = float64(shelterCenter.Z) + 0.5
	}

	targetCenterX := cx
	targetCenterZ := cz
	if targetVoxel != nil {
		targetCenterX = float64(targetVoxel.X) + 0.5
		targetCenterZ = float64(targetVoxel.Z) + 0.5
	}

	// Direction away from the target voxel
	dirX := pos.X - targetCenterX
	dirZ := pos.Z - targetCenterZ
	if math.Hypot(dirX, dirZ) < 0.05 {
		dirX = pos.X - cx
		dirZ = pos.Z - cz
	}
	if math.Hypot(dirX, dirZ) < 0.05 {
		// Default to South (towards doorway at Z=+2)
		dirX = 0
		dirZ = 1
	}

	d := math.Hypot(dirX, dirZ)
	normX := dirX / d
	normZ := dirZ / d

	// Step back outside the 5x5 construction boundary (half-width 2.5, place at 3.5)
	const boundaryRadius = 3.5
	x, z := cx+normX*boundaryRadius, cz+normZ*boundaryRadius
	// A move within the navigator's arrival tolerance never happens (cq-6l3.1).
	if math.Hypot(x-pos.X, z-pos.Z) < CabinStepBackMinDist {
		x, z = pos.X+normX*CabinStepBackMinDist, pos.Z+normZ*CabinStepBackMinDist
	}
	return &civquestv1.Vec3{
		X: math.Round(x*10) / 10,
		Y: cy,
		Z: math.Round(z*10) / 10,
	}
}
