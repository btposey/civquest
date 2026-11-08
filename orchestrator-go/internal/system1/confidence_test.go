package system1

import (
	"math"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestMobThreat(t *testing.T) {
	tests := []struct {
		entityType string
		expected   float64
	}{
		{"minecraft:zombie", 15.0},
		{"zombie", 15.0},
		{"minecraft:husk", 15.0},
		{"minecraft:drowned", 15.0},
		{"minecraft:skeleton", 20.0},
		{"skeleton", 20.0},
		{"minecraft:stray", 20.0},
		{"minecraft:wither_skeleton", 20.0},
		{"minecraft:creeper", 35.0},
		{"creeper", 35.0},
		{"minecraft:pillager", 25.0},
		{"pillager", 25.0},
		{"minecraft:vindicator", 25.0},
		{"minecraft:evoker", 25.0},
		{"minecraft:warden", 500.0},
		{"warden", 500.0},
		{"minecraft:spider", 15.0},
		{"minecraft:blaze", 15.0},
		{"unknown_entity", 15.0},
	}

	for _, tt := range tests {
		got := MobThreat(tt.entityType)
		if got != tt.expected {
			t.Errorf("MobThreat(%q) = %v, expected %v", tt.entityType, got, tt.expected)
		}
	}
}

func TestGearMultiplier(t *testing.T) {
	// Naked agent
	if m := CalculateGearMultiplier(nil); m != 0.5 {
		t.Fatalf("expected naked multiplier 0.5, got %v", m)
	}

	// Iron sword only
	itemsIronSword := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:iron_sword", Count: 1, Slot: 0},
	}
	if m := CalculateGearMultiplier(itemsIronSword); math.Abs(m-1.15) > 1e-4 {
		t.Fatalf("expected iron sword multiplier 1.15, got %v", m)
	}

	// Full iron armor + iron sword + shield
	// Base 0.5 + 0.65 (weapon) + 0.5 (shield) + 4*0.15 (armor) = 2.25
	itemsFullIron := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:iron_helmet", Count: 1, Slot: 103},
		{ItemId: "minecraft:iron_chestplate", Count: 1, Slot: 102},
		{ItemId: "minecraft:iron_leggings", Count: 1, Slot: 101},
		{ItemId: "minecraft:iron_boots", Count: 1, Slot: 100},
		{ItemId: "minecraft:iron_sword", Count: 1, Slot: 0},
		{ItemId: "minecraft:shield", Count: 1, Slot: 40},
	}
	if m := CalculateGearMultiplier(itemsFullIron); math.Abs(m-2.25) > 1e-4 {
		t.Fatalf("expected full iron multiplier 2.25, got %v", m)
	}

	// Full netherite gear (uncapped 0.5 + 1.0 + 0.5 + 1.0 = 3.0, capped at 2.5)
	itemsNetherite := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:netherite_helmet", Count: 1, Slot: 103},
		{ItemId: "minecraft:netherite_chestplate", Count: 1, Slot: 102},
		{ItemId: "minecraft:netherite_leggings", Count: 1, Slot: 101},
		{ItemId: "minecraft:netherite_boots", Count: 1, Slot: 100},
		{ItemId: "minecraft:netherite_sword", Count: 1, Slot: 0},
		{ItemId: "minecraft:shield", Count: 1, Slot: 40},
	}
	if m := CalculateGearMultiplier(itemsNetherite); m != 2.5 {
		t.Fatalf("expected netherite multiplier clamped to 2.5, got %v", m)
	}
}

func TestCalculateConfidence(t *testing.T) {
	// Baseline: Self HP = 20, 0 allies, 1 Zombie (threat = 15)
	// M_gear = 1.15 (iron sword), Bravery = 0.5
	// Confidence = (20 / 15) * 1.15 * (0.5 + 0.5) = 1.3333 * 1.15 * 1.0 = 1.5333
	conf := CalculateConfidence(20, 0, 15, 1.15, 0.5)
	expected := (20.0 / 15.0) * 1.15 * 1.0
	if math.Abs(conf-expected) > 1e-4 {
		t.Fatalf("expected confidence %v, got %v", expected, conf)
	}

	// With an ally (Ally HP = 20)
	// Numerator = 40. Confidence = (40 / 15) * 1.15 * 1.0 = 3.0667
	confAlly := CalculateConfidence(20, 20, 15, 1.15, 0.5)
	expectedAlly := (40.0 / 15.0) * 1.15 * 1.0
	if math.Abs(confAlly-expectedAlly) > 1e-4 {
		t.Fatalf("expected confidence with ally %v, got %v", expectedAlly, confAlly)
	}

	// No hostiles -> 999.0
	if confNoThreat := CalculateConfidence(20, 0, 0, 1.0, 0.5); confNoThreat != 999.0 {
		t.Fatalf("expected 999.0 for zero threat, got %v", confNoThreat)
	}
}

func TestPanicOverride_LowHP(t *testing.T) {
	// HP_self <= 0.35 * HP_max (7.0 for 20 HP)
	if !ShouldPanic(7.0, 20.0, 5.0, false, false) {
		t.Fatal("expected panic at HP = 7.0 (<= 0.35*20)")
	}
	if !ShouldPanic(5.0, 20.0, 5.0, false, false) {
		t.Fatal("expected panic at HP = 5.0")
	}
	if ShouldPanic(8.0, 20.0, 2.0, false, false) {
		t.Fatal("expected no panic at HP = 8.0 with high confidence and no hostiles")
	}
}

func TestPanicOverride_CreeperFuse(t *testing.T) {
	// Creeper within 5 blocks has fuse_ticks > 0 -> panic override regardless of confidence
	if !ShouldPanic(20.0, 20.0, 5.0, true, true) {
		t.Fatal("expected panic override when hissing creeper is nearby")
	}
}

func TestPanicOverride_LowConfidence(t *testing.T) {
	// Confidence < 1.0 when hostiles present -> panic
	if !ShouldPanic(20.0, 20.0, 0.85, true, false) {
		t.Fatal("expected panic when hostiles present and confidence < 1.0")
	}
	// Confidence >= 1.0 -> no panic
	if ShouldPanic(20.0, 20.0, 1.25, true, false) {
		t.Fatal("expected no panic when confidence >= 1.0")
	}
}

func TestEvaluateReflexes_PanicActionSequence(t *testing.T) {
	// Agent with low confidence against 2 skeletons, low health, and food in inventory
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Health:    10.0,
			MaxHealth: 20.0,
			FoodLevel: 14,
			Position:  &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:cooked_beef", Count: 5, Slot: 3},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   10,
				EntityType: "minecraft:skeleton",
				Distance:   8.0,
				IsHostile:  true,
				Position:   &civquestv1.Vec3{X: 108, Y: 64, Z: 100},
				HeldItemId: "minecraft:bow",
			},
			{
				EntityId:   11,
				EntityType: "minecraft:skeleton",
				Distance:   10.0,
				IsHostile:  true,
				Position:   &civquestv1.Vec3{X: 100, Y: 64, Z: 110},
				HeldItemId: "minecraft:bow",
			},
		},
	}

	ctx := ReflexContext{
		AgentID: "settler_1",
		Bravery: 0.5,
		Frame:   frame,
		CanEat:  true,
	}

	res := EvaluateReflexes(ctx)
	if !res.Evaluated {
		t.Fatal("expected reflexes to be evaluated")
	}
	if !res.IsPanic {
		t.Fatalf("expected panic state, confidence was %v", res.Confidence)
	}
	if !res.ShouldAct {
		t.Fatal("expected ShouldAct = true")
	}

	// Must generate AttackIntent (shield+flee), NavigateIntent, and UseItemIntent (eat food)
	var hasAttack, hasNavigate, hasUseItem bool
	for _, intent := range res.Intents {
		if att := intent.GetAttack(); att != nil {
			hasAttack = true
			if !att.RaiseShield || !att.FleeImmediately {
				t.Errorf("expected AttackIntent to have raise_shield=true and flee_immediately=true, got %+v", att)
			}
		}
		if nav := intent.GetNavigate(); nav != nil {
			hasNavigate = true
			if !nav.Sprint {
				t.Errorf("expected NavigateIntent to sprint while fleeing")
			}
			// Flee target must be oriented away from hostiles (hostiles are at X+8 and Z+10, so flee target must be X < 100 and Z < 100)
			if nav.Target.X >= 100 || nav.Target.Z >= 100 {
				t.Errorf("expected flee target away from hostile centroid, got X=%v, Z=%v", nav.Target.X, nav.Target.Z)
			}
		}
		if use := intent.GetUseItem(); use != nil {
			hasUseItem = true
			if use.InventorySlot != 3 || !use.IsEatingOrDrinking {
				t.Errorf("expected UseItemIntent on slot 3 with is_eating=true, got %+v", use)
			}
		}
	}

	if !hasAttack {
		t.Error("missing AttackIntent in panic response")
	}
	if !hasNavigate {
		t.Error("missing NavigateIntent in panic response")
	}
	if !hasUseItem {
		t.Error("missing UseItemIntent to eat food while fleeing")
	}
}

func TestEvaluateReflexes_ConfidentStand(t *testing.T) {
	// Fully geared agent facing 1 zombie: Confidence >= 1.0 -> Confident Stand
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:diamond_helmet", Count: 1, Slot: 103},
				{ItemId: "minecraft:diamond_chestplate", Count: 1, Slot: 102},
				{ItemId: "minecraft:diamond_leggings", Count: 1, Slot: 101},
				{ItemId: "minecraft:diamond_boots", Count: 1, Slot: 100},
				{ItemId: "minecraft:diamond_sword", Count: 1, Slot: 0},
				{ItemId: "minecraft:shield", Count: 1, Slot: 40},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   42,
				EntityType: "minecraft:zombie",
				Distance:   5.0,
				IsHostile:  true,
				Position:   &civquestv1.Vec3{X: 55, Y: 64, Z: 50},
			},
		},
	}

	ctx := ReflexContext{
		AgentID: "settler_warrior",
		Bravery: 0.8,
		Frame:   frame,
		CanEat:  false,
	}

	res := EvaluateReflexes(ctx)
	if !res.Evaluated {
		t.Fatal("expected reflexes to be evaluated")
	}
	if res.IsPanic {
		t.Fatalf("expected confident stand, but got panic! confidence = %v", res.Confidence)
	}
	if !res.ShouldAct {
		t.Fatal("expected ShouldAct = true")
	}

	if len(res.Intents) != 1 {
		t.Fatalf("expected 1 combat intent, got %d", len(res.Intents))
	}
	att := res.Intents[0].GetAttack()
	if att == nil {
		t.Fatalf("expected AttackIntent, got %+v", res.Intents[0])
	}
	if att.TargetEntityId != 42 {
		t.Errorf("expected target entity ID 42, got %d", att.TargetEntityId)
	}
	if !att.KeepDistanceKite {
		t.Errorf("expected keep_distance_kite = true")
	}
	if att.FleeImmediately {
		t.Errorf("expected flee_immediately = false for confident stand")
	}
	if att.RaiseShield {
		t.Errorf("expected raise_shield = false against melee zombie")
	}
}

func TestEvaluateReflexes_RangedShieldRaising(t *testing.T) {
	// Confident stand against a skeleton -> must raise shield facing ranged attacker
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 250,
		Status: &civquestv1.AgentStatus{
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:netherite_chestplate", Count: 1, Slot: 102},
				{ItemId: "minecraft:diamond_sword", Count: 1, Slot: 0},
				{ItemId: "minecraft:shield", Count: 1, Slot: 40},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   99,
				EntityType: "minecraft:skeleton",
				Distance:   12.0,
				IsHostile:  true,
				HeldItemId: "minecraft:bow",
				Position:   &civquestv1.Vec3{X: 20, Y: 64, Z: 10},
			},
		},
	}

	ctx := ReflexContext{
		AgentID: "settler_knight",
		Bravery: 1.0,
		Frame:   frame,
		CanEat:  false,
	}

	res := EvaluateReflexes(ctx)
	if res.IsPanic {
		t.Fatalf("expected confident stand, got panic with confidence %v", res.Confidence)
	}
	if len(res.Intents) != 1 {
		t.Fatalf("expected 1 intent, got %d", len(res.Intents))
	}
	att := res.Intents[0].GetAttack()
	if att == nil || !att.RaiseShield {
		t.Errorf("expected raise_shield = true when facing ranged skeleton attacker, got %+v", att)
	}
}

func TestComputeFleeTarget_WithSafeNodes(t *testing.T) {
	selfPos := &civquestv1.Vec3{X: 10, Y: 64, Z: 10}
	hostiles := []*civquestv1.VisibleEntity{
		{
			EntityId: 1,
			Position: &civquestv1.Vec3{X: 20, Y: 64, Z: 10}, // East
		},
	}

	safeNodes := []db.MapNode{
		{ID: "danger-east", X: 30, Y: 64, Z: 10, SafetyScore: 0.9}, // East (towards hostiles, bad!)
		{ID: "safe-west", X: -10, Y: 64, Z: 10, SafetyScore: 1.0},  // West (away from hostiles, good!)
	}

	target := ComputeFleeTarget(selfPos, hostiles, 16.0, safeNodes)
	if target.X != -10 || target.Z != 10 {
		t.Errorf("expected flee target to pick safe-west (-10, 64, 10), got (%v, %v, %v)", target.X, target.Y, target.Z)
	}
}

func BenchmarkEvaluateReflexes(b *testing.B) {
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		Status: &civquestv1.AgentStatus{
			Health:    15.0,
			MaxHealth: 20.0,
			FoodLevel: 10,
			Position:  &civquestv1.Vec3{X: 128.5, Y: 64.0, Z: 256.5},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:iron_sword", Count: 1, Slot: 0},
				{ItemId: "minecraft:iron_chestplate", Count: 1, Slot: 102},
				{ItemId: "minecraft:cooked_beef", Count: 16, Slot: 8},
				{ItemId: "minecraft:shield", Count: 1, Slot: 40},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{EntityId: 1, EntityType: "minecraft:zombie", Distance: 8.0, IsHostile: true, Position: &civquestv1.Vec3{X: 132, Y: 64, Z: 256}},
			{EntityId: 2, EntityType: "minecraft:skeleton", Distance: 12.0, IsHostile: true, Position: &civquestv1.Vec3{X: 120, Y: 64, Z: 260}, HeldItemId: "minecraft:bow"},
			{EntityId: 3, EntityType: "minecraft:player", Distance: 6.0, IsHostile: false, Health: 20.0, Position: &civquestv1.Vec3{X: 126, Y: 64, Z: 255}},
		},
	}

	safeNodes := []db.MapNode{
		{ID: "tavern", X: 100, Y: 64, Z: 200, SafetyScore: 0.95},
		{ID: "gate", X: 110, Y: 64, Z: 220, SafetyScore: 0.85},
	}

	ctx := ReflexContext{
		AgentID:   "settler_bench",
		Bravery:   0.6,
		SafeNodes: safeNodes,
		Frame:     frame,
		CanEat:    true,
	}

	b.ResetTimer()
	start := time.Now()

	for i := 0; i < b.N; i++ {
		res := EvaluateReflexes(ctx)
		if !res.Evaluated {
			b.Fatal("not evaluated")
		}
	}

	elapsed := time.Since(start)
	avgPerOp := elapsed / time.Duration(b.N)

	// Hard architectural limit: must complete in <2ms
	if avgPerOp > 2*time.Millisecond {
		b.Fatalf("benchmark failed: average latency %v exceeds 2ms budget", avgPerOp)
	}
}
