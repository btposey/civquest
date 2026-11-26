package system1

import (
	"math"
	"strings"
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

	intent := EvaluateStrategicDirective(frame, state, cfg, nil)
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
	frameFar := withWoodenPickaxe(newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5))
	frameFar.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}, BlockId: "minecraft:coal_ore"},
	}
	navIntent := EvaluateStrategicDirective(frameFar, state, cfg, nil)
	if navIntent == nil || navIntent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent toward distant coal_ore, got %+v", navIntent)
	}
	if navIntent.GetNavigate().Target.X != 10.5 {
		t.Fatalf("expected navigate target X=10.5, got %.1f", navIntent.GetNavigate().Target.X)
	}

	// 2. Within reach -> HARVEST_BREAK
	frameNear := withWoodenPickaxe(newDirectiveTestFrame("aria", 250, 4000, 8.5, 64, 0.5))
	frameNear.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}, BlockId: "minecraft:coal_ore"},
	}
	breakIntent := EvaluateStrategicDirective(frameNear, state, cfg, nil)
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
	frameNone := withWoodenPickaxe(newDirectiveTestFrame("aria", 300, 4000, 0.5, 64, 0.5))
	poiIntent := EvaluateStrategicDirective(frameNone, state, cfg, nil)
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

	// The directive is a bid source: it makes the Foraging activity eligible (no inline executor).
	if intent := EvaluateStrategicDirective(frame, state, cfg, nil); intent != nil {
		t.Fatalf("directive must not execute foraging itself, got %+v", intent)
	}
	fsm := NewAgentFSM(StageSettlement, nil)
	res := fsm.Tick(&FSMContext{AgentID: "aria", Frame: frame, Config: cfg, State: state})
	if len(res.Intents) == 0 || res.Intents[0].Intent.GetInteractBlock() == nil ||
		res.Intents[0].Intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK on sweet_berry_bush for FORAGE_FOOD, got %+v", res.Intents)
	}
}

func TestStrategicDirective_ScoutFrontierFanOutAndCompletion(t *testing.T) {
	a := newExpRig(t, "aria", 2)
	b := newExpRig(t, "bran", 2)

	// First tick starts an expedition frame over the directive and emits the 360 survey (ADR-032).
	surveyA, surveyB := a.step(510), b.step(510)
	if !strings.HasPrefix(firstIntent(surveyA), "Survey(") || !strings.HasPrefix(firstIntent(surveyB), "Survey(") {
		t.Fatalf("expected initial survey intents for SCOUT_FRONTIER, got A=%v B=%v", surveyA.Intents, surveyB.Intents)
	}

	// Completed uniform open-plains surveys -> agents fan out by complementary clan wedge bearings.
	xA, zA := navTarget(t, a.step(545, withSurvey(uniformSurvey(0, 0, 540))))
	xB, zB := navTarget(t, b.step(545, withSurvey(uniformSurvey(0, 0, 540))))
	distA, distB := math.Hypot(xA, zA), math.Hypot(xB, zB)
	if distA < 31.5 || distA > 48.5 || distB < 31.5 || distB > 48.5 {
		t.Fatalf("expected frontier target 32-48m away, got distA=%.2f distB=%.2f", distA, distB)
	}
	if math.Hypot(xA-xB, zA-zB) < 1.0 {
		t.Fatalf("expected different agents to fan out in different directions, got A=(%.1f,%.1f) B=(%.1f,%.1f)", xA, zA, xB, zB)
	}

	// Arriving at the waypoint ends the directive; the expedition carries on with its next survey.
	if _, active := a.st.ActiveStrategicDirective(560); !active {
		t.Fatal("directive must still be active before arrival")
	}
	a.step(600, withPos(xA, 64, zA))
	if _, active := a.st.ActiveStrategicDirective(600); active {
		t.Fatal("expected SCOUT_FRONTIER directive to be cleared after reaching the waypoint")
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
	navIntent := EvaluateStrategicDirective(frameFar, state, cfg, nil)
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
	if nearIntent := EvaluateStrategicDirective(frameNear, state, cfg, nil); nearIntent != nil {
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
	if intent := EvaluateStrategicDirective(frameHostile, state, cfg, nil); intent != nil {
		t.Fatalf("expected hostile threat to preempt directive, got %+v", intent)
	}

	// 2. Critical hunger -> preempts directive
	frameHungry := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	frameHungry.Status.FoodLevel = 5
	frameHungry.InterestingBlocks = frameHostile.InterestingBlocks
	if intent := EvaluateStrategicDirective(frameHungry, state, cfg, nil); intent != nil {
		t.Fatalf("expected critical hunger to preempt directive, got %+v", intent)
	}

	// 3. Latency check (<5ms)
	frameClean := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	frameClean.InterestingBlocks = frameHostile.InterestingBlocks
	start := time.Now()
	for i := 0; i < 100; i++ {
		frameClean.WorldTick = int64(200 + i*25)
		_ = EvaluateStrategicDirective(frameClean, state, cfg, nil)
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

	frame := withWoodenPickaxe(newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5))
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

func TestEvaluateStrategicDirective_IgnoresDistantHostileAndPreservesSurvey(t *testing.T) {
	state := NewAgentState()
	state.SetStrategicDirective(DirectiveActionMineStone, "ridge", "MINE_STONE: Gather stone", 100, 6100)
	state.activityKinds = []string{activityKindExpedition, activityKindSurvey}
	cfg := xdg.DefaultAgentIdentityConfig("aria")

	// Zombie at 18m (> MaxPerceptionRadius 16m) must NOT preempt or clear ExpeditionSurveying.
	frameDistant := newDirectiveTestFrame("aria", 200, 19000, 0, 86, 0)
	frameDistant.VisibleEntities = []*civquestv1.VisibleEntity{
		{EntityId: 9001, EntityType: "minecraft:zombie", IsHostile: true, Distance: 18.0, Position: &civquestv1.Vec3{X: 18, Y: 86, Z: 0}},
	}
	if intent := EvaluateStrategicDirective(frameDistant, state, cfg, nil); intent != nil {
		t.Fatalf("expected nil during active ExpeditionSurveying, got %+v", intent)
	}
	if !state.IsExpeditionSurveying(200) {
		t.Fatal("expected the survey frame preserved when a distant 18m zombie is present")
	}

	// Harmless daytime zombie at 12m (> 8m) must NOT preempt directive.
	state.activityKinds = nil
	frameDay := withWoodenPickaxe(newDirectiveTestFrame("aria", 220, 3000, 0.5, 64, 0.5))
	frameDay.VisibleEntities = []*civquestv1.VisibleEntity{
		{EntityId: 9002, EntityType: "minecraft:zombie", IsHostile: true, Distance: 12.0, Position: &civquestv1.Vec3{X: 12.5, Y: 64, Z: 0.5}},
	}
	frameDay.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:stone"},
	}
	if intent := EvaluateStrategicDirective(frameDay, state, cfg, nil); intent == nil || intent.GetInteractBlock() == nil {
		t.Fatalf("expected daytime directive to ignore 12m non-creeper zombie and harvest stone, got %+v", intent)
	}
}

func withWoodenPickaxe(f *civquestv1.TelemetryFrame) *civquestv1.TelemetryFrame {
	f.Status.Inventory = append(f.Status.Inventory, &civquestv1.ItemStackProto{Slot: 8, ItemId: "minecraft:wooden_pickaxe", Count: 1})
	return f
}
