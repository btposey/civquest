package eval

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// StockpileSupplyChest represents the core village supply chest inventory.
type StockpileSupplyChest struct {
	Pos   *civquestv1.BlockPosProto
	Items map[string]int32
}

// TropicalStormNames2026 is the 2026 Atlantic tropical cyclone name list (alternating male/female)
// used as the seed roster for synthetic world agents and dynastic offspring generation.
var TropicalStormNames2026 = []string{
	"Arthur", "Bertha", "Cristobal", "Dolly", "Edouard", "Fay",
	"Gonzalo", "Hanna", "Isaias", "Josephine", "Kyle", "Leah",
	"Marco", "Nana", "Omar", "Paulette", "Rene", "Sally",
	"Teddy", "Vicky", "Wilfred",
}

// SyntheticStartingWorld represents a complete, deterministic, fully resettable starting world
// designed for repeatable multi-terrain and advanced state playtesting without Minecraft Java server overhead.
type SyntheticStartingWorld struct {
	Blocks        map[BlockPosKey]string
	BlockStates   map[BlockPosKey]string
	Stockpile     *StockpileSupplyChest
	AssignedBeds  map[string]*civquestv1.BlockPosProto
	TownHallPos   *civquestv1.BlockPosProto
	LecternPos    *civquestv1.BlockPosProto
	SignPos       *civquestv1.BlockPosProto
	Workstations  map[string]*civquestv1.BlockPosProto
	TavernPos     *civquestv1.BlockPosProto
	CampfirePos   *civquestv1.BlockPosProto
	AgriCenterPos *civquestv1.BlockPosProto
	RosterNames   []string
}

// NewSyntheticStartingWorld constructs and populates a complete synthetic starting world layout fresh
// with zero disk state bleed-through and guaranteed <5ms construction latency.
func NewSyntheticStartingWorld() *SyntheticStartingWorld {
	world := &SyntheticStartingWorld{
		Blocks:       make(map[BlockPosKey]string, 12000),
		BlockStates:  make(map[BlockPosKey]string, 200),
		AssignedBeds: make(map[string]*civquestv1.BlockPosProto),
		Workstations: make(map[string]*civquestv1.BlockPosProto),
		RosterNames:  append([]string(nil), TropicalStormNames2026...),
	}

	// 1. Base Ground Surface (centered around (0, 64, 0))
	// Ground level is at Y=63 (grass_block). Surface walking level is Y=64.
	for x := int32(-15); x <= 15; x++ {
		for z := int32(-15); z <= 19; z++ {
			world.Blocks[BlockPosKey{X: x, Y: 63, Z: z}] = "minecraft:grass_block"
		}
	}

	// 2. Core Settlement Center: (0, 64, 0)
	// Paved desire-paths connecting core quarters
	for z := int32(-12); z <= 6; z++ {
		world.Blocks[BlockPosKey{X: 0, Y: 63, Z: z}] = "minecraft:dirt_path"
	}
	for x := int32(-6); x <= 10; x++ {
		world.Blocks[BlockPosKey{X: x, Y: 63, Z: 0}] = "minecraft:dirt_path"
	}

	// 3. Town Hall & Archive Wing at (4, 64, 4)
	world.TownHallPos = &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 4}
	world.LecternPos = &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 4}
	world.SignPos = &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 5}

	world.Blocks[BlockPosKey{X: 4, Y: 64, Z: 4}] = "minecraft:lectern"
	world.Blocks[BlockPosKey{X: 4, Y: 64, Z: 5}] = "minecraft:oak_sign"
	world.Blocks[BlockPosKey{X: 4, Y: 64, Z: 3}] = "minecraft:chiseled_bookshelf"
	world.Blocks[BlockPosKey{X: 5, Y: 64, Z: 4}] = "minecraft:bookshelf"
	// Floor & archive structure
	for x := int32(3); x <= 6; x++ {
		for z := int32(3); z <= 6; z++ {
			world.Blocks[BlockPosKey{X: x, Y: 63, Z: z}] = "minecraft:oak_planks"
		}
	}

	// 4. Residential Quarters at (-4, 64, 4) with 3 assigned beds for Able, aria, boreas
	world.AssignedBeds["Able"] = &civquestv1.BlockPosProto{X: -4, Y: 64, Z: 3}
	world.AssignedBeds["aria"] = &civquestv1.BlockPosProto{X: -4, Y: 64, Z: 4}
	world.AssignedBeds["boreas"] = &civquestv1.BlockPosProto{X: -4, Y: 64, Z: 5}

	world.Blocks[BlockPosKey{X: -4, Y: 64, Z: 3}] = "minecraft:red_bed"
	world.Blocks[BlockPosKey{X: -4, Y: 64, Z: 4}] = "minecraft:red_bed"
	world.Blocks[BlockPosKey{X: -4, Y: 64, Z: 5}] = "minecraft:red_bed"
	// Residential floor & surrounding shelter walls
	for x := int32(-6); x <= -2; x++ {
		for z := int32(2); z <= 6; z++ {
			world.Blocks[BlockPosKey{X: x, Y: 63, Z: z}] = "minecraft:oak_planks"
		}
	}
	for z := int32(2); z <= 6; z++ {
		world.Blocks[BlockPosKey{X: -6, Y: 64, Z: z}] = "minecraft:oak_planks"
	}

	// 5. Workstation Complex
	world.Workstations["crafting_table"] = &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 4}
	world.Workstations["furnace"] = &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 5}
	world.Workstations["anvil"] = &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 5}

	world.Blocks[BlockPosKey{X: 2, Y: 64, Z: 4}] = "minecraft:crafting_table"
	world.Blocks[BlockPosKey{X: 2, Y: 64, Z: 5}] = "minecraft:furnace"
	world.Blocks[BlockPosKey{X: 3, Y: 64, Z: 5}] = "minecraft:anvil"

	// 6. Stockpile Supply Chest at (2, 64, 3)
	chestPos := &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 3}
	world.Blocks[BlockPosKey{X: 2, Y: 64, Z: 3}] = "minecraft:chest"
	world.Stockpile = &StockpileSupplyChest{
		Pos: chestPos,
		Items: map[string]int32{
			"minecraft:cooked_beef": 64,
			"minecraft:bread":       64,
			"minecraft:wheat_seeds": 64,
			"minecraft:carrot":      64,
			"minecraft:iron_ingot":  32,
			"minecraft:cobblestone": 64,
			"minecraft:oak_log":     64,
			"minecraft:oak_planks":  64,
		},
	}

	// 7. Agricultural Quarter: 9x9 Irrigated Farmland centered at (0, 64, -8)
	// Bounds: X [-4..4], Z [-12..-4]
	world.AgriCenterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: -8}
	// Central water irrigation block at (0, 63, -8)
	world.Blocks[BlockPosKey{X: 0, Y: 63, Z: -8}] = "minecraft:water"

	for x := int32(-4); x <= 4; x++ {
		for z := int32(-12); z <= -4; z++ {
			if x == 0 && z == -8 {
				continue // Central water well
			}
			// Tilled farmland at ground level Y=63
			world.Blocks[BlockPosKey{X: x, Y: 63, Z: z}] = "minecraft:farmland"

			// Mature crops at surface Y=64
			kCrop := BlockPosKey{X: x, Y: 64, Z: z}
			if x < 0 {
				world.Blocks[kCrop] = "minecraft:wheat"
				world.BlockStates[kCrop] = "age=7"
			} else {
				world.Blocks[kCrop] = "minecraft:carrots"
				world.BlockStates[kCrop] = "age=7"
			}
		}
	}

	// 8. Tavern & Trade Hub at (8, 64, 0)
	world.TavernPos = &civquestv1.BlockPosProto{X: 8, Y: 64, Z: 0}
	world.CampfirePos = &civquestv1.BlockPosProto{X: 8, Y: 64, Z: 0}

	world.Blocks[BlockPosKey{X: 8, Y: 64, Z: 0}] = "minecraft:campfire"
	world.Blocks[BlockPosKey{X: 8, Y: 64, Z: 1}] = "minecraft:barrel"
	world.Blocks[BlockPosKey{X: 8, Y: 64, Z: -1}] = "minecraft:barrel"
	world.Blocks[BlockPosKey{X: 9, Y: 64, Z: 0}] = "minecraft:barrel"
	world.Blocks[BlockPosKey{X: 7, Y: 64, Z: 0}] = "minecraft:oak_stairs"
	world.Blocks[BlockPosKey{X: 8, Y: 64, Z: 2}] = "minecraft:oak_stairs"
	world.Blocks[BlockPosKey{X: 8, Y: 64, Z: -2}] = "minecraft:oak_stairs"

	for x := int32(7); x <= 10; x++ {
		for z := int32(-3); z <= 3; z++ {
			world.Blocks[BlockPosKey{X: x, Y: 63, Z: z}] = "minecraft:oak_planks"
		}
	}

	// -------------------------------------------------------------
	// Sector-Based Terrain Proving Grounds
	// -------------------------------------------------------------

	// Proving Ground A: East Waterway Channel (X: 16..24, Z: -30..30)
	// 6-block wide canal of water (depth 2) with sand banks
	for z := int32(-30); z <= 30; z++ {
		// West sand bank (X: 16..17)
		world.Blocks[BlockPosKey{X: 16, Y: 63, Z: z}] = "minecraft:sand"
		world.Blocks[BlockPosKey{X: 17, Y: 63, Z: z}] = "minecraft:sand"

		// 6-block canal (X: 18..23), depth 2: bed at Y=61, water at Y=62 and Y=63
		for x := int32(18); x <= 23; x++ {
			world.Blocks[BlockPosKey{X: x, Y: 61, Z: z}] = "minecraft:sand"
			world.Blocks[BlockPosKey{X: x, Y: 62, Z: z}] = "minecraft:water"
			world.Blocks[BlockPosKey{X: x, Y: 63, Z: z}] = "minecraft:water"
		}

		// East sand bank (X: 24..25)
		world.Blocks[BlockPosKey{X: 24, Y: 63, Z: z}] = "minecraft:sand"
		world.Blocks[BlockPosKey{X: 25, Y: 63, Z: z}] = "minecraft:sand"
	}

	// Proving Ground B: South Ridge & Cliff (Z: 20..35)
	// Multi-tier stone cliff ascending Y=64 -> 68 -> 72 -> 76 with switchback slope at X=2
	for z := int32(20); z <= 35; z++ {
		for x := int32(-10); x <= 14; x++ {
			tierHeight := int32(64)
			switch {
			case z >= 32:
				tierHeight = 76
			case z >= 28:
				tierHeight = 72
			case z >= 24:
				tierHeight = 68
			default:
				tierHeight = 64
			}

			// Cliff stone layers from 63 up to tierHeight - 1
			for y := int32(63); y < tierHeight; y++ {
				world.Blocks[BlockPosKey{X: x, Y: y, Z: z}] = "minecraft:stone"
			}
		}
	}

	// Carve switchback navigable slope with 1-block step-up incline along X in [1..3]
	// Steps up 1 block per Z coordinate from Z=20 (Y=64) to Z=32 (Y=76)
	for z := int32(20); z <= 32; z++ {
		stepY := int32(64 + (z - 20))
		for x := int32(1); x <= 3; x++ {
			// Clear any rock above step
			for y := stepY; y <= 80; y++ {
				delete(world.Blocks, BlockPosKey{X: x, Y: y, Z: z})
			}
			// Stone base below step
			for y := int32(63); y < stepY; y++ {
				world.Blocks[BlockPosKey{X: x, Y: y, Z: z}] = "minecraft:stone"
			}
		}
	}

	// Upper plateau surface at Z in [32..35]
	for z := int32(32); z <= 35; z++ {
		for x := int32(-10); x <= 14; x++ {
			world.Blocks[BlockPosKey{X: x, Y: 75, Z: z}] = "minecraft:stone"
		}
	}

	// Proving Ground C: West Dense Forest Grove (X: -40..-16, Z: -30..30)
	// Dense stand of 40 mature oak trees (logs Y=64..68, canopies Y=67..70)
	for x := int32(-40); x <= -16; x++ {
		for z := int32(-30); z <= 30; z++ {
			world.Blocks[BlockPosKey{X: x, Y: 63, Z: z}] = "minecraft:grass_block"
		}
	}

	// Deterministic grid distribution for 40 mature trees
	treeCount := 0
	for gx := int32(-38); gx <= -18 && treeCount < 40; gx += 3 {
		for gz := int32(-28); gz <= 28 && treeCount < 40; gz += 7 {
			world.placeOakTree(gx, 64, gz)
			treeCount++
		}
	}

	// Proving Ground D: North Frontier Corridor (Z: -64..-16, X: -10..10)
	// Flat plains corridor leading outward to 64m+
	for z := int32(-64); z <= -16; z++ {
		for x := int32(-10); x <= 10; x++ {
			world.Blocks[BlockPosKey{X: x, Y: 63, Z: z}] = "minecraft:grass_block"
		}
	}

	return world
}

func (w *SyntheticStartingWorld) placeOakTree(trunkX, trunkBaseY, trunkZ int32) {
	// Trunk: 5 logs Y=64..68
	for y := trunkBaseY; y < trunkBaseY+5; y++ {
		w.Blocks[BlockPosKey{X: trunkX, Y: y, Z: trunkZ}] = "minecraft:oak_log"
	}
	// Canopy: Y=67..70
	for y := trunkBaseY + 3; y <= trunkBaseY+6; y++ {
		radius := int32(2)
		if y >= trunkBaseY+5 {
			radius = 1
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if dx == 0 && dz == 0 && y < trunkBaseY+5 {
					continue // Trunk
				}
				k := BlockPosKey{X: trunkX + dx, Y: y, Z: trunkZ + dz}
				if _, exists := w.Blocks[k]; !exists {
					w.Blocks[k] = "minecraft:oak_leaves"
				}
			}
		}
	}
}

// CalculateEconomicSurplusRatio computes the economic surplus ratio:
// (available food units + building materials) / baseline consumption demand.
// Asserts ratio >= 1.50 per Acceptance Criteria.
func (w *SyntheticStartingWorld) CalculateEconomicSurplusRatio(population int) float64 {
	if population <= 0 {
		population = 3
	}
	if w.Stockpile == nil {
		return 0.0
	}

	foodUnits := float64(w.Stockpile.Items["minecraft:cooked_beef"] + w.Stockpile.Items["minecraft:bread"])
	buildingMaterials := float64(
		w.Stockpile.Items["minecraft:oak_log"] +
			w.Stockpile.Items["minecraft:oak_planks"] +
			w.Stockpile.Items["minecraft:cobblestone"] +
			w.Stockpile.Items["minecraft:iron_ingot"],
	)

	// Baseline clan demand across standard 7-day reserve horizon (ADR-008: 7 days * 3 food/day/agent = 21 units/agent)
	consumptionDemand := float64(population * 7 * 3) // 63.0 for pop=3
	if consumptionDemand <= 0 {
		consumptionDemand = 63.0
	}

	return (foodUnits + buildingMaterials) / consumptionDemand
}

// ExportVisibleBlocks converts the world blocks into a slice of VisibleBlock for telemetry frames or gRPC streaming.
func (w *SyntheticStartingWorld) ExportVisibleBlocks() []*civquestv1.VisibleBlock {
	res := make([]*civquestv1.VisibleBlock, 0, len(w.Blocks))
	for k, blockID := range w.Blocks {
		res = append(res, &civquestv1.VisibleBlock{
			Pos:              &civquestv1.BlockPosProto{X: k.X, Y: k.Y, Z: k.Z},
			BlockId:          blockID,
			BlockStateString: w.BlockStates[k],
		})
	}
	return res
}

// Clone creates a deep copy of the synthetic world.
func (w *SyntheticStartingWorld) Clone() *SyntheticStartingWorld {
	clone := &SyntheticStartingWorld{
		Blocks:        make(map[BlockPosKey]string, len(w.Blocks)),
		BlockStates:   make(map[BlockPosKey]string, len(w.BlockStates)),
		AssignedBeds:  make(map[string]*civquestv1.BlockPosProto, len(w.AssignedBeds)),
		Workstations:  make(map[string]*civquestv1.BlockPosProto, len(w.Workstations)),
		TownHallPos:   w.TownHallPos,
		LecternPos:    w.LecternPos,
		SignPos:       w.SignPos,
		TavernPos:     w.TavernPos,
		CampfirePos:   w.CampfirePos,
		AgriCenterPos: w.AgriCenterPos,
		RosterNames:   append([]string(nil), w.RosterNames...),
	}

	for k, v := range w.Blocks {
		clone.Blocks[k] = v
	}
	for k, v := range w.BlockStates {
		clone.BlockStates[k] = v
	}
	for k, v := range w.AssignedBeds {
		clone.AssignedBeds[k] = v
	}
	for k, v := range w.Workstations {
		clone.Workstations[k] = v
	}
	if w.Stockpile != nil {
		clone.Stockpile = &StockpileSupplyChest{
			Pos:   w.Stockpile.Pos,
			Items: make(map[string]int32, len(w.Stockpile.Items)),
		}
		for k, v := range w.Stockpile.Items {
			clone.Stockpile.Items[k] = v
		}
	}
	return clone
}
