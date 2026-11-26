package system1

import (
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func newTestRecipeGraph() *crafting.RecipeGraph {
	planks := []string{"minecraft:birch_planks", "minecraft:oak_planks", "minecraft:spruce_planks"}
	logs := []string{"minecraft:birch_log", "minecraft:oak_log", "minecraft:spruce_log"}
	coals := []string{"minecraft:charcoal", "minecraft:coal"}
	cobble := []string{"minecraft:cobbled_deepslate", "minecraft:cobblestone"}

	return crafting.NewRecipeGraph([]*civquestv1.RecipeNode{
		{
			RecipeId:     "minecraft:oak_planks",
			RecipeType:   "minecraft:crafting_shapeless",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:oak_log"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 4},
		},
		{
			RecipeId:     "minecraft:birch_planks",
			RecipeType:   "minecraft:crafting_shapeless",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:birch_log"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:birch_planks", Count: 4},
		},
		{
			RecipeId:     "minecraft:spruce_planks",
			RecipeType:   "minecraft:crafting_shapeless",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:spruce_log"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:spruce_planks", Count: 4},
		},
		{
			RecipeId:     "minecraft:stick",
			RecipeType:   "minecraft:crafting_shaped",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planks, Count: 2},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:stick", Count: 4},
		},
		{
			RecipeId:     "minecraft:crafting_table",
			RecipeType:   "minecraft:crafting_shaped",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planks, Count: 4},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:crafting_table", Count: 1},
		},
		{
			RecipeId:         "minecraft:wooden_axe",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planks, Count: 3},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 2},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_axe", Count: 1},
		},
		{
			RecipeId:         "minecraft:wooden_sword",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planks, Count: 2},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_sword", Count: 1},
		},
		{
			RecipeId:         "minecraft:stone_sword",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: cobble, Count: 2},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:stone_sword", Count: 1},
		},
		{
			RecipeId:     "minecraft:torch",
			RecipeType:   "minecraft:crafting_shaped",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: coals, Count: 1},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:torch", Count: 4},
		},
		{
			RecipeId:         "minecraft:campfire",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 3},
				{AcceptedItemIds: coals, Count: 1},
				{AcceptedItemIds: logs, Count: 3},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:campfire", Count: 1},
		},
	})
}

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
	state.SetRecipeGraph(newTestRecipeGraph())
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
	_ = EvaluateBootstrapGoals("Able", frame1, cfg, state, nil)

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
	harvestIntent := EvaluateBootstrapGoals("Able", frame2, cfg, state, nil)
	if harvestIntent == nil || harvestIntent.GetInteractBlock() == nil ||
		harvestIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK intent for nearby oak_log, got %+v", harvestIntent)
	}

	// Frame 3: Agent now has 2 oak_logs in inventory, no crafting table placed ->
	// RecipeGraph plans multi-step chain: oak_planks -> stick -> oak_planks -> crafting_table!
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
	craftPlanksIntent := EvaluateBootstrapGoals("Able", frame3, cfg, state, nil)
	if craftPlanksIntent == nil || craftPlanksIntent.GetCraftItem() == nil || craftPlanksIntent.GetCraftItem().RecipeId != "minecraft:oak_planks" {
		t.Fatalf("expected CraftItemIntent for minecraft:oak_planks first, got %+v", craftPlanksIntent)
	}

	// Frame 3b: Agent now has oak_planks: 6, stick: 4 -> pocket-crafts crafting_table!
	frame3b := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		TimeOfDay: 2100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 6},
				{Slot: 1, ItemId: "minecraft:stick", Count: 4},
			},
		},
	}
	craftTableIntent := EvaluateBootstrapGoals("Able", frame3b, cfg, state, nil)
	if craftTableIntent == nil || craftTableIntent.GetCraftItem() == nil || craftTableIntent.GetCraftItem().RecipeId != "minecraft:crafting_table" {
		t.Fatalf("expected CraftItemIntent for minecraft:crafting_table, got %+v", craftTableIntent)
	}

	// Frame 4: Agent now has planks + sticks + 1 crafting_table in inventory -> should place crafting table!
	frame4 := &civquestv1.TelemetryFrame{
		WorldTick: 220,
		TimeOfDay: 2120,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 3},
				{Slot: 1, ItemId: "minecraft:stick", Count: 2},
				{Slot: 2, ItemId: "minecraft:crafting_table", Count: 1},
			},
		},
	}
	placeTableIntent := EvaluateBootstrapGoals("Able", frame4, cfg, state, nil)
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
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 3},
				{Slot: 1, ItemId: "minecraft:stick", Count: 2},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     tablePos,
				BlockId: "minecraft:crafting_table",
			},
		},
	}
	craftAxeIntent := EvaluateBootstrapGoals("Able", frame5, cfg, state, nil)
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
	state.SetRecipeGraph(newTestRecipeGraph())
	state.mem.telemetryFramesSeen = 5
	walk := &walkLocal{lastExploreTick: 100}
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Agent at (0, 64, 0) with planks + sticks ready for 3x3 wooden_axe, crafting table at (10, 64, 0) (distance 10 > 4.5 reach)
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 3},
				{Slot: 1, ItemId: "minecraft:stick", Count: 2},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0},
				BlockId: "minecraft:crafting_table",
			},
		},
	}
	navIntent := EvaluateBootstrapGoals("Able", frame, cfg, state, walk)
	if navIntent == nil || navIntent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent towards distant crafting table when ready for 3x3 craft, got %+v", navIntent)
	}
	target := navIntent.GetNavigate().Target
	if target == nil || target.X != 10.5 || target.Z != 0.5 {
		t.Fatalf("expected navigation target (10.5, 64, 0.5), got %+v", target)
	}
}

func TestCraftingTableUndergroundIgnoredInFavorOfPocketCrafting(t *testing.T) {
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	state.mem.telemetryFramesSeen = 5
	walk := &walkLocal{lastExploreTick: 100}
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Agent on surface at (0, 64, 0) needing tools, carrying 6 oak planks and 4 sticks.
	// Crafting table exists in a dugout cave directly below at (0, 50, 0) (dy = -14, hDist = 0).
	// Because dy > 3 and hDist < |dy|*1.5, the table is unreachable.
	// The agent must NOT navigate towards the underground table; it should craft a new table in pocket crafting!
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 6},
				{Slot: 1, ItemId: "minecraft:stick", Count: 4},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 0, Y: 50, Z: 0},
				BlockId: "minecraft:crafting_table",
			},
		},
	}

	intent := EvaluateBootstrapGoals("Able", frame, cfg, state, walk)
	if intent == nil || intent.GetCraftItem() == nil {
		t.Fatalf("expected CraftItemIntent for pocket crafting crafting_table, got %+v", intent)
	}
	if intent.GetCraftItem().RecipeId != "minecraft:crafting_table" {
		t.Fatalf("expected recipeId minecraft:crafting_table, got %s", intent.GetCraftItem().RecipeId)
	}
}

func TestCraftingTableStoneUpgrade(t *testing.T) {
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	state.mem.telemetryFramesSeen = 5
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
	craftIntent := EvaluateBootstrapGoals("Able", frame, cfg, state, nil)
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
	intent := EvaluateBootstrapGoalsDirect("Able", frame, pos, civquestv1.NavigationState_NAVIGATION_STATE_IDLE, state, nil)
	duration := time.Since(start)

	// Invariant: Sub-5ms execution budget
	if duration > 5*time.Millisecond {
		t.Errorf("EvaluateBootstrapGoalsDirect took %v, exceeded 5ms budget", duration)
	}
	_ = intent
}

func TestTaskSubroutine_GenericPhasesAndLifecycle(t *testing.T) {
	state := NewAgentState()
	prepareCalled := false
	actCalled := false
	completed := false
	failed := false

	// 1. Generic non-tree TaskFSM progressing APPROACH -> PREPARE -> ACT -> EVALUATE -> COMPLETE
	targetPos := &civquestv1.BlockPosProto{X: 8, Y: 64, Z: 0}
	var task *TaskFSM
	task = NewTaskFSM(TaskDescriptor{
		ID:              "generic_block_op:8,64,0",
		Kind:            "generic_op",
		Stage:           StageSettlement,
		Routine:         RoutinePrimaryDuty,
		ReachRadius:     3.0,
		ActionCooldown:  10,
		ProgressTimeout: 100,
		Preemptible:     true,
		Target: TaskTarget{
			Kind:     TaskTargetBlock,
			BlockPos: targetPos,
			Position: &civquestv1.Vec3{X: 8.5, Y: 64, Z: 0.5},
		},
		IsComplete: func(s *AgentState, f *civquestv1.TelemetryFrame) bool {
			return actCalled && f.WorldTick >= 150
		},
		SelectSubTarget: func(s *AgentState, f *civquestv1.TelemetryFrame, cur *TaskSubroutine) (*civquestv1.Vec3, *civquestv1.BlockPosProto, string, bool) {
			return &civquestv1.Vec3{X: 8.5, Y: 64, Z: 0.5}, targetPos, "minecraft:stone", true
		},
		PrepareIntent: func(agentID string, s *AgentState, f *civquestv1.TelemetryFrame, cur *TaskSubroutine) *civquestv1.AgentIntent {
			if prepareCalled {
				return nil
			}
			prepareCalled = true
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             targetPos,
						InteractionType: civquestv1.InteractBlockIntent_USE_WORKSTATION,
						BlockOrItemId:   "minecraft:iron_pickaxe",
					},
				},
			}
		},
		BuildActIntent: func(agentID string, s *AgentState, f *civquestv1.TelemetryFrame, cur *TaskSubroutine) *civquestv1.AgentIntent {
			actCalled = true
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             targetPos,
						InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
						BlockOrItemId:   "minecraft:stone",
					},
				},
			}
		},
		OnComplete: func(s *AgentState, f *civquestv1.TelemetryFrame, cur *TaskSubroutine) {
			completed = true
		},
	}, 100)

	// Distant frame -> APPROACH
	farFrame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	res1 := task.Tick("agent", state, farFrame)
	if res1.Phase != TaskPhaseApproach || res1.Intent == nil || res1.Intent.GetNavigate() == nil {
		t.Fatalf("expected APPROACH with NavigateIntent, got phase=%s intent=%+v", res1.Phase, res1.Intent)
	}

	// Within reach -> PREPARE
	nearFrame := &civquestv1.TelemetryFrame{
		WorldTick: 120,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 7.0, Y: 64, Z: 0.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	res2 := task.Tick("agent", state, nearFrame)
	if res2.Phase != TaskPhasePrepare || !prepareCalled || res2.Intent == nil ||
		res2.Intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_USE_WORKSTATION {
		t.Fatalf("expected PREPARE phase intent, got phase=%s intent=%+v", res2.Phase, res2.Intent)
	}

	// Next tick -> ACT
	nearFrame.WorldTick = 121
	res3 := task.Tick("agent", state, nearFrame)
	if res3.Phase != TaskPhaseAct || !actCalled || res3.Intent == nil ||
		res3.Intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected ACT phase HARVEST_BREAK intent, got phase=%s intent=%+v", res3.Phase, res3.Intent)
	}
	if task.Phase != TaskPhaseEvaluate {
		t.Fatalf("expected task phase to advance to EVALUATE after ACT, got %s", task.Phase)
	}

	// Completion tick -> COMPLETE
	nearFrame.WorldTick = 150
	res4 := task.Tick("agent", state, nearFrame)
	if res4.Phase != TaskPhaseComplete || !res4.Done || !completed {
		t.Fatalf("expected COMPLETE and OnComplete fired, got phase=%s done=%v completed=%v", res4.Phase, res4.Done, completed)
	}

	// 2. Unachievable task -> FAILED
	failTask := NewTaskFSM(TaskDescriptor{
		ID:      "generic_fail",
		Kind:    "generic_op",
		Stage:   StageSettlement,
		Routine: RoutinePrimaryDuty,
		IsUnachievable: func(s *AgentState, f *civquestv1.TelemetryFrame) bool {
			return true
		},
		OnFail: func(s *AgentState, f *civquestv1.TelemetryFrame, cur *TaskSubroutine, reason string) {
			failed = true
		},
	}, 200)
	resFail := failTask.Tick("agent", state, nearFrame)
	if resFail.Phase != TaskPhaseFailed || !resFail.Done || !failed {
		t.Fatalf("expected FAILED and OnFail fired, got phase=%s done=%v failed=%v", resFail.Phase, resFail.Done, failed)
	}
}

func TestHarvestTreeTask_StrictColumnCommitmentAndReachability(t *testing.T) {
	state := NewAgentState()
	state.mem.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("Logger")
	cfg.Profession = "EXPLORER"

	// Target tree at (4, 64..67, 0); distracting closer tree at (2, 64, 3).
	// Agent starts with empty inventory, locks onto (4, 0), and once committed must NOT craft
	// even as harvested logs enter inventory.
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 3.5, Y: 64, Z: 0.5},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 4, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 4, Y: 66, Z: 0}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 4, Y: 67, Z: 0}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 40, Y: 64, Z: 3}, BlockId: "minecraft:birch_log"},
		},
	}

	// Select and lock onto (4, 0)
	intent1 := EvaluateBootstrapGoals("Logger", frame, cfg, state, rigWalk(state))
	if intent1 == nil || intent1.GetInteractBlock() == nil ||
		intent1.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK ||
		intent1.GetInteractBlock().Pos.X != 4 || intent1.GetInteractBlock().Pos.Y != 64 || intent1.GetInteractBlock().Pos.Z != 0 {
		t.Fatalf("expected HARVEST_BREAK at bottom log (4,64,0), got %+v", intent1)
	}
	if locked, tx, tz := treeOf(rigTask(state)); !locked || tx != 4 || tz != 0 {
		t.Fatalf("expected tree lock on (4,0), got locked=%v (%d,%d)", locked, tx, tz)
	}

	// During 20-tick harvest cooldown (log still breaking), exploration AND crafting MUST be suppressed!
	frame.WorldTick = 105
	// Move birch_log closer (distance 1) and put oak_logs in inventory — must NOT switch tree or craft!
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 66, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 67, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 1}, BlockId: "minecraft:birch_log"},
	}
	frame.Status.Inventory = []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:oak_log", Count: 2},
	}
	if cooldownIntent := EvaluateBootstrapGoals("Logger", frame, cfg, state, rigWalk(state)); cooldownIntent != nil {
		t.Fatalf("expected nil intent during harvest cooldown (no exploration/crafting fallthrough), got %+v", cooldownIntent)
	}
	if exploreIntent := EvaluateExploration("Logger", frame, cfg, state, rigWalk(state)); exploreIntent != nil {
		t.Fatalf("expected EvaluateExploration to suppress wander while committed to tree, got %+v", exploreIntent)
	}

	// Next tick after (4, 64, 0) breaks -> must break (4, 65, 0) on the locked trunk, NOT (3, 64, 1)
	frame.WorldTick = 121
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 66, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 67, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 1}, BlockId: "minecraft:birch_log"},
	}
	intent2 := EvaluateBootstrapGoals("Logger", frame, cfg, state, rigWalk(state))
	if intent2 == nil || intent2.GetInteractBlock() == nil ||
		intent2.GetInteractBlock().Pos.X != 4 || intent2.GetInteractBlock().Pos.Y != 65 || intent2.GetInteractBlock().Pos.Z != 0 {
		t.Fatalf("expected continued HARVEST_BREAK on locked trunk (4,65,0), got %+v", intent2)
	}

	// Condition (a): Suppose only a canopy log beyond vanilla reach remains in the column. Reach is eye
	// (feet+1.62) to the nearest AABB point (cq-gpa.9), so Y=69 would be reachable; Y=72 (bottom face
	// 6.38m above the eye) is not.
	frame.WorldTick = 145
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 72, Z: 0}, BlockId: "minecraft:oak_log"},
	}
	// Agent is within 2.5m horizontally of (4, 0) and zero logs remain within 4.5m reach -> task completes and unlocks!
	_ = EvaluateBootstrapGoals("Logger", frame, cfg, state, rigWalk(state))
	if state.HasActiveTask() {
		t.Fatalf("expected HarvestTreeTask to complete when no logs remain within 4.5m reach at trunk")
	}
	if locked, _, _ := treeOf(rigTask(state)); locked {
		t.Fatalf("expected the harvest focus cleared once reachable logs exhausted")
	}

	// Condition (b): New tree lock where column [Y-1..Y+5] becomes completely empty
	rigClear(state)
	frame.Status.Inventory = nil
	frame.WorldTick = 200
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
	}
	if startIntent := EvaluateBootstrapGoals("Logger", frame, cfg, state, rigWalk(state)); startIntent == nil || startIntent.GetInteractBlock() == nil {
		t.Fatalf("expected new HARVEST_BREAK on (2,64,0), got %+v", startIntent)
	}
	// While pathing toward tree, exploration must stay suppressed
	rigClear(state)
	rigStart(state, NewHarvestTreeTask(12, 64, 0, StageBootstrap, 210))
	frame.WorldTick = 211
	frame.Status.Position = &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
	}
	if pathingIntent := EvaluateBootstrapGoals("Logger", frame, cfg, state, rigWalk(state)); pathingIntent != nil {
		t.Fatalf("expected nil while Baritone pathing to locked tree (no wander), got %+v", pathingIntent)
	}
	// Now trunk disappears completely from [Y-1..Y+5] -> completes!
	frame.WorldTick = 212
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	frame.InterestingBlocks = nil
	_ = EvaluateBootstrapGoals("Logger", frame, cfg, state, rigWalk(state))
	if state.HasActiveTask() {
		t.Fatalf("expected task complete when column [Y-1..Y+5] has no logs")
	}
}

func TestHarvestTreeTask_HardTimeoutUnlocksAndCoolsDown(t *testing.T) {
	state := NewAgentState()
	state.mem.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("TimeoutAgent")

	// Blocked tree at (10, 64, 0) far outside 4.5m reach, and another tree at (3, 64, 0)
	rigStart(state, NewHarvestTreeTask(10, 64, 0, StageBootstrap, 100))

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 0}, BlockId: "minecraft:birch_log"},
		},
	}

	// At tick 249 (149 ticks elapsed), still committed to (10, 0)
	frame.WorldTick = 249
	_ = EvaluateBootstrapGoals("TimeoutAgent", frame, cfg, state, rigWalk(state))
	if locked, tx, _ := treeOf(rigTask(state)); !locked || tx != 10 {
		t.Fatalf("expected tree lock on (10,0) before 150-tick timeout, got locked=%v tx=%d", locked, tx)
	}

	// At tick 250 (150 ticks without breaking a block) -> hard timeout, unlock, cooldown on (10, 0), select (3, 0)!
	frame.WorldTick = 250
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	intent := EvaluateBootstrapGoals("TimeoutAgent", frame, cfg, state, rigWalk(state))
	if intent == nil || intent.GetInteractBlock() == nil || intent.GetInteractBlock().Pos.X != 3 {
		t.Fatalf("expected agent to recover and target unobstructed tree (3,64,0) after timeout, got %+v", intent)
	}
	if !state.IsTreeOnCooldownLocked(10, 0, 250) {
		t.Fatalf("expected timed-out tree (10,0) to be placed on cooldown")
	}
}

func TestHarvestTreeTask_CollectsDroppedWoodThenTransitionsCleanly(t *testing.T) {
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	state.mem.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("Collector")

	// Locked onto tree at (4, 64, 0)
	rigStart(state, NewHarvestTreeTask(4, 64, 0, StageBootstrap, 100))

	// Tree trunk is now gone, and a dropped Oak Log entity sits at (4.5, 64, 0.5) (distance 4.5m > 2.5m pickup radius)
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 140,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0.0, Y: 64, Z: 0.5},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:    777,
				EntityType:  "minecraft:item",
				DisplayName: "Oak Log",
				Position:    &civquestv1.Vec3{X: 4.5, Y: 64, Z: 0.5},
			},
		},
	}

	collectNav := EvaluateBootstrapGoals("Collector", frame, cfg, state, rigWalk(state))
	if collectNav == nil || collectNav.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent toward dropped Oak Log after tree harvest, got %+v", collectNav)
	}
	active, kind, _, routine := state.ActiveTaskInfo()
	if !active || kind != "collect_items" || routine != RoutineCollectItems {
		t.Fatalf("expected active collect_items / COLLECT_ITEMS task, got active=%v kind=%s routine=%s", active, kind, routine)
	}

	// Item picked up (VisibleEntities empty, 2 oak_log now in inventory) -> transitions cleanly to oak_planks (first step)!
	frame.WorldTick = 160
	frame.VisibleEntities = nil
	frame.Status.Position = &civquestv1.Vec3{X: 4.0, Y: 64, Z: 0.5}
	frame.Status.Inventory = []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:oak_log", Count: 2},
	}
	craftIntent := EvaluateBootstrapGoals("Collector", frame, cfg, state, rigWalk(state))
	if craftIntent == nil || craftIntent.GetCraftItem() == nil || craftIntent.GetCraftItem().RecipeId != "minecraft:oak_planks" {
		t.Fatalf("expected clean transition to CraftItemIntent(minecraft:oak_planks), got %+v", craftIntent)
	}
	if state.HasActiveTask() {
		t.Fatalf("expected collect_items task cleared once drops collected")
	}
}

func TestTaskSubroutine_ReceiptImmediateAdvancementAndRecovery(t *testing.T) {
	state := NewAgentState()

	// 1. RECEIPT_STATUS_COMPLETED clears ActionCooldown & advances immediately
	stepIdx := 0
	var failReason string
	onFailCalled := false

	task := NewTaskSubroutine(TaskDescriptor{
		ID:                 "receipt_test",
		Kind:               "receipt_test",
		Stage:              StageSettlement,
		Routine:            RoutinePrimaryDuty,
		ReachRadius:        4.5,
		ArrivalTolerance:   2,
		ActionCooldown:     20,
		MaxReceiptFailures: 2,
		SelectSubTarget: func(s *AgentState, f *civquestv1.TelemetryFrame, cur *TaskSubroutine) (*civquestv1.Vec3, *civquestv1.BlockPosProto, string, bool) {
			return &civquestv1.Vec3{X: 2, Y: 64, Z: 0}, &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, "target_block", true
		},
		NeedsSubTargetRefresh: func(s *AgentState, f *civquestv1.TelemetryFrame, cur *TaskSubroutine) bool {
			return false
		},
		BuildActIntent: func(agentID string, s *AgentState, f *civquestv1.TelemetryFrame, cur *TaskSubroutine) *civquestv1.AgentIntent {
			stepIdx++
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0},
						InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
					},
				},
			}
		},
		OnFail: func(s *AgentState, f *civquestv1.TelemetryFrame, cur *TaskSubroutine, reason string) {
			onFailCalled = true
			failReason = reason
		},
	}, 100)

	rigStart(state, task)

	// Tick 100: emits first ACT intent and sets LastActionTick = 100
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	res1 := rigStep(state, "agent", frame)
	if res1.Intent == nil || res1.Phase != TaskPhaseAct || stepIdx != 1 {
		t.Fatalf("expected initial ACT intent on tick 100, got %+v (stepIdx=%d)", res1, stepIdx)
	}

	// Tick 101 without receipt: blocked by 20-tick ActionCooldown
	frame.WorldTick = 101
	resWait := rigStep(state, "agent", frame)
	if resWait.Intent != nil || resWait.Phase != TaskPhaseAct || stepIdx != 1 {
		t.Fatalf("expected cooldown hold without receipt at tick 101, got %+v", resWait)
	}

	// Tick 102 with RECEIPT_STATUS_COMPLETED: must advance immediately without waiting 20 ticks
	frame.WorldTick = 102
	rigDeliver(state, &civquestv1.IntentReceipt{
		BatchId: 10,
		Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
	})
	resAdvance := rigStep(state, "agent", frame)
	if resAdvance.Intent == nil || resAdvance.Phase != TaskPhaseAct || stepIdx != 2 {
		t.Fatalf("expected immediate advancement to next ACT on RECEIPT_STATUS_COMPLETED, got %+v (stepIdx=%d)", resAdvance, stepIdx)
	}

	// 2. RECEIPT_STATUS_PREEMPTED_BY_REFLEX resets cooldowns without counting as failure
	frame.WorldTick = 103
	rigDeliver(state, &civquestv1.IntentReceipt{
		BatchId: 11,
		Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_PREEMPTED_BY_REFLEX,
	})
	resPreempt := rigStep(state, "agent", frame)
	if !resPreempt.Active || resPreempt.Done || task.ConsecutiveFailures != 0 {
		t.Fatalf("expected clean preemption reset with 0 failures, got res=%+v failures=%d", resPreempt, task.ConsecutiveFailures)
	}

	// 3. First RECEIPT_STATUS_FAILED triggers recovery without failing task
	frame.WorldTick = 104
	rigDeliver(state, &civquestv1.IntentReceipt{
		BatchId:       12,
		Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
		FailureReason: "path blocked",
	})
	resRecover := rigStep(state, "agent", frame)
	if !resRecover.Active || resRecover.Done || task.ConsecutiveFailures != 1 {
		t.Fatalf("expected recovery on first failure (failures=1), got res=%+v failures=%d", resRecover, task.ConsecutiveFailures)
	}
	if task.Metadata["last_failure_reason"] != "path blocked" {
		t.Fatalf("expected last_failure_reason recorded, got %+v", task.Metadata)
	}

	// 4. Second RECEIPT_STATUS_FAILED reaches MaxReceiptFailures and fails task
	frame.WorldTick = 105
	rigDeliver(state, &civquestv1.IntentReceipt{
		BatchId:       13,
		Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
		FailureReason: "target unreachable",
	})
	resFail := rigStep(state, "agent", frame)
	if resFail.Phase != TaskPhaseFailed || !resFail.Done || !onFailCalled {
		t.Fatalf("expected TaskPhaseFailed and OnFail callback on second failure, got res=%+v onFail=%v", resFail, onFailCalled)
	}
	if failReason == "" || state.HasActiveTask() {
		t.Fatalf("expected failure reason and cleared active task, got reason=%q active=%v", failReason, state.HasActiveTask())
	}
}

func TestDistributedTreeSelection_AcrossAgents(t *testing.T) {
	// 6 distinct tree trunks all within 32m of origin (0, 64, 0)
	blocks := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 8, Y: 64, Z: 2}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 10}, BlockId: "minecraft:birch_log"},
		{Pos: &civquestv1.BlockPosProto{X: -12, Y: 64, Z: 4}, BlockId: "minecraft:spruce_log"},
		{Pos: &civquestv1.BlockPosProto{X: 6, Y: 64, Z: -14}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: -15, Y: 64, Z: -10}, BlockId: "minecraft:birch_log"},
	}

	type xz struct {
		x, z int32
	}
	chosen := make(map[xz]int)
	agents := []string{"Able", "Baker", "Carl", "Dawn", "Eve", "Frank"}

	for _, id := range agents {
		state := NewAgentState()
		state.mem.telemetryFramesSeen = 5
		cfg := xdg.DefaultAgentIdentityConfig(id)
		frame := &civquestv1.TelemetryFrame{
			WorldTick: 200,
			TimeOfDay: 2000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
			InterestingBlocks: blocks,
		}
		intent := EvaluateBootstrapGoals(id, frame, cfg, state, rigWalk(state))
		if intent == nil {
			t.Fatalf("expected non-nil intent for agent %s", id)
		}
		locked, tx, tz := treeOf(rigTask(state))
		if !locked {
			t.Fatalf("expected agent %s to lock onto a tree trunk", id)
		}
		chosen[xz{tx, tz}]++
	}

	if len(chosen) < 3 {
		t.Fatalf("expected agents to distribute across multiple distinct trees, got %+v", chosen)
	}
}

func TestCraftingTableSelfPlacement_IgnoresDistantTable(t *testing.T) {
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	state.mem.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Able has planks + sticks + crafting_table in inventory, stands at (0, 64, 0) facing +Z (Yaw=0),
	// and sees another agent's distant crafting table at (40, 64, 0).
	// Able MUST place its own crafting table immediately in front instead of navigating to (40, 64, 0).
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 300,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Yaw:             0,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 3},
				{Slot: 1, ItemId: "minecraft:stick", Count: 2},
				{Slot: 2, ItemId: "minecraft:crafting_table", Count: 1},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 40, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
		},
	}

	placeIntent := EvaluateBootstrapGoals("Able", frame, cfg, state, nil)
	if placeIntent == nil || placeIntent.GetInteractBlock() == nil ||
		placeIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK ||
		placeIntent.GetInteractBlock().BlockOrItemId != "minecraft:crafting_table" {
		t.Fatalf("expected self-placement PLACE_BLOCK for minecraft:crafting_table instead of distant navigation, got %+v", placeIntent)
	}
	placedPos := placeIntent.GetInteractBlock().Pos
	if placedPos == nil || placedPos.X != 0 || placedPos.Y != 64 || placedPos.Z != 1 {
		t.Fatalf("expected crafting table placed ~1.5m in front at (0, 64, 1), got %+v", placedPos)
	}

	// Next tick: placed crafting table appears at (0, 64, 1), consumed from inventory -> crafts wooden_axe!
	frame.WorldTick = 325
	frame.Status.Inventory = []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:oak_planks", Count: 3},
		{Slot: 1, ItemId: "minecraft:stick", Count: 2},
	}
	frame.InterestingBlocks = append(frame.InterestingBlocks, &civquestv1.VisibleBlock{
		Pos:     placedPos,
		BlockId: "minecraft:crafting_table",
	})
	craftIntent := EvaluateBootstrapGoals("Able", frame, cfg, state, nil)
	if craftIntent == nil || craftIntent.GetCraftItem() == nil || craftIntent.GetCraftItem().RecipeId != "minecraft:wooden_axe" {
		t.Fatalf("expected immediate transition to CraftItemIntent(minecraft:wooden_axe), got %+v", craftIntent)
	}
}

func TestBootstrap_CraftingTableRecallFromSpatialIndex(t *testing.T) {
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	state.mem.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Known crafting table 30 blocks away in SpatialIndex (empty InterestingBlocks)
	state.SpatialIndex().UpsertPOI(db.AgentPOI{
		POIID:          FormatPOIID(POICategoryWorkstation, 30, 64, 0),
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              30.5,
		Y:              64.0,
		Z:              0.5,
		AccessX:        29.5,
		AccessY:        64.0,
		AccessZ:        0.5,
		ElevationLayer: ElevationSurface,
		SafetyScore:    1.0,
		Reliability:    1.0,
		IsActive:       true,
	})

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 6},
				{Slot: 1, ItemId: "minecraft:stick", Count: 4},
			},
		},
	}

	// Agent 30 blocks away with EMPTY InterestingBlocks recalls table and navigates to Access coords
	navIntent := EvaluateBootstrapGoals("Able", frame, cfg, state, nil)
	if navIntent == nil || navIntent.GetNavigate() == nil || navIntent.GetNavigate().Target == nil {
		t.Fatalf("expected NavigateIntent towards recalled crafting table, got %+v", navIntent)
	}
	target := navIntent.GetNavigate().Target
	if target.X != 29.5 || target.Y != 64.0 || target.Z != 0.5 {
		t.Fatalf("expected NavigateIntent target (29.5, 64, 0.5), got %+v", target)
	}

	// While pathing, exploration must not hijack navigation
	frame.WorldTick = 110
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	if pathingIntent := EvaluateBootstrapGoals("Able", frame, cfg, state, nil); pathingIntent != nil {
		t.Fatalf("expected nil while pathing to recalled table, got %+v", pathingIntent)
	}

	// Once within 4.5m (e.g. arrived at 29.5, 64, 0.5), craft directly at recalled workstation
	frame.WorldTick = 150
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	frame.Status.Position = &civquestv1.Vec3{X: 29.5, Y: 64.0, Z: 0.5}
	craftIntent := EvaluateBootstrapGoals("Able", frame, cfg, state, nil)
	if craftIntent == nil || craftIntent.GetCraftItem() == nil || craftIntent.GetCraftItem().RecipeId != "minecraft:wooden_axe" {
		t.Fatalf("expected CraftItemIntent(minecraft:wooden_axe) when within 4.5m of recalled table, got %+v", craftIntent)
	}
	if craftIntent.GetCraftItem().WorkstationPos == nil || craftIntent.GetCraftItem().WorkstationPos.X != 30 {
		t.Fatalf("expected WorkstationPos X=30, got %+v", craftIntent.GetCraftItem().WorkstationPos)
	}

	// Low reliability (< 0.5) table is ignored -> falls back to pocket crafting a new table
	state2 := NewAgentState()
	state2.SetRecipeGraph(newTestRecipeGraph())
	state2.mem.telemetryFramesSeen = 5
	state2.SpatialIndex().UpsertPOI(db.AgentPOI{
		POIID:          FormatPOIID(POICategoryWorkstation, 30, 64, 0),
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              30.5,
		Y:              64.0,
		Z:              0.5,
		AccessX:        29.5,
		AccessY:        64.0,
		AccessZ:        0.5,
		ElevationLayer: ElevationSurface,
		SafetyScore:    1.0,
		Reliability:    0.3,
		IsActive:       true,
	})
	frame.Status.Position = &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
	pocketCraft := EvaluateBootstrapGoals("Able", frame, cfg, state2, nil)
	if pocketCraft == nil || pocketCraft.GetCraftItem() == nil || pocketCraft.GetCraftItem().RecipeId != "minecraft:crafting_table" {
		t.Fatalf("expected pocket crafting fallback when recalled table reliability < 0.5, got %+v", pocketCraft)
	}
}

func TestBootstrap_TimberStandRecallFromSpatialIndex(t *testing.T) {
	state := NewAgentState()
	state.mem.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("Logger")
	cfg.Profession = "EXPLORER"

	// Known oak_log stand 30 blocks away in SpatialIndex (empty InterestingBlocks)
	poiID := FormatPOIID(POICategoryResource, 30, 64, 0)
	state.SpatialIndex().UpsertPOI(db.AgentPOI{
		POIID:          poiID,
		Category:       POICategoryResource,
		BlockID:        "minecraft:oak_log",
		X:              30.5,
		Y:              64.0,
		Z:              0.5,
		AccessX:        29.5,
		AccessY:        64.0,
		AccessZ:        0.5,
		ElevationLayer: ElevationSurface,
		SafetyScore:    1.0,
		Reliability:    1.0,
		IsActive:       true,
	})

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}

	navIntent := EvaluateBootstrapGoals("Logger", frame, cfg, state, nil)
	if navIntent == nil || navIntent.GetNavigate() == nil || navIntent.GetNavigate().Target == nil {
		t.Fatalf("expected NavigateIntent towards recalled timber stand, got %+v", navIntent)
	}
	target := navIntent.GetNavigate().Target
	if target.X != 29.5 || target.Y != 64.0 || target.Z != 0.5 {
		t.Fatalf("expected NavigateIntent target (29.5, 64, 0.5), got %+v", target)
	}

	// While pathing, exploration must not overwrite path
	frame.WorldTick = 110
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	if pathingIntent := EvaluateBootstrapGoals("Logger", frame, cfg, state, nil); pathingIntent != nil {
		t.Fatalf("expected nil while pathing to recalled timber stand, got %+v", pathingIntent)
	}

	// Arriving at (29.5, 64, 0.5) with still-empty InterestingBlocks prunes stale log POI
	frame.WorldTick = 160
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	frame.Status.Position = &civquestv1.Vec3{X: 29.5, Y: 64.0, Z: 0.5}
	_ = EvaluateBootstrapGoals("Logger", frame, cfg, state, nil)
	if _, exists := state.SpatialIndex().GetPOI(poiID); exists {
		t.Fatalf("expected stale log POI %s to be pruned when absent at close range", poiID)
	}
}

func TestPioneerGenesis_SettlementNucleationLifecycle(t *testing.T) {
	// 1. Pioneer Genesis Invariant: Defaults must be EXPLORER in BOOTSTRAP stage
	cfg := xdg.DefaultAgentIdentityConfig("earl")
	if cfg.Profession != "EXPLORER" {
		t.Errorf("expected default profession EXPLORER, got %q", cfg.Profession)
	}
	if cfg.Stage != "BOOTSTRAP" {
		t.Errorf("expected default stage BOOTSTRAP, got %q", cfg.Stage)
	}
	if cfg.EffectiveStage() != "BOOTSTRAP" {
		t.Errorf("expected EffectiveStage() = BOOTSTRAP, got %q", cfg.EffectiveStage())
	}

	state := NewAgentState()
	si := state.SpatialIndex()

	// 2. Wilderness Phase: No settlement POI exists in SpatialIndex
	if si.HasSettlementPOI(nil, 0) {
		t.Fatalf("expected HasSettlementPOI=false in raw wilderness without hearth")
	}

	specializedRoles := []string{"FARMER", "MINER", "BUILDER", "CRAFTER", "RANCHER", "GUARD"}
	for _, role := range specializedRoles {
		if !IsSpecializedProfession(role) {
			t.Errorf("expected %s to be recognized as specialized profession", role)
		}
		if !IsPioneerNucleationLocked(si, role) {
			t.Errorf("expected %s nucleation to be locked without settlement POI", role)
		}
		if eff := EffectivePioneerProfession(si, role); eff != "EXPLORER" {
			t.Errorf("expected %s to fall back to EXPLORER before settlement genesis, got %q", role, eff)
		}
	}

	// Pioneer Explorer itself is never locked
	if IsPioneerNucleationLocked(si, "EXPLORER") {
		t.Errorf("expected EXPLORER to not be locked")
	}
	if eff := EffectivePioneerProfession(si, "EXPLORER"); eff != "EXPLORER" {
		t.Errorf("expected EXPLORER to remain EXPLORER, got %q", eff)
	}

	// Verify SurvivalKitEvaluator facts reflect locked nucleation
	cfgFarmer := &xdg.AgentIdentityConfig{Name: "carl", Profession: "FARMER"}
	eval := NewSurvivalKitEvaluator()
	graph := NewFactGraph()
	evalCtx := &EvaluatorContext{
		AgentID: "carl",
		Config:  cfgFarmer,
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 100,
			Status:    &civquestv1.AgentStatus{Position: &civquestv1.Vec3{X: 0, Y: 64, Z: 0}},
		},
	}
	if err := eval.Evaluate(evalCtx, graph); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}
	if graph.GetBool("bootstrap.has_settlement_poi") {
		t.Errorf("expected bootstrap.has_settlement_poi=false")
	}
	if !graph.GetBool("bootstrap.nucleation_locked") {
		t.Errorf("expected bootstrap.nucleation_locked=true for FARMER in wilderness")
	}

	// 3. Settlement Genesis: Consecrate a campfire hearth POI in SpatialIndex
	hearthPOI := db.AgentPOI{
		POIID:       FormatPOIID(POICategoryHabitation, 0, 64, 0),
		Category:    POICategoryHabitation,
		BlockID:     "minecraft:campfire",
		X:           0.5,
		Y:           64.0,
		Z:           0.5,
		Reliability: 1.0,
	}
	si.UpsertPOI(hearthPOI)

	if !si.HasSettlementPOI(nil, 0) {
		t.Fatalf("expected HasSettlementPOI=true after registering campfire hearth")
	}
	nearestHearth := si.FindSettlementHearth(&civquestv1.Vec3{X: 10, Y: 64, Z: 10}, 64.0)
	if nearestHearth == nil || nearestHearth.POIID != hearthPOI.POIID {
		t.Fatalf("expected to find nearest hearth %s, got %+v", hearthPOI.POIID, nearestHearth)
	}

	// Now career nucleation unlocks: specialized roles can be assumed!
	for _, role := range specializedRoles {
		if IsPioneerNucleationLocked(si, role) {
			t.Errorf("expected %s nucleation to be unlocked once settlement hearth exists", role)
		}
		if eff := EffectivePioneerProfession(si, role); eff != role {
			t.Errorf("expected %s effective profession to be active after settlement genesis, got %q", role, eff)
		}
	}

	// Verify SurvivalKitEvaluator facts reflect unlocked settlement genesis
	graphSettled := NewFactGraph()
	if err := eval.Evaluate(evalCtx, graphSettled); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}
	if !graphSettled.GetBool("bootstrap.has_settlement_poi") {
		t.Errorf("expected bootstrap.has_settlement_poi=true after hearth established")
	}
	if graphSettled.GetBool("bootstrap.nucleation_locked") {
		t.Errorf("expected bootstrap.nucleation_locked=false after hearth established")
	}
}

// TestHierarchicalPathfinding_UndergroundRejectsDistantSurfaceTreeWithoutCorridor verifies ADR-026:
// An underground agent at Y=58 does not target a surface tree at Y=96 unless an established
// TrailCorridor connects them, preventing solid wall/ceiling grinding.
func TestHierarchicalPathfinding_UndergroundRejectsDistantSurfaceTreeWithoutCorridor(t *testing.T) {
	state := NewAgentState()
	si := state.SpatialIndex()

	treePOI := db.AgentPOI{
		POIID:          FormatPOIID(POICategoryResource, 10, 96, 20),
		Category:       POICategoryResource,
		BlockID:        "minecraft:oak_log",
		X:              10.5,
		Y:              96.0,
		Z:              20.5,
		AccessX:        10.5,
		AccessY:        96.0,
		AccessZ:        19.5,
		ElevationLayer: ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	si.UpsertPOI(treePOI)

	// Agent inside underground dugout at Y=58 (ElevationDugout)
	undergroundPos := &civquestv1.Vec3{X: 10.0, Y: 58.0, Z: 20.0}
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 50,
		Status: &civquestv1.AgentStatus{
			Position: undergroundPos,
		},
	}

	// 1. Without corridor: findNearestTreeTrunk MUST NOT return the surface tree
	targetLog, recalledPOI := findNearestTreeTrunk("Able", frame, undergroundPos, state)
	if targetLog != nil {
		t.Fatalf("expected nil targetLog underground without visible blocks, got %+v", targetLog)
	}
	if recalledPOI != nil {
		t.Fatalf("ADR-026 VIOLATION: underground agent recalled distant surface tree without corridor: %+v", recalledPOI)
	}

	// 2. Add an established TrailCorridor connecting the dugout shelter to the surface tree
	dugoutPOIID := FormatPOIID(POICategoryHabitation, 10, 58, 20)
	state.HabitTrailTracker().SetOriginPOI(dugoutPOIID, undergroundPos)

	waypoints := []*civquestv1.Vec3{
		{X: 10.0, Y: 58.0, Z: 20.0},
		{X: 10.0, Y: 64.0, Z: 20.0},
		{X: 10.0, Y: 96.0, Z: 19.5},
	}
	wpJSON, _ := EncodeWaypointsJSON(waypoints)
	si.UpsertCorridor(db.TrailCorridor{
		ID:            "corridor_dugout_to_tree",
		FromPOIID:     dugoutPOIID,
		ToPOIID:       treePOI.POIID,
		WaypointsJSON: wpJSON,
		TotalDistance: 45.0,
		IsPaved:       true,
		TraverseCount: 5,
	})

	// 3. With corridor connecting origin to tree: now recalled tree is reachable via trail
	_, recalledWithCorridor := findNearestTreeTrunk("Able", frame, undergroundPos, state)
	if recalledWithCorridor == nil || recalledWithCorridor.POIID != treePOI.POIID {
		t.Fatalf("expected recalledTreePOI %s with established corridor, got %+v", treePOI.POIID, recalledWithCorridor)
	}

	// 4. Verify evaluateBootstrapStage navigates along corridor waypoints
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	frame.Status.Inventory = []*civquestv1.ItemStackProto{} // lacks timber
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE

	intent := EvaluateBootstrapGoals("Able", frame, cfg, state, nil)
	if intent == nil || intent.GetNavigate() == nil || intent.GetNavigate().Target == nil {
		t.Fatalf("expected corridor NavigateIntent, got %+v", intent)
	}
	// Initial corridor target must be W_0 (10, 58, 20)
	target := intent.GetNavigate().Target
	if target.X != 10.0 || target.Y != 58.0 || target.Z != 20.0 {
		t.Fatalf("expected first waypoint (10, 58, 20), got (%v, %v, %v)", target.X, target.Y, target.Z)
	}
}

func TestInspectBootstrapInventory_CoalAndCharcoal(t *testing.T) {
	inv := inspectBootstrapInventory([]*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:oak_log", Count: 2},
		{Slot: 1, ItemId: "minecraft:crimson_stem", Count: 1},
		{Slot: 2, ItemId: "minecraft:stick", Count: 3},
		{Slot: 3, ItemId: "minecraft:coal", Count: 2},
		{Slot: 4, ItemId: "minecraft:charcoal", Count: 4},
		{Slot: 5, ItemId: "minecraft:coal_ore", Count: 5},
		{Slot: 6, ItemId: "minecraft:coal_block", Count: 1},
	})
	if inv.logCount != 3 {
		t.Errorf("expected logCount 3, got %d", inv.logCount)
	}
	if inv.stickCount != 3 {
		t.Errorf("expected stickCount 3, got %d", inv.stickCount)
	}
	if inv.coalCount != 6 {
		t.Errorf("expected coalCount 6 (2 coal + 4 charcoal, excluding coal_ore/coal_block), got %d", inv.coalCount)
	}
}

func TestBootstrapLightCrafting_ADR029StrictRecipes(t *testing.T) {
	baseTools := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
		{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
	}

	cases := []struct {
		name              string
		extraItems        []*civquestv1.ItemStackProto
		interestingBlocks []*civquestv1.VisibleBlock
		wantRecipe        string // "" expects nil intent
		wantWorkstation   bool
	}{
		{
			name: "Campfire crafted with table in reach, 3 logs, 3 sticks, 1 coal",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 3},
				{Slot: 3, ItemId: "minecraft:stick", Count: 3},
				{Slot: 4, ItemId: "minecraft:coal", Count: 1},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe:      "minecraft:campfire",
			wantWorkstation: true,
		},
		{
			name: "Campfire crafted with table in reach, 3 logs, 3 sticks, 1 charcoal",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:birch_log", Count: 3},
				{Slot: 3, ItemId: "minecraft:stick", Count: 3},
				{Slot: 4, ItemId: "minecraft:charcoal", Count: 1},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe:      "minecraft:campfire",
			wantWorkstation: true,
		},
		{
			name: "No coal or charcoal blocks campfire and torch even with logs, planks, sticks, and table",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 6},
				{Slot: 3, ItemId: "minecraft:oak_planks", Count: 8},
				{Slot: 4, ItemId: "minecraft:stick", Count: 6},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe: "",
		},
		{
			name: "No sticks with 3 logs and 1 coal and table crafts oak_planks then stick then torch",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 3},
				{Slot: 3, ItemId: "minecraft:coal", Count: 1},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe:      "minecraft:oak_planks",
			wantWorkstation: false,
		},
		{
			name: "Campfire crafts sticks first when 3 logs + 4 planks + 1 coal at table",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 3},
				{Slot: 3, ItemId: "minecraft:oak_planks", Count: 4},
				{Slot: 4, ItemId: "minecraft:coal", Count: 1},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe:      "minecraft:stick",
			wantWorkstation: false,
		},
		{
			name: "Table out of horizontal reach falls back to 2x2 pocket torch instead of campfire",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 3},
				{Slot: 3, ItemId: "minecraft:stick", Count: 3},
				{Slot: 4, ItemId: "minecraft:coal", Count: 1},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 5, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe:      "minecraft:torch",
			wantWorkstation: false,
		},
		{
			name: "Table out of vertical reach (dy > 2.5) falls back to 2x2 pocket torch",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 3},
				{Slot: 3, ItemId: "minecraft:stick", Count: 3},
				{Slot: 4, ItemId: "minecraft:coal", Count: 1},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 67, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe:      "minecraft:torch",
			wantWorkstation: false,
		},
		{
			name: "Fewer than 3 logs with sticks and coal crafts 2x2 pocket torch instead of campfire",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 2},
				{Slot: 3, ItemId: "minecraft:stick", Count: 3},
				{Slot: 4, ItemId: "minecraft:coal", Count: 1},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe:      "minecraft:torch",
			wantWorkstation: false,
		},
		{
			name: "Fewer than 3 sticks with exactly 3 logs (no spare logs for sticks) and coal crafts 2x2 pocket torch instead of campfire",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 3},
				{Slot: 3, ItemId: "minecraft:stick", Count: 2},
				{Slot: 4, ItemId: "minecraft:coal", Count: 1},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe:      "minecraft:torch",
			wantWorkstation: false,
		},
		{
			name: "Pocket craft torch without crafting table using 1 stick and 1 coal",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 6},
				{Slot: 3, ItemId: "minecraft:stick", Count: 1},
				{Slot: 4, ItemId: "minecraft:coal", Count: 1},
			},
			wantRecipe:      "minecraft:torch",
			wantWorkstation: false,
		},
		{
			name: "Pocket craft torch without crafting table using 1 stick and 1 charcoal",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 6},
				{Slot: 3, ItemId: "minecraft:stick", Count: 1},
				{Slot: 4, ItemId: "minecraft:charcoal", Count: 1},
			},
			wantRecipe:      "minecraft:torch",
			wantWorkstation: false,
		},
		{
			name: "Already has light source emits no light craft intent",
			extraItems: []*civquestv1.ItemStackProto{
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 6},
				{Slot: 3, ItemId: "minecraft:stick", Count: 3},
				{Slot: 4, ItemId: "minecraft:coal", Count: 1},
				{Slot: 5, ItemId: "minecraft:torch", Count: 4},
			},
			interestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
			wantRecipe: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := NewAgentState()
			state.SetRecipeGraph(newTestRecipeGraph())
			pos := &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
			inv := append([]*civquestv1.ItemStackProto{}, baseTools...)
			inv = append(inv, tc.extraItems...)
			frame := &civquestv1.TelemetryFrame{
				WorldTick: 200,
				TimeOfDay: 2000,
				Status: &civquestv1.AgentStatus{
					Position:        pos,
					Health:          20,
					FoodLevel:       20,
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
					Inventory:       inv,
				},
				InterestingBlocks: tc.interestingBlocks,
			}

			intent := EvaluateBootstrapGoalsDirect("Able", frame, pos, civquestv1.NavigationState_NAVIGATION_STATE_IDLE, state, nil)
			if tc.wantRecipe == "" {
				if intent != nil {
					t.Fatalf("expected nil intent, got %+v", intent)
				}
				return
			}
			craft := intent.GetCraftItem()
			if craft == nil {
				t.Fatalf("expected CraftItemIntent(%s), got %+v", tc.wantRecipe, intent)
			}
			if craft.RecipeId != tc.wantRecipe {
				t.Fatalf("expected RecipeId %q, got %q", tc.wantRecipe, craft.RecipeId)
			}
			if tc.wantWorkstation {
				if craft.WorkstationPos == nil || craft.WorkstationPos.X != 1 || craft.WorkstationPos.Y != 64 || craft.WorkstationPos.Z != 0 {
					t.Fatalf("expected WorkstationPos (1, 64, 0), got %+v", craft.WorkstationPos)
				}
			} else if craft.WorkstationPos != nil {
				t.Fatalf("expected nil WorkstationPos for 2x2 pocket craft, got %+v", craft.WorkstationPos)
			}
		})
	}
}

func TestBootstrapNoCoalDoesNotEmitBogusCraftOrInfiniteChop(t *testing.T) {
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	pos := &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        pos,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:oak_log", Count: 6},
				{Slot: 3, ItemId: "minecraft:stick", Count: 4},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			{Pos: &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		},
	}

	intent := EvaluateBootstrapGoalsDirect("Able", frame, pos, civquestv1.NavigationState_NAVIGATION_STATE_IDLE, state, nil)
	if intent != nil {
		t.Fatalf("expected nil bootstrap intent when agent has tools, >= 6 logs, and no coal/charcoal, got %+v", intent)
	}
}

func TestBootstrapDynamicRecipeGraph_BirchLogToWoodenAxeStepByStep(t *testing.T) {
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	state.mem.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("BirchCrafter")
	cfg.Profession = "EXPLORER"

	inv := map[string]int{"minecraft:birch_log": 4}
	toProtoInv := func(m map[string]int) []*civquestv1.ItemStackProto {
		var out []*civquestv1.ItemStackProto
		var slot int32
		for id, cnt := range m {
			if cnt > 0 {
				out = append(out, &civquestv1.ItemStackProto{Slot: slot, ItemId: id, Count: int32(cnt)})
				slot++
			}
		}
		return out
	}

	tableBlock := &civquestv1.VisibleBlock{
		Pos:     &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0},
		BlockId: "minecraft:crafting_table",
	}

	expectedSequence := []string{
		"minecraft:birch_planks",
		"minecraft:stick",
		"minecraft:birch_planks",
		"minecraft:wooden_axe",
	}

	for stepIdx, wantRecipe := range expectedSequence {
		frame := &civquestv1.TelemetryFrame{
			WorldTick: int64(100 + stepIdx*25),
			TimeOfDay: 2000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory:       toProtoInv(inv),
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{tableBlock},
		}
		intent := EvaluateBootstrapGoals("BirchCrafter", frame, cfg, state, nil)
		craft := intent.GetCraftItem()
		if craft == nil {
			t.Fatalf("step %d: expected CraftItemIntent(%s), got %+v", stepIdx, wantRecipe, intent)
		}
		if craft.RecipeId != wantRecipe {
			t.Fatalf("step %d: expected recipe %q, got %q", stepIdx, wantRecipe, craft.RecipeId)
		}

		switch wantRecipe {
		case "minecraft:birch_planks":
			if craft.WorkstationPos != nil {
				t.Fatalf("step %d: expected nil WorkstationPos for 2x2 birch_planks, got %+v", stepIdx, craft.WorkstationPos)
			}
			inv["minecraft:birch_log"]--
			inv["minecraft:birch_planks"] += 4
		case "minecraft:stick":
			if craft.WorkstationPos != nil {
				t.Fatalf("step %d: expected nil WorkstationPos for 2x2 stick, got %+v", stepIdx, craft.WorkstationPos)
			}
			inv["minecraft:birch_planks"] -= 2
			inv["minecraft:stick"] += 4
		case "minecraft:wooden_axe":
			if craft.WorkstationPos == nil || craft.WorkstationPos.X != 1 {
				t.Fatalf("step %d: expected WorkstationPos at table (1,64,0), got %+v", stepIdx, craft.WorkstationPos)
			}
			inv["minecraft:birch_planks"] -= 3
			inv["minecraft:stick"] -= 2
			inv["minecraft:wooden_axe"]++
		}
	}
}

func TestBootstrapDynamicRecipeGraph_NoCraftingTableMagnetismWith1Log(t *testing.T) {
	state := NewAgentState()
	state.SetRecipeGraph(newTestRecipeGraph())
	state.mem.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("Forager")
	cfg.Profession = "EXPLORER"

	// Recalled crafting table 30 blocks away in SpatialIndex
	state.SpatialIndex().UpsertPOI(db.AgentPOI{
		POIID:          FormatPOIID(POICategoryWorkstation, 30, 64, 0),
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              30.5,
		Y:              64.0,
		Z:              0.5,
		AccessX:        29.5,
		AccessY:        64.0,
		AccessZ:        0.5,
		ElevationLayer: ElevationSurface,
		SafetyScore:    1.0,
		Reliability:    1.0,
		IsActive:       true,
	})

	// Agent already has wooden_sword, needs wooden_axe, holds only 1 birch_log (insufficient for wooden_axe!),
	// and sees a nearby birch_log tree at (3, 64, 0).
	// Must NOT walk back to the crafting table at (30, 64, 0); must harvest the nearby tree!
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 1, ItemId: "minecraft:birch_log", Count: 1},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 0}, BlockId: "minecraft:birch_log"},
		},
	}

	intent := EvaluateBootstrapGoals("Forager", frame, cfg, state, nil)
	if intent == nil || intent.GetInteractBlock() == nil ||
		intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK on nearby tree instead of walking to distant table with 1 log, got %+v", intent)
	}

	// With 2 birch_logs >4.5m from table and still missing planks/sticks, crafts 2x2 birch_planks first!
	frame.WorldTick = 240
	rigClear(state)
	frame.Status.Inventory = []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:wooden_sword", Count: 1},
		{Slot: 1, ItemId: "minecraft:birch_log", Count: 2},
	}
	pocketIntent := EvaluateBootstrapGoals("Forager", frame, cfg, state, nil)
	if pocketIntent == nil || pocketIntent.GetCraftItem() == nil || pocketIntent.GetCraftItem().RecipeId != "minecraft:birch_planks" {
		t.Fatalf("expected 2x2 pocket craft birch_planks before walking to distant table, got %+v", pocketIntent)
	}
}

func TestBootstrapDynamicRecipeGraph_FailClosedWithoutSyncedCatalog(t *testing.T) {
	state := NewAgentState() // recipeGraph is nil (not yet synced)
	state.mem.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("NoRecipes")
	cfg.Profession = "EXPLORER"

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 6},
				{Slot: 1, ItemId: "minecraft:stick", Count: 4},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			{Pos: &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		},
	}

	intent := EvaluateBootstrapGoals("NoRecipes", frame, cfg, state, nil)
	if intent != nil && intent.GetCraftItem() != nil {
		t.Fatalf("expected fail-closed behavior (no CraftItemIntent) when RecipeGraph is nil, got %+v", intent)
	}
	if intent == nil || intent.GetInteractBlock() == nil ||
		intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected fallback to timber harvesting when RecipeGraph is nil, got %+v", intent)
	}
}

func TestFindNearestTreeTrunk_DeprioritizesPeerOccupiedTrunks(t *testing.T) {
	state := NewAgentState()
	pos := &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}

	// Two visible trunks at (5, 64, 0) and (8, 64, 0). A peer agent "bert" is already at (5.5, 64, 0.5).
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			AgentId:  "able",
			Position: pos,
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:    2,
				EntityType:  "minecraft:player",
				DisplayName: "bert",
				Position:    &civquestv1.Vec3{X: 5.5, Y: 64, Z: 0.5},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 5, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 8, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		},
	}

	// Ensure even for any agentID, the unoccupied trunk (8, 64, 0) is chosen over peer-occupied (5, 64, 0)
	for _, id := range []string{"able", "carl", "dave", "earl", "fred"} {
		chosen, _ := findNearestTreeTrunk(id, frame, pos, state)
		if chosen == nil || chosen.Pos == nil || chosen.Pos.X != 8 {
			t.Fatalf("agent %s: expected unoccupied trunk X=8 to be chosen over peer-occupied X=5, got %+v", id, chosen)
		}
	}
}
