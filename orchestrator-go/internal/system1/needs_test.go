package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestFoodScore_CommonFoods(t *testing.T) {
	// Golden apple highest
	if scoreGA := CalculateFoodScore("minecraft:golden_apple"); scoreGA < 400.0 {
		t.Fatalf("expected golden apple to have high score, got %v", scoreGA)
	}

	// Cooked beef > bread
	scoreBeef := CalculateFoodScore("minecraft:cooked_beef")
	scoreBread := CalculateFoodScore("minecraft:bread")
	if scoreBeef <= scoreBread {
		t.Fatalf("expected cooked_beef (%v) > bread (%v)", scoreBeef, scoreBread)
	}

	// Farmer's Delight roast chicken > tomato
	scoreRoast := CalculateFoodScore("farmersdelight:roast_chicken")
	scoreTomato := CalculateFoodScore("farmersdelight:tomato")
	if scoreRoast <= scoreTomato {
		t.Fatalf("expected roast_chicken (%v) > tomato (%v)", scoreRoast, scoreTomato)
	}

	// Toxic items must have score 0
	toxicItems := []string{
		"minecraft:rotten_flesh",
		"rotten_flesh",
		"minecraft:spider_eye",
		"spider_eye",
		"minecraft:pufferfish",
		"pufferfish",
		"minecraft:poisonous_potato",
		"poisonous_potato",
		"minecraft:suspicious_stew",
		"suspicious_stew",
	}
	for _, item := range toxicItems {
		if s := CalculateFoodScore(item); s != 0 {
			t.Errorf("expected food score 0 for toxic item %q, got %v", item, s)
		}
	}
}

func TestFindOptimalFood(t *testing.T) {
	inventory := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:rotten_flesh", Count: 64, Slot: 0},
		{ItemId: "minecraft:apple", Count: 5, Slot: 1},
		{ItemId: "farmersdelight:tomato", Count: 10, Slot: 2},
		{ItemId: "minecraft:cooked_beef", Count: 3, Slot: 3},
		{ItemId: "minecraft:bread", Count: 8, Slot: 4},
	}

	slot, item, found := FindOptimalFood(inventory)
	if !found {
		t.Fatal("expected to find food item")
	}
	if slot != 3 || item.ItemId != "minecraft:cooked_beef" {
		t.Fatalf("expected slot 3 (cooked_beef), got slot %d (%s)", slot, item.ItemId)
	}
}

func TestEvaluateHunger_Thresholds(t *testing.T) {
	inventory := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:bread", Count: 4, Slot: 1},
	}

	// 1. Critical hunger: food_level <= 6 should trigger even when moving/busy
	t.Run("Critical Hunger Active Duty", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			WorldTick: 100,
			Status: &civquestv1.AgentStatus{
				FoodLevel: 5, // <= 6
				Inventory: inventory,
			},
		}
		state := NewAgentState()
		intent := EvaluateHunger("agent1", frame, state, false) // restingOrIdle = false
		if intent == nil {
			t.Fatal("expected UseItemIntent for critical hunger")
		}
		useItem := intent.GetUseItem()
		if useItem == nil || useItem.InventorySlot != 1 || !useItem.IsEatingOrDrinking {
			t.Fatalf("unexpected UseItem intent: %+v", useItem)
		}
	})

	// 2. Resting hunger: food_level < 14 should trigger when idle
	t.Run("Resting Hunger Idle", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			WorldTick: 200,
			Status: &civquestv1.AgentStatus{
				FoodLevel: 12, // < 14
				Inventory: inventory,
			},
		}
		state := NewAgentState()
		intent := EvaluateHunger("agent1", frame, state, true) // restingOrIdle = true
		if intent == nil {
			t.Fatal("expected UseItemIntent for resting hunger when idle")
		}
	})

	// 3. Resting hunger: food_level < 14 should NOT trigger when busy/moving
	t.Run("Resting Hunger Busy", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			WorldTick: 300,
			Status: &civquestv1.AgentStatus{
				FoodLevel: 12,
				Inventory: inventory,
			},
		}
		state := NewAgentState()
		intent := EvaluateHunger("agent1", frame, state, false) // restingOrIdle = false
		if intent != nil {
			t.Fatal("expected no intent when busy and food_level > 6")
		}
	})

	// 4. Well fed: food_level >= 14 should NOT trigger even when idle
	t.Run("Well Fed Idle", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			WorldTick: 400,
			Status: &civquestv1.AgentStatus{
				FoodLevel: 16,
				Inventory: inventory,
			},
		}
		state := NewAgentState()
		intent := EvaluateHunger("agent1", frame, state, true)
		if intent != nil {
			t.Fatal("expected no intent when food_level >= 14")
		}
	})

	// 5. Eating cooldown: 32 ticks
	t.Run("Eating Cooldown", func(t *testing.T) {
		state := NewAgentState()
		state.SetLastEatingTick(500)

		frame := &civquestv1.TelemetryFrame{
			WorldTick: 520, // 20 ticks later (< 32)
			Status: &civquestv1.AgentStatus{
				FoodLevel: 4,
				Inventory: inventory,
			},
		}
		intent := EvaluateHunger("agent1", frame, state, true)
		if intent != nil {
			t.Fatal("expected no intent during 32-tick eating cooldown")
		}

		// After 32 ticks (533) -> should trigger again
		frame.WorldTick = 533
		intent2 := EvaluateHunger("agent1", frame, state, true)
		if intent2 == nil {
			t.Fatal("expected intent after eating cooldown expired")
		}
	})
}

func TestEvaluateSleep_BedFSM(t *testing.T) {
	bedPos := &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}

	// 1. Night time, far from bed -> Navigate to bed
	t.Run("Night Navigate To Bed", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 14000, // Night (13000-23500)
			Status: &civquestv1.AgentStatus{
				Position:   &civquestv1.Vec3{X: 50, Y: 64, Z: 50}, // ~56 blocks away
				IsSleeping: false,
			},
		}
		state := NewAgentState()
		state.SetAssignedBed(bedPos)

		intent := EvaluateSleep("agent1", frame, state)
		if intent == nil {
			t.Fatal("expected NavigateIntent to bed")
		}
		nav := intent.GetNavigate()
		if nav == nil || nav.Target == nil {
			t.Fatalf("expected NavigateIntent, got %+v", intent)
		}
		if nav.Target.X != 10.5 || nav.Target.Z != 10.5 {
			t.Errorf("expected target near bed (10.5, 64, 10.5), got %+v", nav.Target)
		}
	})

	// 2. Night time, close to bed (dist <= 3) -> InteractBlock SLEEP_IN_BED
	t.Run("Night Enter Bed", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 14000,
			Status: &civquestv1.AgentStatus{
				Position:   &civquestv1.Vec3{X: 11.0, Y: 64, Z: 10.0}, // dist = 1 block
				IsSleeping: false,
			},
		}
		state := NewAgentState()
		state.SetAssignedBed(bedPos)

		intent := EvaluateSleep("agent1", frame, state)
		if intent == nil {
			t.Fatal("expected SLEEP_IN_BED intent")
		}
		interact := intent.GetInteractBlock()
		if interact == nil || interact.InteractionType != civquestv1.InteractBlockIntent_SLEEP_IN_BED {
			t.Fatalf("expected SLEEP_IN_BED, got %+v", interact)
		}
	})

	// 3. Night time, already sleeping -> No action
	t.Run("Night Already Sleeping", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 15000,
			Status: &civquestv1.AgentStatus{
				Position:   &civquestv1.Vec3{X: 10.0, Y: 64, Z: 10.0},
				IsSleeping: true,
			},
		}
		state := NewAgentState()
		state.SetAssignedBed(bedPos)

		intent := EvaluateSleep("agent1", frame, state)
		if intent != nil {
			t.Fatalf("expected no intent while already sleeping, got %+v", intent)
		}
	})

	// 4. Dawn awakening -> WAKE_UP
	t.Run("Dawn Awakening", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 23600, // Dawn (>= 23500)
			Status: &civquestv1.AgentStatus{
				Position:   &civquestv1.Vec3{X: 10.0, Y: 64, Z: 10.0},
				IsSleeping: true,
			},
		}
		state := NewAgentState()
		state.SetAssignedBed(bedPos)

		intent := EvaluateSleep("agent1", frame, state)
		if intent == nil {
			t.Fatal("expected WAKE_UP intent at dawn")
		}
		interact := intent.GetInteractBlock()
		if interact == nil || interact.InteractionType != civquestv1.InteractBlockIntent_WAKE_UP {
			t.Fatalf("expected WAKE_UP, got %+v", interact)
		}
	})

	// 5. Thunderstorm sleep trigger during daytime
	t.Run("Thunderstorm Sleeping", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 4000, // Midday
			Weather:   civquestv1.Weather_WEATHER_THUNDER,
			Status: &civquestv1.AgentStatus{
				Position:   &civquestv1.Vec3{X: 11.0, Y: 64, Z: 10.0},
				IsSleeping: false,
			},
		}
		state := NewAgentState()
		state.SetAssignedBed(bedPos)

		intent := EvaluateSleep("agent1", frame, state)
		if intent == nil {
			t.Fatal("expected sleep intent during thunderstorm")
		}
		interact := intent.GetInteractBlock()
		if interact == nil || interact.InteractionType != civquestv1.InteractBlockIntent_SLEEP_IN_BED {
			t.Fatalf("expected SLEEP_IN_BED during thunderstorm, got %+v", interact)
		}
	})

	// 6. Bed auto-discovery
	t.Run("Bed Auto Discovery", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 14000,
			Status: &civquestv1.AgentStatus{
				Position:   &civquestv1.Vec3{X: 20.0, Y: 64, Z: 20.0},
				IsSleeping: false,
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{
					Pos:     &civquestv1.BlockPosProto{X: 21, Y: 64, Z: 20},
					BlockId: "minecraft:red_bed",
				},
			},
		}
		state := NewAgentState()
		if state.GetAssignedBed() != nil {
			t.Fatal("expected initial assigned bed to be nil")
		}

		intent := EvaluateSleep("agent1", frame, state)
		if state.GetAssignedBed() == nil {
			t.Fatal("expected bed to be auto-discovered and assigned")
		}
		if intent == nil {
			t.Fatal("expected sleep intent with newly assigned bed")
		}
	})

	// 7. Sleep interaction backoff (40 ticks)
	t.Run("Sleep Interaction Backoff", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			TimeOfDay: 14000,
			Status: &civquestv1.AgentStatus{
				Position:   &civquestv1.Vec3{X: 11.0, Y: 64, Z: 10.0}, // dist = 1 block
				IsSleeping: false,
			},
		}
		state := NewAgentState()
		state.SetAssignedBed(bedPos)

		// First attempt succeeds and emits SLEEP_IN_BED
		intent1 := EvaluateSleep("agent1", frame, state)
		if intent1 == nil || intent1.GetInteractBlock() == nil ||
			intent1.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_SLEEP_IN_BED {
			t.Fatalf("expected SLEEP_IN_BED on first attempt, got %+v", intent1)
		}

		// Subsequent attempt at tick 1010 (< 40 ticks elapsed) must be throttled
		frame.WorldTick = 1010
		intent2 := EvaluateSleep("agent1", frame, state)
		if intent2 != nil {
			t.Fatalf("expected sleep backoff to throttle intent at tick 1010, got %+v", intent2)
		}

		// After 40 ticks (tick 1045) -> retry is allowed
		frame.WorldTick = 1045
		intent3 := EvaluateSleep("agent1", frame, state)
		if intent3 == nil || intent3.GetInteractBlock() == nil ||
			intent3.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_SLEEP_IN_BED {
			t.Fatalf("expected SLEEP_IN_BED after backoff expires, got %+v", intent3)
		}
	})

	// 8. Baritone churn mitigation: do not re-emit NavigateIntent if already PATHING
	t.Run("Baritone Churn Mitigation", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 14000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 50.0, Y: 64, Z: 50.0},
				IsSleeping:      false,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
			},
		}
		state := NewAgentState()
		state.SetAssignedBed(bedPos)

		intent := EvaluateSleep("agent1", frame, state)
		if intent != nil {
			t.Fatalf("expected nil intent when already PATHING toward bed, got %+v", intent)
		}
	})
}

func TestEvaluateBiologicalNeeds_PrioritizesEatingOverSleeping(t *testing.T) {
	bedPos := &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}
	inventory := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:bread", Count: 4, Slot: 1},
	}

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		TimeOfDay: 14000, // Night time
		Status: &civquestv1.AgentStatus{
			Position:   &civquestv1.Vec3{X: 10.5, Y: 64, Z: 10.5},
			FoodLevel:  4, // Critical hunger <= 6
			IsSleeping: false,
			Inventory:  inventory,
		},
	}
	state := NewAgentState()
	state.SetAssignedBed(bedPos)

	intents := EvaluateBiologicalNeeds("agent1", frame, state)
	if len(intents) != 1 {
		t.Fatalf("expected exactly 1 intent prioritizing eating, got %d intents", len(intents))
	}
	useItem := intents[0].GetUseItem()
	if useItem == nil || !useItem.IsEatingOrDrinking {
		t.Fatalf("expected eating intent to take precedence over sleeping, got %+v", intents[0])
	}
}
