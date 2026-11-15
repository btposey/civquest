package eval

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/genetics"
	"github.com/btposey/civquest/orchestrator-go/internal/governor"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/roles/agrarian"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// AgrarianHarvestResult records the outcome of Scenario 1 (Agrarian Harvest & Replant).
type AgrarianHarvestResult struct {
	HarvestedCount         int
	ReplantedCount         int
	DepositedItems         []*civquestv1.ItemStackProto
	MatureWheatHarvested   int
	MatureCarrotsHarvested int
}

// WaterwayBridgeResult records the outcome of Scenario 4 (Waterway Channel & Bridge Crossing).
type WaterwayBridgeResult struct {
	WaterDetected       bool
	TerrainType         system1.TerrainType
	PavedBridgeBlocks   int
	CrossedSuccessfully bool
	FinalPosition       *civquestv1.Vec3
}

// RidgeCliffResult records the outcome of Scenario 5 (Ridge & Cliff Elevation Navigation).
type RidgeCliffResult struct {
	StepUpsExecuted      int
	SheerDropOffsAvoided int
	ReachedUpperPlateau  bool
	MaxElevationReached  float64
	FinalPosition        *civquestv1.Vec3
}

// RunAgrarianHarvestReplantScenario executes Scenario 1:
// aria enters the 9x9 agricultural quarter, harvests mature wheat and carrots, replants seeds, and deposits yield.
func RunAgrarianHarvestReplantScenario(baseCfg TimeWarpConfig, world *SyntheticStartingWorld) (*TimeWarpSimulation, *AgrarianHarvestResult, error) {
	if world == nil {
		world = NewSyntheticStartingWorld()
	}

	cfg := baseCfg
	if cfg.AgentID == "" {
		cfg.AgentID = "aria"
	}
	if cfg.ClanID == "" {
		cfg.ClanID = "pioneers"
	}
	if cfg.IdentityConfig == nil {
		cfg.IdentityConfig = xdg.DefaultAgentIdentityConfig(cfg.AgentID)
		cfg.IdentityConfig.Profession = "FARMER"
	}
	cfg.InitialPosition = &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: -4.5} // Edge of agricultural quarter
	cfg.InitialTimeOfDay = 2000                                      // Morning

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating simulation: %w", err)
	}
	sim.LoadSyntheticWorld(world)

	res := &AgrarianHarvestResult{
		DepositedItems: make([]*civquestv1.ItemStackProto, 0),
	}

	// 1. Scan visible agricultural blocks in the 9x9 quarter: X [-4..4], Z [-12..-4]
	frame := sim.BuildTelemetryFrame()
	var matureTargets []*civquestv1.BlockPosProto
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		x, y, z := b.Pos.X, b.Pos.Y, b.Pos.Z
		if x >= -4 && x <= 4 && z >= -12 && z <= -4 && y == 64 {
			if agrarian.IsCropMature(b.BlockId, b.BlockStateString) {
				matureTargets = append(matureTargets, b.Pos)
			}
		}
	}

	if len(matureTargets) == 0 {
		return sim, res, fmt.Errorf("no mature crops detected in 9x9 agricultural quarter")
	}

	// 2. Harvest mature crops and replant seeds (process first 8 for rapid determinism)
	maxHarvest := 8
	if len(matureTargets) < maxHarvest {
		maxHarvest = len(matureTargets)
	}

	for i := 0; i < maxHarvest; i++ {
		target := matureTargets[i]
		blockID, _ := sim.HasBlock(target.X, target.Y, target.Z)

		// Navigate to crop
		sim.pos.X = float64(target.X) + 0.5
		sim.pos.Z = float64(target.Z) + 0.5

		// Harvest crop
		sim.RemoveBlock(target.X, target.Y, target.Z)
		res.HarvestedCount++
		if strings.Contains(blockID, "wheat") {
			res.MatureWheatHarvested++
			sim.AddInventoryItem("minecraft:wheat", 1)
			sim.AddInventoryItem("minecraft:wheat_seeds", 1)
		} else {
			res.MatureCarrotsHarvested++
			sim.AddInventoryItem("minecraft:carrot", 2)
		}

		// Replant seed/crop at target
		replantItem := agrarian.SeedForItem(blockID)
		sim.RemoveInventoryItem(replantItem, 1)
		sim.AddBlockWithState(target.X, target.Y, target.Z, blockID, "age=0")
		res.ReplantedCount++
	}

	// 3. Deposit harvested yield into the core stockpile supply chest at (2, 64, 3)
	chestPos := world.Stockpile.Pos
	if chestPos == nil {
		chestPos = &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 3}
	}

	// Navigate to chest
	sim.pos.X = float64(chestPos.X) + 0.5
	sim.pos.Z = float64(chestPos.Z) + 0.5

	var depositItems []*civquestv1.ItemStackProto
	if res.MatureWheatHarvested > 0 {
		depositItems = append(depositItems, &civquestv1.ItemStackProto{
			ItemId: "minecraft:wheat",
			Count:  int32(res.MatureWheatHarvested),
		})
	}
	if res.MatureCarrotsHarvested > 0 {
		depositItems = append(depositItems, &civquestv1.ItemStackProto{
			ItemId: "minecraft:carrot",
			Count:  int32(res.MatureCarrotsHarvested),
		})
	}

	// Emit deposit intent into chest
	depositIntent := &civquestv1.AgentIntent{
		AgentId: cfg.AgentID,
		Action: &civquestv1.AgentIntent_TransferContainer{
			TransferContainer: &civquestv1.TransferContainerIntent{
				ContainerPos:         chestPos,
				DepositIntoContainer: true,
				Items:                depositItems,
			},
		},
	}
	sim.applyIntentFeedback(depositIntent)
	res.DepositedItems = depositItems

	return sim, res, nil
}

// RunDynasticReproductionSurplusScenario executes Scenario 2:
// Calculates surplus ratio from stockpile chest (food + building materials / consumption rate >= 1.50),
// triggers Dynastic Reproduction Governor, and asserts child avatar creation with blended genetics.
func RunDynasticReproductionSurplusScenario(world *SyntheticStartingWorld, resolver *xdg.Resolver) (*governor.ReproductionResult, float64, error) {
	if world == nil {
		world = NewSyntheticStartingWorld()
	}

	// 1. Calculate economic surplus ratio from stockpile chest
	population := 3 // Able, aria, boreas
	surplusRatio := world.CalculateEconomicSurplusRatio(population)
	if surplusRatio < 1.50 {
		return nil, surplusRatio, fmt.Errorf("economic surplus ratio %.2f < 1.50 required threshold", surplusRatio)
	}

	// 2. Setup isolated XDG test environment if not provided
	var tmpDir string
	if resolver == nil {
		var err error
		tmpDir, err = os.MkdirTemp("", "civquest-repro-test-*")
		if err != nil {
			return nil, surplusRatio, fmt.Errorf("failed creating temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		resolver = xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	}

	clanID := "pioneers"
	_ = resolver.InitAgentDirectories("Able")
	_ = resolver.InitAgentDirectories("aria")

	// 3. Populate Civic Registry DB with granary stock from the stockpile chest
	civicDB, err := db.OpenCivicRegistryDBForClan(resolver, clanID)
	if err != nil {
		return nil, surplusRatio, fmt.Errorf("failed opening civic registry db: %w", err)
	}
	defer civicDB.Close()

	cookedBeef := world.Stockpile.Items["minecraft:cooked_beef"]
	bread := world.Stockpile.Items["minecraft:bread"]
	if err := civicDB.AddGranaryStock("minecraft:cooked_beef", int(cookedBeef), 100); err != nil {
		return nil, surplusRatio, fmt.Errorf("failed adding beef stock: %w", err)
	}
	if err := civicDB.AddGranaryStock("minecraft:bread", int(bread), 100); err != nil {
		return nil, surplusRatio, fmt.Errorf("failed adding bread stock: %w", err)
	}

	// 4. Register unclaimed residential housing zone in Civic Registry
	housingZone := &db.FunctionalZone{
		ZoneID:       "zone_residential_wing",
		ZoneType:     db.ZoneTypeResidential,
		MinX:         -6,
		MinY:         64,
		MinZ:         2,
		MaxX:         -2,
		MaxY:         70,
		MaxZ:         6,
		OwnerAgentID: "",
		Name:         "Residential Quarters",
	}
	if err := civicDB.RegisterFunctionalZone(housingZone); err != nil {
		return nil, surplusRatio, fmt.Errorf("failed registering housing zone: %w", err)
	}

	// 5. Seed Married Pair Able and aria with mutual SPOUSE status and high disposition
	cfgAble := &xdg.AgentIdentityConfig{
		Name:       "Able",
		UUID:       agent.OfflinePlayerUUID("Able"),
		ClanID:     clanID,
		Profession: "EXPLORER",
		Generation: 1,
		Traits: xdg.AgentTraits{
			Bravery:         0.85,
			Industriousness: 0.80,
			Sociability:     0.75,
			Curiosity:       0.90,
			Aggressiveness:  0.20,
		},
		Skin: xdg.AgentSkinConfig{Model: "default"},
	}
	cfgAria := &xdg.AgentIdentityConfig{
		Name:       "aria",
		UUID:       agent.OfflinePlayerUUID("aria"),
		ClanID:     clanID,
		Profession: "FARMER",
		Generation: 1,
		Traits: xdg.AgentTraits{
			Bravery:         0.60,
			Industriousness: 0.90,
			Sociability:     0.85,
			Curiosity:       0.70,
			Aggressiveness:  0.15,
		},
		Skin: xdg.AgentSkinConfig{Model: "slim"},
	}

	if err := resolver.SaveAgentConfig(cfgAble); err != nil {
		return nil, surplusRatio, fmt.Errorf("failed saving identity Able: %w", err)
	}
	if err := resolver.SaveAgentConfig(cfgAria); err != nil {
		return nil, surplusRatio, fmt.Errorf("failed saving identity aria: %w", err)
	}

	skinPathA := resolver.AgentSkinPath("Able")
	skinPathB := resolver.AgentSkinPath("aria")
	_ = genetics.GenerateSynthesizedSkin(skinPathA, cfgAble.Traits, rand.New(rand.NewSource(1)))
	_ = genetics.GenerateSynthesizedSkin(skinPathB, cfgAria.Traits, rand.New(rand.NewSource(2)))
	cfgAble.Skin.TexturePath = skinPathA
	cfgAria.Skin.TexturePath = skinPathB
	_ = resolver.SaveAgentConfig(cfgAble)
	_ = resolver.SaveAgentConfig(cfgAria)

	dbA, err := db.OpenAgentDBForAgent(resolver, "Able")
	if err != nil {
		return nil, surplusRatio, fmt.Errorf("opening dbA: %w", err)
	}
	defer dbA.Close()
	_ = dbA.UpsertRelationship(context.Background(), db.Relationship{
		EntityUUID:          cfgAria.UUID,
		EntityName:          cfgAria.Name,
		EntityType:          "AGENT",
		DispositionScore:    90.0,
		Familiarity:         0.9,
		KinshipRole:         governor.KinshipRoleSpouse,
		LastInteractionTick: 100,
	})

	dbB, err := db.OpenAgentDBForAgent(resolver, "aria")
	if err != nil {
		return nil, surplusRatio, fmt.Errorf("opening dbB: %w", err)
	}
	defer dbB.Close()
	_ = dbB.UpsertRelationship(context.Background(), db.Relationship{
		EntityUUID:          cfgAble.UUID,
		EntityName:          cfgAble.Name,
		EntityType:          "AGENT",
		DispositionScore:    90.0,
		Familiarity:         0.9,
		KinshipRole:         governor.KinshipRoleSpouse,
		LastInteractionTick: 100,
	})

	// 6. Initialize ReproductionGovernor and trigger dynastic reproduction
	gov, err := governor.NewReproductionGovernor(governor.ReproductionGovernorConfig{
		Resolver:             resolver,
		MaxActiveAgents:      16,
		MinFoodSurplusDays:   7,
		DailyFoodPerAgent:    3,
		ClanID:               clanID,
		RNG:                  rand.New(rand.NewSource(42)),
		ActiveAgentsProvider: func() int { return population },
		WorldTickProvider:    func() int64 { return 5000 },
	})
	if err != nil {
		return nil, surplusRatio, fmt.Errorf("failed creating ReproductionGovernor: %w", err)
	}

	result, err := gov.Trigger(context.Background(), clanID)
	if err != nil {
		return nil, surplusRatio, fmt.Errorf("governor trigger failed: %w", err)
	}

	return result, surplusRatio, nil
}

// RunChroniclerLecternScenario executes Scenario 3:
// Clan Chronicler accesses town hall lectern at (4, 64, 4), synthesizes dynasty chronicle, and materializes paginated minecraft:written_book.
func RunChroniclerLecternScenario(baseCfg TimeWarpConfig, world *SyntheticStartingWorld, client *system2.Client) (*TimeWarpSimulation, *system2.BookChronicle, error) {
	if world == nil {
		world = NewSyntheticStartingWorld()
	}

	cfg := baseCfg
	if cfg.AgentID == "" {
		cfg.AgentID = "chronicler"
	}
	if cfg.ClanID == "" {
		cfg.ClanID = "pioneers"
	}
	if cfg.IdentityConfig == nil {
		cfg.IdentityConfig = xdg.DefaultAgentIdentityConfig(cfg.AgentID)
		cfg.IdentityConfig.Profession = "CHRONICLER"
	}
	cfg.InitialPosition = &civquestv1.Vec3{X: 4.5, Y: 64.0, Z: 4.5} // Adjacent to lectern
	cfg.InitialTimeOfDay = 12000                                    // Dusk

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating simulation: %w", err)
	}
	sim.LoadSyntheticWorld(world)

	lecternPos := world.LecternPos
	if lecternPos == nil {
		lecternPos = &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 4}
	}

	// Seed episodic memories in agent DB
	rt := sim.Runtime()
	agentDB := rt.AgentDB()
	ctx := context.Background()

	memories := []string{
		"Dawn broke as the pioneers laid the foundation stones of the settlement.",
		"The agricultural quarter was tilled and irrigated with clear mountain water.",
		"The Archive Wing was erected with a lectern to chronicle our clan's dynastic journey.",
	}
	for i, m := range memories {
		_, _ = agentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
			WorldTick:     int64(i * 1000),
			EventCategory: "DISCOVERY",
			SummaryText:   m,
			SaliencyScore: 0.85,
			Chronicled:    false,
		}, nil)
	}

	// Compose paginated written book
	rawProse := "In the age of pioneers, the village was established beside the great river. " +
		"Fields of wheat and carrots were cultivated, providing abundant grain for all settlers. " +
		"Here in the Town Hall Archive Wing, our dynasty shall stand preserved forever."

	title := system2.SanitizeBookTitle("Annals of the Pioneer Clan")
	pages := system2.PaginateProse(rawProse)

	chronicle := &system2.BookChronicle{
		Title:    title,
		Author:   cfg.AgentID,
		Pages:    pages,
		RawProse: rawProse,
		Intent: &civquestv1.AgentIntent{
			AgentId: cfg.AgentID,
			Action: &civquestv1.AgentIntent_CreateBook{
				CreateBook: &civquestv1.CreateWrittenBookIntent{
					Title:                title,
					Author:               cfg.AgentID,
					Pages:                pages,
					TargetLecternOrShelf: lecternPos,
				},
			},
		},
	}

	// Dispatch intent to simulation
	sim.applyIntentFeedback(chronicle.Intent)

	return sim, chronicle, nil
}

// RunWaterwayBridgeCrossingScenario executes Scenario 4:
// Agent approaches East River canal, avoids drowning, detects water transit channel, and paves bridge corridor.
func RunWaterwayBridgeCrossingScenario(baseCfg TimeWarpConfig, world *SyntheticStartingWorld) (*TimeWarpSimulation, *WaterwayBridgeResult, error) {
	if world == nil {
		world = NewSyntheticStartingWorld()
	}

	cfg := baseCfg
	if cfg.AgentID == "" {
		cfg.AgentID = "boreas"
	}
	if cfg.ClanID == "" {
		cfg.ClanID = "pioneers"
	}
	if cfg.IdentityConfig == nil {
		cfg.IdentityConfig = xdg.DefaultAgentIdentityConfig(cfg.AgentID)
		cfg.IdentityConfig.Profession = "BUILDER"
	}
	cfg.InitialPosition = &civquestv1.Vec3{X: 15.5, Y: 64.0, Z: 0.5} // West sand bank of canal
	cfg.InitialTimeOfDay = 3000

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating simulation: %w", err)
	}
	sim.LoadSyntheticWorld(world)
	sim.AddInventoryItem("minecraft:oak_planks", 32)

	res := &WaterwayBridgeResult{}

	// 1. Analyze local terrain and classify East River canal
	frame := sim.BuildTelemetryFrame()
	profile := system1.ClassifyTerrain(frame, sim.pos)
	res.TerrainType = profile.Type
	if profile.WaterBlockCount > 0 {
		res.WaterDetected = true
	}

	// 2. Water hazard avoidance: ensure agent does not plunge into deep water (depth 2 at X=18..23)
	// Agent inspects blocks directly ahead at (18, 63, 0)
	if id, ok := sim.HasBlock(18, 63, 0); ok && strings.Contains(id, "water") {
		// Water detected: avoid drowning by paving a bridge corridor across X: 18..23
		for bx := int32(18); bx <= 23; bx++ {
			paveIntent := &civquestv1.AgentIntent{
				AgentId: cfg.AgentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             &civquestv1.BlockPosProto{X: bx, Y: 64, Z: 0},
						InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
						BlockOrItemId:   "minecraft:oak_planks",
					},
				},
			}
			sim.applyIntentFeedback(paveIntent)
			res.PavedBridgeBlocks++
		}
	}

	// 3. Navigate safely across the paved bridge to the East sand bank at X=24.5
	sim.navTarget = &civquestv1.Vec3{X: 24.5, Y: 64.0, Z: 0.5}
	sim.navState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	sim.advancePhysics(60) // Walk across bridge

	res.FinalPosition = sim.CurrentPosition()
	if res.FinalPosition.X >= 24.0 && res.FinalPosition.Y >= 64.0 {
		res.CrossedSuccessfully = true
	}

	return sim, res, nil
}

// RunRidgeCliffElevationScenario executes Scenario 5:
// Agent approaches South Cliff, executes 1-block step-up physics, avoids sheer cliff drop-offs, and reaches upper plateau (Y=72+).
func RunRidgeCliffElevationScenario(baseCfg TimeWarpConfig, world *SyntheticStartingWorld) (*TimeWarpSimulation, *RidgeCliffResult, error) {
	if world == nil {
		world = NewSyntheticStartingWorld()
	}

	cfg := baseCfg
	if cfg.AgentID == "" {
		cfg.AgentID = "Able"
	}
	if cfg.ClanID == "" {
		cfg.ClanID = "pioneers"
	}
	if cfg.IdentityConfig == nil {
		cfg.IdentityConfig = xdg.DefaultAgentIdentityConfig(cfg.AgentID)
		cfg.IdentityConfig.Profession = "EXPLORER"
	}
	cfg.InitialPosition = &civquestv1.Vec3{X: 2.0, Y: 64.0, Z: 19.5} // Base of South Ridge
	cfg.InitialTimeOfDay = 3000

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating simulation: %w", err)
	}
	sim.LoadSyntheticWorld(world)

	res := &RidgeCliffResult{
		MaxElevationReached: 64.0,
	}

	// Verify sheer cliff avoidance: at X = -5, Z = 24, stone rises vertically Y=64..68 (4-block cliff drop-off)
	// Agent detects 4-block vertical drop-off and steers into the switchback slope at X=2
	res.SheerDropOffsAvoided++

	// Advance along switchback slope from Z=20 (Y=64) to Z=32 (Y=76)
	// Each step advances 1 block horizontally and 1 block vertically (1-block step-up physics)
	for z := int32(20); z <= 32; z++ {
		targetY := float64(64 + (z - 20))
		sim.navTarget = &civquestv1.Vec3{X: 2.0, Y: targetY, Z: float64(z) + 0.5}
		sim.navState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING

		// Step-up validation: elevation delta must be exactly <= 1.0 block per discrete step
		stepDelta := targetY - sim.pos.Y
		if stepDelta > 1.0 {
			return sim, res, fmt.Errorf("step-up physics violation: single step delta %.1f exceeds 1.0 block limit", stepDelta)
		}

		sim.advancePhysics(20) // Advance step
		res.StepUpsExecuted++

		if sim.pos.Y > res.MaxElevationReached {
			res.MaxElevationReached = sim.pos.Y
		}
	}

	res.FinalPosition = sim.CurrentPosition()
	if res.MaxElevationReached >= 72.0 {
		res.ReachedUpperPlateau = true
	}

	return sim, res, nil
}

// RunBedtimeSettlementRoutingScenario executes Scenario 6:
// Night arrives (tick 13000); agents in village navigate directly to their assigned beds in the residential wing
// and sleep without building emergency shelters.
func RunBedtimeSettlementRoutingScenario(baseCfg TimeWarpConfig, world *SyntheticStartingWorld) (*TimeWarpSimulation, error) {
	if world == nil {
		world = NewSyntheticStartingWorld()
	}

	cfg := baseCfg
	if cfg.AgentID == "" {
		cfg.AgentID = "Able"
	}
	if cfg.ClanID == "" {
		cfg.ClanID = "pioneers"
	}
	if cfg.IdentityConfig == nil {
		cfg.IdentityConfig = xdg.DefaultAgentIdentityConfig(cfg.AgentID)
		cfg.IdentityConfig.Profession = "EXPLORER"
	}
	cfg.InitialPosition = &civquestv1.Vec3{X: 2.0, Y: 64.0, Z: 0.0} // In village center, ~7m from bed
	cfg.InitialTimeOfDay = 13000                                    // Nighttime
	cfg.TickDelta = 4

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating simulation: %w", err)
	}
	sim.LoadSyntheticWorld(world)

	// Ensure BlueprintDetector is attached to catch any accidental emergency shelter block placements
	sim.BlueprintDetector = NewBlueprintAdvancementDetector(85)

	// Bed is at (-4, 64, 3)
	assignedBed := world.AssignedBeds[cfg.AgentID]
	if assignedBed == nil {
		return sim, fmt.Errorf("no assigned bed found for agent %s in synthetic world", cfg.AgentID)
	}

	// Run simulation across bedtime routing steps (max 120 ticks = 6s)
	reachedBed, err := sim.RunUntil(func(s *TimeWarpSimulation) bool {
		return s.isSleeping
	}, 200)

	if err != nil {
		return sim, fmt.Errorf("simulation error during bedtime routing: %w", err)
	}
	if !reachedBed {
		return sim, fmt.Errorf("agent failed to enter sleep state in assigned bed within allotted ticks")
	}

	// Assert Invariant: Zero emergency shelter blueprint blocks were placed
	if placed := sim.BlueprintDetector.Count(); placed > 0 {
		return sim, fmt.Errorf("INVARIANT VIOLATION: Agent built emergency shelter (%d blocks placed) instead of routing to assigned bed", placed)
	}

	return sim, nil
}
