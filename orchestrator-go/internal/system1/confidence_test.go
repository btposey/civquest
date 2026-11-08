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

func TestCalculateConfidence_NaNAndInf(t *testing.T) {
	// Any NaN or Inf input must result in 0.0
	nan := math.NaN()
	inf := math.Inf(1)
	negInf := math.Inf(-1)

	cases := []struct {
		name       string
		selfHP     float64
		alliesHP   float64
		threat     float64
		gear       float64
		bravery    float64
		expectZero bool
	}{
		{"NaN selfHP", nan, 0, 15, 1.0, 0.5, true},
		{"Inf selfHP", inf, 0, 15, 1.0, 0.5, true},
		{"-Inf selfHP", negInf, 0, 15, 1.0, 0.5, true},
		{"NaN alliesHP", 20, nan, 15, 1.0, 0.5, true},
		{"Inf alliesHP", 20, inf, 15, 1.0, 0.5, true},
		{"NaN threat", 20, 0, nan, 1.0, 0.5, true},
		{"Inf threat", 20, 0, inf, 1.0, 0.5, true},
		{"NaN gear", 20, 0, 15, nan, 0.5, true},
		{"Inf gear", 20, 0, 15, inf, 0.5, true},
		{"NaN bravery", 20, 0, 15, 1.0, nan, true},
		{"Inf bravery", 20, 0, 15, 1.0, inf, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalculateConfidence(tc.selfHP, tc.alliesHP, tc.threat, tc.gear, tc.bravery)
			if tc.expectZero && got != 0.0 {
				t.Errorf("expected 0.0 for %s, got %v", tc.name, got)
			}
		})
	}
}

func TestShouldPanic_NaNConfidence(t *testing.T) {
	// NaN or Inf confidence must immediately trigger panic
	if !ShouldPanic(20.0, 20.0, math.NaN(), true, false) {
		t.Error("expected panic when confidence is NaN with hostiles present")
	}
	if !ShouldPanic(20.0, 20.0, math.NaN(), false, false) {
		t.Error("expected panic when confidence is NaN even without hostiles")
	}
	if !ShouldPanic(20.0, 20.0, math.Inf(1), true, false) {
		t.Error("expected panic when confidence is +Inf")
	}
}

func TestGearMultiplier_SlotFiltering(t *testing.T) {
	// 1. Items in backpack (slots 9..35) must be completely ignored
	backpackItems := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:netherite_helmet", Count: 1, Slot: 9},
		{ItemId: "minecraft:netherite_chestplate", Count: 1, Slot: 10},
		{ItemId: "minecraft:netherite_leggings", Count: 1, Slot: 11},
		{ItemId: "minecraft:netherite_boots", Count: 1, Slot: 12},
		{ItemId: "minecraft:netherite_sword", Count: 1, Slot: 13},
		{ItemId: "minecraft:shield", Count: 1, Slot: 14},
	}
	mBackpack := CalculateGearMultiplier(backpackItems)
	if mBackpack != 0.5 {
		t.Fatalf("expected backpack items to be ignored (multiplier 0.5), got %v", mBackpack)
	}

	// 2. Modded item like 'shield_plate' must NOT count as a shield
	shieldPlateItem := []*civquestv1.ItemStackProto{
		{ItemId: "mod:shield_plate", Count: 1, Slot: 40},
	}
	mPlate := CalculateGearMultiplier(shieldPlateItem)
	if mPlate != 0.5 {
		t.Fatalf("expected shield_plate to be ignored (multiplier 0.5), got %v", mPlate)
	}

	// 3. Exact shield in offhand (slot 40) counts
	shieldOffhand := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:shield", Count: 1, Slot: 40},
	}
	if m := CalculateGearMultiplier(shieldOffhand); math.Abs(m-1.0) > 1e-4 {
		t.Fatalf("expected shield in offhand to give 1.0 (0.5+0.5), got %v", m)
	}

	// 4. Exact shield in hotbar (slot 1) counts
	shieldHotbar := []*civquestv1.ItemStackProto{
		{ItemId: "shield", Count: 1, Slot: 1},
	}
	if m := CalculateGearMultiplier(shieldHotbar); math.Abs(m-1.0) > 1e-4 {
		t.Fatalf("expected shield in hotbar to give 1.0, got %v", m)
	}

	// 5. Weapon in offhand (slot 40) does NOT count as main weapon
	weaponOffhand := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:diamond_sword", Count: 1, Slot: 40},
	}
	if m := CalculateGearMultiplier(weaponOffhand); m != 0.5 {
		t.Fatalf("expected weapon in offhand to be ignored, got %v", m)
	}

	// 6. Armor in vanilla slots (36..39) counts
	armorSlots36to39 := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:iron_boots", Count: 1, Slot: 36},
		{ItemId: "minecraft:iron_leggings", Count: 1, Slot: 37},
		{ItemId: "minecraft:iron_chestplate", Count: 1, Slot: 38},
		{ItemId: "minecraft:iron_helmet", Count: 1, Slot: 39},
	}
	// 0.5 + 4 * 0.15 = 1.10
	if m := CalculateGearMultiplier(armorSlots36to39); math.Abs(m-1.10) > 1e-4 {
		t.Fatalf("expected armor in slots 36..39 to give 1.10, got %v", m)
	}
}

func TestFoodScore_HazardousFoodRejected(t *testing.T) {
	hazardous := []string{
		"minecraft:suspicious_stew",
		"suspicious_stew",
		"minecraft:poisonous_potato",
		"poisonous_potato",
		"minecraft:pufferfish",
		"pufferfish",
		"minecraft:rotten_flesh",
		"rotten_flesh",
		"minecraft:spider_eye",
		"spider_eye",
	}

	for _, item := range hazardous {
		if score := FoodScore(item); score != 0 {
			t.Errorf("expected FoodScore(%q) = 0, got %d", item, score)
		}
	}

	// FindBestFoodItem with only hazardous food returns not found
	inventory := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:suspicious_stew", Count: 1, Slot: 0},
		{ItemId: "minecraft:poisonous_potato", Count: 10, Slot: 1},
		{ItemId: "minecraft:pufferfish", Count: 2, Slot: 2},
	}
	_, found := FindBestFoodItem(inventory)
	if found {
		t.Fatal("expected FindBestFoodItem to return found=false for inventory with only toxic food")
	}
}

func TestSelectPriorityHostile_CreeperProximity(t *testing.T) {
	// Creeper > 5.0 blocks away hissing must NOT override an adjacent skeleton
	distantCreeper := &civquestv1.VisibleEntity{
		EntityId:   1,
		EntityType: "minecraft:creeper",
		Distance:   8.0,
		FuseTicks:  15,
	}
	closeSkeleton := &civquestv1.VisibleEntity{
		EntityId:   2,
		EntityType: "minecraft:skeleton",
		Distance:   3.0,
		FuseTicks:  0,
	}

	hostilesDistant := []*civquestv1.VisibleEntity{distantCreeper, closeSkeleton}
	chosen := SelectPriorityHostile(hostilesDistant)
	if chosen == nil || chosen.EntityId != 2 {
		t.Fatalf("expected close skeleton (id=2) to be chosen over distant creeper (>5m), got %+v", chosen)
	}

	// Creeper <= 5.0 blocks away hissing MUST be prioritized over closer mob
	closeCreeper := &civquestv1.VisibleEntity{
		EntityId:   3,
		EntityType: "minecraft:creeper",
		Distance:   4.5,
		FuseTicks:  10,
	}
	adjacentZombie := &civquestv1.VisibleEntity{
		EntityId:   4,
		EntityType: "minecraft:zombie",
		Distance:   2.0,
		FuseTicks:  0,
	}

	hostilesClose := []*civquestv1.VisibleEntity{adjacentZombie, closeCreeper}
	chosenClose := SelectPriorityHostile(hostilesClose)
	if chosenClose == nil || chosenClose.EntityId != 3 {
		t.Fatalf("expected hissing creeper within 5m (id=3) to be prioritized over adjacent zombie, got %+v", chosenClose)
	}
}

func TestComputeFleeTarget_UnseenAttackerAndFinite(t *testing.T) {
	selfPos := &civquestv1.Vec3{X: 100, Y: 64, Z: 100}

	// 1. Unseen attacker: len(hostiles) == 0, safe node present -> flees toward best safe node
	safeNodes := []db.MapNode{
		{ID: "safe_haven", X: 120, Y: 64, Z: 120, SafetyScore: 0.95},
	}
	targetSafe := ComputeFleeTarget(selfPos, nil, 16.0, safeNodes)
	if targetSafe.X != 120 || targetSafe.Z != 120 {
		t.Fatalf("expected flee target toward safe node (120, 64, 120), got (%v, %v, %v)", targetSafe.X, targetSafe.Y, targetSafe.Z)
	}

	// 2. Unseen attacker: no hostiles and NO safe nodes -> returns agent's current position
	targetNoSafe := ComputeFleeTarget(selfPos, nil, 16.0, nil)
	if targetNoSafe.X != selfPos.X || targetNoSafe.Y != selfPos.Y || targetNoSafe.Z != selfPos.Z {
		t.Fatalf("expected current pos (%v, %v, %v), got (%v, %v, %v)", selfPos.X, selfPos.Y, selfPos.Z, targetNoSafe.X, targetNoSafe.Y, targetNoSafe.Z)
	}

	// 3. Hostiles with NaN or Inf coordinates are ignored
	corruptedHostiles := []*civquestv1.VisibleEntity{
		{
			EntityId: 1,
			Position: &civquestv1.Vec3{X: math.NaN(), Y: 64, Z: math.Inf(1)},
		},
		{
			EntityId: 2,
			Position: &civquestv1.Vec3{X: 110, Y: 64, Z: 100}, // Valid hostile East
		},
	}
	targetFinite := ComputeFleeTarget(selfPos, corruptedHostiles, 16.0, nil)
	if math.IsNaN(targetFinite.X) || math.IsInf(targetFinite.X, 0) ||
		math.IsNaN(targetFinite.Z) || math.IsInf(targetFinite.Z, 0) {
		t.Fatalf("expected strictly finite coordinates, got (%v, %v, %v)", targetFinite.X, targetFinite.Y, targetFinite.Z)
	}
	if targetFinite.X >= 100 {
		t.Fatalf("expected flee target away from valid hostile (X < 100), got X=%v", targetFinite.X)
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
