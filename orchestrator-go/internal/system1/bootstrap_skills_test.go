package system1

import (
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestBootstrapGatherSkill_Preconditions(t *testing.T) {
	skill := NewBootstrapGatherSkill()
	if skill.Name() != "bootstrap_gather" {
		t.Errorf("expected name 'bootstrap_gather', got %q", skill.Name())
	}
	if skill.Category() != "bootstrap" {
		t.Errorf("expected category 'bootstrap', got %q", skill.Category())
	}
	if !skill.Interruptible() {
		t.Errorf("expected skill to be interruptible")
	}

	if skill.Preconditions(nil) {
		t.Errorf("expected false for nil facts")
	}

	facts := NewFactGraph()
	// Has kit -> false
	facts.SetBool("bootstrap.has_kit", "bootstrap", true, "has kit")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when has_kit is true")
	}

	// On cooldown -> false
	facts = NewFactGraph()
	facts.SetBool("bootstrap.on_cooldown", "bootstrap", true, "cooldown")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when on_cooldown is true")
	}

	// log_count >= 4 -> false
	facts = NewFactGraph()
	facts.SetInt("bootstrap.log_count", "bootstrap", 4, "4 logs")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when log_count >= 4")
	}

	// log_count < 4 without kit or cooldown -> true
	facts = NewFactGraph()
	facts.SetInt("bootstrap.log_count", "bootstrap", 2, "2 logs")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true when log_count < 4 and kit incomplete")
	}
}

func TestBootstrapCraftSkill_Preconditions(t *testing.T) {
	skill := NewBootstrapCraftSkill()
	if skill.Name() != "bootstrap_craft" {
		t.Errorf("expected name 'bootstrap_craft', got %q", skill.Name())
	}
	if skill.Category() != "bootstrap" {
		t.Errorf("expected category 'bootstrap', got %q", skill.Category())
	}
	if !skill.Interruptible() {
		t.Errorf("expected skill to be interruptible")
	}

	if skill.Preconditions(nil) {
		t.Errorf("expected false for nil facts")
	}

	// Has kit -> false
	facts := NewFactGraph()
	facts.SetBool("bootstrap.has_kit", "bootstrap", true, "has kit")
	facts.SetInt("bootstrap.log_count", "bootstrap", 5, "5 logs")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when has_kit is true")
	}

	// On cooldown -> false
	facts = NewFactGraph()
	facts.SetBool("bootstrap.on_cooldown", "bootstrap", true, "cooldown")
	facts.SetInt("bootstrap.log_count", "bootstrap", 2, "2 logs")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when on_cooldown is true")
	}

	// 0 logs, 0 planks -> false
	facts = NewFactGraph()
	facts.SetInt("bootstrap.log_count", "bootstrap", 0, "0 logs")
	facts.SetInt("bootstrap.plank_count", "bootstrap", 0, "0 planks")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when 0 logs and 0 planks")
	}

	// log_count >= 1 -> true
	facts = NewFactGraph()
	facts.SetInt("bootstrap.log_count", "bootstrap", 1, "1 log")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true when log_count >= 1")
	}

	// plank_count >= 4 -> true
	facts = NewFactGraph()
	facts.SetInt("bootstrap.plank_count", "bootstrap", 4, "4 planks")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true when plank_count >= 4")
	}

	// stick_count >= 1 without coal -> false
	facts = NewFactGraph()
	facts.SetInt("bootstrap.stick_count", "bootstrap", 3, "3 sticks")
	facts.SetInt("bootstrap.coal_count", "bootstrap", 0, "0 coal")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when only sticks are held without coal")
	}

	// stick_count >= 1 && coal_count >= 1 -> true (pocket torch crafting)
	facts = NewFactGraph()
	facts.SetInt("bootstrap.stick_count", "bootstrap", 1, "1 stick")
	facts.SetInt("bootstrap.coal_count", "bootstrap", 1, "1 coal")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true when stick_count >= 1 and coal_count >= 1")
	}
}

func TestShelterConstructionSkill_Preconditions(t *testing.T) {
	skill := NewShelterConstructionSkill()
	if skill.Name() != "shelter_construction" {
		t.Errorf("expected name 'shelter_construction', got %q", skill.Name())
	}
	if skill.Category() != "shelter" {
		t.Errorf("expected category 'shelter', got %q", skill.Category())
	}
	if !skill.Interruptible() {
		t.Errorf("expected skill to be interruptible")
	}

	if skill.Preconditions(nil) {
		t.Errorf("expected false for nil facts")
	}

	// Complete -> false
	facts := NewFactGraph()
	facts.SetBool("shelter.complete", "shelter", true, "complete")
	facts.SetBool("circadian.is_dusk_or_night", "Circadian", true, "night")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when shelter.complete is true")
	}

	// On cooldown -> false
	facts = NewFactGraph()
	facts.SetBool("shelter.on_cooldown", "shelter", true, "cooldown")
	facts.SetBool("circadian.is_dusk_or_night", "Circadian", true, "night")
	if skill.Preconditions(facts) {
		t.Errorf("expected false when shelter.on_cooldown is true")
	}

	// Daytime and not sleep routine -> false
	facts = NewFactGraph()
	facts.SetBool("circadian.is_dusk_or_night", "Circadian", false, "day")
	facts.SetString("circadian.routine", "Circadian", "PRIMARY_DUTY", "working")
	if skill.Preconditions(facts) {
		t.Errorf("expected false during daytime work shift")
	}

	// Dusk/night active -> true
	facts = NewFactGraph()
	facts.SetBool("circadian.is_dusk_or_night", "Circadian", true, "night")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true during dusk/night")
	}

	// Routine SLEEP active -> true
	facts = NewFactGraph()
	facts.SetString("circadian.routine", "Circadian", "SLEEP", "sleep")
	if !skill.Preconditions(facts) {
		t.Errorf("expected true when routine is SLEEP")
	}
}

func TestBootstrapGatherSkill_Execute(t *testing.T) {
	skill := NewBootstrapGatherSkill()

	// Tree log within reach (distance <= 4.5 blocks)
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 50,
		Status: &civquestv1.AgentStatus{
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				BlockId: "minecraft:oak_log",
				Pos:     &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 10},
			},
		},
	}
	state := NewAgentState()
	skillCtx := &SkillContext{
		AgentID: "agent-gather",
		Frame:   frame,
		State:   state,
	}
	facts := NewFactGraph()
	facts.SetInt("bootstrap.log_count", "bootstrap", 0, "0 logs")

	intents, err := skill.Execute(skillCtx, facts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intents) == 0 {
		t.Fatalf("expected gather intent, got 0")
	}

	interact := intents[0].GetInteractBlock()
	if interact == nil {
		t.Fatalf("expected InteractBlock intent, got %+v", intents[0].Action)
	}
	if interact.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Errorf("expected HARVEST_BREAK, got %v", interact.InteractionType)
	}
}

func TestBootstrapCraftSkill_Execute(t *testing.T) {
	skill := NewBootstrapCraftSkill()

	// Agent has 2 oak logs, needs crafting table
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 50,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_log", Count: 2},
			},
		},
	}
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	skillCtx := &SkillContext{
		AgentID: "agent-craft",
		Frame:   frame,
		State:   state,
	}
	facts := NewFactGraph()
	facts.SetInt("bootstrap.log_count", "bootstrap", 2, "2 logs")

	intents, err := skill.Execute(skillCtx, facts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intents) == 0 {
		t.Fatalf("expected craft intent, got 0")
	}

	craft := intents[0].GetCraftItem()
	if craft == nil {
		t.Fatalf("expected CraftItem intent, got %+v", intents[0].Action)
	}
	if craft.RecipeId != "minecraft:oak_planks" {
		t.Errorf("expected recipe minecraft:oak_planks as first multi-step craft from logs, got %s", craft.RecipeId)
	}
}

func TestShelterConstructionSkill_Execute(t *testing.T) {
	skill := NewShelterConstructionSkill()

	// Agent at night with oak planks
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 120,
		TimeOfDay: 14000,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_planks", Count: 64},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				BlockId: "minecraft:campfire",
				Pos:     &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 11},
			},
		},
	}
	state := NewAgentState()
	state.ShelterPos = &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}
	local := newShelterLocal(state)
	skillCtx := &SkillContext{
		AgentID: "agent-shelter",
		Frame:   frame,
		State:   state,
		Shelter: &local,
	}
	facts := NewFactGraph()
	facts.SetBool("circadian.is_dusk_or_night", "Circadian", true, "night")

	intents, err := skill.Execute(skillCtx, facts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intents) == 0 {
		t.Fatalf("expected shelter construction intent, got 0")
	}

	// Should emit a block placement or navigation intent
	if intents[0].GetInteractBlock() == nil && intents[0].GetNavigate() == nil {
		t.Fatalf("expected InteractBlock or Navigate intent, got %+v", intents[0].Action)
	}
}

func TestFSMIntegration_BootstrapAndShelterSkills(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 1000, // Daytime
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_log", Count: 2},
			},
		},
	}
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	ctx := &FSMContext{
		AgentID: "fsm-agent",
		Frame:   frame,
		State:   state,
	}

	// OnTick should run BootstrapStateHandler, evaluate facts, and execute bootstrap_craft
	res := fsm.Tick(ctx)
	if len(res.Intents) == 0 {
		t.Fatalf("expected FSM to emit bootstrap crafting intent")
	}
	if res.Intents[0].Intent.GetCraftItem() == nil {
		t.Errorf("expected CraftItem intent, got %+v", res.Intents[0].Intent.Action)
	}

	// Verify precomputed facts were populated
	if ctx.Facts == nil {
		t.Fatalf("expected ctx.Facts to be populated")
	}
	if ctx.HasSurvivalKit() {
		t.Errorf("expected HasSurvivalKit false")
	}
	if ctx.IsShelterComplete() {
		t.Errorf("expected IsShelterComplete false")
	}
}

func BenchmarkBootstrapSkills_Execution(b *testing.B) {
	gatherSkill := NewBootstrapGatherSkill()
	craftSkill := NewBootstrapCraftSkill()
	shelterSkill := NewShelterConstructionSkill()

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_log", Count: 2},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{BlockId: "minecraft:oak_log", Pos: &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 10}},
		},
	}
	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{Traits: xdg.AgentTraits{Bravery: 0.5}}
	skillCtx := &SkillContext{
		AgentID: "bench-agent",
		Frame:   frame,
		State:   state,
		Config:  cfg,
	}
	facts := NewFactGraph()
	facts.SetInt("bootstrap.log_count", "bootstrap", 2, "2 logs")
	facts.SetBool("circadian.is_dusk_or_night", "Circadian", true, "night")

	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		_, _ = gatherSkill.Execute(skillCtx, facts)
		_, _ = craftSkill.Execute(skillCtx, facts)
		_, _ = shelterSkill.Execute(skillCtx, facts)
	}
	elapsed := time.Since(start)

	if b.N > 0 {
		perOp := elapsed / time.Duration(b.N)
		if perOp > 5*time.Millisecond {
			b.Fatalf("Bootstrap skills execution exceeded 5ms limit: %v per op", perOp)
		}
	}
}
