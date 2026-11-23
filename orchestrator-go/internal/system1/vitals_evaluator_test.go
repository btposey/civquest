package system1

import (
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestVitalsEvaluator_PopulatesAllFacts(t *testing.T) {
	eval := NewVitalsEvaluator()
	if eval.Name() != "vitals" {
		t.Errorf("expected name 'vitals', got %q", eval.Name())
	}
	if eval.Category() != "vitals" {
		t.Errorf("expected category 'vitals', got %q", eval.Category())
	}

	// 1. Healthy agent with food
	frameHealthy := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Health:     18,
			MaxHealth:  20,
			FoodLevel:  15,
			IsOnFire:   false,
			IsSleeping: false,
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:cooked_beef", Count: 5, Slot: 2},
			},
		},
	}
	ctxHealthy := &EvaluatorContext{
		AgentID: "agent-test",
		Frame:   frameHealthy,
	}
	graphHealthy := NewFactGraph()
	if err := eval.Evaluate(ctxHealthy, graphHealthy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	h, _ := graphHealthy.GetFloat("vitals.health")
	if h != 18.0 {
		t.Errorf("expected vitals.health 18.0, got %f", h)
	}
	mh, _ := graphHealthy.GetFloat("vitals.max_health")
	if mh != 20.0 {
		t.Errorf("expected vitals.max_health 20.0, got %f", mh)
	}
	if graphHealthy.GetBool("vitals.is_health_critical") {
		t.Errorf("expected vitals.is_health_critical false for 18 HP")
	}
	fl, _ := graphHealthy.GetInt("vitals.food_level")
	if fl != 15 {
		t.Errorf("expected vitals.food_level 15, got %d", fl)
	}
	if graphHealthy.GetBool("vitals.is_critical_hunger") {
		t.Errorf("expected vitals.is_critical_hunger false for food level 15")
	}
	if !graphHealthy.GetBool("vitals.has_edible_food") {
		t.Errorf("expected vitals.has_edible_food true")
	}
	if graphHealthy.GetString("vitals.optimal_food_id") != "minecraft:cooked_beef" {
		t.Errorf("expected optimal food 'minecraft:cooked_beef', got %q", graphHealthy.GetString("vitals.optimal_food_id"))
	}
	slot, _ := graphHealthy.GetInt("vitals.optimal_food_slot")
	if slot != 2 {
		t.Errorf("expected optimal food slot 2, got %d", slot)
	}
	if graphHealthy.GetBool("vitals.is_on_fire") {
		t.Errorf("expected vitals.is_on_fire false")
	}
	if graphHealthy.GetBool("vitals.is_sleeping") {
		t.Errorf("expected vitals.is_sleeping false")
	}
	if !graphHealthy.GetBool("vitals.can_eat") {
		t.Errorf("expected vitals.can_eat true (15 < 20 and has beef)")
	}

	// 2. Critical health and hunger agent
	frameCritical := &civquestv1.TelemetryFrame{
		WorldTick: 101,
		Status: &civquestv1.AgentStatus{
			Health:     5,
			MaxHealth:  20,
			FoodLevel:  4,
			IsOnFire:   true,
			IsSleeping: true,
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:bread", Count: 3, Slot: 1},
			},
		},
	}
	ctxCritical := &EvaluatorContext{
		AgentID: "agent-test",
		Frame:   frameCritical,
	}
	graphCritical := NewFactGraph()
	if err := eval.Evaluate(ctxCritical, graphCritical); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !graphCritical.GetBool("vitals.is_health_critical") {
		t.Errorf("expected vitals.is_health_critical true for 5/20 HP")
	}
	if !graphCritical.GetBool("vitals.is_critical_hunger") {
		t.Errorf("expected vitals.is_critical_hunger true for food level 4 with bread")
	}
	if !graphCritical.GetBool("vitals.is_on_fire") {
		t.Errorf("expected vitals.is_on_fire true")
	}
	if !graphCritical.GetBool("vitals.is_sleeping") {
		t.Errorf("expected vitals.is_sleeping true")
	}

	// 3. Full hunger (foodLevel == 20) cannot eat
	frameFull := &civquestv1.TelemetryFrame{
		WorldTick: 102,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:apple", Count: 1, Slot: 0},
			},
		},
	}
	graphFull := NewFactGraph()
	_ = eval.Evaluate(&EvaluatorContext{Frame: frameFull}, graphFull)
	if graphFull.GetBool("vitals.can_eat") {
		t.Errorf("expected vitals.can_eat false when foodLevel == 20")
	}

	// 4. Starving without food
	frameStarvingNoFood := &civquestv1.TelemetryFrame{
		WorldTick: 103,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 2,
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:stick", Count: 5, Slot: 0},
			},
		},
	}
	graphNoFood := NewFactGraph()
	_ = eval.Evaluate(&EvaluatorContext{Frame: frameStarvingNoFood}, graphNoFood)
	if graphNoFood.GetBool("vitals.has_edible_food") {
		t.Errorf("expected vitals.has_edible_food false with only sticks")
	}
	if graphNoFood.GetBool("vitals.is_critical_hunger") {
		t.Errorf("expected vitals.is_critical_hunger false when no food present")
	}
	if graphNoFood.GetBool("vitals.can_eat") {
		t.Errorf("expected vitals.can_eat false when no food present")
	}

	// 5. Nil safety
	graphNil := NewFactGraph()
	if err := eval.Evaluate(nil, graphNil); err != nil {
		t.Fatalf("expected nil error on nil context: %v", err)
	}
	if err := eval.Evaluate(&EvaluatorContext{}, graphNil); err != nil {
		t.Fatalf("expected nil error on empty context: %v", err)
	}
}

func TestThreatEvaluator_PopulatesAllFacts(t *testing.T) {
	eval := NewThreatEvaluator()
	if eval.Name() != "threat" {
		t.Errorf("expected name 'threat', got %q", eval.Name())
	}
	if eval.Category() != "threat" {
		t.Errorf("expected category 'threat', got %q", eval.Category())
	}

	// 1. Safe environment
	frameSafe := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
		},
	}
	graphSafe := NewFactGraph()
	ctxSafe := &EvaluatorContext{
		AgentID: "agent-test",
		Frame:   frameSafe,
		Config:  &xdg.AgentIdentityConfig{Traits: xdg.AgentTraits{Bravery: 0.5}},
	}
	if err := eval.Evaluate(ctxSafe, graphSafe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if graphSafe.GetBool("threat.hostiles_present") {
		t.Errorf("expected threat.hostiles_present false")
	}
	cnt, _ := graphSafe.GetInt("threat.immediate_threat_count")
	if cnt != 0 {
		t.Errorf("expected immediate_threat_count 0, got %d", cnt)
	}
	if graphSafe.GetBool("threat.should_panic") {
		t.Errorf("expected threat.should_panic false")
	}
	if graphSafe.GetBool("threat.damage_taken") {
		t.Errorf("expected threat.damage_taken false")
	}
	if graphSafe.GetBool("threat.is_emergency") {
		t.Errorf("expected threat.is_emergency false in safe environment")
	}
	if !graphSafe.GetBool("threat.threats_cleared") {
		t.Errorf("expected threat.threats_cleared true in safe environment")
	}
	if graphSafe.GetBool("threat.has_shield") {
		t.Errorf("expected threat.has_shield false")
	}
	if graphSafe.GetBool("threat.has_weapon") {
		t.Errorf("expected threat.has_weapon false")
	}
	hid, _ := graphSafe.GetInt("threat.nearest_hostile_id")
	if hid != -1 {
		t.Errorf("expected nearest_hostile_id -1, got %d", hid)
	}

	// 2. Hostile mob with combat gear
	frameHostile := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:iron_sword", Count: 1, Slot: 0},
				{ItemId: "minecraft:shield", Count: 1, Slot: 40},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   42,
				EntityType: "minecraft:zombie",
				IsHostile:  true,
				Distance:   4.5,
				Position:   &civquestv1.Vec3{X: 3, Y: 64, Z: 3},
			},
		},
	}
	graphHostile := NewFactGraph()
	ctxHostile := &EvaluatorContext{
		AgentID: "agent-test",
		Frame:   frameHostile,
		Config:  &xdg.AgentIdentityConfig{Traits: xdg.AgentTraits{Bravery: 0.5}},
	}
	if err := eval.Evaluate(ctxHostile, graphHostile); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !graphHostile.GetBool("threat.hostiles_present") {
		t.Errorf("expected threat.hostiles_present true")
	}
	cnt, _ = graphHostile.GetInt("threat.immediate_threat_count")
	if cnt != 1 {
		t.Errorf("expected immediate_threat_count 1, got %d", cnt)
	}
	hid, _ = graphHostile.GetInt("threat.nearest_hostile_id")
	if hid != 42 {
		t.Errorf("expected nearest_hostile_id 42, got %d", hid)
	}
	hdist, _ := graphHostile.GetFloat("threat.nearest_hostile_dist")
	if hdist != 4.5 {
		t.Errorf("expected nearest_hostile_dist 4.5, got %f", hdist)
	}
	if !graphHostile.GetBool("threat.has_shield") {
		t.Errorf("expected threat.has_shield true")
	}
	if !graphHostile.GetBool("threat.has_weapon") {
		t.Errorf("expected threat.has_weapon true")
	}
	wSlot, _ := graphHostile.GetInt("threat.weapon_slot")
	if wSlot != 0 {
		t.Errorf("expected weapon_slot 0, got %d", wSlot)
	}
	if !graphHostile.GetBool("threat.is_emergency") {
		t.Errorf("expected threat.is_emergency true with zombie present")
	}
	if graphHostile.GetBool("threat.threats_cleared") {
		t.Errorf("expected threat.threats_cleared false with zombie present")
	}

	// 3. Creeper hissing triggers panic
	frameCreeper := &civquestv1.TelemetryFrame{
		WorldTick: 300,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   99,
				EntityType: "minecraft:creeper",
				IsHostile:  true,
				Distance:   2.5,
				FuseTicks:  15,
				Position:   &civquestv1.Vec3{X: 2, Y: 64, Z: 1},
			},
		},
	}
	graphCreeper := NewFactGraph()
	_ = eval.Evaluate(&EvaluatorContext{Frame: frameCreeper}, graphCreeper)
	if !graphCreeper.GetBool("threat.should_panic") {
		t.Errorf("expected threat.should_panic true for hissing creeper")
	}
	if !graphCreeper.GetBool("threat.is_emergency") {
		t.Errorf("expected threat.is_emergency true for hissing creeper")
	}

	// 4. Damage taken
	frameDamage := &civquestv1.TelemetryFrame{
		WorldTick: 400,
		Status: &civquestv1.AgentStatus{
			Health:    15,
			MaxHealth: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
		},
		LastDamageTaken: &civquestv1.DamageEvent{
			DamageAmount: 5,
			SourceType:   civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB,
		},
	}
	graphDamage := NewFactGraph()
	_ = eval.Evaluate(&EvaluatorContext{Frame: frameDamage}, graphDamage)
	if !graphDamage.GetBool("threat.damage_taken") {
		t.Errorf("expected threat.damage_taken true")
	}
	if !graphDamage.GetBool("threat.is_emergency") {
		t.Errorf("expected threat.is_emergency true after damage taken")
	}
	if graphDamage.GetBool("threat.threats_cleared") {
		t.Errorf("expected threat.threats_cleared false with recent damage")
	}
}

func BenchmarkVitalsAndThreatEvaluators(b *testing.B) {
	vitalsEval := NewVitalsEvaluator()
	threatEval := NewThreatEvaluator()

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Health:    16,
			MaxHealth: 20,
			FoodLevel: 12,
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
				EntityType: "minecraft:skeleton",
				IsHostile:  true,
				Distance:   8.0,
				Position:   &civquestv1.Vec3{X: 18, Y: 64, Z: 10},
			},
		},
	}

	ctx := &EvaluatorContext{
		AgentID: "bench-agent",
		Frame:   frame,
		Config:  &xdg.AgentIdentityConfig{Traits: xdg.AgentTraits{Bravery: 0.5}},
	}

	graph := NewFactGraph()

	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		_ = vitalsEval.Evaluate(ctx, graph)
		_ = threatEval.Evaluate(ctx, graph)
	}
	elapsed := time.Since(start)

	if b.N > 0 {
		perOp := elapsed / time.Duration(b.N)
		if perOp > 5*time.Millisecond {
			b.Fatalf("Evaluators exceeded 5ms limit: %v per op", perOp)
		}
	}
}

func TestThreatEvaluator_DrowningNotTreatedAsCombatThreat(t *testing.T) {
	eval := NewThreatEvaluator()

	// 1. Explicit DAMAGE_SOURCE_TYPE_DROWN while IsInWater=true
	frameDrown := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		Status: &civquestv1.AgentStatus{
			Health:    18,
			MaxHealth: 20,
			IsInWater: true,
			Position:  &civquestv1.Vec3{X: 10, Y: 58, Z: 10},
		},
		LastDamageTaken: &civquestv1.DamageEvent{
			DamageAmount: 2,
			SourceType:   civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_DROWN,
		},
	}
	graphDrown := NewFactGraph()
	if err := eval.Evaluate(&EvaluatorContext{AgentID: "Carl", Frame: frameDrown}, graphDrown); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := graphDrown.GetString("threat.threatType"); got != "DROWNING" {
		t.Errorf("expected threat.threatType = DROWNING, got %q", got)
	}
	if !graphDrown.GetBool("threat.is_drowning") {
		t.Errorf("expected threat.is_drowning = true")
	}
	if !graphDrown.GetBool("threat.is_emergency") {
		t.Errorf("expected threat.is_emergency = true for drowning")
	}
	if graphDrown.GetBool("threat.hostiles_present") {
		t.Errorf("expected threat.hostiles_present = false for drowning")
	}
	if graphDrown.GetBool("threat.damage_taken") {
		t.Errorf("expected threat.damage_taken = false (not hostile combat damage) for drowning")
	}
	if conf, ok := graphDrown.GetFloat("threat.confidence"); !ok || conf != 999.0 {
		t.Errorf("expected threat.confidence = 999.0 for environmental drowning, got %v (ok=%v)", conf, ok)
	}

	// 2. Unspecified environmental water damage while IsInWater=true (legacy/unspecified drown source)
	frameUnspecifiedWater := &civquestv1.TelemetryFrame{
		WorldTick: 501,
		Status: &civquestv1.AgentStatus{
			Health:    16,
			MaxHealth: 20,
			IsInWater: true,
			Position:  &civquestv1.Vec3{X: 10, Y: 58, Z: 10},
		},
		LastDamageTaken: &civquestv1.DamageEvent{
			DamageAmount: 2,
			SourceType:   civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_UNSPECIFIED,
		},
	}
	graphUnspec := NewFactGraph()
	if err := eval.Evaluate(&EvaluatorContext{AgentID: "Carl", Frame: frameUnspecifiedWater}, graphUnspec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := graphUnspec.GetString("threat.threatType"); got != "DROWNING" {
		t.Errorf("expected threat.threatType = DROWNING for unspecified water damage, got %q", got)
	}
	if !graphUnspec.GetBool("threat.is_emergency") {
		t.Errorf("expected threat.is_emergency = true for unspecified water damage")
	}
}
