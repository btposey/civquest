package system1

import (
	"math"
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestCivicPatrolSkill_MetadataAndPreconditions(t *testing.T) {
	skill := NewCivicPatrolSkill()
	if skill.Name() != "civic_patrol" {
		t.Errorf("expected name 'civic_patrol', got %q", skill.Name())
	}
	if skill.Category() != "security" {
		t.Errorf("expected category 'security', got %q", skill.Category())
	}
	if !skill.Interruptible() {
		t.Errorf("expected skill to be interruptible")
	}

	if skill.Preconditions(nil) {
		t.Errorf("expected false for nil facts")
	}

	facts := NewFactGraph()
	if skill.Preconditions(facts) {
		t.Errorf("expected false for empty facts")
	}

	// 1. patrol.active = true -> true
	facts = NewFactGraph()
	facts.SetBool("patrol.active", "navigation", true, "guard on duty")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true when patrol.active is true")
	}

	// 2. circadian.routine = CIVIC_PATROL -> true
	facts = NewFactGraph()
	facts.SetString("circadian.routine", "Circadian", "CIVIC_PATROL", "night watch")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true when routine is CIVIC_PATROL")
	}

	// 3. Other routine without patrol.active -> false
	facts = NewFactGraph()
	facts.SetString("circadian.routine", "Circadian", "SLEEP", "sleeping")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when neither patrol.active nor CIVIC_PATROL routine")
	}
}

func TestCivicPatrolSkill_Execute(t *testing.T) {
	skill := NewCivicPatrolSkill()

	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{
		Profession: "GUARD",
		Traits:     xdg.AgentTraits{Curiosity: 0.5},
	}

	// Provide two warmup frames so EvaluatePatrol advances past the telemetryFramesSeen < 2 check
	pos := &civquestv1.Vec3{X: 100, Y: 64, Z: 100}
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 10,
		Status: &civquestv1.AgentStatus{
			Position:        pos,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			Position:        pos,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}

	ctx := &SkillContext{
		Walk:    &walkLocal{},
		AgentID: "guard-1",
		Frame:   frame1,
		Config:  cfg,
		State:   state,
	}

	facts := NewFactGraph()
	facts.SetBool("patrol.active", "security", true, "patrol active")

	// First execution warms up frame count
	_, _ = skill.Execute(ctx, facts)

	// Second execution emits NavigateIntent
	ctx.Frame = frame2
	intents, err := skill.Execute(ctx, facts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intents) == 0 {
		t.Fatalf("expected patrol NavigateIntent, got none")
	}

	nav := intents[0].GetNavigate()
	if nav == nil || nav.Target == nil {
		t.Fatalf("expected NavigateIntent action, got %v", intents[0].Action)
	}

	// Verify target is within radial patrol distance
	dist := math.Hypot(nav.Target.X-pos.X, nav.Target.Z-pos.Z)
	if dist < 1.0 {
		t.Errorf("patrol target too close to position: %f", dist)
	}
}

func TestFrontierScoutingSkill_MetadataAndPreconditions(t *testing.T) {
	skill := NewFrontierScoutingSkill()
	if skill.Name() != "frontier_scouting" {
		t.Errorf("expected name 'frontier_scouting', got %q", skill.Name())
	}
	if skill.Category() != "exploration" {
		t.Errorf("expected category 'exploration', got %q", skill.Category())
	}
	if !skill.Interruptible() {
		t.Errorf("expected skill to be interruptible")
	}

	if skill.Preconditions(nil) {
		t.Errorf("expected false for nil facts")
	}

	// 1. scouting.active true + PRIMARY_DUTY -> true
	facts := NewFactGraph()
	facts.SetBool("scouting.active", "navigation", true, "explorer active")
	facts.SetString("circadian.routine", "Circadian", "PRIMARY_DUTY", "working")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true for scouting.active + PRIMARY_DUTY")
	}

	// 2. scouting.active true + DAWN_PREP -> true
	facts = NewFactGraph()
	facts.SetBool("scouting.active", "navigation", true, "explorer active")
	facts.SetString("circadian.routine", "Circadian", "DAWN_PREP", "morning prep")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true for scouting.active + DAWN_PREP")
	}

	// 3. scouting.active true + SLEEP -> false
	facts = NewFactGraph()
	facts.SetBool("scouting.active", "navigation", true, "explorer active")
	facts.SetString("circadian.routine", "Circadian", "SLEEP", "sleeping")
	if skill.Preconditions(facts) {
		t.Errorf("expected false for scouting during SLEEP")
	}

	// 4. scouting.active false + PRIMARY_DUTY -> false
	facts = NewFactGraph()
	facts.SetBool("scouting.active", "navigation", false, "not scouting")
	facts.SetString("circadian.routine", "Circadian", "PRIMARY_DUTY", "working")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when scouting.active is false")
	}
}

func TestFrontierScoutingSkill_Execute(t *testing.T) {
	skill := NewFrontierScoutingSkill()

	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{
		Profession: "EXPLORER",
	}

	pos := &civquestv1.Vec3{X: 500, Y: 64, Z: 500}
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 10,
		Status: &civquestv1.AgentStatus{
			Position:        pos,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        pos,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}

	ctx := &SkillContext{
		Walk:    &walkLocal{},
		AgentID: "scout-1",
		Frame:   frame1,
		Config:  cfg,
		State:   state,
	}

	facts := NewFactGraph()
	facts.SetBool("scouting.active", "navigation", true, "scouting active")
	facts.SetString("circadian.routine", "Circadian", "PRIMARY_DUTY", "working")

	// First execution warms up seen frames
	_, _ = skill.Execute(ctx, facts)

	// Second execution emits scouting NavigateIntent
	ctx.Frame = frame2
	intents, err := skill.Execute(ctx, facts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intents) == 0 {
		t.Fatalf("expected scouting NavigateIntent, got none")
	}

	nav := intents[0].GetNavigate()
	if nav == nil || nav.Target == nil {
		t.Fatalf("expected NavigateIntent target, got %v", intents[0].Action)
	}
	dist := math.Hypot(nav.Target.X-pos.X, nav.Target.Z-pos.Z)
	if dist < 10.0 {
		t.Errorf("expected progressive outward leg distance, got %f", dist)
	}
}

func TestRoadPavingSkill_MetadataAndPreconditions(t *testing.T) {
	skill := NewRoadPavingSkill()
	if skill.Name() != "road_paving" {
		t.Errorf("expected name 'road_paving', got %q", skill.Name())
	}
	if skill.Category() != "infrastructure" {
		t.Errorf("expected category 'infrastructure', got %q", skill.Category())
	}
	if !skill.Interruptible() {
		t.Errorf("expected skill to be interruptible")
	}

	if skill.Preconditions(nil) {
		t.Errorf("expected false for nil facts")
	}

	// 1. paving.needs_pave true + vitals.is_sleeping false -> true
	facts := NewFactGraph()
	facts.SetBool("paving.needs_pave", "infrastructure", true, "needs paving")
	facts.SetBool("vitals.is_sleeping", "vitals", false, "awake")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true when needs_pave is true and not sleeping")
	}

	// 2. paving.needs_pave true + vitals.is_sleeping true -> false
	facts = NewFactGraph()
	facts.SetBool("paving.needs_pave", "infrastructure", true, "needs paving")
	facts.SetBool("vitals.is_sleeping", "vitals", true, "asleep")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when agent is sleeping")
	}

	// 3. paving.needs_pave false -> false
	facts = NewFactGraph()
	facts.SetBool("paving.needs_pave", "infrastructure", false, "no paving")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when needs_pave is false")
	}
}

func TestRoadPavingSkill_Execute(t *testing.T) {
	skill := NewRoadPavingSkill()

	state := NewAgentState()
	tracker := NewRoadPavingTracker(100, 3) // threshold of 3 steps
	state.SetPavingTracker(tracker)

	// Set home origin far enough that agent is outside radial exclusion zone (radius 4.0)
	tracker.SetHomeOrigin(&civquestv1.Vec3{X: 0, Y: 64, Z: 0})

	facts := NewFactGraph()
	facts.SetBool("paving.needs_pave", "infrastructure", true, "needs paving")

	// Step 1: Agent at (20, 64, 20) on grass_block
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 10,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 20.5, Y: 64.0, Z: 20.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 20, Y: 63, Z: 20},
				BlockId: "minecraft:grass_block",
			},
		},
	}
	ctx := &SkillContext{
		Walk:          &walkLocal{},
		AgentID:       "builder-1",
		Frame:         frame1,
		State:         state,
		PavingTracker: tracker,
	}

	// Traverse between 20,63,20 and 21,63,20 repeatedly to simulate corridor foot traffic
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 20,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 21.5, Y: 64.0, Z: 20.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 21, Y: 63, Z: 20},
				BlockId: "minecraft:grass_block",
			},
		},
	}

	var paveIntents []*civquestv1.AgentIntent
	var err error

	// Alternate steps to accumulate foot traffic on (20, 63, 20)
	for i := 0; i < 4; i++ {
		ctx.Frame = frame1
		intents, e := skill.Execute(ctx, facts)
		if len(intents) > 0 {
			paveIntents = intents
		}
		if e != nil {
			err = e
		}

		ctx.Frame = frame2
		_, _ = skill.Execute(ctx, facts)
	}

	if err != nil {
		t.Fatalf("unexpected error during road paving execute: %v", err)
	}
	if len(paveIntents) == 0 {
		t.Fatalf("expected PAVE_ROAD_PATH intent after crossing step threshold, got none")
	}

	intent := paveIntents[0]
	action := intent.GetInteractBlock()
	if action == nil {
		t.Fatalf("expected InteractBlockIntent, got %v", intent.Action)
	}
	if action.InteractionType != civquestv1.InteractBlockIntent_PAVE_ROAD_PATH {
		t.Errorf("expected PAVE_ROAD_PATH interaction, got %v", action.InteractionType)
	}
}

func TestDefaultRegistry_NavigationSkillsIntegration(t *testing.T) {
	reg := NewDefaultEngineRegistry()

	// 1. Verify evaluators registered
	navEval, ok := reg.GetEvaluator("navigation")
	if !ok || navEval == nil {
		t.Fatalf("expected 'navigation' evaluator registered in NewDefaultEngineRegistry()")
	}
	infraEval, ok := reg.GetEvaluator("infrastructure")
	if !ok || infraEval == nil {
		t.Fatalf("expected 'infrastructure' evaluator registered in NewDefaultEngineRegistry()")
	}

	// 2. Verify skills registered
	patrolSkill, ok := reg.GetSkill("civic_patrol")
	if !ok || patrolSkill == nil {
		t.Fatalf("expected 'civic_patrol' skill registered in NewDefaultEngineRegistry()")
	}
	scoutSkill, ok := reg.GetSkill("frontier_scouting")
	if !ok || scoutSkill == nil {
		t.Fatalf("expected 'frontier_scouting' skill registered in NewDefaultEngineRegistry()")
	}
	paveSkill, ok := reg.GetSkill("road_paving")
	if !ok || paveSkill == nil {
		t.Fatalf("expected 'road_paving' skill registered in NewDefaultEngineRegistry()")
	}

	// 3. Test SelectAndExecuteSkill for security (patrol)
	state := NewAgentState()
	state.SetCivicPatrolActive(true)
	pos := &civquestv1.Vec3{X: 10, Y: 64, Z: 10}
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 50,
		Status: &civquestv1.AgentStatus{
			Position:        pos,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 300,
		Status: &civquestv1.AgentStatus{
			Position:        pos,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}

	cfg := &xdg.AgentIdentityConfig{Profession: "GUARD"}
	skillCtx := &SkillContext{
		Walk:    &walkLocal{},
		AgentID: "reg-guard",
		Frame:   frame1,
		Config:  cfg,
		State:   state,
	}

	evalCtx := &EvaluatorContext{
		AgentID: "reg-guard",
		Frame:   frame1,
		Config:  cfg,
		State:   state,
	}
	facts := reg.EvaluateAll(evalCtx)

	// Warmup
	_, _, _ = reg.SelectAndExecuteSkill(skillCtx, facts, "security")

	// Execute with frame2
	skillCtx.Frame = frame2
	intents, name, executed := reg.SelectAndExecuteSkill(skillCtx, facts, "security")
	if !executed || len(intents) == 0 {
		t.Fatalf("expected SelectAndExecuteSkill to execute 'civic_patrol', got executed=%t, intents=%d", executed, len(intents))
	}
	if name != "civic_patrol" {
		t.Errorf("expected skill name 'civic_patrol', got %q", name)
	}
}

func TestNavigationSkills_Sub5msBenchmark(t *testing.T) {
	patrol := NewCivicPatrolSkill()
	scout := NewFrontierScoutingSkill()
	pave := NewRoadPavingSkill()

	facts := NewFactGraph()
	facts.SetBool("patrol.active", "navigation", true, "patrol active")
	facts.SetBool("scouting.active", "navigation", true, "scouting active")
	facts.SetBool("paving.needs_pave", "infrastructure", true, "paving active")
	facts.SetString("circadian.routine", "Circadian", "PRIMARY_DUTY", "working")

	start := time.Now()
	iterations := 5000
	for i := 0; i < iterations; i++ {
		_ = patrol.Preconditions(facts)
		_ = scout.Preconditions(facts)
		_ = pave.Preconditions(facts)
	}
	elapsed := time.Since(start)
	perOp := elapsed / time.Duration(iterations*3)

	if perOp > 5*time.Millisecond {
		t.Fatalf("Preconditions took %v per op, exceeded 5ms SLA", perOp)
	}
}
