package integration

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog/watchdogtest"
)

func TestIntegration_BarrenDesertBiome_MultiHopUntilOasisDiscovery(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// Determine the solo desert nomad's deterministic initial open-plains heading so we can place
	// a single distant oasis at ~108m along that corridor while keeping <85m from spawn 100% barren.
	// At spawn (0, 64, 0), all 36 sectors within 64m have ZERO trees and ZERO water; only after
	// chaining multiple open-desert hops (>60m) can the 64m raycast even reach the oasis.
	const oasisDist = 108.0
	oasisX, oasisZ := 108.0, 0.0
	// Place oasis East (270°); guide initial sector choice slightly by placing sand dunes in the
	// opposite western arc (60°..120°) so all open desert sectors are barren OPEN_PLAINS, and also
	// compute the exact open bearing that "desert_nomad" picks on hop 0 if no bias existed.
	// Better: keep dunes only on 4 sectors (SW/W) and place the oasis at ~108m along the natural
	// first-hop azimuth of "desert_nomad" so there is zero artificial funneling.
	probeFrame := &civquestv1.TelemetryFrame{
		WorldTick: 8,
		BiomeId:   "minecraft:desert",
		Status: &civquestv1.AgentStatus{
			AgentId:   "desert_nomad",
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
		},
	}
	probeState := system1.NewAgentState()
	probeSectors := make([]*civquestv1.HorizonSector, 0, 36)
	for i := int32(0); i < 36; i++ {
		b := float32(i * 10)
		rad := float64(b) * math.Pi / 180.0
		dx, dz := -math.Sin(rad), math.Cos(rad)
		isDune := b >= 40 && b <= 80
		feat := "OPEN_PLAINS"
		blk := "minecraft:sand"
		hitDist := float32(64.0)
		occluded := false
		openH := true
		if isDune {
			feat = "RIDGE_OBSTRUCTION"
			blk = "minecraft:sandstone"
			hitDist = 26.0
			occluded = true
			openH = false
		}
		probeSectors = append(probeSectors, &civquestv1.HorizonSector{
			SectorIndex: i,
			BearingDeg:  b,
			HitDistance: hitDist,
			FeatureType: feat,
			HitBlockId:  blk,
			HitX:        dx * float64(hitDist),
			HitY:        64,
			HitZ:        dz * float64(hitDist),
			Occluded:    occluded,
			OpenHorizon: openH,
		})
	}
	probeState.IngestHorizonSurveyReport(&civquestv1.HorizonSurveyReport{
		OriginX: 0, OriginY: 64, OriginZ: 0, CompletedTick: 8, MaxRange: 64, Sectors: probeSectors,
	}, 8)
	if tx, tz, ok := system1.PlanExpeditionTarget("desert_nomad", probeFrame, probeState); ok {
		norm := math.Hypot(tx, tz)
		if norm > 1.0 {
			oasisX = math.Round((tx / norm) * oasisDist)
			oasisZ = math.Round((tz / norm) * oasisDist)
		}
	}

	world := &simWorld{
		biomeID: "minecraft:desert",
		blocks:  make(map[worldBlockKey]string),
		forests: []forestStand{
			{Name: "desert_oasis_palms", CenterX: oasisX, CenterZ: oasisZ, Radius: 14.0, Species: "oak"},
		},
		ponds: []waterPond{
			{CenterX: oasisX + 6.0, CenterZ: oasisZ + 6.0, Radius: 8.5},
		},
	}

	// Populate sand floor near spawn and oasis trees/water at ~108m (nothing within 85m of spawn).
	for dx := int32(-12); dx <= 12; dx += 4 {
		for dz := int32(-12); dz <= 12; dz += 4 {
			world.blocks[worldBlockKey{X: dx, Y: 63, Z: dz}] = "minecraft:sand"
		}
	}
	for _, f := range world.forests {
		cx := int32(math.Round(f.CenterX))
		cz := int32(math.Round(f.CenterZ))
		for _, off := range [][2]int32{{0, 0}, {5, 3}, {-5, -3}, {3, -5}} {
			tx, tz := cx+off[0], cz+off[1]
			for dy := int32(0); dy <= 3; dy++ {
				world.blocks[worldBlockKey{X: tx, Y: 64 + dy, Z: tz}] = "minecraft:oak_log"
			}
			for dx := int32(-1); dx <= 1; dx++ {
				for dz := int32(-1); dz <= 1; dz++ {
					if dx != 0 || dz != 0 {
						world.blocks[worldBlockKey{X: tx + dx, Y: 67, Z: tz + dz}] = "minecraft:oak_leaves"
					}
				}
			}
		}
	}
	for _, p := range world.ponds {
		cx := int32(math.Round(p.CenterX))
		cz := int32(math.Round(p.CenterZ))
		for dx := int32(-4); dx <= 4; dx++ {
			for dz := int32(-4); dz <= 4; dz++ {
				if float64(dx*dx+dz*dz) <= p.Radius*p.Radius {
					world.blocks[worldBlockKey{X: cx + dx, Y: 63, Z: cz + dz}] = "minecraft:water"
				}
			}
		}
	}

	world.customRaycast = func(ox, oy, oz float64, completedTick int64, numSectors int32, maxRange float32) *civquestv1.HorizonSurveyReport {
		if numSectors <= 0 {
			numSectors = 36
		}
		if maxRange <= 0 {
			maxRange = 64.0
		}
		stepDeg := 360.0 / float64(numSectors)
		sectors := make([]*civquestv1.HorizonSector, 0, numSectors)
		for i := int32(0); i < numSectors; i++ {
			bearing := float32(float64(i) * stepDeg)
			rad := float64(bearing) * math.Pi / 180.0
			dx, dz := -math.Sin(rad), math.Cos(rad)

			bestDist := float64(maxRange)
			bestFeat := "OPEN_PLAINS"
			bestBlock := "minecraft:sand"
			bestY := oy
			occluded := false
			openHorizon := true

			for dist := 2.0; dist <= float64(maxRange); dist += 1.5 {
				rx := ox + dx*dist
				rz := oz + dz*dist

				// Fixed desert sand dunes in the 40..80 deg arc near spawn (dist ~26m from spawn).
				if math.Hypot(ox, oz) < 15.0 && bearing >= 40 && bearing <= 80 && dist >= 26.0 {
					bestDist = dist
					bestFeat = "RIDGE_OBSTRUCTION"
					bestBlock = "minecraft:sandstone"
					bestY = oy + 5.0
					occluded = true
					openHorizon = false
					break
				}

				hitOasis := false
				for _, f := range world.forests {
					if math.Hypot(rx-f.CenterX, rz-f.CenterZ) <= f.Radius {
						bestDist = dist
						bestFeat = "FOREST_CANOPY"
						bestBlock = "minecraft:oak_leaves"
						bestY = oy + 4.0
						openHorizon = false
						hitOasis = true
						break
					}
				}
				if hitOasis {
					break
				}
				for _, p := range world.ponds {
					if math.Hypot(rx-p.CenterX, rz-p.CenterZ) <= p.Radius {
						bestDist = dist
						bestFeat = "WATER"
						bestBlock = "minecraft:water"
						bestY = oy - 1.0
						openHorizon = false
						hitOasis = true
						break
					}
				}
				if hitOasis {
					break
				}
			}

			sectors = append(sectors, &civquestv1.HorizonSector{
				SectorIndex:    i,
				BearingDeg:     bearing,
				HitDistance:    float32(bestDist),
				FeatureType:    bestFeat,
				HitBlockId:     bestBlock,
				HitX:           ox + dx*bestDist,
				HitY:           bestY,
				HitZ:           oz + dz*bestDist,
				ElevationDelta: float32(bestY - oy),
				Occluded:       occluded,
				OpenHorizon:    openHorizon,
			})
		}
		return &civquestv1.HorizonSurveyReport{
			OriginX: ox, OriginY: oy, OriginZ: oz,
			CompletedTick: completedTick, MaxRange: maxRange, Sectors: sectors,
		}
	}

	sa := newSimAgent(
		t, "desert_nomad", "desert_solo",
		civquestv1.Vec3{X: 0.0, Y: 64.0, Z: 0.0},
		res, nil, nil, world, false,
	)
	defer sa.close()
	sa.runtime.System1State().SetClanSize(1)

	const tickDelta = int64(4)
	for frameIdx := 0; frameIdx < 500; frameIdx++ {
		worldTick := int64(4) + int64(frameIdx)*tickDelta
		tod := (2000 + int64(frameIdx)*tickDelta) % 24000
		sa.stepPhysics(worldTick, tickDelta)
		frame := sa.buildFrame(worldTick, tod)
		sa.runtime.HandleTelemetryFrame(frame)
		intents := sa.runtime.DrainIntents()
		sa.applyIntents(intents, worldTick)
	}
	sa.runtime.AwaitPOIPersistence()

	st := sa.runtime.System1State()
	_, hops, _, _ := sa.runtime.AgentFSM().ExpeditionStatus()
	distToOasis := math.Hypot(sa.pos.X-oasisX, sa.pos.Z-oasisZ)
	oasisPOIs := st.SpatialIndex().FindPOIsInRadius(&civquestv1.Vec3{X: oasisX, Y: 64, Z: oasisZ}, system1.POICategoryResource, 32.0)

	t.Logf("[DESERT-DIAG] surveys=%d/%d hops=%d maxDist=%.1fm distToOasis=%.1fm oasisPOIs=%d homesteadClaimed=%v phase=%s inFlightDup=%d maxSameSpot=%d pos=(%.1f,%.1f)",
		len(sa.surveyEmissions), sa.surveysCompleted, hops, sa.maxDistFromSpawn,
		distToOasis, len(oasisPOIs), st.HomesteadClaimed, sa.runtime.AgentFSM().ActiveActivityKind(),
		sa.surveyWhileInFlight, sa.maxConsecSameSpotSurvey, sa.pos.X, sa.pos.Z)

	if sa.surveysCompleted < 2 {
		t.Errorf("FAIL: expected >= 2 completed surveys across desert hops, got %d", sa.surveysCompleted)
	}
	if hops < 2 && !st.HomesteadClaimed {
		t.Errorf("FAIL: expected >= 2 chained expedition hops across open desert, got %d", hops)
	}
	if sa.maxDistFromSpawn <= 60.0 {
		t.Errorf("FAIL: desert explorer stalled before traversing > 60m from spawn (maxDist=%.1fm)", sa.maxDistFromSpawn)
	}
	if sa.surveyWhileInFlight > 2 {
		t.Errorf("FAIL: duplicate survey storm while in-flight (%d)", sa.surveyWhileInFlight)
	}
	if sa.maxConsecSameSpotSurvey > 2 {
		t.Errorf("FAIL: explorer clustered with %d consecutive same-spot surveys (want <= 2)", sa.maxConsecSameSpotSurvey)
	}
	if len(oasisPOIs) == 0 && distToOasis > 30.0 {
		t.Errorf("FAIL: desert explorer failed to discover or reach distant oasis at (%.1f, %.1f): oasisPOIs=0 distToOasis=%.1fm",
			oasisX, oasisZ, distToOasis)
	}

	// Verify oasis survey sectors persisted into AgentDB
	sectors, err := sa.agentDB.GetRecentHorizonSectors(ctx, 200)
	if err != nil {
		t.Fatalf("GetRecentHorizonSectors: %v", err)
	}
	if len(sectors) < 72 {
		t.Errorf("FAIL: expected >= 72 persisted horizon_sectors across >=2 surveys, got %d", len(sectors))
	}
}

func TestIntegration_SurfaceLavaPoolAndRiverBarrier_PauseResurveyAndDetour(t *testing.T) {
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// Lava pool + river edge across z in [-28, -16], x in [-18, 18], directly blocking
	// the route from spawn (0, 64, 0) to the distant northern forest at (0, 64, -50).
	const (
		lavaMinX = -18.0
		lavaMaxX = 18.0
		lavaMinZ = -28.0
		lavaMaxZ = -16.0
	)
	inLavaZone := func(x, z float64) bool {
		return x >= lavaMinX && x <= lavaMaxX && z >= lavaMinZ && z <= lavaMaxZ
	}

	world := &simWorld{
		biomeID: "minecraft:plains",
		blocks:  make(map[worldBlockKey]string),
		forests: []forestStand{
			{Name: "north_oak", CenterX: 0, CenterZ: -50, Radius: 9.0, Species: "oak"},
			{Name: "nw_birch", CenterX: -42, CenterZ: -38, Radius: 9.0, Species: "birch"},
			{Name: "ne_oak", CenterX: 42, CenterZ: -38, Radius: 9.0, Species: "oak"},
		},
		ponds: []waterPond{
			{CenterX: -46, CenterZ: -44, Radius: 6.5},
			{CenterX: 46, CenterZ: -44, Radius: 6.5},
		},
	}

	// Populate lava pool and bordering river blocks so InterestingBlocks contains lava & water in frustum.
	for x := int32(-18); x <= 18; x += 2 {
		for z := int32(-28); z <= -16; z += 2 {
			world.blocks[worldBlockKey{X: x, Y: 63, Z: z}] = "minecraft:lava"
		}
		world.blocks[worldBlockKey{X: x, Y: 63, Z: -30}] = "minecraft:water"
	}
	for _, f := range world.forests {
		cx := int32(math.Round(f.CenterX))
		cz := int32(math.Round(f.CenterZ))
		for _, off := range [][2]int32{{0, 0}, {4, 2}, {-4, -2}} {
			tx, tz := cx+off[0], cz+off[1]
			for dy := int32(0); dy <= 3; dy++ {
				world.blocks[worldBlockKey{X: tx, Y: 64 + dy, Z: tz}] = "minecraft:" + f.Species + "_log"
			}
			world.blocks[worldBlockKey{X: tx, Y: 68, Z: tz}] = "minecraft:" + f.Species + "_leaves"
		}
	}

	world.isBlocked = func(x, y, z float64) (bool, string) {
		if inLavaZone(x, z) {
			return true, "navigation_stuck_lava_river_barrier"
		}
		return false, ""
	}

	world.customRaycast = func(ox, oy, oz float64, completedTick int64, numSectors int32, maxRange float32) *civquestv1.HorizonSurveyReport {
		if numSectors <= 0 {
			numSectors = 36
		}
		if maxRange <= 0 {
			maxRange = 64.0
		}
		stepDeg := 360.0 / float64(numSectors)
		sectors := make([]*civquestv1.HorizonSector, 0, numSectors)
		for i := int32(0); i < numSectors; i++ {
			bearing := float32(float64(i) * stepDeg)
			rad := float64(bearing) * math.Pi / 180.0
			dx, dz := -math.Sin(rad), math.Cos(rad)

			bestDist := float64(maxRange)
			bestFeat := "OPEN_PLAINS"
			bestBlock := "minecraft:grass_block"
			bestY := oy
			occluded := false
			openHorizon := true

			for dist := 2.0; dist <= float64(maxRange); dist += 1.5 {
				rx := ox + dx*dist
				rz := oz + dz*dist

				// When near the lava/river edge, close-range surface hazard blocks the forward sectors.
				if inLavaZone(rx, rz) && dist < 12.0 && oz > lavaMaxZ-1.5 {
					bestDist = dist
					bestFeat = "RIDGE_OBSTRUCTION"
					bestBlock = "minecraft:lava"
					bestY = oy - 1.0
					occluded = true
					openHorizon = false
					break
				}

				hitFeature := false
				for _, f := range world.forests {
					if math.Hypot(rx-f.CenterX, rz-f.CenterZ) <= f.Radius {
						bestDist = dist
						bestFeat = "FOREST_CANOPY"
						bestBlock = "minecraft:" + f.Species + "_leaves"
						bestY = oy + 4.0
						openHorizon = false
						hitFeature = true
						break
					}
				}
				if hitFeature {
					break
				}
				for _, p := range world.ponds {
					if math.Hypot(rx-p.CenterX, rz-p.CenterZ) <= p.Radius {
						bestDist = dist
						bestFeat = "WATER"
						bestBlock = "minecraft:water"
						bestY = oy - 1.0
						openHorizon = false
						hitFeature = true
						break
					}
				}
				if hitFeature {
					break
				}
			}

			sectors = append(sectors, &civquestv1.HorizonSector{
				SectorIndex:    i,
				BearingDeg:     bearing,
				HitDistance:    float32(bestDist),
				FeatureType:    bestFeat,
				HitBlockId:     bestBlock,
				HitX:           ox + dx*bestDist,
				HitY:           bestY,
				HitZ:           oz + dz*bestDist,
				ElevationDelta: float32(bestY - oy),
				Occluded:       occluded,
				OpenHorizon:    openHorizon,
			})
		}
		return &civquestv1.HorizonSurveyReport{
			OriginX: ox, OriginY: oy, OriginZ: oz,
			CompletedTick: completedTick, MaxRange: maxRange, Sectors: sectors,
		}
	}

	sa := newSimAgent(
		t, "lava_scout", "pioneers",
		civquestv1.Vec3{X: 0.0, Y: 64.0, Z: 0.0},
		res, nil, nil, world, false,
	)
	defer sa.close()

	const tickDelta = int64(4)
	for frameIdx := 0; frameIdx < 400; frameIdx++ {
		worldTick := int64(4) + int64(frameIdx)*tickDelta
		tod := (2000 + int64(frameIdx)*tickDelta) % 24000
		sa.stepPhysics(worldTick, tickDelta)
		frame := sa.buildFrame(worldTick, tod)
		sa.runtime.HandleTelemetryFrame(frame)
		intents := sa.runtime.DrainIntents()
		sa.applyIntents(intents, worldTick)
	}

	// Assert explorer NEVER entered the lava zone
	for _, p := range sa.visitedPositions {
		if inLavaZone(p.X, p.Z) {
			t.Fatalf("FAIL: explorer entered lava pool zone at (%.2f, %.2f)", p.X, p.Z)
		}
	}

	hazards := sa.runtime.System1State().SpatialIndex().FindPOIsInRadius(&sa.pos, system1.POICategoryHazard, 128.0)
	if len(hazards) == 0 && sa.barrierRimPos != nil {
		hazards = sa.runtime.System1State().SpatialIndex().FindPOIsInRadius(sa.barrierRimPos, system1.POICategoryHazard, 64.0)
	}
	escaped := sa.barrierRimPos != nil &&
		(sa.pos.X < lavaMinX-2.0 || sa.pos.X > lavaMaxX+2.0 || sa.pos.Z < lavaMinZ-2.0 || sa.pos.Z > lavaMaxZ+10.0) &&
		sa.maxDistFromSpawn > 25.0

	t.Logf("[LAVA-DIAG] stuckCount=%d hazards=%d rimResurveyed=%v surveys=%d escaped=%v maxDist=%.1fm finalPos=(%.1f,%.1f) phase=%s",
		sa.barrierStuckCount, len(hazards), sa.barrierResurveyed, len(sa.surveyEmissions),
		escaped, sa.maxDistFromSpawn, sa.pos.X, sa.pos.Z, sa.runtime.AgentFSM().ActiveActivityKind())

	if sa.barrierStuckCount == 0 {
		t.Fatalf("Test setup error: scout never reached the lava/river barrier edge (finalPos=(%.1f,%.1f))", sa.pos.X, sa.pos.Z)
	}
	if len(hazards) == 0 {
		t.Errorf("FAIL: expected HAZARD POI recorded in SpatialIndex at lava/river barrier, got 0")
	}
	if !sa.barrierResurveyed {
		t.Errorf("FAIL: expected explorer to pause at lava/river edge and emit a fresh 360° rim survey")
	}
	if !escaped {
		t.Errorf("FAIL: expected explorer to detour cleanly around lava/river barrier and progress (finalPos=(%.1f,%.1f), maxDist=%.1fm)",
			sa.pos.X, sa.pos.Z, sa.maxDistFromSpawn)
	}
	if sa.barrierStuckCount > 4 {
		t.Errorf("FAIL: explorer repeatedly bumped into lava/river barrier %d times (want <= 4)", sa.barrierStuckCount)
	}
}

func TestIntegration_AggroMobClusterAmbushMidSurvey_PreemptSurviveAndResume(t *testing.T) {
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	world := &simWorld{
		biomeID: "minecraft:plains",
		blocks:  make(map[worldBlockKey]string),
		forests: []forestStand{
			{Name: "east_oak", CenterX: 48, CenterZ: 0, Radius: 9.0, Species: "oak"},
			{Name: "south_birch", CenterX: 0, CenterZ: 48, Radius: 9.0, Species: "birch"},
		},
		ponds: []waterPond{
			{CenterX: 54, CenterZ: 8, Radius: 6.5},
		},
	}
	for _, f := range world.forests {
		cx := int32(math.Round(f.CenterX))
		cz := int32(math.Round(f.CenterZ))
		for dy := int32(0); dy <= 3; dy++ {
			world.blocks[worldBlockKey{X: cx, Y: 64 + dy, Z: cz}] = "minecraft:" + f.Species + "_log"
		}
		world.blocks[worldBlockKey{X: cx, Y: 68, Z: cz}] = "minecraft:" + f.Species + "_leaves"
	}

	sa := newSimAgent(
		t, "ambush_scout", "pioneers",
		civquestv1.Vec3{X: 0.0, Y: 64.0, Z: 0.0},
		res, nil, nil, world, false,
	)
	defer sa.close()

	ambushInjected := false
	preemptedIntoEmergency := false
	emittedFleeAttack := false
	emittedFleeNav := false
	exitedEmergencyAfterFlee := false
	surveysCompletedPostEscape := 0
	surveysAtAmbush := 0

	const tickDelta = int64(4)
	for frameIdx := 0; frameIdx < 350; frameIdx++ {
		worldTick := int64(4) + int64(frameIdx)*tickDelta
		tod := (2000 + int64(frameIdx)*tickDelta) % 24000

		sa.stepPhysics(worldTick, tickDelta)

		// Mid-survey (around tick 24, while a 72-tick SurveyHorizonIntent is active), inject an aggro mob cluster.
		if !ambushInjected && worldTick >= 24 && sa.surveyActive != nil {
			ambushInjected = true
			surveysAtAmbush = sa.surveysCompleted
			world.mu.Lock()
			world.entities = []*civquestv1.VisibleEntity{
				{
					EntityId:   9101,
					Uuid:       "00000000-0000-0000-0000-000000009101",
					EntityType: "minecraft:zombie",
					Position:   &civquestv1.Vec3{X: sa.pos.X - 4.5, Y: sa.pos.Y, Z: sa.pos.Z - 1.0},
					Health:     20,
					IsHostile:  true,
				},
				{
					EntityId:   9102,
					Uuid:       "00000000-0000-0000-0000-000000009102",
					EntityType: "minecraft:zombie",
					Position:   &civquestv1.Vec3{X: sa.pos.X - 5.2, Y: sa.pos.Y, Z: sa.pos.Z + 1.5},
					Health:     20,
					IsHostile:  true,
				},
				{
					EntityId:   9103,
					Uuid:       "00000000-0000-0000-0000-000000009103",
					EntityType: "minecraft:skeleton",
					HeldItemId: "minecraft:bow",
					Position:   &civquestv1.Vec3{X: sa.pos.X - 4.0, Y: sa.pos.Y, Z: sa.pos.Z + 3.0},
					Health:     20,
					IsHostile:  true,
				},
				{
					EntityId:   9104,
					Uuid:       "00000000-0000-0000-0000-000000009104",
					EntityType: "minecraft:creeper",
					FuseTicks:  10,
					Position:   &civquestv1.Vec3{X: sa.pos.X - 4.0, Y: sa.pos.Y, Z: sa.pos.Z - 2.0},
					Health:     20,
					IsHostile:  true,
				},
			}
			world.mu.Unlock()

			// Drop HP to 6.0 (<= 0.35 * 20) and deliver melee ambush damage event.
			sa.health = 6.0
			sa.lastDamageTaken = &civquestv1.DamageEvent{
				AttackerEntityId: 9101,
				AttackerName:     "Zombie",
				DamageAmount:     14.0,
				SourceType:       civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB,
			}
		}

		frame := sa.buildFrame(worldTick, tod)
		sa.runtime.HandleTelemetryFrame(frame)
		intents := sa.runtime.DrainIntents()
		sa.applyIntents(intents, worldTick)

		if ambushInjected {
			stage := sa.runtime.FSMState()
			if stage == system1.StageEmergency {
				preemptedIntoEmergency = true
				for _, in := range intents {
					if atk := in.GetAttack(); atk != nil && atk.FleeImmediately {
						emittedFleeAttack = true
					}
					if nav := in.GetNavigate(); nav != nil && nav.Target != nil {
						// Moving away from mob cluster centroid (~ -4.4, 0.4)
						if math.Hypot(nav.Target.X-(-4.4), nav.Target.Z-0.4) > math.Hypot(sa.pos.X-(-4.4), sa.pos.Z-0.4) {
							emittedFleeNav = true
						}
					}
				}
			}

			distFromCluster := math.Hypot(sa.pos.X-(-4.4), sa.pos.Z-0.4)
			if preemptedIntoEmergency && distFromCluster > 18.0 && stage != system1.StageEmergency {
				exitedEmergencyAfterFlee = true
			}
		}
	}
	surveysCompletedPostEscape = sa.surveysCompleted - surveysAtAmbush

	t.Logf("[AMBUSH-DIAG] injected=%v preempted=%v fleeAtk=%v fleeNav=%v exitedEmergency=%v postSurveys=%d inFlightDup=%d maxSameSpot=%d finalHP=%.1f finalStage=%s pos=(%.1f,%.1f)",
		ambushInjected, preemptedIntoEmergency, emittedFleeAttack, emittedFleeNav,
		exitedEmergencyAfterFlee, surveysCompletedPostEscape,
		sa.surveyWhileInFlight, sa.maxConsecSameSpotSurvey, sa.health,
		sa.runtime.FSMState(), sa.pos.X, sa.pos.Z)

	if !ambushInjected {
		t.Fatalf("Test setup error: mid-survey ambush was never injected")
	}
	if !preemptedIntoEmergency {
		t.Errorf("FAIL: System 1 did not preempt SURVEYING into StageEmergency upon mob cluster ambush")
	}
	if !emittedFleeAttack {
		t.Errorf("FAIL: expected AttackIntent(FleeImmediately=true) during ambush emergency")
	}
	if !emittedFleeNav {
		t.Errorf("FAIL: expected flee NavigateIntent moving away from mob cluster centroid")
	}
	if !exitedEmergencyAfterFlee {
		t.Errorf("FAIL: agent remained locked in StageEmergency after fleeing >18m at HP=%.1f (finalStage=%s)",
			sa.health, sa.runtime.FSMState())
	}
	if surveysCompletedPostEscape < 1 {
		t.Errorf("FAIL: expected agent to resume and complete a fresh 360° survey after escaping ambush, got %d",
			surveysCompletedPostEscape)
	}
	if sa.surveyWhileInFlight > 2 || sa.maxConsecSameSpotSurvey > 2 {
		t.Errorf("FAIL: duplicate survey storm detected (inFlight=%d, maxSameSpot=%d)",
			sa.surveyWhileInFlight, sa.maxConsecSameSpotSurvey)
	}
}

func TestIntegration_AlpineMountainPassAndUndergroundCaveRecovery(t *testing.T) {
	t.Run("AlpineMountainPass", func(t *testing.T) {
		tmpDir := t.TempDir()
		res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

		// Steep alpine valley in stony_peaks:
		// 30 of 36 bearings are occluded cliff walls (<10m).
		// Narrow 6-bearing open pass at bearings 150°..200° (North, -Z) leads out to a forest at (0, 96, -46).
		isInPassArc := func(x, z float64) bool {
			if math.Hypot(x, z) < 7.5 {
				return true
			}
			b := math.Mod(math.Atan2(-x, z)*180.0/math.Pi+360.0, 360.0)
			return b >= 150.0 && b <= 200.0
		}

		world := &simWorld{
			biomeID: "minecraft:stony_peaks",
			blocks:  make(map[worldBlockKey]string),
			forests: []forestStand{
				{Name: "pass_spruce", CenterX: 0, CenterZ: -46, Radius: 9.0, Species: "spruce"},
			},
			ponds: []waterPond{
				{CenterX: -4, CenterZ: -54, Radius: 5.5},
			},
		}
		world.blocks[worldBlockKey{X: 0, Y: 96, Z: -46}] = "minecraft:spruce_log"
		world.blocks[worldBlockKey{X: 0, Y: 100, Z: -46}] = "minecraft:spruce_leaves"

		world.isBlocked = func(x, y, z float64) (bool, string) {
			if !isInPassArc(x, z) {
				return true, "navigation_stuck_alpine_cliff"
			}
			return false, ""
		}

		world.customRaycast = func(ox, oy, oz float64, completedTick int64, numSectors int32, maxRange float32) *civquestv1.HorizonSurveyReport {
			if numSectors <= 0 {
				numSectors = 36
			}
			if maxRange <= 0 {
				maxRange = 64.0
			}
			stepDeg := 360.0 / float64(numSectors)
			sectors := make([]*civquestv1.HorizonSector, 0, numSectors)
			for i := int32(0); i < numSectors; i++ {
				bearing := float32(float64(i) * stepDeg)
				rad := float64(bearing) * math.Pi / 180.0
				dx, dz := -math.Sin(rad), math.Cos(rad)

				bestDist := float64(maxRange)
				bestFeat := "OPEN_PLAINS"
				bestBlock := "minecraft:stone"
				bestY := oy
				occluded := false
				openHorizon := true

				// Inside the valley origin (<12m from spawn), 30 of 36 bearings hit steep cliff walls at 7m.
				if math.Hypot(ox, oz) < 12.0 && (bearing < 150.0 || bearing > 200.0) {
					bestDist = 7.0
					bestFeat = "RIDGE_OBSTRUCTION"
					bestBlock = "minecraft:stone"
					bestY = oy + 18.0
					occluded = true
					openHorizon = false
				} else {
					for dist := 2.0; dist <= float64(maxRange); dist += 1.5 {
						rx := ox + dx*dist
						rz := oz + dz*dist
						if math.Hypot(rx-0, rz-(-46)) <= 9.0 {
							bestDist = dist
							bestFeat = "FOREST_CANOPY"
							bestBlock = "minecraft:spruce_leaves"
							bestY = oy + 4.0
							openHorizon = false
							break
						}
					}
				}

				sectors = append(sectors, &civquestv1.HorizonSector{
					SectorIndex:    i,
					BearingDeg:     bearing,
					HitDistance:    float32(bestDist),
					FeatureType:    bestFeat,
					HitBlockId:     bestBlock,
					HitX:           ox + dx*bestDist,
					HitY:           bestY,
					HitZ:           oz + dz*bestDist,
					ElevationDelta: float32(bestY - oy),
					Occluded:       occluded,
					OpenHorizon:    openHorizon,
				})
			}
			return &civquestv1.HorizonSurveyReport{
				OriginX: ox, OriginY: oy, OriginZ: oz,
				CompletedTick: completedTick, MaxRange: maxRange, Sectors: sectors,
			}
		}

		sa := newSimAgent(
			t, "alpine_scout", "pioneers",
			civquestv1.Vec3{X: 0.0, Y: 96.0, Z: 0.0},
			res, nil, nil, world, false,
		)
		defer sa.close()
		watchdogtest.Expect(t, sa.runtime.AgentFSM(), watchdog.Thrash, "plan",
			"cq-3sa.4: plan and bootstrap's expedition pull the agent back and forth")

		const tickDelta = int64(4)
		for frameIdx := 0; frameIdx < 250; frameIdx++ {
			worldTick := int64(4) + int64(frameIdx)*tickDelta
			tod := (2000 + int64(frameIdx)*tickDelta) % 24000
			sa.stepPhysics(worldTick, tickDelta)
			frame := sa.buildFrame(worldTick, tod)
			sa.runtime.HandleTelemetryFrame(frame)
			intents := sa.runtime.DrainIntents()
			sa.applyIntents(intents, worldTick)
		}

		var firstPassBearing float32
		if len(sa.navHistory) > 0 && sa.navHistory[0].Target != nil {
			tgt := sa.navHistory[0].Target
			firstPassBearing = float32(math.Mod(math.Atan2(-tgt.X, tgt.Z)*180.0/math.Pi+360.0, 360.0))
		}

		t.Logf("[ALPINE-DIAG] firstBearing=%.1f° cliffStuck=%d maxDist=%.1fm finalPos=(%.1f,%.1f) navCount=%d",
			firstPassBearing, sa.barrierStuckCount, sa.maxDistFromSpawn, sa.pos.X, sa.pos.Z, len(sa.navHistory))

		if len(sa.navHistory) == 0 {
			t.Fatalf("FAIL: alpine explorer emitted 0 navigation intents")
		}
		if firstPassBearing < 150.0 || firstPassBearing > 200.0 {
			t.Errorf("FAIL: expected initial bearing through open pass [150°, 200°], got %.1f°", firstPassBearing)
		}
		if sa.barrierStuckCount > 0 {
			t.Errorf("FAIL: expected explorer to avoid occluded cliff walls cleanly (barrierStuckCount=%d)", sa.barrierStuckCount)
		}
		if sa.maxDistFromSpawn <= 30.0 {
			t.Errorf("FAIL: expected explorer to traverse > 30m out of alpine valley, got %.1fm", sa.maxDistFromSpawn)
		}
	})

	t.Run("UndergroundCaveEnclosedRecovery", func(t *testing.T) {
		tmpDir := t.TempDir()
		res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

		// Agent spawns in an underground stone cave at (0.5, 40.0, 0.5).
		// Enclosed pocket sealed by stone & coal_ore around x=1 (step1X=1, step2X=2), with an
		// open exit tunnel once blocks at (1, 41, 0) and (2, 41, 0) (or step-up) are cleared so
		// NavigationEnclosed transitions from true -> false once excavated!
		world := &simWorld{
			biomeID: "minecraft:dripstone_caves",
			blocks:  make(map[worldBlockKey]string),
		}
		// Build enclosed stone & coal_ore cave pocket around (0, 40, 0) with a 2-step upward
		// exit tunnel along +X (x=1, x=2) that requires mining stone and coal_ore while
		// NavigationEnclosed == true across >40 ticks before emerging at x >= 2.3, y >= 41.
		world.blocks[worldBlockKey{X: 1, Y: 40, Z: 0}] = "minecraft:stone"
		world.blocks[worldBlockKey{X: 1, Y: 41, Z: 0}] = "minecraft:stone"
		world.blocks[worldBlockKey{X: 1, Y: 42, Z: 0}] = "minecraft:coal_ore"
		world.blocks[worldBlockKey{X: 2, Y: 40, Z: 0}] = "minecraft:stone"
		world.blocks[worldBlockKey{X: 2, Y: 41, Z: 0}] = "minecraft:stone"
		world.blocks[worldBlockKey{X: 2, Y: 42, Z: 0}] = "minecraft:stone"
		world.blocks[worldBlockKey{X: 2, Y: 43, Z: 0}] = "minecraft:stone"
		// Seal other 3 sides with stone & coal_ore
		for _, off := range [][2]int32{{-1, 0}, {0, 1}, {0, -1}} {
			world.blocks[worldBlockKey{X: off[0], Y: 40, Z: off[1]}] = "minecraft:stone"
			world.blocks[worldBlockKey{X: off[0], Y: 41, Z: off[1]}] = "minecraft:coal_ore"
			world.blocks[worldBlockKey{X: off[0], Y: 42, Z: off[1]}] = "minecraft:stone"
		}
		world.blocks[worldBlockKey{X: 0, Y: 43, Z: 0}] = "minecraft:stone"

		world.isEnclosed = func(pos civquestv1.Vec3, blocks map[worldBlockKey]string) bool {
			// Radial (not directional) on purpose, and equal to the original criterion on the one
			// side the original covered: the old test said "enclosed while x < 2.3", i.e. 1.8m east
			// of the pocket centre (0.5). hypot < 1.8 is exactly that at +X and the same 1.8m reach
			// on every other side. Why not keep "must tunnel east": since cq-2sr.14 Java no longer
			// digs; it only reports MotorFacts{enclosed}. ADR-037 s3.4 makes Recover choose the dig
			// direction itself (heading lock seeded from the blocked bearing / the thinnest or
			// nearest diggable face), so the pocket is sealed on all four sides by diggable stone
			// and any excavation direction is a legitimate escape. Real mod semantics are the same
			// (see testdata/motorfacts/enclosed_pit.json): enclosed means "no walkable neighbour
			// within reach", a property of the whole pocket, not of one face. The pocket is escaped
			// when the agent stands >= 1.8m from its centre, i.e. beyond the 1-thick W/N/S walls
			// (outer face at 1.5m) or inside the dug east tunnel (cell x=2 starts at 1.5m).
			return math.Hypot(pos.X-0.5, pos.Z-0.5) < 1.8
		}

		world.isBlocked = func(x, y, z float64) (bool, string) {
			bx := int32(math.Floor(x))
			by := int32(math.Floor(y))
			bz := int32(math.Floor(z))
			world.mu.Lock()
			defer world.mu.Unlock()
			if _, hasBody := world.blocks[worldBlockKey{X: bx, Y: by + 1, Z: bz}]; hasBody {
				return true, "navigation_stuck_cave_wall"
			}
			if _, hasHead := world.blocks[worldBlockKey{X: bx, Y: by + 2, Z: bz}]; hasHead {
				return true, "navigation_stuck_cave_ceiling"
			}
			return false, ""
		}

		// Raycast inside cave sees tight stone walls on all 36 sectors while enclosed.
		world.customRaycast = func(ox, oy, oz float64, completedTick int64, numSectors int32, maxRange float32) *civquestv1.HorizonSurveyReport {
			if numSectors <= 0 {
				numSectors = 36
			}
			if maxRange <= 0 {
				maxRange = 64.0
			}
			stepDeg := 360.0 / float64(numSectors)
			sectors := make([]*civquestv1.HorizonSector, 0, numSectors)
			for i := int32(0); i < numSectors; i++ {
				bearing := float32(float64(i) * stepDeg)
				rad := float64(bearing) * math.Pi / 180.0
				dx, dz := -math.Sin(rad), math.Cos(rad)
				dist := 2.5
				feat := "RIDGE_OBSTRUCTION"
				blk := "minecraft:stone"
				occluded := true
				openH := false
				if ox >= 1.3 && bearing >= 250 && bearing <= 290 {
					dist = 48.0
					feat = "OPEN_PLAINS"
					blk = "minecraft:air"
					occluded = false
					openH = true
				}
				sectors = append(sectors, &civquestv1.HorizonSector{
					SectorIndex: i,
					BearingDeg:  bearing,
					HitDistance: float32(dist),
					FeatureType: feat,
					HitBlockId:  blk,
					HitX:        ox + dx*dist,
					HitY:        oy,
					HitZ:        oz + dz*dist,
					Occluded:    occluded,
					OpenHorizon: openH,
				})
			}
			return &civquestv1.HorizonSurveyReport{
				OriginX: ox, OriginY: oy, OriginZ: oz,
				CompletedTick: completedTick, MaxRange: maxRange, Sectors: sectors,
			}
		}

		sa := newSimAgent(
			t, "cave_miner", "pioneers",
			civquestv1.Vec3{X: 0.5, Y: 40.0, Z: 0.5},
			res, nil, nil, world, false,
		)
		defer sa.close()

		// Track blocks broken via inventory additions
		enclosedTicks := 0
		escapedEnclosed := false
		minStagePanic := false

		const tickDelta = int64(4)
		for frameIdx := 0; frameIdx < 200; frameIdx++ {
			worldTick := int64(4) + int64(frameIdx)*tickDelta
			tod := (2000 + int64(frameIdx)*tickDelta) % 24000
			sa.stepPhysics(worldTick, tickDelta)
			frame := sa.buildFrame(worldTick, tod)
			if frame.Status.GetNavigationEnclosed() {
				enclosedTicks++
			} else if enclosedTicks > 0 {
				escapedEnclosed = true
			}
			sa.runtime.HandleTelemetryFrame(frame)
			if sa.runtime.FSMState() == system1.StageEmergency {
				minStagePanic = true
			}
			intents := sa.runtime.DrainIntents()
			sa.applyIntents(intents, worldTick)
		}

		blocksMined := int32(0)
		for _, it := range sa.inventory {
			if it != nil {
				blocksMined += it.Count
			}
		}

		t.Logf("[CAVE-DIAG] enclosedTicks=%d escapedEnclosed=%v blocksMined=%d pos=(%.1f,%.1f,%.1f) surveys=%d/%d inFlightDup=%d maxSameSpot=%d stage=%s panic=%v",
			enclosedTicks, escapedEnclosed, blocksMined, sa.pos.X, sa.pos.Y, sa.pos.Z,
			len(sa.surveyEmissions), sa.surveysCompleted, sa.surveyWhileInFlight,
			sa.maxConsecSameSpotSurvey, sa.runtime.FSMState(), minStagePanic)

		if minStagePanic {
			t.Errorf("FAIL: underground enclosed cave triggered false StageEmergency panic lock")
		}
		if sa.surveyWhileInFlight > 2 || sa.maxConsecSameSpotSurvey > 2 {
			t.Errorf("FAIL: enclosed cave triggered duplicate survey storm (inFlight=%d, maxSameSpot=%d)",
				sa.surveyWhileInFlight, sa.maxConsecSameSpotSurvey)
		}
		if blocksMined == 0 && !escapedEnclosed {
			t.Errorf("FAIL: agent trapped in zero-progress enclosed cave loop (blocksMined=0, escapedEnclosed=false, pos=(%.1f,%.1f,%.1f))",
				sa.pos.X, sa.pos.Y, sa.pos.Z)
		}
		if !escapedEnclosed {
			t.Errorf("FAIL: agent mined %d block(s) but failed to escape NavigationEnclosed pocket (pos=(%.1f,%.1f,%.1f))",
				blocksMined, sa.pos.X, sa.pos.Y, sa.pos.Z)
		}
	})
}

func init() {
	_ = fmt.Sprintf("")
	_ = strings.TrimSpace("")
}
