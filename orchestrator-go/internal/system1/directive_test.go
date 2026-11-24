package system1

import (
	"math"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func newDirectiveTestFrame(agentID string, worldTick, timeOfDay int64, x, y, z float64) *civquestv1.TelemetryFrame {
	return &civquestv1.TelemetryFrame{
		WorldTick: worldTick,
		TimeOfDay: timeOfDay,
		Weather:   civquestv1.Weather_WEATHER_CLEAR,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: x, Y: y, Z: z},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
}

func TestDirectiveEvaluator_FactGraphAndExpiration(t *testing.T) {
	state := NewAgentState()
	state.SetStrategicDirective(
		DirectiveActionMineStone,
		"northern outcrop",
		"MINE_STONE: Gather coal and cobblestone",
		1000,
		7000,
	)

	eval := NewDirectiveEvaluator()
	gActive := NewFactGraph()
	if err := eval.Evaluate(&EvaluatorContext{
		AgentID: "aria",
		Frame:   newDirectiveTestFrame("aria", 1500, 4000, 0, 64, 0),
		State:   state,
	}, gActive); err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	if !gActive.GetBool("system2.directive_active") {
		t.Fatal("expected system2.directive_active=true")
	}
	if got := gActive.GetString("system2.directive_action"); got != DirectiveActionMineStone {
		t.Fatalf("expected action %s, got %q", DirectiveActionMineStone, got)
	}
	if got := gActive.GetString("system2.directive_target"); got != "northern outcrop" {
		t.Fatalf("expected target 'northern outcrop', got %q", got)
	}
	if got := gActive.GetString("system2.immediate_goal"); got != "MINE_STONE: Gather coal and cobblestone" {
		t.Fatalf("expected immediate_goal preserved, got %q", got)
	}

	// Expired at tick 7000
	gExpired := NewFactGraph()
	if err := eval.Evaluate(&EvaluatorContext{
		AgentID: "aria",
		Frame:   newDirectiveTestFrame("aria", 7000, 4000, 0, 64, 0),
		State:   state,
	}, gExpired); err != nil {
		t.Fatalf("Evaluate expired failed: %v", err)
	}
	if gExpired.GetBool("system2.directive_active") {
		t.Fatal("expected system2.directive_active=false when expired")
	}
}

func TestEvaluateStrategicDirective_GatherTimber(t *testing.T) {
	state := NewAgentState()
	state.SetStrategicDirective(DirectiveActionGatherTimber, "oak grove", "GATHER_TIMBER: Harvest oak logs", 100, 6100)
	cfg := xdg.DefaultAgentIdentityConfig("aria")

	frame := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
	}

	intent := EvaluateStrategicDirective(frame, state, cfg)
	if intent == nil || intent.GetInteractBlock() == nil ||
		intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK for GATHER_TIMBER, got %+v", intent)
	}
	active, kind, _, _ := state.ActiveTaskInfo()
	if !active || kind != "harvest_tree" {
		t.Fatalf("expected harvest_tree task started, got active=%v kind=%q", active, kind)
	}
}

func TestEvaluateStrategicDirective_MineStone(t *testing.T) {
	state := NewAgentState()
	state.SetStrategicDirective(DirectiveActionMineStone, "coal vein", "MINE_STONE: Extract coal and stone", 100, 6100)
	cfg := xdg.DefaultAgentIdentityConfig("aria")

	// 1. Distant ore -> Navigate toward ore
	frameFar := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	frameFar.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}, BlockId: "minecraft:coal_ore"},
	}
	navIntent := EvaluateStrategicDirective(frameFar, state, cfg)
	if navIntent == nil || navIntent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent toward distant coal_ore, got %+v", navIntent)
	}
	if navIntent.GetNavigate().Target.X != 10.5 {
		t.Fatalf("expected navigate target X=10.5, got %.1f", navIntent.GetNavigate().Target.X)
	}

	// 2. Within reach -> HARVEST_BREAK
	frameNear := newDirectiveTestFrame("aria", 250, 4000, 8.5, 64, 0.5)
	frameNear.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}, BlockId: "minecraft:coal_ore"},
	}
	breakIntent := EvaluateStrategicDirective(frameNear, state, cfg)
	if breakIntent == nil || breakIntent.GetInteractBlock() == nil ||
		breakIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK on nearby coal_ore, got %+v", breakIntent)
	}

	// 3. No visible stone -> recalls mine POI from SpatialIndex
	state.SpatialIndex().UpsertPOI(db.AgentPOI{
		POIID:       "poi_stone_1",
		Category:    POICategoryResource,
		BlockID:     "minecraft:iron_ore",
		X:           30,
		Y:           64,
		Z:           10,
		AccessX:     29,
		AccessY:     64,
		AccessZ:     10,
		Reliability: 0.9,
		IsActive:    true,
	})
	frameNone := newDirectiveTestFrame("aria", 300, 4000, 0.5, 64, 0.5)
	poiIntent := EvaluateStrategicDirective(frameNone, state, cfg)
	if poiIntent == nil || poiIntent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent to recalled mine POI, got %+v", poiIntent)
	}
}

func TestEvaluateStrategicDirective_ForageFood(t *testing.T) {
	state := NewAgentState()
	state.SetStrategicDirective(DirectiveActionForageFood, "berry bushes", "FORAGE_FOOD: Stockpile berries", 100, 6100)
	cfg := xdg.DefaultAgentIdentityConfig("aria")

	// Well-fed (FoodLevel=20) should still forage when explicitly directed by System 2
	frame := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	frame.Status.FoodLevel = 20
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:sweet_berry_bush", BlockStateString: "age=3"},
	}

	intent := EvaluateStrategicDirective(frame, state, cfg)
	if intent == nil || intent.GetInteractBlock() == nil ||
		intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK on sweet_berry_bush for FORAGE_FOOD, got %+v", intent)
	}
}

func TestEvaluateStrategicDirective_ScoutFrontierFanOutAndCompletion(t *testing.T) {
	stateA := NewAgentState()
	stateA.SetClanSize(2)
	stateA.SetStrategicDirective(DirectiveActionScoutFrontier, "outer sector", "SCOUT_FRONTIER: Chart sector", 500, 6500)
	stateB := NewAgentState()
	stateB.SetClanSize(2)
	stateB.SetStrategicDirective(DirectiveActionScoutFrontier, "outer sector", "SCOUT_FRONTIER: Chart sector", 500, 6500)

	frameA := newDirectiveTestFrame("aria", 510, 4000, 0, 64, 0)
	frameB := newDirectiveTestFrame("bran", 510, 4000, 0, 64, 0)

	// First tick emits 360° SurveyHorizonIntent (ADR-032)
	surveyA := EvaluateStrategicDirective(frameA, stateA, xdg.DefaultAgentIdentityConfig("aria"))
	surveyB := EvaluateStrategicDirective(frameB, stateB, xdg.DefaultAgentIdentityConfig("bran"))
	if surveyA == nil || surveyA.GetSurveyHorizon() == nil || surveyB == nil || surveyB.GetSurveyHorizon() == nil {
		t.Fatalf("expected initial SurveyHorizonIntents for SCOUT_FRONTIER, got A=%+v B=%+v", surveyA, surveyB)
	}

	// Simulate completed uniform open plains surveys -> agents fan out by complementary clan wedge bearings
	openSectors := make([]*civquestv1.HorizonSector, 36)
	for i := 0; i < 36; i++ {
		openSectors[i] = &civquestv1.HorizonSector{
			SectorIndex: int32(i),
			BearingDeg:  float32(i * 10),
			HitDistance: 64.0,
			FeatureType: "OPEN_PLAINS",
			OpenHorizon: true,
		}
	}
	report := &civquestv1.HorizonSurveyReport{
		OriginX: 0, OriginY: 64, OriginZ: 0, CompletedTick: 540, MaxRange: 64.0, Sectors: openSectors,
	}
	stateA.IngestHorizonSurveyReport(report, 540)
	stateB.IngestHorizonSurveyReport(report, 540)
	frameA.WorldTick = 545
	frameB.WorldTick = 545

	intentA := EvaluateStrategicDirective(frameA, stateA, xdg.DefaultAgentIdentityConfig("aria"))
	intentB := EvaluateStrategicDirective(frameB, stateB, xdg.DefaultAgentIdentityConfig("bran"))
	if intentA == nil || intentA.GetNavigate() == nil || intentB == nil || intentB.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntents for SCOUT_FRONTIER after survey, got A=%+v B=%+v", intentA, intentB)
	}

	tA := intentA.GetNavigate().Target
	tB := intentB.GetNavigate().Target
	distA := math.Hypot(tA.X, tA.Z)
	distB := math.Hypot(tB.X, tB.Z)
	if distA < 31.5 || distA > 48.5 || distB < 31.5 || distB > 48.5 {
		t.Fatalf("expected frontier target 32-48m away, got distA=%.2f distB=%.2f", distA, distB)
	}
	if math.Hypot(tA.X-tB.X, tA.Z-tB.Z) < 1.0 {
		t.Fatalf("expected different agents to fan out in different directions, got tA=%+v tB=%+v", tA, tB)
	}

	// Arriving within 5m clears the directive
	arrivedFrame := newDirectiveTestFrame("aria", 600, 4000, tA.X-2.0, tA.Y, tA.Z)
	afterIntent := EvaluateStrategicDirective(arrivedFrame, stateA, xdg.DefaultAgentIdentityConfig("aria"))
	if afterIntent != nil {
		t.Fatalf("expected nil intent upon reaching frontier waypoint, got %+v", afterIntent)
	}
	if _, active := stateA.ActiveStrategicDirective(600); active {
		t.Fatal("expected SCOUT_FRONTIER directive to be cleared after arriving within 5m")
	}
}

func TestEvaluateStrategicDirective_Socialize(t *testing.T) {
	state := NewAgentState()
	state.SetStrategicDirective(DirectiveActionSocialize, "camp peers", "SOCIALIZE: Coordinate with Bran", 100, 6100)
	cfg := xdg.DefaultAgentIdentityConfig("aria")

	// Peer Bran is 18m away; self Aria should be ignored
	frameFar := newDirectiveTestFrame("aria", 200, 4000, 0, 64, 0)
	frameFar.VisibleEntities = []*civquestv1.VisibleEntity{
		{EntityId: 1, EntityType: "minecraft:player", DisplayName: "aria", Position: &civquestv1.Vec3{X: 0, Y: 64, Z: 0}},
		{EntityId: 2, EntityType: "minecraft:player", DisplayName: "Bran", Position: &civquestv1.Vec3{X: 18, Y: 64, Z: 0}},
	}
	navIntent := EvaluateStrategicDirective(frameFar, state, cfg)
	if navIntent == nil || navIntent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent toward peer Bran, got %+v", navIntent)
	}
	target := navIntent.GetNavigate().Target
	distFromPeer := math.Hypot(target.X-18.0, target.Z)
	if distFromPeer < 4.0 || distFromPeer > 6.0 {
		t.Fatalf("expected socialize standoff 4-6m from peer, got %.2fm", distFromPeer)
	}

	// Within 5m of Bran -> stays in social range (nil)
	frameNear := newDirectiveTestFrame("aria", 250, 4000, 13.0, 64, 0)
	frameNear.VisibleEntities = frameFar.VisibleEntities
	if nearIntent := EvaluateStrategicDirective(frameNear, state, cfg); nearIntent != nil {
		t.Fatalf("expected nil intent when already within 4-6m of peer, got %+v", nearIntent)
	}
}

func TestEvaluateStrategicDirective_SurvivalPreemptionAndSub5ms(t *testing.T) {
	state := NewAgentState()
	state.SetStrategicDirective(DirectiveActionMineStone, "ridge", "MINE_STONE: Gather stone", 100, 6100)
	cfg := xdg.DefaultAgentIdentityConfig("aria")

	// 1. Hostile nearby -> preempts directive
	frameHostile := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	frameHostile.VisibleEntities = []*civquestv1.VisibleEntity{
		{EntityId: 99, EntityType: "minecraft:zombie", IsHostile: true, Distance: 5.0, Position: &civquestv1.Vec3{X: 5, Y: 64, Z: 0}},
	}
	frameHostile.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:stone"},
	}
	if intent := EvaluateStrategicDirective(frameHostile, state, cfg); intent != nil {
		t.Fatalf("expected hostile threat to preempt directive, got %+v", intent)
	}

	// 2. Critical hunger -> preempts directive
	frameHungry := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	frameHungry.Status.FoodLevel = 5
	frameHungry.InterestingBlocks = frameHostile.InterestingBlocks
	if intent := EvaluateStrategicDirective(frameHungry, state, cfg); intent != nil {
		t.Fatalf("expected critical hunger to preempt directive, got %+v", intent)
	}

	// 3. Latency check (<5ms)
	frameClean := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	frameClean.InterestingBlocks = frameHostile.InterestingBlocks
	start := time.Now()
	for i := 0; i < 100; i++ {
		frameClean.WorldTick = int64(200 + i*25)
		_ = EvaluateStrategicDirective(frameClean, state, cfg)
	}
	perCall := time.Since(start) / 100
	if perCall >= 5*time.Millisecond {
		t.Fatalf("expected sub-5ms directive evaluation, got %v", perCall)
	}
}

func TestEvaluateGoals_PreservesSystem2DirectiveGoalAndYieldsToEmergencies(t *testing.T) {
	state := NewAgentState()
	state.SetStrategicDirective(
		DirectiveActionMineStone,
		"iron vein",
		"MINE_STONE: Excavate iron ore from northern ridge",
		100,
		6100,
	)
	cfg := xdg.DefaultAgentIdentityConfig("aria")

	frame := newDirectiveTestFrame("aria", 200, 4000, 0, 64, 0)
	goals := EvaluateGoals(frame, cfg, state)
	if goals.ImmediateGoal != "MINE_STONE: Excavate iron ore from northern ridge" {
		t.Fatalf("expected ImmediateGoal preserved from directive, got %q", goals.ImmediateGoal)
	}

	// Hostile override still takes precedence over System 2 directive
	frame.VisibleEntities = []*civquestv1.VisibleEntity{
		{EntityId: 10, EntityType: "minecraft:creeper", IsHostile: true, Distance: 4.0},
	}
	goalsCombat := EvaluateGoals(frame, cfg, state)
	if goalsCombat.ImmediateGoal != "COMBAT_SURVIVAL: Defend against or evade nearby hostile threats" {
		t.Fatalf("expected COMBAT_SURVIVAL override, got %q", goalsCombat.ImmediateGoal)
	}
}

func TestAgentFSM_ExecutesDirectiveInBootstrapAndSettlement(t *testing.T) {
	// Bootstrap stage with no immediate craft step -> directive guides physical mining instead of wandering
	state := NewAgentState()
	state.SetStrategicDirective(DirectiveActionMineStone, "coal", "MINE_STONE: Mine coal", 100, 6100)
	fsm := NewAgentFSM(StageBootstrap, nil)
	cfg := xdg.DefaultAgentIdentityConfig("aria")

	frame := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:coal_ore"},
	}

	res := fsm.Tick(&FSMContext{
		AgentID: "aria",
		Frame:   frame,
		Config:  cfg,
		State:   state,
	})
	if res.Facts == nil || !res.Facts.GetBool("system2.directive_active") {
		t.Fatal("expected FactGraph system2.directive_active=true after FSM Tick")
	}
	foundBreak := false
	for _, fi := range res.Intents {
		if fi.Intent != nil && fi.Intent.GetInteractBlock() != nil &&
			fi.Intent.GetInteractBlock().InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK {
			foundBreak = true
		}
	}
	if !foundBreak {
		t.Fatalf("expected FSM in StageBootstrap to emit HARVEST_BREAK for MINE_STONE directive, got %+v", res.Intents)
	}
	if res.GoalHierarchy == nil || res.GoalHierarchy.ImmediateGoal != "MINE_STONE: Mine coal" {
		t.Fatalf("expected GoalHierarchy.ImmediateGoal to preserve System 2 directive goal, got %+v", res.GoalHierarchy)
	}

	// Settlement stage -> directive executes during daytime PrimaryDuty
	stateSet := NewAgentState()
	stateSet.SetStrategicDirective(DirectiveActionScoutFrontier, "east", "SCOUT_FRONTIER: East frontier", 100, 6100)
	fsmSet := NewAgentFSM(StageSettlement, nil)
	frameSet := newDirectiveTestFrame("aria", 250, 4000, 0, 64, 0)
	frameSet.Status.Inventory = []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
		{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
		{Slot: 2, ItemId: "minecraft:torch", Count: 4},
	}
	resSet := fsmSet.Tick(&FSMContext{
		AgentID: "aria",
		Frame:   frameSet,
		Config:  cfg,
		State:   stateSet,
	})
	if len(resSet.Intents) == 0 || resSet.Intents[0].Intent.GetSurveyHorizon() == nil {
		t.Fatalf("expected Settlement FSM tick to emit SCOUT_FRONTIER SurveyHorizonIntent, got %+v", resSet.Intents)
	}
	if resSet.GoalHierarchy == nil || resSet.GoalHierarchy.ImmediateGoal != "SCOUT_FRONTIER: East frontier" {
		t.Fatalf("expected Settlement GoalHierarchy preserved, got %+v", resSet.GoalHierarchy)
	}

	// After HorizonSurveyReport arrives, next tick emits NavigateIntent along chosen bearing
	sectors := make([]*civquestv1.HorizonSector, 36)
	for i := 0; i < 36; i++ {
		sectors[i] = &civquestv1.HorizonSector{
			SectorIndex: int32(i), BearingDeg: float32(i * 10), HitDistance: 64.0, FeatureType: "OPEN_PLAINS", OpenHorizon: true,
		}
	}
	stateSet.IngestHorizonSurveyReport(&civquestv1.HorizonSurveyReport{
		OriginX: 0, OriginY: 64, OriginZ: 0, CompletedTick: 260, MaxRange: 64.0, Sectors: sectors,
	}, 260)
	frameSet.WorldTick = 265
	resSetNav := fsmSet.Tick(&FSMContext{
		AgentID: "aria",
		Frame:   frameSet,
		Config:  cfg,
		State:   stateSet,
	})
	if len(resSetNav.Intents) == 0 || resSetNav.Intents[0].Intent.GetNavigate() == nil {
		t.Fatalf("expected Settlement FSM tick after survey to emit SCOUT_FRONTIER NavigateIntent, got %+v", resSetNav.Intents)
	}
}
