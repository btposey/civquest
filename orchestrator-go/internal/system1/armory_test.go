package system1

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestCheckArmorySufficiency_Deficits(t *testing.T) {
	// 1. Empty inventory -> deficient (weapons deficit)
	isSuff, cat := CheckArmorySufficiency(nil, nil)
	if isSuff {
		t.Fatalf("expected empty inventory to be insufficient")
	}
	if cat != CategoryWeapons {
		t.Errorf("expected CategoryWeapons for empty inventory, got %s", cat)
	}

	// 2. Only arrows deficit
	invArrowsDeficit := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:iron_sword", Count: 1},
		{Slot: 1, ItemId: "minecraft:bow", Count: 1},
		{Slot: 2, ItemId: "minecraft:iron_helmet", Count: 1},
		{Slot: 3, ItemId: "minecraft:iron_chestplate", Count: 1},
		{Slot: 4, ItemId: "minecraft:iron_leggings", Count: 1},
		{Slot: 5, ItemId: "minecraft:iron_boots", Count: 1},
		{Slot: 6, ItemId: "minecraft:arrow", Count: 5}, // < 10 default quota
		{Slot: 7, ItemId: "minecraft:tnt", Count: 4},
	}
	isSuff, cat = CheckArmorySufficiency(invArrowsDeficit, nil)
	if isSuff {
		t.Fatalf("expected insufficient due to arrows")
	}
	if cat != CategoryArrows {
		t.Errorf("expected CategoryArrows, got %s", cat)
	}

	// 3. Only TNT deficit
	invTNTDeficit := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:iron_sword", Count: 1},
		{Slot: 1, ItemId: "minecraft:bow", Count: 1},
		{Slot: 2, ItemId: "minecraft:iron_helmet", Count: 1},
		{Slot: 3, ItemId: "minecraft:iron_chestplate", Count: 1},
		{Slot: 4, ItemId: "minecraft:iron_leggings", Count: 1},
		{Slot: 5, ItemId: "minecraft:iron_boots", Count: 1},
		{Slot: 6, ItemId: "minecraft:arrow", Count: 32},
		{Slot: 7, ItemId: "minecraft:tnt", Count: 2}, // < 4 default quota
	}
	isSuff, cat = CheckArmorySufficiency(invTNTDeficit, nil)
	if isSuff {
		t.Fatalf("expected insufficient due to TNT")
	}
	if cat != CategoryTNT {
		t.Errorf("expected CategoryTNT, got %s", cat)
	}

	// 4. Only weapons deficit
	invWeaponsDeficit := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:iron_helmet", Count: 1},
		{Slot: 1, ItemId: "minecraft:iron_chestplate", Count: 1},
		{Slot: 2, ItemId: "minecraft:iron_leggings", Count: 1},
		{Slot: 3, ItemId: "minecraft:iron_boots", Count: 1},
		{Slot: 4, ItemId: "minecraft:arrow", Count: 32},
		{Slot: 5, ItemId: "minecraft:tnt", Count: 4},
	}
	isSuff, cat = CheckArmorySufficiency(invWeaponsDeficit, nil)
	if isSuff {
		t.Fatalf("expected insufficient due to weapons")
	}
	if cat != CategoryWeapons {
		t.Errorf("expected CategoryWeapons, got %s", cat)
	}

	// 5. Only armor deficit
	invArmorDeficit := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:iron_sword", Count: 1},
		{Slot: 1, ItemId: "minecraft:bow", Count: 1},
		{Slot: 2, ItemId: "minecraft:iron_helmet", Count: 1},
		{Slot: 3, ItemId: "minecraft:arrow", Count: 32},
		{Slot: 4, ItemId: "minecraft:tnt", Count: 4},
	}
	isSuff, cat = CheckArmorySufficiency(invArmorDeficit, nil)
	if isSuff {
		t.Fatalf("expected insufficient due to armor")
	}
	if cat != CategoryArmor {
		t.Errorf("expected CategoryArmor, got %s", cat)
	}
}

func TestCheckArmorySufficiency_Sufficient(t *testing.T) {
	// Full militia outfit for N=1
	invSufficient := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:iron_sword", Count: 1},
		{Slot: 1, ItemId: "minecraft:bow", Count: 1},
		{Slot: 2, ItemId: "minecraft:iron_helmet", Count: 1},
		{Slot: 3, ItemId: "minecraft:iron_chestplate", Count: 1},
		{Slot: 4, ItemId: "minecraft:iron_leggings", Count: 1},
		{Slot: 5, ItemId: "minecraft:iron_boots", Count: 1},
		{Slot: 6, ItemId: "minecraft:arrow", Count: 32},
		{Slot: 7, ItemId: "minecraft:tnt", Count: 4},
	}

	isSuff, cat := CheckArmorySufficiency(invSufficient, nil)
	if !isSuff {
		t.Fatalf("expected armory to be sufficient, got deficit in %s", cat)
	}
	if cat != "" {
		t.Errorf("expected empty neededCategory for sufficient armory, got %s", cat)
	}
}

func TestCheckArmorySufficiency_PopulationScaling(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "civic_test.db")
	civicDB, err := db.OpenCivicRegistryDB(dbPath, "clan_solaria")
	if err != nil {
		t.Fatalf("failed to open civic registry db: %v", err)
	}
	defer civicDB.Close()

	// 1. Register 3 settlers in civic DB -> N = 3
	_ = civicDB.RegisterSettler("settler_1", "GUARD", 100)
	_ = civicDB.RegisterSettler("settler_2", "FARMER", 100)
	_ = civicDB.RegisterSettler("settler_3", "MINER", 100)

	pop := DetermineSettlementPopulation(civicDB)
	if pop != 3 {
		t.Fatalf("expected population 3, got %d", pop)
	}

	quotas := ComputeArmoryQuotas(pop)
	if quotas.Swords != 3 || quotas.Bows != 3 || quotas.Helmets != 3 || quotas.Chestplates != 3 ||
		quotas.Leggings != 3 || quotas.Boots != 3 || quotas.Arrows != 30 || quotas.TNT != 4 {
		t.Fatalf("unexpected quotas for N=3: %+v", quotas)
	}

	// Carried inventory with only 25 arrows (< 30 quota for N=3)
	inv := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:iron_sword", Count: 3},
		{Slot: 1, ItemId: "minecraft:bow", Count: 3},
		{Slot: 2, ItemId: "minecraft:iron_helmet", Count: 3},
		{Slot: 3, ItemId: "minecraft:iron_chestplate", Count: 3},
		{Slot: 4, ItemId: "minecraft:iron_leggings", Count: 3},
		{Slot: 5, ItemId: "minecraft:iron_boots", Count: 3},
		{Slot: 6, ItemId: "minecraft:arrow", Count: 25}, // Deficit (needs 30)
		{Slot: 7, ItemId: "minecraft:tnt", Count: 4},
	}

	isSuff, needed := CheckArmorySufficiency(inv, civicDB)
	if isSuff {
		t.Fatalf("expected insufficient armory for N=3 with 25 arrows")
	}
	if needed != CategoryArrows {
		t.Errorf("expected CategoryArrows, got %s", needed)
	}

	// Add 10 arrows to granary stock in civic DB -> total arrows = 35 >= 30!
	_ = civicDB.AddGranaryStock("minecraft:arrow", 10, 200)

	isSuff2, needed2 := CheckArmorySufficiency(inv, civicDB)
	if !isSuff2 {
		t.Fatalf("expected armory to be sufficient after adding granary arrows, got deficit: %s", needed2)
	}

	// 2. Test N=5 scaling for TNT reserve stockpile (max(4, 5) = 5)
	quotas5 := ComputeArmoryQuotas(5)
	if quotas5.TNT != 5 {
		t.Errorf("expected TNT quota 5 for population 5, got %d", quotas5.TNT)
	}
	if quotas5.Arrows != 50 {
		t.Errorf("expected Arrows quota 50 for population 5, got %d", quotas5.Arrows)
	}
}

func TestEvaluateArmoryMaintenance_CraftArrows(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("NightSentry")
	cfg.Profession = "GUARD"
	cfg.Shift = xdg.ShiftThird

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		TimeOfDay: 10500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:feather", Count: 4},
				{Slot: 1, ItemId: "minecraft:stick", Count: 4},
				{Slot: 2, ItemId: "minecraft:flint", Count: 4},
			},
		},
	}

	intent := EvaluateArmoryMaintenance("NightSentry", frame, cfg, state, nil, nil)
	if intent == nil {
		t.Fatalf("expected crafting intent for arrows, got nil")
	}

	craft := intent.GetCraftItem()
	if craft == nil {
		t.Fatalf("expected CraftItem action, got %+v", intent.Action)
	}
	if craft.RecipeId != "minecraft:arrow" {
		t.Errorf("expected recipe minecraft:arrow, got %s", craft.RecipeId)
	}
	if craft.BatchCount != 1 {
		t.Errorf("expected batch count 1, got %d", craft.BatchCount)
	}
	if state.GetLastCraftTick() != 500 {
		t.Errorf("expected lastCraftTick 500, got %d", state.GetLastCraftTick())
	}
}

func TestEvaluateArmoryMaintenance_CraftTNT(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("NightSentry")
	cfg.Profession = "GUARD"
	cfg.Shift = xdg.ShiftThird

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 600,
		TimeOfDay: 10500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				// Satisfied weapons, armor, arrows, but missing TNT
				{Slot: 0, ItemId: "minecraft:iron_sword", Count: 1},
				{Slot: 1, ItemId: "minecraft:bow", Count: 1},
				{Slot: 2, ItemId: "minecraft:iron_helmet", Count: 1},
				{Slot: 3, ItemId: "minecraft:iron_chestplate", Count: 1},
				{Slot: 4, ItemId: "minecraft:iron_leggings", Count: 1},
				{Slot: 5, ItemId: "minecraft:iron_boots", Count: 1},
				{Slot: 6, ItemId: "minecraft:arrow", Count: 32},
				// TNT materials:
				{Slot: 7, ItemId: "minecraft:gunpowder", Count: 5},
				{Slot: 8, ItemId: "minecraft:sand", Count: 4},
			},
		},
	}

	intent := EvaluateArmoryMaintenance("NightSentry", frame, cfg, state, nil, nil)
	if intent == nil {
		t.Fatalf("expected crafting intent for TNT, got nil")
	}

	craft := intent.GetCraftItem()
	if craft == nil {
		t.Fatalf("expected CraftItem action, got %+v", intent.Action)
	}
	if craft.RecipeId != "minecraft:tnt" {
		t.Errorf("expected recipe minecraft:tnt, got %s", craft.RecipeId)
	}
	if craft.BatchCount != 1 {
		t.Errorf("expected batch count 1, got %d", craft.BatchCount)
	}
	if state.GetLastCraftTick() != 600 {
		t.Errorf("expected lastCraftTick 600, got %d", state.GetLastCraftTick())
	}
}

func TestEvaluateArmoryMaintenance_CraftWeaponsAndArmor(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("NightSentry")
	cfg.Profession = "GUARD"
	cfg.Shift = xdg.ShiftThird

	// Frame 1: Agent missing weapons and armor, carries iron and planks
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 700,
		TimeOfDay: 10500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 10},
				{Slot: 1, ItemId: "minecraft:iron_ingot", Count: 16},
				{Slot: 2, ItemId: "minecraft:arrow", Count: 32},
				{Slot: 3, ItemId: "minecraft:tnt", Count: 4},
			},
		},
	}

	intent1 := EvaluateArmoryMaintenance("NightSentry", frame1, cfg, state, nil, nil)
	if intent1 == nil {
		t.Fatalf("expected weapon crafting intent (shield), got nil")
	}
	craft1 := intent1.GetCraftItem()
	if craft1 == nil || craft1.RecipeId != "minecraft:shield" {
		t.Fatalf("expected minecraft:shield recipe, got %+v", craft1)
	}

	// Frame 2: Weapons quota now satisfied (iron sword + bow present), but armor missing. Carries iron.
	state.SetLastCraftTick(700)
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 730, // 30 ticks later (> 20 cooldown)
		TimeOfDay: 10530,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:iron_sword", Count: 1},
				{Slot: 1, ItemId: "minecraft:bow", Count: 1},
				{Slot: 2, ItemId: "minecraft:iron_ingot", Count: 16},
				{Slot: 3, ItemId: "minecraft:arrow", Count: 32},
				{Slot: 4, ItemId: "minecraft:tnt", Count: 4},
			},
		},
	}

	intent2 := EvaluateArmoryMaintenance("NightSentry", frame2, cfg, state, nil, nil)
	if intent2 == nil {
		t.Fatalf("expected armor crafting intent (iron_chestplate), got nil")
	}
	craft2 := intent2.GetCraftItem()
	if craft2 == nil || craft2.RecipeId != "minecraft:iron_chestplate" {
		t.Fatalf("expected minecraft:iron_chestplate recipe, got %+v", craft2)
	}
}

func TestEvaluateArmoryMaintenance_CraftingTableInteraction(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("NightSentry")
	cfg.Profession = "GUARD"

	// 1. Nearby crafting table at (11, 64, 10) (dist 1.0 <= 4.5 reach)
	frameNearby := &civquestv1.TelemetryFrame{
		WorldTick: 800,
		TimeOfDay: 10500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:feather", Count: 1},
				{Slot: 1, ItemId: "minecraft:stick", Count: 1},
				{Slot: 2, ItemId: "minecraft:flint", Count: 1},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 10},
				BlockId: "minecraft:crafting_table",
			},
		},
	}

	intentNearby := EvaluateArmoryMaintenance("NightSentry", frameNearby, cfg, state, nil, nil)
	if intentNearby == nil || intentNearby.GetCraftItem() == nil {
		t.Fatalf("expected CraftItem intent at nearby table")
	}
	if intentNearby.GetCraftItem().WorkstationPos == nil || intentNearby.GetCraftItem().WorkstationPos.X != 11 {
		t.Errorf("expected WorkstationPos at X=11, got %+v", intentNearby.GetCraftItem().WorkstationPos)
	}

	// 2. Distant crafting table at (30, 64, 10) (dist 20 > 4.5) -> should navigate
	state.SetLastCraftTick(0)
	frameDistant := &civquestv1.TelemetryFrame{
		WorldTick: 850,
		TimeOfDay: 10500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:feather", Count: 1},
				{Slot: 1, ItemId: "minecraft:stick", Count: 1},
				{Slot: 2, ItemId: "minecraft:flint", Count: 1},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 30, Y: 64, Z: 10},
				BlockId: "minecraft:crafting_table",
			},
		},
	}

	intentDistant := EvaluateArmoryMaintenance("NightSentry", frameDistant, cfg, state, nil, nil)
	if intentDistant == nil || intentDistant.GetNavigate() == nil {
		t.Fatalf("expected Navigate intent to distant table, got %+v", intentDistant)
	}
	if intentDistant.GetNavigate().Target == nil || intentDistant.GetNavigate().Target.X != 30.5 {
		t.Errorf("expected target X=30.5, got %+v", intentDistant.GetNavigate().Target)
	}

	// 3. No table in frustum, but crafting table in inventory -> should place table
	state.SetLastCraftTick(0)
	framePlace := &civquestv1.TelemetryFrame{
		WorldTick: 900,
		TimeOfDay: 10500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:crafting_table", Count: 1},
				{Slot: 1, ItemId: "minecraft:feather", Count: 1},
				{Slot: 2, ItemId: "minecraft:stick", Count: 1},
				{Slot: 3, ItemId: "minecraft:flint", Count: 1},
			},
		},
	}

	intentPlace := EvaluateArmoryMaintenance("NightSentry", framePlace, cfg, state, nil, nil)
	if intentPlace == nil || intentPlace.GetInteractBlock() == nil {
		t.Fatalf("expected InteractBlock intent to place crafting table, got %+v", intentPlace)
	}
	if intentPlace.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
		t.Errorf("expected PLACE_BLOCK, got %v", intentPlace.GetInteractBlock().InteractionType)
	}
}

func TestEvaluateArmoryMaintenance_Sub5msExecution(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("NightSentry")
	cfg.Profession = "GUARD"

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 10500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:feather", Count: 10},
				{Slot: 1, ItemId: "minecraft:stick", Count: 10},
				{Slot: 2, ItemId: "minecraft:flint", Count: 10},
			},
		},
	}

	start := time.Now()
	for i := 0; i < 1000; i++ {
		state.SetLastCraftTick(0)
		_ = EvaluateArmoryMaintenance("NightSentry", frame, cfg, state, nil, nil)
	}
	elapsed := time.Since(start)
	avgPerCall := elapsed / 1000

	// Invariant: Sub-5ms execution budget per ADR-005/ADR-013
	if avgPerCall > 5*time.Millisecond {
		t.Fatalf("EvaluateArmoryMaintenance exceeded 5ms budget: average %v", avgPerCall)
	}
}

func TestEvaluateArmoryMaintenance_CraftingTableRecallFromSpatialIndex(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("NightSentry")
	cfg.Profession = "GUARD"

	// Distant crafting table at (40, 64, 10) stored only in SpatialIndex (empty InterestingBlocks)
	state.SpatialIndex().UpsertPOI(db.AgentPOI{
		POIID:          FormatPOIID(POICategoryWorkstation, 40, 64, 10),
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              40.5,
		Y:              64.0,
		Z:              10.5,
		AccessX:        39.5,
		AccessY:        64.0,
		AccessZ:        10.5,
		ElevationLayer: ElevationSurface,
		SafetyScore:    1.0,
		Reliability:    1.0,
		IsActive:       true,
	})

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 850,
		TimeOfDay: 10500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:crafting_table", Count: 1},
				{Slot: 1, ItemId: "minecraft:feather", Count: 1},
				{Slot: 2, ItemId: "minecraft:stick", Count: 1},
				{Slot: 3, ItemId: "minecraft:flint", Count: 1},
			},
		},
	}

	intent := EvaluateArmoryMaintenance("NightSentry", frame, cfg, state, nil, nil)
	if intent == nil || intent.GetNavigate() == nil || intent.GetNavigate().Target == nil {
		t.Fatalf("expected NavigateIntent towards recalled crafting table, got %+v", intent)
	}
	target := intent.GetNavigate().Target
	if target.X != 39.5 || target.Y != 64.0 || target.Z != 10.5 {
		t.Fatalf("expected navigation target (39.5, 64, 10.5), got %+v", target)
	}

	// Within 4.5m -> craft at recalled table
	frame.WorldTick = 900
	frame.Status.Position = &civquestv1.Vec3{X: 39.5, Y: 64.0, Z: 10.5}
	craftIntent := EvaluateArmoryMaintenance("NightSentry", frame, cfg, state, nil, nil)
	if craftIntent == nil || craftIntent.GetCraftItem() == nil {
		t.Fatalf("expected CraftItemIntent when within 4.5m of recalled table, got %+v", craftIntent)
	}
	if craftIntent.GetCraftItem().WorkstationPos == nil || craftIntent.GetCraftItem().WorkstationPos.X != 40 {
		t.Fatalf("expected WorkstationPos X=40, got %+v", craftIntent.GetCraftItem().WorkstationPos)
	}
}
