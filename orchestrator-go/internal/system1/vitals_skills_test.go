package system1

import (
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestBiologicalNeedsSkill_Preconditions(t *testing.T) {
	skill := NewBiologicalNeedsSkill()
	if skill.Name() != "biological_needs" {
		t.Errorf("expected name 'biological_needs', got %q", skill.Name())
	}
	if skill.Category() != "vitals" {
		t.Errorf("expected category 'vitals', got %q", skill.Category())
	}
	if !skill.Interruptible() {
		t.Errorf("expected biological needs skill to be interruptible")
	}

	if skill.Preconditions(nil) {
		t.Errorf("expected Preconditions to return false for nil facts")
	}

	facts := NewFactGraph()
	if skill.Preconditions(facts) {
		t.Errorf("expected Preconditions false for empty facts")
	}

	// 1. can_eat activates skill
	facts.SetBool("vitals.can_eat", "vitals", true, "hungry with food")
	if !skill.Preconditions(facts) {
		t.Errorf("expected Preconditions true when vitals.can_eat is true")
	}

	// 2. is_sleeping activates skill (for dawn wake-up)
	facts = NewFactGraph()
	facts.SetBool("vitals.is_sleeping", "vitals", true, "sleeping")
	if !skill.Preconditions(facts) {
		t.Errorf("expected Preconditions true when vitals.is_sleeping is true")
	}

	// 3. RoutineSleep activates skill
	facts = NewFactGraph()
	facts.SetString("circadian.routine", "Circadian", string(RoutineSleep), "sleep routine")
	if !skill.Preconditions(facts) {
		t.Errorf("expected Preconditions true during RoutineSleep")
	}

	// 4. Night time activates skill
	facts = NewFactGraph()
	facts.SetInt("circadian.time_of_day", "Circadian", 14000, "night time")
	if !skill.Preconditions(facts) {
		t.Errorf("expected Preconditions true during night time (14000)")
	}
}

func TestBiologicalNeedsSkill_Execute_EatingIntent(t *testing.T) {
	skill := NewBiologicalNeedsSkill()

	// Critical hunger (foodLevel == 4 <= 6) with cooked beef in slot 3
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 50,
		Status: &civquestv1.AgentStatus{
			Health:    14,
			MaxHealth: 20,
			FoodLevel: 4,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:cooked_beef", Count: 5, Slot: 3},
			},
		},
	}

	state := NewAgentState()
	skillCtx := &SkillContext{
		AgentID: "agent-bio",
		Frame:   frame,
		State:   state,
	}

	facts := NewFactGraph()
	facts.SetBool("vitals.can_eat", "vitals", true, "critical hunger")

	intents, err := skill.Execute(skillCtx, facts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intents) == 0 {
		t.Fatalf("expected eating intent to be produced")
	}

	useItem := intents[0].GetUseItem()
	if useItem == nil {
		t.Fatalf("expected UseItem intent, got %+v", intents[0].Action)
	}
	if !useItem.IsEatingOrDrinking {
		t.Errorf("expected IsEatingOrDrinking true")
	}
	if useItem.InventorySlot != 3 {
		t.Errorf("expected slot 3, got %d", useItem.InventorySlot)
	}

	// Cooldown test: within 32 ticks, eating intent should not be re-emitted
	frame.WorldTick = 60 // 10 ticks later (< 32 tick eating cooldown)
	intentsCooldown, err := skill.Execute(skillCtx, facts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intentsCooldown) > 0 {
		t.Errorf("expected eating cooldown to suppress new eating intent, got %d intents", len(intentsCooldown))
	}
}

func TestBiologicalNeedsSkill_Execute_SleepAndWakeIntent(t *testing.T) {
	skill := NewBiologicalNeedsSkill()
	state := NewAgentState()
	bedPos := &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}
	state.SetAssignedBed(bedPos)

	// 1. Night time, close to bed (within reach <= 3m) -> Enter bed
	frameNight := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 14000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10.5, Y: 64, Z: 11.0}, // dist ~1.0m
		},
	}
	skillCtx := &SkillContext{
		AgentID: "agent-bio",
		Frame:   frameNight,
		State:   state,
	}
	facts := NewFactGraph()
	facts.SetInt("circadian.time_of_day", "Circadian", 14000, "night")

	intents, err := skill.Execute(skillCtx, facts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intents) == 0 {
		t.Fatalf("expected sleep intent near bed at night")
	}
	interact := intents[0].GetInteractBlock()
	if interact == nil || interact.InteractionType != civquestv1.InteractBlockIntent_SLEEP_IN_BED {
		t.Fatalf("expected SLEEP_IN_BED interaction, got %+v", intents[0].Action)
	}

	// 2. Dawn awakening -> Wake up
	frameDawn := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		TimeOfDay: 23800, // Dawn
		Status: &civquestv1.AgentStatus{
			Health:     20,
			MaxHealth:  20,
			FoodLevel:  20,
			IsSleeping: true,
			Position:   &civquestv1.Vec3{X: 10.5, Y: 64, Z: 10.5},
		},
	}
	skillCtxDawn := &SkillContext{
		AgentID: "agent-bio",
		Frame:   frameDawn,
		State:   state,
	}
	factsDawn := NewFactGraph()
	factsDawn.SetBool("vitals.is_sleeping", "vitals", true, "sleeping")
	factsDawn.SetInt("circadian.time_of_day", "Circadian", 23800, "dawn")

	intentsWake, err := skill.Execute(skillCtxDawn, factsDawn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intentsWake) == 0 {
		t.Fatalf("expected wake up intent at dawn")
	}
	interactWake := intentsWake[0].GetInteractBlock()
	if interactWake == nil || interactWake.InteractionType != civquestv1.InteractBlockIntent_WAKE_UP {
		t.Fatalf("expected WAKE_UP interaction, got %+v", intentsWake[0].Action)
	}
}

func TestCombatDefenseSkill_Preconditions(t *testing.T) {
	skill := NewCombatDefenseSkill()
	if skill.Name() != "combat_defense" {
		t.Errorf("expected name 'combat_defense', got %q", skill.Name())
	}
	if skill.Category() != "combat" {
		t.Errorf("expected category 'combat', got %q", skill.Category())
	}
	if skill.Interruptible() {
		t.Errorf("expected combat defense skill to NOT be interruptible")
	}

	if skill.Preconditions(nil) {
		t.Errorf("expected Preconditions to return false for nil facts")
	}

	facts := NewFactGraph()
	if skill.Preconditions(facts) {
		t.Errorf("expected Preconditions false for empty facts")
	}

	// 1. hostiles_present
	facts.SetBool("threat.hostiles_present", "threat", true, "zombie near")
	if !skill.Preconditions(facts) {
		t.Errorf("expected Preconditions true when threat.hostiles_present")
	}

	// 2. should_panic
	facts = NewFactGraph()
	facts.SetBool("threat.should_panic", "threat", true, "panic")
	if !skill.Preconditions(facts) {
		t.Errorf("expected Preconditions true when threat.should_panic")
	}

	// 3. damage_taken
	facts = NewFactGraph()
	facts.SetBool("threat.damage_taken", "threat", true, "took damage")
	if !skill.Preconditions(facts) {
		t.Errorf("expected Preconditions true when threat.damage_taken")
	}

	// 4. is_on_fire
	facts = NewFactGraph()
	facts.SetBool("vitals.is_on_fire", "vitals", true, "burning")
	if !skill.Preconditions(facts) {
		t.Errorf("expected Preconditions true when vitals.is_on_fire")
	}
}

func TestCombatDefenseSkill_Execute_Reflexes(t *testing.T) {
	skill := NewCombatDefenseSkill()

	// 1. Zombie in melee range -> attack intent emitted
	frameZombie := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:diamond_sword", Count: 1, Slot: 0},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   12,
				EntityType: "minecraft:zombie",
				IsHostile:  true,
				Distance:   2.0, // In melee range <= 3.0
				Position:   &civquestv1.Vec3{X: 1.5, Y: 64, Z: 0},
			},
		},
	}

	factsZombie := NewFactGraph()
	factsZombie.SetBool("threat.hostiles_present", "threat", true, "zombie nearby")
	factsZombie.SetBool("threat.has_weapon", "threat", true, "has sword")
	factsZombie.SetInt("threat.weapon_slot", "threat", 0, "hotbar slot 0")

	ctxZombie := &SkillContext{
		AgentID: "agent-combat",
		Frame:   frameZombie,
		Config:  &xdg.AgentIdentityConfig{Traits: xdg.AgentTraits{Bravery: 0.8}},
	}

	intents, err := skill.Execute(ctxZombie, factsZombie)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intents) == 0 {
		t.Fatalf("expected reflex intents for melee zombie, got 0")
	}

	hasAttack := false
	for _, it := range intents {
		if it.GetAttack() != nil {
			hasAttack = true
			if it.GetAttack().TargetEntityId != 12 {
				t.Errorf("expected target entity ID 12, got %d", it.GetAttack().TargetEntityId)
			}
		}
	}
	if !hasAttack {
		t.Errorf("expected Attack intent against zombie in melee range")
	}

	// 2. Ranged skeleton with shield -> Shield raise intent
	frameSkeleton := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			Health:    18,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:shield", Count: 1, Slot: 40},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   33,
				EntityType: "minecraft:skeleton",
				IsHostile:  true,
				Distance:   8.0,
				HeldItemId: "minecraft:bow",
				Position:   &civquestv1.Vec3{X: 8, Y: 64, Z: 0},
			},
		},
	}

	factsSkeleton := NewFactGraph()
	factsSkeleton.SetBool("threat.hostiles_present", "threat", true, "skeleton nearby")
	factsSkeleton.SetBool("threat.has_shield", "threat", true, "shield available")

	ctxSkeleton := &SkillContext{
		AgentID: "agent-combat",
		Frame:   frameSkeleton,
		Config:  &xdg.AgentIdentityConfig{Traits: xdg.AgentTraits{Bravery: 0.5}},
	}

	intentsSkel, err := skill.Execute(ctxSkeleton, factsSkeleton)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intentsSkel) == 0 {
		t.Fatalf("expected reflex intent against skeleton")
	}
	hasShieldRaise := false
	for _, it := range intentsSkel {
		if att := it.GetAttack(); att != nil && att.RaiseShield {
			hasShieldRaise = true
		}
	}
	if !hasShieldRaise {
		t.Errorf("expected Attack intent with RaiseShield=true against ranged skeleton")
	}
}

func BenchmarkSkills_Execution(b *testing.B) {
	bioSkill := NewBiologicalNeedsSkill()
	combatSkill := NewCombatDefenseSkill()

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Health:    16,
			MaxHealth: 20,
			FoodLevel: 4,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:cooked_beef", Count: 8, Slot: 0},
				{ItemId: "minecraft:iron_sword", Count: 1, Slot: 1},
				{ItemId: "minecraft:shield", Count: 1, Slot: 40},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   55,
				EntityType: "minecraft:zombie",
				IsHostile:  true,
				Distance:   2.5,
				Position:   &civquestv1.Vec3{X: 12, Y: 64, Z: 10},
			},
		},
	}

	state := NewAgentState()
	skillCtx := &SkillContext{
		AgentID: "bench-agent",
		Frame:   frame,
		State:   state,
		Config:  &xdg.AgentIdentityConfig{Traits: xdg.AgentTraits{Bravery: 0.5}},
	}

	facts := NewFactGraph()
	facts.SetBool("vitals.can_eat", "vitals", true, "hungry")
	facts.SetBool("threat.hostiles_present", "threat", true, "zombie nearby")
	facts.SetBool("threat.has_weapon", "threat", true, "has sword")
	facts.SetInt("threat.weapon_slot", "threat", 1, "slot 1")

	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		_, _ = bioSkill.Execute(skillCtx, facts)
		_, _ = combatSkill.Execute(skillCtx, facts)
	}
	elapsed := time.Since(start)

	if b.N > 0 {
		perOp := elapsed / time.Duration(b.N)
		if perOp > 5*time.Millisecond {
			b.Fatalf("Skills execution exceeded 5ms limit: %v per op", perOp)
		}
	}
}
