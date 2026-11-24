package system1

import (
	"math"
	"strings"
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func makeUniformSurveyReport(ox, oy, oz float64, tick int64, featureType, blockID string, dist float32, occluded, open bool) *civquestv1.HorizonSurveyReport {
	sectors := make([]*civquestv1.HorizonSector, 36)
	for i := 0; i < 36; i++ {
		bearing := float32(i * 10)
		dx, dz := bearingToUnitVector(bearing)
		sectors[i] = &civquestv1.HorizonSector{
			SectorIndex: int32(i),
			BearingDeg:  bearing,
			HitDistance: dist,
			FeatureType: featureType,
			HitBlockId:  blockID,
			HitX:        ox + dx*float64(dist),
			HitY:        oy,
			HitZ:        oz + dz*float64(dist),
			Occluded:    occluded,
			OpenHorizon: open,
		}
	}
	return &civquestv1.HorizonSurveyReport{
		OriginX:       ox,
		OriginY:       oy,
		OriginZ:       oz,
		CompletedTick: tick,
		MaxRange:      64.0,
		Sectors:       sectors,
	}
}

func makeIdleExploreFrame(agentID string, tick int64, x, y, z float64, navState civquestv1.NavigationState) *civquestv1.TelemetryFrame {
	return &civquestv1.TelemetryFrame{
		WorldTick: tick,
		TimeOfDay: 3000,
		Weather:   civquestv1.Weather_WEATHER_CLEAR,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          20.0,
			MaxHealth:       20.0,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: x, Y: y, Z: z},
			NavigationState: navState,
		},
	}
}

func TestHorizonBearingMathAndSummary(t *testing.T) {
	// Verify exact Minecraft native yaw math: dx = -sin(rad), dz = cos(rad)
	cases := []struct {
		bearing float32
		wantDX  float64
		wantDZ  float64
		wantDir string
	}{
		{0, 0, 1, "S"},
		{90, -1, 0, "W"},
		{180, 0, -1, "N"},
		{270, 1, 0, "E"},
	}
	for _, tc := range cases {
		dx, dz := bearingToUnitVector(tc.bearing)
		if math.Abs(dx-tc.wantDX) > 1e-6 || math.Abs(dz-tc.wantDZ) > 1e-6 {
			t.Fatalf("bearing %.0f: got (%.6f, %.6f), want (%.6f, %.6f)", tc.bearing, dx, dz, tc.wantDX, tc.wantDZ)
		}
		gotBearing := vectorToBearingDeg(dx, dz)
		if angularDiffDeg(gotBearing, tc.bearing) > 1e-3 {
			t.Fatalf("inverse bearing for (%.2f, %.2f): got %.2f, want %.2f", dx, dz, gotBearing, tc.bearing)
		}
		if dir := primaryCardinalForBearing(tc.bearing); dir != tc.wantDir {
			t.Fatalf("bearing %.0f: got cardinal %s, want %s", tc.bearing, dir, tc.wantDir)
		}
	}

	report := makeUniformSurveyReport(0, 64, 0, 100, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	// N (180°): oak forest canopy at 42m
	report.Sectors[18].FeatureType = "FOREST_CANOPY"
	report.Sectors[18].HitBlockId = "minecraft:oak_leaves"
	report.Sectors[18].HitDistance = 42.0
	report.Sectors[18].OpenHorizon = false
	// S (0°): ridge obstruction at 8m
	report.Sectors[0].FeatureType = "RIDGE_OBSTRUCTION"
	report.Sectors[0].HitBlockId = "minecraft:stone"
	report.Sectors[0].HitDistance = 8.0
	report.Sectors[0].Occluded = true
	report.Sectors[0].OpenHorizon = false
	// W (90°): water at 38m
	report.Sectors[9].FeatureType = "WATER"
	report.Sectors[9].HitBlockId = "minecraft:water"
	report.Sectors[9].HitDistance = 38.0
	report.Sectors[9].OpenHorizon = false

	summary := FormatHorizonSurveySummary(report)
	for _, substr := range []string{"N: oak forest canopy at 42m", "E: open plains 64m+", "S: ridge obstruction at 8m", "W: water at 38m"} {
		if !strings.Contains(summary, substr) {
			t.Fatalf("expected summary %q to contain %q", summary, substr)
		}
	}
}

func TestChainedSurveyTraverseSurveyLoop_FindsDistantForestAndIndexesPOIs(t *testing.T) {
	state := NewAgentState()
	state.SetClanSize(1)
	cfg := xdg.DefaultAgentIdentityConfig("pioneer")
	cfg.Profession = "EXPLORER"

	// 1. Bootstrap warmup + empty horizon -> emits SurveyHorizonIntent(36, 64, 2)
	warmup := makeIdleExploreFrame("pioneer", 50, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	_ = EvaluateBootstrapGoals("pioneer", warmup, cfg, state)

	frame1 := makeIdleExploreFrame("pioneer", 100, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	intent1 := EvaluateBootstrapGoals("pioneer", frame1, cfg, state)
	if intent1 == nil || intent1.GetSurveyHorizon() == nil {
		t.Fatalf("expected SurveyHorizonIntent on empty horizon, got %+v", intent1)
	}
	sh := intent1.GetSurveyHorizon()
	if sh.NumSectors != 36 || sh.MaxRange != 64.0 || sh.TicksPerSector != 2 {
		t.Fatalf("unexpected SurveyHorizonIntent params: %+v", sh)
	}
	if state.GetExpeditionPhase() != ExpeditionSurveying {
		t.Fatalf("expected ExpeditionSurveying, got %s", state.GetExpeditionPhase())
	}

	// 2. While surveying (<120 ticks), subsequent ticks must hold steady (nil) and NOT clobber SURVEY_HORIZON
	frameHold := makeIdleExploreFrame("pioneer", 130, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	if holdIntent := EvaluateBootstrapGoals("pioneer", frameHold, cfg, state); holdIntent != nil {
		t.Fatalf("expected nil hold during ExpeditionSurveying, got %+v", holdIntent)
	}
	if holdExplore := EvaluateExploration("pioneer", frameHold, cfg, state); holdExplore != nil {
		t.Fatalf("expected EvaluateExploration to hold steady during ExpeditionSurveying, got %+v", holdExplore)
	}

	// 3. Deliver HorizonSurveyReport spotting FOREST_CANOPY to the East (270° => +X) at 42m
	report := makeUniformSurveyReport(0, 64, 0, 172, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	dxE, dzE := bearingToUnitVector(270) // (+1, 0)
	report.Sectors[27].FeatureType = "FOREST_CANOPY"
	report.Sectors[27].HitBlockId = "minecraft:oak_leaves"
	report.Sectors[27].HitDistance = 42.0
	report.Sectors[27].HitX = dxE * 42.0
	report.Sectors[27].HitY = 70.0
	report.Sectors[27].HitZ = dzE * 42.0
	report.Sectors[27].ElevationDelta = 6.0
	report.Sectors[27].OpenHorizon = false

	// Also place WATER at West (90°) and STRUCTURE at North (180°) to verify POI indexing
	dxW, dzW := bearingToUnitVector(90)
	report.Sectors[9].FeatureType = "WATER"
	report.Sectors[9].HitBlockId = "minecraft:water"
	report.Sectors[9].HitDistance = 35.0
	report.Sectors[9].HitX = dxW * 35.0
	report.Sectors[9].HitY = 63.0
	report.Sectors[9].HitZ = dzW * 35.0

	state.IngestHorizonSurveyReport(report, 172)
	if state.GetExpeditionPhase() != ExpeditionIdle {
		t.Fatalf("expected ExpeditionIdle after ingesting survey, got %s", state.GetExpeditionPhase())
	}

	// Verify distant forest POI is immediately discoverable by bootstrap/shelter SpatialIndex queries
	_, recalledTree := findNearestTreeTrunk("pioneer", frameHold, frameHold.Status.Position, state)
	if recalledTree == nil {
		t.Fatal("expected findNearestTreeTrunk to discover distant FOREST_CANOPY POI in SpatialIndex")
	}
	if math.Abs(recalledTree.X-42.0) > 0.5 || math.Abs(recalledTree.Z) > 0.5 {
		t.Fatalf("expected recalled forest POI near (42, 70, 0), got (%v, %v, %v)", recalledTree.X, recalledTree.Y, recalledTree.Z)
	}

	// Verify HorizonExplorer directly selects bearing 270° (+X East) -> target at ~39m (+X)
	frameSelect := makeIdleExploreFrame("pioneer", 175, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	navIntent := EvaluateHorizonExplorer("pioneer", frameSelect, cfg, state)
	if navIntent == nil || navIntent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent toward spotted forest, got %+v", navIntent)
	}
	target := navIntent.GetNavigate().Target
	if target.X < 35.0 || math.Abs(target.Z) > 2.0 {
		t.Fatalf("expected target along East 270° (+X ~39m), got %+v", target)
	}
	if state.GetExpeditionPhase() != ExpeditionTraversing {
		t.Fatalf("expected ExpeditionTraversing, got %s", state.GetExpeditionPhase())
	}

	// 4. Simulate arrival within 5m of target (note: only 1 forest sector, so solo homestead threshold not met -> chains next Survey!)
	frameArrive := makeIdleExploreFrame("pioneer", 300, target.X-1.5, 64, target.Z, civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)
	nextIntent := EvaluateHorizonExplorer("pioneer", frameArrive, cfg, state)
	if nextIntent == nil || nextIntent.GetSurveyHorizon() == nil {
		t.Fatalf("expected chained SurveyHorizonIntent upon arriving at new vantage, got %+v", nextIntent)
	}
	if state.ExpeditionHops != 1 {
		t.Fatalf("expected ExpeditionHops=1, got %d", state.ExpeditionHops)
	}

	// 5. Verify safety timeout (>120 ticks) recovers from stuck SURVEYING
	frameTimeout := makeIdleExploreFrame("pioneer", 450, target.X-1.5, 64, target.Z, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	recoverIntent := EvaluateHorizonExplorer("pioneer", frameTimeout, cfg, state)
	if recoverIntent == nil || recoverIntent.GetSurveyHorizon() == nil {
		t.Fatalf("expected recovery SurveyHorizonIntent after >120-tick survey timeout, got %+v", recoverIntent)
	}
}

func TestRavineMidTraverse_RimSurveyAndFlankFallback(t *testing.T) {
	state := NewAgentState()
	state.SetClanSize(2)
	cfg := xdg.DefaultAgentIdentityConfig("scout")
	cfg.Profession = "EXPLORER"

	// Initial survey at (0, 64, 0) spots open plains everywhere, slightly preferring South (0° => +Z) or forest at South
	initialReport := makeUniformSurveyReport(0, 64, 0, 100, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	initialReport.Sectors[0].FeatureType = "FOREST_CANOPY"
	initialReport.Sectors[0].HitBlockId = "minecraft:oak_leaves"
	initialReport.Sectors[0].HitDistance = 48.0
	initialReport.Sectors[0].HitX = 0
	initialReport.Sectors[0].HitZ = 48.0
	state.IngestHorizonSurveyReport(initialReport, 100)

	// Start traversing South (+Z) toward (0, 64, 45)
	frameStart := makeIdleExploreFrame("scout", 105, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	nav1 := EvaluateHorizonExplorer("scout", frameStart, cfg, state)
	if nav1 == nil || nav1.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent toward South forest, got %+v", nav1)
	}
	if angularDiffDeg(state.ExpeditionBearing, 0) > 1.0 {
		t.Fatalf("expected ExpeditionBearing=0° (South), got %.1f", state.ExpeditionBearing)
	}

	// Mid-traverse at (0, 64, 20) — >6m from LastSurveyOrigin (0, 64, 0) — hits a ravine (NAVIGATION_STATE_STUCK)!
	frameStuckRim := makeIdleExploreFrame("scout", 180, 0, 64, 20, civquestv1.NavigationState_NAVIGATION_STATE_STUCK)
	rimIntent := EvaluateHorizonExplorer("scout", frameStuckRim, cfg, state)

	// Must mark HAZARD barrier POI in SpatialIndex ~3m ahead along bearing 0° (0, 64, 23)
	hazards := state.SpatialIndex().FindPOIsInRadius(&civquestv1.Vec3{X: 0, Y: 64, Z: 23}, POICategoryHazard, 4.0)
	if len(hazards) == 0 || hazards[0].BlockID != "civquest:ravine_barrier" {
		t.Fatalf("expected civquest:ravine_barrier HAZARD POI recorded ahead of rim, got %+v", hazards)
	}

	// Must pause and immediately trigger a fresh 360° SurveyHorizonIntent right from the rim (NOT blind 22m flank!)
	if rimIntent == nil || rimIntent.GetSurveyHorizon() == nil {
		t.Fatalf("expected immediate rim SurveyHorizonIntent on ravine STUCK, got %+v", rimIntent)
	}
	if state.GetExpeditionPhase() != ExpeditionSurveying {
		t.Fatalf("expected ExpeditionSurveying at rim, got %s", state.GetExpeditionPhase())
	}

	// Rim survey completes: South (0°) through West (90°..130°) are occluded ridge/chasm, East (270°) is wide open plains 64m+
	rimReport := makeUniformSurveyReport(0, 64, 20, 250, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	for i := 0; i < 36; i++ {
		if i <= 13 || i >= 32 {
			rimReport.Sectors[i].Occluded = true
			rimReport.Sectors[i].FeatureType = "RIDGE_OBSTRUCTION"
			rimReport.Sectors[i].HitDistance = 6.0
			rimReport.Sectors[i].OpenHorizon = false
		}
	}
	state.IngestHorizonSurveyReport(rimReport, 250)

	// Next tick selects clear flanking/detour bearing away from blocked South & occluded West -> picks Eastside (~270°)
	framePostRim := makeIdleExploreFrame("scout", 255, 0, 64, 20, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	detourNav := EvaluateHorizonExplorer("scout", framePostRim, cfg, state)
	if detourNav == nil || detourNav.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent along clear rim detour bearing, got %+v", detourNav)
	}
	if angularDiffDeg(state.ExpeditionBearing, 0) <= 35.0 {
		t.Fatalf("expected chosen bearing to avoid blocked South 0°, got %.1f", state.ExpeditionBearing)
	}
	if detourNav.GetNavigate().Target.X <= 5.0 {
		t.Fatalf("expected Eastward detour (+X), got %+v", detourNav.GetNavigate().Target)
	}

	// Now suppose agent STILL hits STUCK at the exact same rim spot (<6.0m from LastSurveyOrigin (0, 64, 20)):
	// must NOT re-survey in an infinite loop; must execute 22m perpendicular flank to the clearer side!
	frameStuckAgain := makeIdleExploreFrame("scout", 280, 1.0, 64, 20.5, civquestv1.NavigationState_NAVIGATION_STATE_STUCK)
	flankIntent := EvaluateHorizonExplorer("scout", frameStuckAgain, cfg, state)
	if flankIntent == nil || flankIntent.GetNavigate() == nil {
		t.Fatalf("expected 22m perpendicular flank NavigateIntent when already surveyed within <6m, got %+v", flankIntent)
	}
	if state.GetExpeditionPhase() != ExpeditionFlanking {
		t.Fatalf("expected ExpeditionFlanking, got %s", state.GetExpeditionPhase())
	}
	if state.ExpeditionFlankCount != 1 {
		t.Fatalf("expected ExpeditionFlankCount=1, got %d", state.ExpeditionFlankCount)
	}
}

func TestMultiMemberClan_FanOutAndRendezvousReturn(t *testing.T) {
	// Verify distinct clan explorers fan out across complementary bearings on hop 0
	agents := []string{"aria", "bran", "cora", "dorn", "elena", "finn"}
	chosenBearings := make(map[string]float32)
	sharedOpenReport := makeUniformSurveyReport(0, 64, 0, 100, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)

	for _, id := range agents {
		st := NewAgentState()
		st.SetClanSize(6)
		st.IngestHorizonSurveyReport(sharedOpenReport, 100)
		f := makeIdleExploreFrame(id, 110, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
		in := EvaluateHorizonExplorer(id, f, xdg.DefaultAgentIdentityConfig(id), st)
		if in == nil || in.GetNavigate() == nil {
			t.Fatalf("agent %s expected NavigateIntent, got %+v", id, in)
		}
		chosenBearings[id] = st.ExpeditionBearing
	}
	unique := make(map[int]bool)
	for _, b := range chosenBearings {
		unique[int(b/10)] = true
	}
	if len(unique) < 3 {
		t.Fatalf("expected 6 clan explorers to fan out into multiple bearings, got %+v", chosenBearings)
	}

	// Verify 2-hop outbound expedition -> return to RendezvousOrigin -> PendingRendezvousSync within 20m
	st := NewAgentState()
	st.SetClanSize(6)
	st.SetRendezvousOrigin(0, 64, 0)
	st.IngestHorizonSurveyReport(sharedOpenReport, 100)

	// Hop 1 start
	f1 := makeIdleExploreFrame("aria", 110, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	nav1 := EvaluateHorizonExplorer("aria", f1, xdg.DefaultAgentIdentityConfig("aria"), st)
	t1 := nav1.GetNavigate().Target

	// Hop 1 arrival -> chains Hop 2 SurveyHorizonIntent
	fArrive1 := makeIdleExploreFrame("aria", 200, t1.X, 64, t1.Z, civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)
	survey2 := EvaluateHorizonExplorer("aria", fArrive1, xdg.DefaultAgentIdentityConfig("aria"), st)
	if survey2 == nil || survey2.GetSurveyHorizon() == nil {
		t.Fatalf("expected Hop 2 SurveyHorizonIntent after 1st hop, got %+v", survey2)
	}

	// Hop 2 survey completes
	report2 := makeUniformSurveyReport(t1.X, 64, t1.Z, 270, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	st.IngestHorizonSurveyReport(report2, 270)
	f2 := makeIdleExploreFrame("aria", 275, t1.X, 64, t1.Z, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	nav2 := EvaluateHorizonExplorer("aria", f2, xdg.DefaultAgentIdentityConfig("aria"), st)
	t2 := nav2.GetNavigate().Target

	// Hop 2 arrival -> triggers ExpeditionReturning toward RendezvousOrigin (0, 64, 0)
	fArrive2 := makeIdleExploreFrame("aria", 400, t2.X, 64, t2.Z, civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)
	retNav := EvaluateHorizonExplorer("aria", fArrive2, xdg.DefaultAgentIdentityConfig("aria"), st)
	if retNav == nil || retNav.GetNavigate() == nil {
		t.Fatalf("expected return NavigateIntent to RendezvousOrigin, got %+v", retNav)
	}
	if st.GetExpeditionPhase() != ExpeditionReturning {
		t.Fatalf("expected ExpeditionReturning after 2 hops, got %s", st.GetExpeditionPhase())
	}
	if retNav.GetNavigate().Target.X != 0 || retNav.GetNavigate().Target.Z != 0 {
		t.Fatalf("expected return target (0, 64, 0), got %+v", retNav.GetNavigate().Target)
	}

	// Returning agent arrives within 20m of RendezvousOrigin -> triggers PendingRendezvousSync
	fHome := makeIdleExploreFrame("aria", 550, 12, 64, -10, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	_ = EvaluateHorizonExplorer("aria", fHome, xdg.DefaultAgentIdentityConfig("aria"), st)
	synced, summary := st.ConsumePendingRendezvousSync()
	if !synced || summary == "" {
		t.Fatalf("expected PendingRendezvousSync=true with non-empty summary, got synced=%v summary=%q", synced, summary)
	}
	if st.ExpeditionHops != 0 || st.GetExpeditionPhase() != ExpeditionIdle {
		t.Fatalf("expected hops reset to 0 and phase IDLE after rendezvous, got hops=%d phase=%s",
			st.ExpeditionHops, st.GetExpeditionPhase())
	}
}

func TestSoloExplorer_NeverReturnsAndClaimsHomestead(t *testing.T) {
	st := NewAgentState()
	st.SetClanSize(1)
	st.SetRendezvousOrigin(0, 64, 0)
	cfg := xdg.DefaultAgentIdentityConfig("nomad")
	cfg.Profession = "EXPLORER"

	// Hop 1: barren plains -> does NOT claim homestead and does NOT return to (0,64,0)
	barren := makeUniformSurveyReport(0, 64, 0, 100, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	st.IngestHorizonSurveyReport(barren, 100)
	f1 := makeIdleExploreFrame("nomad", 110, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	nav1 := EvaluateHorizonExplorer("nomad", f1, cfg, st)
	t1 := nav1.GetNavigate().Target

	// Hop 1 arrival -> chains Hop 2 outward
	fA1 := makeIdleExploreFrame("nomad", 200, t1.X, 64, t1.Z, civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)
	s2 := EvaluateHorizonExplorer("nomad", fA1, cfg, st)
	if s2 == nil || s2.GetSurveyHorizon() == nil {
		t.Fatalf("expected solo explorer to chain Hop 2 survey, got %+v", s2)
	}

	// Hop 2 & Hop 3: still barren -> even at 2+ hops, solo explorer NEVER enters ExpeditionReturning!
	barren2 := makeUniformSurveyReport(t1.X, 64, t1.Z, 270, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	st.IngestHorizonSurveyReport(barren2, 270)
	f2 := makeIdleExploreFrame("nomad", 275, t1.X, 64, t1.Z, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	nav2 := EvaluateHorizonExplorer("nomad", f2, cfg, st)
	t2 := nav2.GetNavigate().Target

	fA2 := makeIdleExploreFrame("nomad", 375, t2.X, 64, t2.Z, civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)
	s3 := EvaluateHorizonExplorer("nomad", fA2, cfg, st)
	if st.GetExpeditionPhase() == ExpeditionReturning {
		t.Fatal("solo explorer (ClanSize=1) must NEVER return to RendezvousOrigin")
	}
	if s3 == nil || s3.GetSurveyHorizon() == nil {
		t.Fatalf("expected solo explorer to keep chaining surveys outward, got %+v", s3)
	}

	// Hop 3 finds a prime homestead site (forest + plains + water)
	lush := makeUniformSurveyReport(t2.X, 64, t2.Z, 450, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	for i := 0; i < 6; i++ {
		lush.Sectors[i].FeatureType = "FOREST_CANOPY"
		lush.Sectors[i].HitBlockId = "minecraft:oak_leaves"
		lush.Sectors[i].HitDistance = 30.0
		lush.Sectors[i].OpenHorizon = false
	}
	for i := 6; i < 9; i++ {
		lush.Sectors[i].FeatureType = "WATER"
		lush.Sectors[i].HitBlockId = "minecraft:water"
		lush.Sectors[i].HitDistance = 32.0
		lush.Sectors[i].OpenHorizon = false
	}
	st.IngestHorizonSurveyReport(lush, 450)
	f3 := makeIdleExploreFrame("nomad", 455, t2.X, 64, t2.Z, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	nav3 := EvaluateHorizonExplorer("nomad", f3, cfg, st)
	t3 := nav3.GetNavigate().Target

	// Arrival at Hop 3 target claims homestead directly!
	fA3 := makeIdleExploreFrame("nomad", 560, t3.X, 64, t3.Z, civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)
	claimIntent := EvaluateHorizonExplorer("nomad", fA3, cfg, st)
	if claimIntent != nil {
		t.Fatalf("expected nil wander intent upon claiming homestead, got %+v", claimIntent)
	}
	if !st.HomesteadClaimed || st.GetExpeditionPhase() != ExpeditionHomesteadClaimed {
		t.Fatalf("expected HomesteadClaimed=true and ExpeditionHomesteadClaimed, got claimed=%v phase=%s",
			st.HomesteadClaimed, st.GetExpeditionPhase())
	}
	if math.Hypot(st.HomesteadPos[0]-t3.X, st.HomesteadPos[2]-t3.Z) > 0.1 {
		t.Fatalf("expected HomesteadPos at (%v, %v), got %+v", t3.X, t3.Z, st.HomesteadPos)
	}
	if synced, _ := st.ConsumePendingRendezvousSync(); synced {
		t.Fatal("solo explorer must never trigger rendezvous sync")
	}
}

func TestSurvivalReflexesStrictlyPreemptSurveying(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("pioneer")
	cfg.Profession = "EXPLORER"
	fsm := NewAgentFSM(StageBootstrap, nil)

	// Warmup + Tick 1: trigger SurveyHorizonIntent
	_ = fsm.Tick(&FSMContext{AgentID: "pioneer", Frame: makeIdleExploreFrame("pioneer", 50, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE), Config: cfg, State: state})
	frame1 := makeIdleExploreFrame("pioneer", 100, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	res1 := fsm.Tick(&FSMContext{AgentID: "pioneer", Frame: frame1, Config: cfg, State: state})
	if len(res1.Intents) == 0 || res1.Intents[0].Intent.GetSurveyHorizon() == nil {
		t.Fatalf("expected SurveyHorizonIntent on tick 1, got %+v", res1.Intents)
	}

	// Tick 2: Hostile creeper appears -> EMERGENCY combat/flee preempts SURVEYING and clears survey state
	frame2 := makeIdleExploreFrame("pioneer", 105, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	frame2.VisibleEntities = []*civquestv1.VisibleEntity{
		{
			EntityId:   99,
			EntityType: "minecraft:creeper",
			IsHostile:  true,
			FuseTicks:  15,
			Distance:   4.0,
			Position:   &civquestv1.Vec3{X: 3, Y: 64, Z: 0},
		},
	}
	res2 := fsm.Tick(&FSMContext{AgentID: "pioneer", Frame: frame2, Config: cfg, State: state})
	if res2.Stage != StageEmergency {
		t.Fatalf("expected StageEmergency on hostile creeper, got %s", res2.Stage)
	}
	if state.GetExpeditionPhase() == ExpeditionSurveying {
		t.Fatal("expected Emergency preemption to clear ExpeditionSurveying")
	}

	// Tick 3: Critical hunger with foraging target -> ForagingSkill preempts SURVEYING
	stateForage := NewAgentState()
	fsmForage := NewAgentFSM(StageBootstrap, nil)
	_ = fsmForage.Tick(&FSMContext{AgentID: "forager", Frame: makeIdleExploreFrame("forager", 150, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE), Config: cfg, State: stateForage})
	fSurvey := makeIdleExploreFrame("forager", 200, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	_ = fsmForage.Tick(&FSMContext{AgentID: "forager", Frame: fSurvey, Config: cfg, State: stateForage})
	if stateForage.GetExpeditionPhase() != ExpeditionSurveying {
		t.Fatalf("expected ExpeditionSurveying before hunger test, got %s", stateForage.GetExpeditionPhase())
	}

	fHungry := makeIdleExploreFrame("forager", 210, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	fHungry.Status.FoodLevel = 6
	fHungry.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:sweet_berry_bush", BlockStateString: "age=3"},
	}
	start := time.Now()
	resHungry := fsmForage.Tick(&FSMContext{AgentID: "forager", Frame: fHungry, Config: cfg, State: stateForage})
	if time.Since(start) > 5*time.Millisecond {
		t.Fatalf("FSM tick exceeded 5ms latency budget")
	}
	foundHarvest := false
	for _, fi := range resHungry.Intents {
		if fi.Intent != nil && fi.Intent.GetInteractBlock() != nil {
			foundHarvest = true
		}
	}
	if !foundHarvest {
		t.Fatalf("expected ForagingSkill to preempt surveying and harvest berry bush, got %+v", resHungry.Intents)
	}
	if stateForage.GetExpeditionPhase() == ExpeditionSurveying {
		t.Fatal("expected ForagingSkill preemption to clear ExpeditionSurveying")
	}
}

func TestHorizonExplorer_DuplicateSurveyGuardAndArrivalProximity(t *testing.T) {
	state := NewAgentState()
	state.SetClanSize(1)
	cfg := xdg.DefaultAgentIdentityConfig("nomad")
	cfg.Profession = "EXPLORER"

	// 1. Emit initial survey at tick 100
	f1 := makeIdleExploreFrame("nomad", 100, 0, 64, 0, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	in1 := EvaluateHorizonExplorer("nomad", f1, cfg, state)
	if in1 == nil || in1.GetSurveyHorizon() == nil {
		t.Fatalf("expected initial SurveyHorizonIntent, got %+v", in1)
	}

	// Even if ExpeditionSurveying is cleared early, <80 ticks at <6m spot must NOT emit a duplicate survey.
	state.ClearExpeditionSurveyingIfActive()
	fDup := makeIdleExploreFrame("nomad", 140, 2, 64, 1, civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	if inDup := EvaluateHorizonExplorer("nomad", fDup, cfg, state); inDup != nil {
		t.Fatalf("expected duplicate survey guard to suppress SurveyHorizonIntent within 80 ticks at <6m, got %+v", inDup)
	}

	// 2. False NAVIGATION_STATE_ARRIVED 25m away from ExpeditionTarget must NOT claim homestead.
	lush := makeUniformSurveyReport(0, 64, 0, 200, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	for i := 0; i < 6; i++ {
		lush.Sectors[i].FeatureType = "FOREST_CANOPY"
		lush.Sectors[i].HitBlockId = "minecraft:oak_leaves"
		lush.Sectors[i].HitDistance = 48.0
		lush.Sectors[i].OpenHorizon = false
	}
	state.IngestHorizonSurveyReport(lush, 200)
	state.ExpeditionPhase = ExpeditionTraversing
	state.ExpeditionTarget = [3]float64{0, 64, 48}
	state.ExpeditionHops = 2

	fFalseArrive := makeIdleExploreFrame("nomad", 220, 0, 64, 20, civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)
	navResume := EvaluateHorizonExplorer("nomad", fFalseArrive, cfg, state)
	if state.HomesteadClaimed || state.GetExpeditionPhase() == ExpeditionHomesteadClaimed {
		t.Fatal("must NOT claim homestead on NAVIGATION_STATE_ARRIVED 28m away from ExpeditionTarget")
	}
	if navResume == nil || navResume.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent to resume toward ExpeditionTarget, got %+v", navResume)
	}
}
