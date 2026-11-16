package system1

import (
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestEngineRegistry_RegistrationAndLookup(t *testing.T) {
	reg := NewEngineRegistry()

	// Initial empty lookups
	if _, ok := reg.GetEvaluator("armory_militia"); ok {
		t.Errorf("expected armory_militia to not be registered")
	}
	if _, ok := reg.GetSkill("armory_crafting"); ok {
		t.Errorf("expected armory_crafting to not be registered")
	}

	// Register evaluator
	eval := NewArmoryMilitiaEvaluator()
	reg.RegisterEvaluator(eval)
	retEval, ok := reg.GetEvaluator("armory_militia")
	if !ok || retEval == nil || retEval.Name() != "armory_militia" {
		t.Fatalf("expected to retrieve armory_militia evaluator")
	}

	// Register skill
	skill := NewArmoryCraftingSkill()
	reg.RegisterSkill(skill)
	retSkill, ok := reg.GetSkill("armory_crafting")
	if !ok || retSkill == nil || retSkill.Name() != "armory_crafting" {
		t.Fatalf("expected to retrieve armory_crafting skill")
	}

	// Reset
	reg.Reset()
	if _, ok := reg.GetEvaluator("armory_militia"); ok {
		t.Errorf("expected evaluator to be cleared after reset")
	}
	if _, ok := reg.GetSkill("armory_crafting"); ok {
		t.Errorf("expected skill to be cleared after reset")
	}
}

func TestEngineRegistry_EvaluateAll(t *testing.T) {
	reg := NewEngineRegistry()
	reg.RegisterEvaluator(NewArmoryMilitiaEvaluator())
	reg.RegisterEvaluator(NewCircadianEvaluator())

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 1200,
		TimeOfDay: 12000, // Late Day Tavern Social
		Weather:   civquestv1.Weather_WEATHER_CLEAR,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:iron_ingot", Count: 10},
				{ItemId: "minecraft:stick", Count: 10},
			},
		},
	}
	cfg := &xdg.AgentIdentityConfig{
		Shift: xdg.ShiftFirst,
	}
	state := NewAgentState()

	evalCtx := &EvaluatorContext{
		AgentID: "agent-test-1",
		Frame:   frame,
		Config:  cfg,
		State:   state,
	}

	facts := reg.EvaluateAll(evalCtx)
	if facts == nil {
		t.Fatalf("expected non-nil FactGraph from EvaluateAll")
	}

	// Verify Armory facts
	if facts.GetBool("armory.sufficient") {
		t.Errorf("expected armory.sufficient to be false due to missing weapons/armor")
	}
	if !facts.GetBool("armory.has_materials") {
		t.Errorf("expected armory.has_materials to be true with iron and sticks")
	}
	pop, ok := facts.GetInt("armory.population")
	if !ok || pop != 1 {
		t.Errorf("expected armory.population to be 1, got (%d, %t)", pop, ok)
	}

	// Verify Circadian facts
	tod, ok := facts.GetInt("circadian.time_of_day")
	if !ok || tod != 12000 {
		t.Errorf("expected circadian.time_of_day to be 12000, got (%d, %t)", tod, ok)
	}
	if !facts.GetBool("circadian.is_tavern_social") {
		t.Errorf("expected circadian.is_tavern_social to be true at tick 12000")
	}
	if facts.GetString("circadian.shift") != xdg.ShiftFirst {
		t.Errorf("expected %s shift, got %q", xdg.ShiftFirst, facts.GetString("circadian.shift"))
	}
}

func TestEngineRegistry_SelectAndExecuteSkill_ArmoryCrafting(t *testing.T) {
	reg := NewEngineRegistry()
	reg.RegisterEvaluator(NewArmoryMilitiaEvaluator())
	reg.RegisterSkill(NewArmoryCraftingSkill())

	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{Shift: xdg.ShiftFirst}

	// 1. Scenario: Deficit exists with materials -> Skill should be selected and produce intent
	frameDeficit := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:feather", Count: 10},
				{ItemId: "minecraft:stick", Count: 10},
				{ItemId: "minecraft:flint", Count: 10},
			},
		},
	}

	evalCtx := &EvaluatorContext{
		AgentID: "agent-1",
		Frame:   frameDeficit,
		Config:  cfg,
		State:   state,
	}
	factsDeficit := reg.EvaluateAll(evalCtx)

	skillCtx := &SkillContext{
		AgentID: "agent-1",
		Frame:   frameDeficit,
		Config:  cfg,
		State:   state,
	}

	intents, skillName, selected := reg.SelectAndExecuteSkill(skillCtx, factsDeficit)
	if !selected {
		t.Fatalf("expected skill to be selected when armory deficit exists and materials are present")
	}
	if skillName != "armory_crafting" {
		t.Errorf("expected skill armory_crafting to be selected, got %q", skillName)
	}
	if len(intents) == 0 {
		t.Fatalf("expected at least one intent from armory_crafting execution")
	}
	craftIntent := intents[0].GetCraftItem()
	if craftIntent == nil {
		t.Fatalf("expected CraftItem intent, got %+v", intents[0].Action)
	}
	if craftIntent.RecipeId != "minecraft:arrow" {
		t.Errorf("expected recipe minecraft:arrow, got %s", craftIntent.RecipeId)
	}

	// 2. Scenario: Armory fully satisfied -> Skill should NOT be selected
	frameSufficient := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:iron_sword", Count: 1},
				{ItemId: "minecraft:bow", Count: 1},
				{ItemId: "minecraft:iron_helmet", Count: 1},
				{ItemId: "minecraft:iron_chestplate", Count: 1},
				{ItemId: "minecraft:iron_leggings", Count: 1},
				{ItemId: "minecraft:iron_boots", Count: 1},
				{ItemId: "minecraft:arrow", Count: 20},
				{ItemId: "minecraft:tnt", Count: 4},
			},
		},
	}

	evalCtxSuff := &EvaluatorContext{
		AgentID: "agent-1",
		Frame:   frameSufficient,
		Config:  cfg,
		State:   state,
	}
	factsSufficient := reg.EvaluateAll(evalCtxSuff)
	if !factsSufficient.GetBool("armory.sufficient") {
		t.Errorf("expected armory.sufficient to be true with complete quotas")
	}

	skillCtxSuff := &SkillContext{
		AgentID: "agent-1",
		Frame:   frameSufficient,
		Config:  cfg,
		State:   state,
	}
	intentsSuff, skillNameSuff, selectedSuff := reg.SelectAndExecuteSkill(skillCtxSuff, factsSufficient)
	if selectedSuff {
		t.Errorf("expected skill to be skipped when armory is sufficient, but got %s with %d intents", skillNameSuff, len(intentsSuff))
	}
}

func TestAgentFSM_Tick_FactsIntegration(t *testing.T) {
	// Verify that AgentFSM.Tick populates ctx.Facts if nil, and SettlementStateHandler uses it
	fsm := NewAgentFSM(StageSettlement, nil)
	state := NewAgentState()
	state.SetStageAndRoutine(StageSettlement, RoutineArmoryMaintenance)
	state.SetArmoryMaintenanceNeeded(true)

	cfg := &xdg.AgentIdentityConfig{
		Shift: xdg.ShiftThird,
	}

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 50,
		TimeOfDay: 1500,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:stone_axe", Count: 1},
				{ItemId: "minecraft:stone_sword", Count: 1},
				{ItemId: "minecraft:torch", Count: 16},
				{ItemId: "minecraft:feather", Count: 5},
				{ItemId: "minecraft:stick", Count: 5},
				{ItemId: "minecraft:flint", Count: 5},
			},
		},
	}

	ctx := &FSMContext{
		AgentID: "agent-integration-1",
		Frame:   frame,
		State:   state,
		Config:  cfg,
	}

	if ctx.Facts != nil {
		t.Fatalf("expected ctx.Facts to initially be nil")
	}

	res := fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Errorf("expected StageSettlement, got %v", res.Stage)
	}

	// ctx.Facts must now be populated
	if ctx.Facts == nil {
		t.Fatalf("expected ctx.Facts to be populated by AgentFSM.Tick")
	}

	if ctx.Facts.GetBool("armory.sufficient") {
		t.Errorf("expected armory.sufficient to be false")
	}
	if !ctx.Facts.GetBool("armory.has_materials") {
		t.Errorf("expected armory.has_materials to be true")
	}

	// Verify fsm.Tick emitted crafting intent via handler.OnTick
	if len(res.Intents) == 0 {
		t.Fatalf("expected fsm.Tick to emit armory crafting intent, got 0 intents")
	}
	craft := res.Intents[0].Intent.GetCraftItem()
	if craft == nil {
		t.Fatalf("expected CraftItem intent from fsm.Tick, got %+v", res.Intents[0].Intent.Action)
	}
	if craft.RecipeId != "minecraft:arrow" {
		t.Errorf("expected recipe minecraft:arrow, got %s", craft.RecipeId)
	}
}

func BenchmarkEvaluateAndSelectSkill(b *testing.B) {
	reg := NewEngineRegistry()
	reg.RegisterEvaluator(NewArmoryMilitiaEvaluator())
	reg.RegisterEvaluator(NewCircadianEvaluator())
	reg.RegisterSkill(NewArmoryCraftingSkill())

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:iron_ingot", Count: 10},
				{ItemId: "minecraft:stick", Count: 10},
			},
		},
	}
	cfg := &xdg.AgentIdentityConfig{Shift: xdg.ShiftFirst}
	state := NewAgentState()

	evalCtx := &EvaluatorContext{
		AgentID: "bench-agent",
		Frame:   frame,
		Config:  cfg,
		State:   state,
	}
	skillCtx := &SkillContext{
		AgentID: "bench-agent",
		Frame:   frame,
		Config:  cfg,
		State:   state,
	}

	b.ResetTimer()
	start := time.Now()

	for i := 0; i < b.N; i++ {
		facts := reg.EvaluateAll(evalCtx)
		_, _, _ = reg.SelectAndExecuteSkill(skillCtx, facts)
	}

	elapsed := time.Since(start)
	perOp := elapsed / time.Duration(b.N)

	// Ensure sub-5ms performance invariant (ADR-005: System 1 must execute synchronously in <5ms)
	if perOp > 5*time.Millisecond {
		b.Fatalf("EvaluateAll + SelectAndExecuteSkill exceeded 5ms limit: %v per op", perOp)
	}
}
