package system1

import (
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestForaging_HarvestsVisibleEdiblePlantBlocks(t *testing.T) {
	plants := []string{
		"minecraft:sweet_berry_bush",
		"minecraft:wheat",
		"minecraft:melon",
	}

	for i, blockID := range plants {
		t.Run(blockID, func(t *testing.T) {
			agentID := "forager_plant_" + blockID
			state := NewAgentState()

			// 1. Out of reach (10m away) -> NavigateIntent toward plant
			farFrame := &civquestv1.TelemetryFrame{
				WorldTick: int64(100 + i*100),
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          18,
					MaxHealth:       20,
					FoodLevel:       8,
					Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
				InterestingBlocks: []*civquestv1.VisibleBlock{
					{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}, BlockId: blockID},
				},
			}
			navIntent := EvaluateForaging(agentID, farFrame, state)
			if navIntent == nil || navIntent.GetNavigate() == nil {
				t.Fatalf("expected NavigateIntent toward %s, got %+v", blockID, navIntent)
			}
			if navIntent.GetNavigate().Target.X != 10.5 || navIntent.GetNavigate().Target.Z != 0.5 {
				t.Fatalf("expected navigate target (10.5, 64, 0.5), got %+v", navIntent.GetNavigate().Target)
			}
			if state.GetGoals().ImmediateGoal != ForagingGoalText {
				t.Fatalf("expected ImmediateGoal=%q, got %q", ForagingGoalText, state.GetGoals().ImmediateGoal)
			}

			// 2. Within reach (<= 4.5m) -> InteractBlock HARVEST_BREAK
			nearFrame := &civquestv1.TelemetryFrame{
				WorldTick: int64(115 + i*100),
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          18,
					MaxHealth:       20,
					FoodLevel:       5,
					Position:        &civquestv1.Vec3{X: 8.5, Y: 64, Z: 0.5},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
				InterestingBlocks: []*civquestv1.VisibleBlock{
					{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}, BlockId: blockID},
				},
			}
			harvestIntent := EvaluateForaging(agentID, nearFrame, state)
			if harvestIntent == nil || harvestIntent.GetInteractBlock() == nil {
				t.Fatalf("expected InteractBlockIntent to harvest %s, got %+v", blockID, harvestIntent)
			}
			ib := harvestIntent.GetInteractBlock()
			if ib.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
				t.Fatalf("expected HARVEST_BREAK for %s, got %v", blockID, ib.InteractionType)
			}
			if ib.Pos.X != 10 || ib.Pos.Y != 64 || ib.Pos.Z != 0 {
				t.Fatalf("expected harvest pos (10, 64, 0), got %+v", ib.Pos)
			}
		})
	}
}

func TestForaging_HuntsVisiblePassiveFoodAnimals(t *testing.T) {
	animals := []string{
		"minecraft:cow",
		"minecraft:pig",
		"minecraft:chicken",
	}

	for i, entityType := range animals {
		t.Run(entityType, func(t *testing.T) {
			agentID := "forager_hunt_" + entityType
			state := NewAgentState()

			// 1. Out of melee reach -> NavigateIntent toward animal
			farFrame := &civquestv1.TelemetryFrame{
				WorldTick: int64(200 + i*100),
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          16,
					MaxHealth:       20,
					FoodLevel:       6,
					Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
				VisibleEntities: []*civquestv1.VisibleEntity{
					{
						EntityId:   int32(500 + i),
						EntityType: entityType,
						Distance:   12.0,
						Position:   &civquestv1.Vec3{X: 12, Y: 64, Z: 0},
					},
				},
			}
			navIntent := EvaluateForaging(agentID, farFrame, state)
			if navIntent == nil || navIntent.GetNavigate() == nil {
				t.Fatalf("expected NavigateIntent toward %s, got %+v", entityType, navIntent)
			}
			if navIntent.GetNavigate().Target.X != 12 {
				t.Fatalf("expected navigate target X=12, got %+v", navIntent.GetNavigate().Target)
			}

			// 2. Within melee reach (<= 3.5m) -> AttackIntent targeting animal ID
			nearFrame := &civquestv1.TelemetryFrame{
				WorldTick: int64(215 + i*100),
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          16,
					MaxHealth:       20,
					FoodLevel:       6,
					Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
				VisibleEntities: []*civquestv1.VisibleEntity{
					{
						EntityId:   int32(500 + i),
						EntityType: entityType,
						Distance:   2.0,
						Position:   &civquestv1.Vec3{X: 12, Y: 64, Z: 0},
					},
				},
			}
			atkIntent := EvaluateForaging(agentID, nearFrame, state)
			if atkIntent == nil || atkIntent.GetAttack() == nil {
				t.Fatalf("expected AttackIntent to hunt %s, got %+v", entityType, atkIntent)
			}
			att := atkIntent.GetAttack()
			if att.TargetEntityId != int32(500+i) || att.FleeImmediately {
				t.Fatalf("unexpected AttackIntent for %s: %+v", entityType, att)
			}
		})
	}
}

func TestForaging_ScoutsOutwardWhenNoFoodVisible(t *testing.T) {
	agentID := "forager_scout"
	state := NewAgentState()
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 300,
		TimeOfDay: 4000,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          14,
			MaxHealth:       20,
			FoodLevel:       5,
			Position:        &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}

	intent := EvaluateForaging(agentID, frame, state)
	if intent == nil || intent.GetNavigate() == nil || intent.GetNavigate().Target == nil {
		t.Fatalf("expected outward NavigateIntent when hungry with no visible food, got %+v", intent)
	}
	tgt := intent.GetNavigate().Target
	if tgt.X == 100 && tgt.Z == 100 {
		t.Fatalf("expected outward exploration target away from (100, 100), got %+v", tgt)
	}
	if state.GetGoals().ImmediateGoal != ForagingGoalText {
		t.Fatalf("expected ImmediateGoal=%q, got %q", ForagingGoalText, state.GetGoals().ImmediateGoal)
	}
}

func TestForaging_EatsFirstWhenEdibleFoodInInventory(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	state := NewAgentState()

	ctx := &FSMContext{
		AgentID: "forager_has_food",
		State:   state,
		Config:  &xdg.AgentIdentityConfig{Shift: xdg.ShiftFirst},
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 400,
			TimeOfDay: 4000,
			Status: &civquestv1.AgentStatus{
				AgentId:         "forager_has_food",
				Health:          18,
				MaxHealth:       20,
				FoodLevel:       5, // critical hunger WITH food in inventory
				Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:cooked_beef", Count: 2, Slot: 1},
				},
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:sweet_berry_bush"},
			},
		},
	}

	res := fsm.Tick(ctx)
	if ctx.Facts != nil && ctx.Facts.GetBool("vitals.needs_foraging") {
		t.Fatalf("expected vitals.needs_foraging=false when edible food is in inventory")
	}
	foragingSkill := NewForagingSkill()
	if foragingSkill.Preconditions(ctx.Facts) {
		t.Fatalf("expected ForagingSkill.Preconditions=false when edible food is in inventory")
	}

	var foundEat bool
	for _, it := range res.Intents {
		if use := it.Intent.GetUseItem(); use != nil && use.IsEatingOrDrinking && use.InventorySlot == 1 {
			foundEat = true
		}
		if it.Intent.GetInteractBlock() != nil {
			t.Fatalf("expected eating rather than harvesting while edible food is already in inventory, got %+v", res.Intents)
		}
	}
	if !foundEat {
		t.Fatalf("expected UseItemIntent to eat cooked_beef from inventory, got %+v", res.Intents)
	}
}

func TestForaging_YieldsToActiveHostileCombat(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	state := NewAgentState()

	ctx := &FSMContext{
		AgentID: "forager_combat",
		State:   state,
		Config:  &xdg.AgentIdentityConfig{Shift: xdg.ShiftFirst, Traits: xdg.AgentTraits{Bravery: 0.5}},
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 4000,
			Status: &civquestv1.AgentStatus{
				AgentId:         "forager_combat",
				Health:          16,
				MaxHealth:       20,
				FoodLevel:       4,
				Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
			VisibleEntities: []*civquestv1.VisibleEntity{
				{EntityId: 77, EntityType: "minecraft:zombie", IsHostile: true, Distance: 4.0, Position: &civquestv1.Vec3{X: 4, Y: 64, Z: 0}},
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: -2, Y: 64, Z: 0}, BlockId: "minecraft:sweet_berry_bush"},
			},
		},
	}

	res := fsm.Tick(ctx)
	if res.Stage != StageEmergency {
		t.Fatalf("expected StageEmergency when hostile zombie is present, got %s", res.Stage)
	}
	if NewForagingSkill().Preconditions(ctx.Facts) {
		t.Fatalf("expected ForagingSkill.Preconditions=false while hostiles are present")
	}
	if EvaluateForaging("forager_combat", ctx.Frame, state) != nil {
		t.Fatalf("expected EvaluateForaging to yield (nil) when hostile zombie is within perception radius")
	}
}

func BenchmarkForagingSkill_Sub5ms(b *testing.B) {
	skill := NewForagingSkill()
	state := NewAgentState()
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		Status: &civquestv1.AgentStatus{
			AgentId:         "bench_forager",
			Health:          10,
			MaxHealth:       20,
			FoodLevel:       5,
			Position:        &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 58, Y: 64, Z: 50}, BlockId: "minecraft:sweet_berry_bush"},
			{Pos: &civquestv1.BlockPosProto{X: 60, Y: 64, Z: 52}, BlockId: "minecraft:wheat"},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{EntityId: 1, EntityType: "minecraft:cow", Distance: 10.0, Position: &civquestv1.Vec3{X: 50, Y: 64, Z: 60}},
		},
	}
	reg := NewDefaultEngineRegistry()
	facts := reg.EvaluateAll(&EvaluatorContext{AgentID: "bench_forager", Frame: frame, State: state})
	skillCtx := &SkillContext{AgentID: "bench_forager", Frame: frame, State: state}

	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		if skill.Preconditions(facts) {
			_, _ = skill.Execute(skillCtx, facts)
		}
	}
	elapsed := time.Since(start)
	if b.N > 0 && elapsed/time.Duration(b.N) > 5*time.Millisecond {
		b.Fatalf("ForagingSkill exceeded 5ms budget: %v/op", elapsed/time.Duration(b.N))
	}
}
