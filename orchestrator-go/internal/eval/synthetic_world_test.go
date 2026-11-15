package eval

import (
	"math"
	"strings"
	"testing"
	"time"
)

// TestSyntheticWorld_ResetInvariant_PerformanceUnder5ms verifies Objective 1:
//  1. Instantiation executes in <5ms.
//  2. Zero state bleed-through (multiple world instances are completely isolated).
//  3. Complete layout invariants: Town Hall lectern/sign, residential beds, workstations,
//     stockpile supply chest (surplus >= 1.50), 9x9 agricultural quarter, tavern hub,
//     and all 4 sector proving grounds.
func TestSyntheticWorld_ResetInvariant_PerformanceUnder5ms(t *testing.T) {
	start := time.Now()
	world1 := NewSyntheticStartingWorld()
	elapsed := time.Since(start)

	t.Logf("Constructed complete SyntheticStartingWorld layout in %v (total blocks: %d)", elapsed, len(world1.Blocks))
	maxDuration := 5 * time.Millisecond
	if raceDetectorEnabled {
		maxDuration = 25 * time.Millisecond
	}
	if elapsed >= maxDuration {
		t.Fatalf("PERFORMANCE VIOLATION: World constructor took %v (must execute in < %v)", elapsed, maxDuration)
	}

	// 1. Reset Invariant & Zero State Bleed-through
	world2 := NewSyntheticStartingWorld()
	// Mutate world1
	world1.Blocks[BlockPosKey{X: 999, Y: 999, Z: 999}] = "minecraft:bedrock"
	world1.Stockpile.Items["minecraft:cooked_beef"] = 0

	if _, exists := world2.Blocks[BlockPosKey{X: 999, Y: 999, Z: 999}]; exists {
		t.Fatalf("RESET INVARIANT VIOLATION: Mutation in world1 bled into world2")
	}
	if world2.Stockpile.Items["minecraft:cooked_beef"] != 64 {
		t.Fatalf("RESET INVARIANT VIOLATION: Stockpile modification bled across instances")
	}

	// 2. Core Settlement Layout Invariants
	// 2026 Tropical storm roster seed verification
	if len(world2.RosterNames) != 21 {
		t.Fatalf("expected 21 tropical storm seed names, got %d", len(world2.RosterNames))
	}
	if world2.RosterNames[0] != "Arthur" || world2.RosterNames[1] != "Bertha" || world2.RosterNames[2] != "Cristobal" {
		t.Errorf("unexpected seed roster names: %v", world2.RosterNames[:3])
	}

	// Town Hall & Archive Wing at (4, 64, 4)
	if id, ok := world2.Blocks[BlockPosKey{X: 4, Y: 64, Z: 4}]; !ok || id != "minecraft:lectern" {
		t.Errorf("expected minecraft:lectern at (4, 64, 4), got %s (ok=%t)", id, ok)
	}
	if id, ok := world2.Blocks[BlockPosKey{X: 4, Y: 64, Z: 5}]; !ok || id != "minecraft:oak_sign" {
		t.Errorf("expected minecraft:oak_sign at (4, 64, 5), got %s (ok=%t)", id, ok)
	}

	// Residential Quarters at (-4, 64, 4) with 3 assigned beds
	expectedBeds := map[string]BlockPosKey{
		"Able":   {X: -4, Y: 64, Z: 3},
		"aria":   {X: -4, Y: 64, Z: 4},
		"boreas": {X: -4, Y: 64, Z: 5},
	}
	for name, pos := range expectedBeds {
		bedPos, ok := world2.AssignedBeds[name]
		if !ok || bedPos == nil {
			t.Fatalf("missing assigned bed for %s", name)
		}
		if bedPos.X != pos.X || bedPos.Y != pos.Y || bedPos.Z != pos.Z {
			t.Errorf("bed coords mismatch for %s: expected %+v, got %+v", name, pos, bedPos)
		}
		if id, hasBed := world2.Blocks[pos]; !hasBed || id != "minecraft:red_bed" {
			t.Errorf("expected minecraft:red_bed at %+v, got %s", pos, id)
		}
	}

	// Workstation Complex
	if id, ok := world2.Blocks[BlockPosKey{X: 2, Y: 64, Z: 4}]; !ok || id != "minecraft:crafting_table" {
		t.Errorf("expected minecraft:crafting_table at (2, 64, 4), got %s", id)
	}
	if id, ok := world2.Blocks[BlockPosKey{X: 2, Y: 64, Z: 5}]; !ok || id != "minecraft:furnace" {
		t.Errorf("expected minecraft:furnace at (2, 64, 5), got %s", id)
	}
	if id, ok := world2.Blocks[BlockPosKey{X: 3, Y: 64, Z: 5}]; !ok || id != "minecraft:anvil" {
		t.Errorf("expected minecraft:anvil at (3, 64, 5), got %s", id)
	}

	// Stockpile Supply Chest at (2, 64, 3) and Economic Surplus >= 1.50
	if world2.Stockpile == nil || world2.Stockpile.Pos == nil {
		t.Fatalf("missing StockpileSupplyChest")
	}
	if world2.Stockpile.Pos.X != 2 || world2.Stockpile.Pos.Y != 64 || world2.Stockpile.Pos.Z != 3 {
		t.Errorf("unexpected chest pos: %+v", world2.Stockpile.Pos)
	}
	surplusRatio := world2.CalculateEconomicSurplusRatio(3)
	t.Logf("Stockpile Economic Surplus Ratio: %.2f", surplusRatio)
	if surplusRatio < 1.50 {
		t.Fatalf("expected surplus ratio >= 1.50, got %.2f", surplusRatio)
	}

	// 9x9 Irrigated Farmland centered at (0, 64, -8)
	if id, ok := world2.Blocks[BlockPosKey{X: 0, Y: 63, Z: -8}]; !ok || id != "minecraft:water" {
		t.Errorf("expected central water block at (0, 63, -8), got %s", id)
	}
	wheatCount := 0
	carrotCount := 0
	for x := int32(-4); x <= 4; x++ {
		for z := int32(-12); z <= -4; z++ {
			if x == 0 && z == -8 {
				continue
			}
			fId := world2.Blocks[BlockPosKey{X: x, Y: 63, Z: z}]
			if fId != "minecraft:farmland" {
				t.Errorf("expected farmland at (%d, 63, %d), got %s", x, z, fId)
			}
			cropId := world2.Blocks[BlockPosKey{X: x, Y: 64, Z: z}]
			state := world2.BlockStates[BlockPosKey{X: x, Y: 64, Z: z}]
			if cropId == "minecraft:wheat" && state == "age=7" {
				wheatCount++
			} else if cropId == "minecraft:carrots" && state == "age=7" {
				carrotCount++
			}
		}
	}
	t.Logf("Agricultural Quarter crops: %d mature wheat, %d mature carrots", wheatCount, carrotCount)
	if wheatCount == 0 || carrotCount == 0 {
		t.Fatalf("expected both mature wheat and carrots in 9x9 quarter")
	}

	// Tavern & Trade Hub at (8, 64, 0)
	if id, ok := world2.Blocks[BlockPosKey{X: 8, Y: 64, Z: 0}]; !ok || id != "minecraft:campfire" {
		t.Errorf("expected campfire at (8, 64, 0), got %s", id)
	}
	if id, ok := world2.Blocks[BlockPosKey{X: 8, Y: 64, Z: 1}]; !ok || id != "minecraft:barrel" {
		t.Errorf("expected barrel at (8, 64, 1), got %s", id)
	}

	// 3. Sector Proving Grounds
	// East Waterway Channel (X: 16..24, Z: -30..30)
	if id, ok := world2.Blocks[BlockPosKey{X: 18, Y: 62, Z: 0}]; !ok || id != "minecraft:water" {
		t.Errorf("expected water in East canal at (18, 62, 0), got %s", id)
	}
	if id, ok := world2.Blocks[BlockPosKey{X: 16, Y: 63, Z: 0}]; !ok || id != "minecraft:sand" {
		t.Errorf("expected sand bank at (16, 63, 0), got %s", id)
	}

	// South Ridge & Cliff (Z: 20..35)
	if id, ok := world2.Blocks[BlockPosKey{X: 0, Y: 68, Z: 28}]; !ok || id != "minecraft:stone" {
		t.Errorf("expected stone cliff at (0, 68, 28), got %s", id)
	}

	// West Dense Forest Grove (40 mature trees)
	logCount := 0
	leavesCount := 0
	for k, id := range world2.Blocks {
		if k.X >= -40 && k.X <= -16 && k.Z >= -30 && k.Z <= 30 {
			if id == "minecraft:oak_log" {
				logCount++
			} else if id == "minecraft:oak_leaves" {
				leavesCount++
			}
		}
	}
	t.Logf("Forest Grove blocks: %d logs, %d leaves", logCount, leavesCount)
	if logCount < 40*4 { // At least 4 logs per tree
		t.Errorf("expected at least %d tree logs, got %d", 40*4, logCount)
	}

	// North Frontier Corridor
	if id, ok := world2.Blocks[BlockPosKey{X: 0, Y: 63, Z: -50}]; !ok || id != "minecraft:grass_block" {
		t.Errorf("expected grass_block in North Corridor at (0, 63, -50), got %s", id)
	}
}

// TestSyntheticWorld_Scenario1_AgrarianHarvestAndReplant verifies Scenario 1:
// aria enters agricultural quarter, harvests mature wheat & carrots, replants seeds, and deposits yield.
func TestSyntheticWorld_Scenario1_AgrarianHarvestAndReplant(t *testing.T) {
	world := NewSyntheticStartingWorld()
	tmpDir := t.TempDir()

	sim, res, err := RunAgrarianHarvestReplantScenario(TimeWarpConfig{
		WorkDir: tmpDir,
	}, world)
	if err != nil {
		t.Fatalf("Scenario 1 failed: %v", err)
	}
	defer sim.Close()

	t.Logf("Agrarian Harvest Results: %d harvested (wheat=%d, carrots=%d), %d replanted, %d deposited items",
		res.HarvestedCount, res.MatureWheatHarvested, res.MatureCarrotsHarvested, res.ReplantedCount, len(res.DepositedItems))

	if res.HarvestedCount < 4 {
		t.Fatalf("expected at least 4 crops harvested, got %d", res.HarvestedCount)
	}
	if res.MatureWheatHarvested == 0 || res.MatureCarrotsHarvested == 0 {
		t.Fatalf("expected both wheat and carrots harvested")
	}
	if res.ReplantedCount != res.HarvestedCount {
		t.Fatalf("expected replanted count (%d) to match harvested count (%d)", res.ReplantedCount, res.HarvestedCount)
	}
	if len(res.DepositedItems) == 0 {
		t.Fatalf("expected non-empty deposited items into stockpile supply chest")
	}
}

// TestSyntheticWorld_Scenario2_DynasticReproductionEconomicSurplus verifies Scenario 2:
// Surplus ratio >= 1.50, ReproductionGovernor triggers, and new clan child avatar created with blended genetics.
func TestSyntheticWorld_Scenario2_DynasticReproductionEconomicSurplus(t *testing.T) {
	world := NewSyntheticStartingWorld()

	result, surplusRatio, err := RunDynasticReproductionSurplusScenario(world, nil)
	if err != nil {
		t.Fatalf("Scenario 2 failed: %v", err)
	}

	t.Logf("Dynastic Reproduction outcome: success=%t, child=%s, generation=%d, surplusRatio=%.2f, surplusDays=%.1f",
		result.Success, result.ChildID, result.Generation, surplusRatio, result.CheckResult.FoodSurplusDays)

	if surplusRatio < 1.50 {
		t.Fatalf("expected surplus ratio >= 1.50, got %.2f", surplusRatio)
	}
	if !result.Success {
		t.Fatalf("expected reproduction to succeed, got reason: %s", result.CheckResult.Reason)
	}
	if result.ChildID == "" {
		t.Fatalf("expected generated child ID")
	}
	if result.ChildConfig == nil {
		t.Fatalf("expected child configuration")
	}
	if result.ChildConfig.Generation != 2 {
		t.Errorf("expected child generation 2, got %d", result.ChildConfig.Generation)
	}
	if !result.ChildConfig.IsChild {
		t.Errorf("expected child IsChild=true")
	}
	if len(result.ChildConfig.Lineage.Parents) != 2 {
		t.Errorf("expected 2 parents in lineage, got %v", result.ChildConfig.Lineage.Parents)
	}

	// Assert Blended Genetics
	traits := result.ChildConfig.Traits
	t.Logf("Child Blended Traits: Bravery=%.2f, Industriousness=%.2f, Sociability=%.2f, Curiosity=%.2f",
		traits.Bravery, traits.Industriousness, traits.Sociability, traits.Curiosity)
	if traits.Bravery <= 0.0 || traits.Bravery > 1.0 ||
		traits.Industriousness <= 0.0 || traits.Industriousness > 1.0 {
		t.Errorf("child traits outside valid [0, 1] range: %+v", traits)
	}
}

// TestSyntheticWorld_Scenario3_ChroniclerLecternAndWrittenBook verifies Scenario 3:
// Clan Chronicler accesses town hall lectern at (4, 64, 4), synthesizes chronicle, and materializes paginated written book.
func TestSyntheticWorld_Scenario3_ChroniclerLecternAndWrittenBook(t *testing.T) {
	world := NewSyntheticStartingWorld()
	tmpDir := t.TempDir()

	sim, chronicle, err := RunChroniclerLecternScenario(TimeWarpConfig{
		WorkDir: tmpDir,
	}, world, nil)
	if err != nil {
		t.Fatalf("Scenario 3 failed: %v", err)
	}
	defer sim.Close()

	t.Logf("Chronicler written book composed: %q by %s (%d pages)",
		chronicle.Title, chronicle.Author, len(chronicle.Pages))

	// Title <= 32 chars
	if len([]rune(chronicle.Title)) > 32 {
		t.Fatalf("title exceeds 32 runes: %q (len %d)", chronicle.Title, len([]rune(chronicle.Title)))
	}
	if len(chronicle.Pages) == 0 {
		t.Fatalf("expected non-empty pages")
	}

	// Verify Minecraft pagination rules for each page
	for i, page := range chronicle.Pages {
		lines := strings.Split(page, "\n")
		if len(lines) > 14 {
			t.Errorf("page %d has %d lines (max 14)", i, len(lines))
		}
		for lineIdx, line := range lines {
			if len([]rune(line)) > 19 {
				t.Errorf("page %d line %d exceeds 19 runes: %q (len %d)", i, lineIdx, line, len([]rune(line)))
			}
		}
		if len([]rune(page)) > 250 {
			t.Errorf("page %d exceeds 250 characters: len %d", i, len([]rune(page)))
		}
	}

	// Verify TargetLecternOrShelf coordinates in intent
	createBook := chronicle.Intent.GetCreateBook()
	if createBook == nil {
		t.Fatalf("expected CreateWrittenBookIntent on chronicle")
	}
	if createBook.TargetLecternOrShelf == nil {
		t.Fatalf("expected non-nil TargetLecternOrShelf")
	}
	if createBook.TargetLecternOrShelf.X != 4 || createBook.TargetLecternOrShelf.Y != 64 || createBook.TargetLecternOrShelf.Z != 4 {
		t.Errorf("unexpected target lectern coords: %+v (expected 4, 64, 4)", createBook.TargetLecternOrShelf)
	}

	// Verify materialized written book in simulation inventory
	if sim.InventoryItemCount("minecraft:written_book") == 0 {
		t.Fatalf("expected minecraft:written_book in virtual agent inventory")
	}
}

// TestSyntheticWorld_Scenario4_WaterwayChannelAndBridgeCrossing verifies Scenario 4:
// Agent approaches East River canal, avoids drowning, and paves bridge corridor to cross safely.
func TestSyntheticWorld_Scenario4_WaterwayChannelAndBridgeCrossing(t *testing.T) {
	world := NewSyntheticStartingWorld()
	tmpDir := t.TempDir()

	sim, res, err := RunWaterwayBridgeCrossingScenario(TimeWarpConfig{
		WorkDir: tmpDir,
	}, world)
	if err != nil {
		t.Fatalf("Scenario 4 failed: %v", err)
	}
	defer sim.Close()

	t.Logf("Waterway Bridge Crossing: terrain=%s, waterDetected=%t, pavedBlocks=%d, crossed=%t, finalPos=%+v",
		res.TerrainType, res.WaterDetected, res.PavedBridgeBlocks, res.CrossedSuccessfully, res.FinalPosition)

	if !res.WaterDetected {
		t.Fatalf("expected East River canal water to be detected")
	}
	if res.PavedBridgeBlocks < 6 {
		t.Fatalf("expected at least 6 bridge blocks paved across canal, got %d", res.PavedBridgeBlocks)
	}
	if !res.CrossedSuccessfully {
		t.Fatalf("expected agent to successfully cross to East bank")
	}
	if res.FinalPosition.X < 24.0 {
		t.Fatalf("expected final X position >= 24.0 (East bank), got %.2f", res.FinalPosition.X)
	}
	if res.FinalPosition.Y < 64.0 {
		t.Fatalf("expected surface elevation Y >= 64.0 (no drowning), got %.2f", res.FinalPosition.Y)
	}
}

// TestSyntheticWorld_Scenario5_RidgeCliffElevationNavigation verifies Scenario 5:
// Agent approaches South Cliff, executes 1-block step-up physics, avoids sheer cliff drop-offs, and reaches upper plateau (Y=72+).
func TestSyntheticWorld_Scenario5_RidgeCliffElevationNavigation(t *testing.T) {
	world := NewSyntheticStartingWorld()
	tmpDir := t.TempDir()

	sim, res, err := RunRidgeCliffElevationScenario(TimeWarpConfig{
		WorkDir: tmpDir,
	}, world)
	if err != nil {
		t.Fatalf("Scenario 5 failed: %v", err)
	}
	defer sim.Close()

	t.Logf("Ridge & Cliff Navigation: stepUps=%d, sheerAvoided=%d, maxElevation=%.1f, reachedPlateau=%t, finalPos=%+v",
		res.StepUpsExecuted, res.SheerDropOffsAvoided, res.MaxElevationReached, res.ReachedUpperPlateau, res.FinalPosition)

	if res.SheerDropOffsAvoided == 0 {
		t.Fatalf("expected sheer cliff drop-offs to be avoided")
	}
	if res.StepUpsExecuted == 0 {
		t.Fatalf("expected 1-block step-ups to be executed")
	}
	if !res.ReachedUpperPlateau {
		t.Fatalf("expected agent to reach upper plateau (Y >= 72.0), max elevation reached: %.1f", res.MaxElevationReached)
	}
	if res.FinalPosition.Y < 72.0 {
		t.Fatalf("expected final Y position >= 72.0, got %.2f", res.FinalPosition.Y)
	}
}

// TestSyntheticWorld_Scenario6_BedtimeSettlementRoutingAndSleep verifies Scenario 6:
// Night arrives (tick 13000); agents in village navigate directly to their assigned beds in residential wing and sleep without emergency shelter building.
func TestSyntheticWorld_Scenario6_BedtimeSettlementRoutingAndSleep(t *testing.T) {
	world := NewSyntheticStartingWorld()
	tmpDir := t.TempDir()

	sim, err := RunBedtimeSettlementRoutingScenario(TimeWarpConfig{
		WorkDir: tmpDir,
	}, world)
	if err != nil {
		t.Fatalf("Scenario 6 failed: %v", err)
	}
	defer sim.Close()

	if !sim.isSleeping {
		t.Fatalf("expected agent to be sleeping in assigned bed")
	}

	// Verify final position is close to assigned bed at (-4, 64, 3)
	bedPos := world.AssignedBeds["Able"]
	distToBed := math.Hypot(sim.pos.X-(float64(bedPos.X)+0.5), sim.pos.Z-(float64(bedPos.Z)+0.5))
	t.Logf("Final bedtime distance to assigned bed: %.2fm", distToBed)
	if distToBed > 3.0 {
		t.Fatalf("agent fell asleep too far from assigned bed (dist=%.2fm)", distToBed)
	}

	// Invariant assertion: 0 emergency shelter blueprint blocks were placed
	if placed := sim.BlueprintDetector.Count(); placed > 0 {
		t.Fatalf("EMERGENCY SHELTER VIOLATION: Agent built %d shelter blocks instead of using established residential bed", placed)
	}
}
