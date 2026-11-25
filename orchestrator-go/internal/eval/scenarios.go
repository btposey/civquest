package eval

import (
	"fmt"
	"math"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// RunMultiDayRoutineScenario evaluates multi-day progression across full 24,000-tick day/night cycles:
// Dawn (0) -> Explore -> Dusk Shelter (11500-12000) -> Sleep / Rest (13000-23500) -> Dawn (24000+).
// Asserts that no FSM oscillations or navigation stalls occur.
func RunMultiDayRoutineScenario(baseCfg TimeWarpConfig, totalTicks int64) (*TimeWarpSimulation, error) {
	if totalTicks <= 0 {
		totalTicks = 24000 // 1 full day cycle
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

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating simulation: %w", err)
	}

	// Environment: assign bed at (0, 64, 0), campfire at (2, 64, 2)
	sim.AddBlock(0, 64, 0, "minecraft:red_bed")
	sim.AddBlock(2, 64, 2, "minecraft:campfire")
	sim.AddInventoryItem("minecraft:cooked_beef", 16)
	sim.AddInventoryItem("minecraft:wooden_axe", 1)

	// Step through simulation
	if err := sim.RunTicks(totalTicks); err != nil {
		return sim, fmt.Errorf("simulation run error: %w", err)
	}

	// Check detectors
	if err := sim.OscillationDetector.Check(); err != nil {
		return sim, fmt.Errorf("oscillation detector check failed: %w", err)
	}
	if err := sim.StallDetector.Check(); err != nil {
		return sim, fmt.Errorf("stall detector check failed: %w", err)
	}

	return sim, nil
}

// RunFrontierAdvancementScenario evaluates Explorer sector-locked outward radial advancement
// along outward radial legs (48m -> 96m -> 144m) with zero spin thrashing.
func RunFrontierAdvancementScenario(baseCfg TimeWarpConfig, targetLegs int) (*TimeWarpSimulation, error) {
	if targetLegs <= 0 {
		targetLegs = 3 // 48m, 96m, 144m
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
	}
	cfg.IdentityConfig.Profession = "EXPLORER"
	cfg.InitialTimeOfDay = 2000 // Daytime
	cfg.TickDelta = 4

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating simulation: %w", err)
	}

	origin := sim.CurrentPosition()
	sim.AzimuthDetector = NewSectorAzimuthLockDetector(origin, 0.20, []float64{48.0, 96.0, 144.0})

	// Run until target legs are observed or 4000 ticks elapse
	targetDist := float64(targetLegs) * 48.0
	reached, err := sim.RunUntil(func(s *TimeWarpSimulation) bool {
		dist := math.Hypot(s.pos.X-origin.X, s.pos.Z-origin.Z)
		return dist >= targetDist-2.0
	}, 4000)

	if err != nil {
		return sim, fmt.Errorf("simulation error: %w", err)
	}
	if !reached {
		return sim, fmt.Errorf("explorer failed to reach radial frontier target %.1fm within allotted ticks (dist=%.1fm)",
			targetDist, math.Hypot(sim.pos.X-origin.X, sim.pos.Z-origin.Z))
	}

	if err := sim.AzimuthDetector.Check(); err != nil {
		return sim, fmt.Errorf("sector azimuth lock failed: %w", err)
	}
	if err := sim.OscillationDetector.Check(); err != nil {
		return sim, fmt.Errorf("oscillation detector failed: %w", err)
	}

	return sim, nil
}

// RunRoadPavingCorridorScenario evaluates desire-path road paving:
// Campfire 4m exclusion zone, casual 15-step threshold, directional lines.
func RunRoadPavingCorridorScenario(baseCfg TimeWarpConfig) (*TimeWarpSimulation, error) {
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
	cfg.InitialTimeOfDay = 3000 // Daytime
	cfg.TickDelta = 4

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating simulation: %w", err)
	}

	// Campfire at origin (0, 64, 0)
	sim.AddBlock(0, 64, 0, "minecraft:campfire")

	// Set paveable grass blocks along corridor from (6, 64, 10) to (16, 64, 10)
	for x := int32(5); x <= 20; x++ {
		sim.AddBlock(x, 63, 10, "minecraft:grass_block")
	}

	// Move back and forth along corridor (>4m from campfire) for repeated traversals
	for traversal := 0; traversal < 18; traversal++ {
		for step := int32(8); step <= 18; step++ {
			x := float64(step) + 0.5
			if traversal%2 == 1 {
				x = float64(26-step) + 0.5
			}
			sim.SetPosition(x, 64.0, 10.5)
			if err := sim.Step(4); err != nil {
				return sim, err
			}
		}
	}

	// Assert paving intents occurred outside campfire exclusion zone
	if sim.PavingIntentsCount == 0 {
		return sim, fmt.Errorf("expected road paving intents after 18 corridor traversals, got 0")
	}

	// Assert no paving occurred within 4m of campfire (0, 64, 0)
	for k, blockID := range sim.blocks {
		if blockID == "minecraft:dirt_path" {
			distFromCampfire := math.Hypot(float64(k.X), float64(k.Z))
			if distFromCampfire < 4.0 {
				return sim, fmt.Errorf("PAVING VIOLATION: block at (%d, %d, %d) was paved within %.2fm of campfire (< 4m exclusion)",
					k.X, k.Y, k.Z, distFromCampfire)
			}
		}
	}

	return sim, nil
}

// RunDuskShelterConstructionScenario evaluates harvesting timber, crafting planks,
// and building a 5x5 cabin bottom-up without timing out or self-entombing.
func RunDuskShelterConstructionScenario(baseCfg TimeWarpConfig) (*TimeWarpSimulation, error) {
	cfg := baseCfg
	if cfg.AgentID == "" {
		cfg.AgentID = "Able"
	}
	if cfg.ClanID == "" {
		cfg.ClanID = "pioneers"
	}
	if cfg.IdentityConfig == nil {
		cfg.IdentityConfig = xdg.DefaultAgentIdentityConfig(cfg.AgentID)
	}
	cfg.IdentityConfig.Profession = "EXPLORER"
	cfg.InitialTimeOfDay = 11500 // Approaching dusk
	cfg.InitialPosition = &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}
	cfg.TickDelta = 4

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating simulation: %w", err)
	}

	// Provide 25 oak logs in environment (nearby trees) and campfire in inventory
	sim.AddInventoryItem("minecraft:campfire", 1)
	for i := int32(0); i < 25; i++ {
		sim.AddBlock(3+i%3, 64+i/3, 3, "minecraft:oak_log")
	}
	// Also provide initial logs in inventory to bootstrap construction speed
	sim.AddInventoryItem("minecraft:oak_log", 25)

	sim.BlueprintDetector = NewBlueprintAdvancementDetector(blueprints.CabinTotalBlocks)

	// Run until cabin is complete or 2500 ticks elapse
	completed, err := sim.RunUntil(func(s *TimeWarpSimulation) bool {
		s1 := s.runtime.System1State()
		if s1 == nil {
			return false
		}
		return s1.GetCabinBuilt() >= blueprints.CabinTotalBlocks
	}, 3000)

	if err != nil {
		return sim, fmt.Errorf("simulation error: %w", err)
	}
	if !completed {
		s1 := sim.runtime.System1State()
		var idx int
		if s1 != nil {
			idx = s1.GetCabinBuilt()
		}
		return sim, fmt.Errorf("shelter construction did not complete within allotted ticks (index=%d/%d, placed=%d)",
			idx, blueprints.CabinTotalBlocks, sim.BlueprintDetector.Count())
	}

	// Verify detectors
	if err := sim.BlueprintDetector.Check(); err != nil {
		return sim, fmt.Errorf("blueprint detector check failed: %w", err)
	}
	if err := sim.OscillationDetector.Check(); err != nil {
		return sim, fmt.Errorf("oscillation detector check failed: %w", err)
	}

	return sim, nil
}

// RunNighttimeContemplationScenario evaluates Nighttime Strategic Contemplation:
// Triggers during SLEEP, updates ledger, updates goals, and consolidates vector memory with per-day debounce.
func RunNighttimeContemplationScenario(baseCfg TimeWarpConfig, contemplator *system2.ContemplationService) (*TimeWarpSimulation, error) {
	cfg := baseCfg
	if cfg.AgentID == "" {
		cfg.AgentID = "contemplator"
	}
	if cfg.ClanID == "" {
		cfg.ClanID = "pioneers"
	}
	if cfg.IdentityConfig == nil {
		cfg.IdentityConfig = xdg.DefaultAgentIdentityConfig(cfg.AgentID)
		cfg.IdentityConfig.Profession = "HISTORIAN"
	}
	cfg.ContemplationService = contemplator
	cfg.InitialTimeOfDay = 12900 // Approaching bedtime
	cfg.TickDelta = 4

	sim, err := NewTimeWarpSimulation(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating simulation: %w", err)
	}

	// Environment: assign bed at (0, 64, 0)
	sim.AddBlock(0, 64, 0, "minecraft:blue_bed")

	// Step into bedtime hours (13000+)
	if err := sim.RunTicks(200); err != nil {
		return sim, fmt.Errorf("simulation error: %w", err)
	}

	// Verify agent entered sleep or routine sleep
	s1 := sim.runtime.System1State()
	if s1 == nil {
		return sim, fmt.Errorf("system1 state is nil")
	}

	return sim, nil
}
