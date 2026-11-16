package system1

import (
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestHasSurvivalKit(t *testing.T) {
	cases := []struct {
		name      string
		inventory []*civquestv1.ItemStackProto
		want      bool
	}{
		{
			name:      "Empty inventory",
			inventory: nil,
			want:      false,
		},
		{
			name: "Only axe",
			inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
			want: false,
		},
		{
			name: "Axe and sword but no light",
			inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
			},
			want: false,
		},
		{
			name: "Full starter kit (axe, sword, campfire)",
			inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:campfire", Count: 1},
			},
			want: true,
		},
		{
			name: "Stone gear with torches",
			inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:stone_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:stone_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 16},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		got := HasSurvivalKit(tc.inventory)
		if got != tc.want {
			t.Errorf("%s: HasSurvivalKit() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEvaluateBootstrapTimberAndCrafting(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Frame 1: warm up telemetryFramesSeen
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	_ = EvaluateBootstrapGoals("Able", frame1, cfg, state)

	// Frame 2: Unarmed agent sees an oak_log within 3 blocks -> should harvest it!
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 140,
		TimeOfDay: 2040,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0},
				BlockId: "minecraft:oak_log",
			},
		},
	}
	harvestIntent := EvaluateBootstrapGoals("Able", frame2, cfg, state)
	if harvestIntent == nil || harvestIntent.GetInteractBlock() == nil ||
		harvestIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK intent for nearby oak_log, got %+v", harvestIntent)
	}

	// Frame 3: Agent now has 2 oak_logs in inventory, no crafting table placed -> should craft crafting_table!
	frame3 := &civquestv1.TelemetryFrame{
		WorldTick: 180,
		TimeOfDay: 2080,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 2},
			},
		},
	}
	craftTableIntent := EvaluateBootstrapGoals("Able", frame3, cfg, state)
	if craftTableIntent == nil || craftTableIntent.GetCraftItem() == nil || craftTableIntent.GetCraftItem().RecipeId != "minecraft:crafting_table" {
		t.Fatalf("expected CraftItemIntent for minecraft:crafting_table, got %+v", craftTableIntent)
	}

	// Frame 4: Agent now has 1 oak_log and 1 crafting_table in inventory -> should place crafting table!
	frame4 := &civquestv1.TelemetryFrame{
		WorldTick: 220,
		TimeOfDay: 2120,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 1},
				{Slot: 1, ItemId: "minecraft:crafting_table", Count: 1},
			},
		},
	}
	placeTableIntent := EvaluateBootstrapGoals("Able", frame4, cfg, state)
	if placeTableIntent == nil || placeTableIntent.GetInteractBlock() == nil ||
		placeTableIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK ||
		placeTableIntent.GetInteractBlock().BlockOrItemId != "minecraft:crafting_table" {
		t.Fatalf("expected PLACE_BLOCK intent for minecraft:crafting_table, got %+v", placeTableIntent)
	}

	// Frame 5: Crafting table is placed on the ground nearby -> should craft wooden_axe at crafting table!
	tablePos := placeTableIntent.GetInteractBlock().Pos
	frame5 := &civquestv1.TelemetryFrame{
		WorldTick: 260,
		TimeOfDay: 2160,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 1},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     tablePos,
				BlockId: "minecraft:crafting_table",
			},
		},
	}
	craftAxeIntent := EvaluateBootstrapGoals("Able", frame5, cfg, state)
	if craftAxeIntent == nil || craftAxeIntent.GetCraftItem() == nil || craftAxeIntent.GetCraftItem().RecipeId != "minecraft:wooden_axe" {
		t.Fatalf("expected CraftItemIntent for minecraft:wooden_axe near crafting table, got %+v", craftAxeIntent)
	}
	if craftAxeIntent.GetCraftItem().WorkstationPos == nil ||
		craftAxeIntent.GetCraftItem().WorkstationPos.X != tablePos.X ||
		craftAxeIntent.GetCraftItem().WorkstationPos.Z != tablePos.Z {
		t.Fatalf("expected workstation_pos to match placed crafting table pos, got %+v", craftAxeIntent.GetCraftItem().WorkstationPos)
	}
}

func TestCraftingTableDistantNavigation(t *testing.T) {
	state := NewAgentState()
	state.telemetryFramesSeen = 5
	state.lastExploreTick = 100
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Agent at (0, 64, 0), crafting table at (10, 64, 0) (distance 10 > 4.5 reach)
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 2},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0},
				BlockId: "minecraft:crafting_table",
			},
		},
	}
	navIntent := EvaluateBootstrapGoals("Able", frame, cfg, state)
	if navIntent == nil || navIntent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent towards distant crafting table, got %+v", navIntent)
	}
	target := navIntent.GetNavigate().Target
	if target == nil || target.X != 10.5 || target.Z != 0.5 {
		t.Fatalf("expected navigation target (10.5, 64, 0.5), got %+v", target)
	}
}

func TestCraftingTableStoneUpgrade(t *testing.T) {
	state := NewAgentState()
	state.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Agent at (0, 64, 0) with cobblestone & stick, crafting table within reach at (1, 64, 0)
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 600,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:cobblestone", Count: 3},
				{Slot: 1, ItemId: "minecraft:stick", Count: 2},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0},
				BlockId: "minecraft:crafting_table",
			},
		},
	}
	craftIntent := EvaluateBootstrapGoals("Able", frame, cfg, state)
	if craftIntent == nil || craftIntent.GetCraftItem() == nil || craftIntent.GetCraftItem().RecipeId != "minecraft:stone_sword" {
		t.Fatalf("expected CraftItemIntent for minecraft:stone_sword, got %+v", craftIntent)
	}
	if craftIntent.GetCraftItem().WorkstationPos == nil || craftIntent.GetCraftItem().WorkstationPos.X != 1 {
		t.Fatalf("expected workstation_pos X=1, got %+v", craftIntent.GetCraftItem().WorkstationPos)
	}
}

func TestEvaluateBootstrapGoalsDirect(t *testing.T) {
	state := NewAgentState()
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 1},
			},
		},
	}
	pos := &civquestv1.Vec3{X: 0, Y: 64, Z: 0}

	start := time.Now()
	intent := EvaluateBootstrapGoalsDirect("Able", frame, pos, civquestv1.NavigationState_NAVIGATION_STATE_IDLE, state)
	duration := time.Since(start)

	// Invariant: Sub-5ms execution budget
	if duration > 5*time.Millisecond {
		t.Errorf("EvaluateBootstrapGoalsDirect took %v, exceeded 5ms budget", duration)
	}
	_ = intent
}
