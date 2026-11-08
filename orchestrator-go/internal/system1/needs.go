package system1

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// FoodItemInfo specifies the nutrition and saturation properties of a consumable item.
type FoodItemInfo struct {
	Nutrition  int
	Saturation float64
	Harmful    bool
}

// foodRegistry contains predefined nutrition and saturation values for common Minecraft and Farmer's Delight foods.
var foodRegistry = map[string]FoodItemInfo{
	// Golden / Magic
	"enchanted_golden_apple": {Nutrition: 4, Saturation: 9.6, Harmful: false},
	"golden_apple":           {Nutrition: 4, Saturation: 9.6, Harmful: false},
	"golden_carrot":          {Nutrition: 6, Saturation: 14.4, Harmful: false},

	// Cooked Meats & Fish
	"cooked_beef":     {Nutrition: 8, Saturation: 12.8, Harmful: false},
	"steak":           {Nutrition: 8, Saturation: 12.8, Harmful: false},
	"cooked_porkchop": {Nutrition: 8, Saturation: 12.8, Harmful: false},
	"cooked_mutton":   {Nutrition: 6, Saturation: 9.6, Harmful: false},
	"cooked_salmon":   {Nutrition: 6, Saturation: 9.6, Harmful: false},
	"cooked_chicken":  {Nutrition: 6, Saturation: 7.2, Harmful: false},
	"cooked_rabbit":   {Nutrition: 5, Saturation: 6.0, Harmful: false},
	"cooked_cod":      {Nutrition: 5, Saturation: 6.0, Harmful: false},

	// Breads, Stews & Crops
	"bread":         {Nutrition: 5, Saturation: 6.0, Harmful: false},
	"baked_potato":  {Nutrition: 5, Saturation: 6.0, Harmful: false},
	"rabbit_stew":   {Nutrition: 10, Saturation: 12.0, Harmful: false},
	"mushroom_stew": {Nutrition: 6, Saturation: 7.2, Harmful: false},
	"beetroot_soup": {Nutrition: 6, Saturation: 7.2, Harmful: false},
	"pumpkin_pie":   {Nutrition: 8, Saturation: 4.8, Harmful: false},
	"apple":         {Nutrition: 4, Saturation: 2.4, Harmful: false},
	"carrot":        {Nutrition: 3, Saturation: 3.6, Harmful: false},
	"melon_slice":   {Nutrition: 2, Saturation: 1.2, Harmful: false},
	"sweet_berries": {Nutrition: 2, Saturation: 0.4, Harmful: false},
	"glow_berries":  {Nutrition: 2, Saturation: 0.4, Harmful: false},
	"cookie":        {Nutrition: 2, Saturation: 0.4, Harmful: false},
	"honey_bottle":  {Nutrition: 6, Saturation: 1.2, Harmful: false},
	"dried_kelp":    {Nutrition: 1, Saturation: 0.6, Harmful: false},

	// Raw items
	"raw_beef":     {Nutrition: 3, Saturation: 1.8, Harmful: false},
	"beef":         {Nutrition: 3, Saturation: 1.8, Harmful: false},
	"raw_porkchop": {Nutrition: 3, Saturation: 1.8, Harmful: false},
	"porkchop":     {Nutrition: 3, Saturation: 1.8, Harmful: false},
	"raw_mutton":   {Nutrition: 2, Saturation: 1.2, Harmful: false},
	"mutton":       {Nutrition: 2, Saturation: 1.2, Harmful: false},
	"raw_chicken":  {Nutrition: 2, Saturation: 1.2, Harmful: false},
	"chicken":      {Nutrition: 2, Saturation: 1.2, Harmful: false},
	"raw_salmon":   {Nutrition: 2, Saturation: 0.4, Harmful: false},
	"salmon":       {Nutrition: 2, Saturation: 0.4, Harmful: false},
	"raw_cod":      {Nutrition: 2, Saturation: 0.4, Harmful: false},
	"cod":          {Nutrition: 2, Saturation: 0.4, Harmful: false},

	// Farmer's Delight Mod Foods
	"tomato":                 {Nutrition: 2, Saturation: 1.0, Harmful: false},
	"cabbage":                {Nutrition: 2, Saturation: 1.0, Harmful: false},
	"onion":                  {Nutrition: 2, Saturation: 1.0, Harmful: false},
	"cabbage_leaf":           {Nutrition: 1, Saturation: 0.5, Harmful: false},
	"tomato_sauce":           {Nutrition: 2, Saturation: 1.0, Harmful: false},
	"fried_egg":              {Nutrition: 4, Saturation: 4.8, Harmful: false},
	"barbecue_stick":         {Nutrition: 6, Saturation: 6.0, Harmful: false},
	"egg_sandwich":           {Nutrition: 8, Saturation: 8.0, Harmful: false},
	"chicken_sandwich":       {Nutrition: 10, Saturation: 10.0, Harmful: false},
	"hamburger":              {Nutrition: 11, Saturation: 13.0, Harmful: false},
	"bacon_sandwich":         {Nutrition: 10, Saturation: 10.0, Harmful: false},
	"mutton_wrap":            {Nutrition: 10, Saturation: 11.0, Harmful: false},
	"dumplings":              {Nutrition: 8, Saturation: 8.0, Harmful: false},
	"stuffed_potato":         {Nutrition: 10, Saturation: 10.0, Harmful: false},
	"cabbage_rolls":          {Nutrition: 6, Saturation: 6.0, Harmful: false},
	"salmon_roll":            {Nutrition: 7, Saturation: 7.0, Harmful: false},
	"cod_roll":               {Nutrition: 7, Saturation: 7.0, Harmful: false},
	"beef_stew":              {Nutrition: 10, Saturation: 12.0, Harmful: false},
	"chicken_soup":           {Nutrition: 10, Saturation: 12.0, Harmful: false},
	"vegetable_soup":         {Nutrition: 8, Saturation: 8.0, Harmful: false},
	"fish_stew":              {Nutrition: 10, Saturation: 10.0, Harmful: false},
	"fried_rice":             {Nutrition: 9, Saturation: 9.0, Harmful: false},
	"pumpkin_soup":           {Nutrition: 8, Saturation: 8.0, Harmful: false},
	"baked_cod_stew":         {Nutrition: 9, Saturation: 9.0, Harmful: false},
	"noodle_soup":            {Nutrition: 10, Saturation: 10.0, Harmful: false},
	"pasta_with_meatballs":   {Nutrition: 12, Saturation: 14.0, Harmful: false},
	"pasta_with_mutton_chop": {Nutrition: 12, Saturation: 14.0, Harmful: false},
	"roasted_mutton_chops":   {Nutrition: 12, Saturation: 14.0, Harmful: false},
	"steak_and_potatoes":     {Nutrition: 12, Saturation: 14.0, Harmful: false},
	"vegetable_noodles":      {Nutrition: 10, Saturation: 10.0, Harmful: false},
	"ratatouille":            {Nutrition: 9, Saturation: 9.0, Harmful: false},
	"squid_ink_pasta":        {Nutrition: 12, Saturation: 14.0, Harmful: false},
	"grilled_salmon":         {Nutrition: 12, Saturation: 14.0, Harmful: false},
	"roast_chicken":          {Nutrition: 14, Saturation: 16.0, Harmful: false},
	"stuffed_pumpkin":        {Nutrition: 14, Saturation: 16.0, Harmful: false},
	"honey_glazed_ham":       {Nutrition: 14, Saturation: 16.0, Harmful: false},
	"shepherds_pie":          {Nutrition: 14, Saturation: 16.0, Harmful: false},
	"apple_pie":              {Nutrition: 8, Saturation: 4.8, Harmful: false},
	"sweet_berry_cheesecake": {Nutrition: 8, Saturation: 4.8, Harmful: false},
	"chocolate_pie":          {Nutrition: 8, Saturation: 4.8, Harmful: false},
	"melon_popsicle":         {Nutrition: 3, Saturation: 1.5, Harmful: false},
	"fruit_salad":            {Nutrition: 6, Saturation: 6.0, Harmful: false},
	"mixed_salad":            {Nutrition: 6, Saturation: 6.0, Harmful: false},
	"nether_salad":           {Nutrition: 5, Saturation: 4.0, Harmful: false},

	// Harmful / Toxic Foods
	"suspicious_stew":  {Nutrition: 6, Saturation: 7.2, Harmful: true},
	"poisonous_potato": {Nutrition: 2, Saturation: 1.2, Harmful: true},
	"pufferfish":       {Nutrition: 1, Saturation: 0.2, Harmful: true},
	"rotten_flesh":     {Nutrition: 4, Saturation: 0.8, Harmful: true},
	"spider_eye":       {Nutrition: 2, Saturation: 3.2, Harmful: true},
}

// LookupFoodItem resolves food info by item ID, accepting namespaced or bare IDs.
func LookupFoodItem(itemID string) (FoodItemInfo, bool) {
	id := strings.ToLower(strings.TrimSpace(itemID))
	if idx := strings.Index(id, ":"); idx != -1 {
		bare := id[idx+1:]
		if info, ok := foodRegistry[bare]; ok {
			return info, true
		}
	}
	info, ok := foodRegistry[id]
	return info, ok
}

// CalculateFoodScore evaluates the restorative value, saturation, and safety of a food item.
// Returns 0.0 for dangerous, poisonous, or unknown non-food items.
func CalculateFoodScore(itemID string) float64 {
	id := strings.ToLower(strings.TrimSpace(itemID))
	bare := id
	if idx := strings.Index(id, ":"); idx != -1 {
		bare = id[idx+1:]
	}

	// Immediate harmful check
	if bare == "suspicious_stew" || bare == "poisonous_potato" || bare == "pufferfish" ||
		bare == "rotten_flesh" || bare == "spider_eye" {
		return 0.0
	}

	// Special golden apples priority
	if bare == "enchanted_golden_apple" {
		return 1000.0
	}
	if bare == "golden_apple" {
		return 500.0
	}
	if bare == "golden_carrot" {
		return 200.0
	}

	if info, ok := LookupFoodItem(itemID); ok {
		if info.Harmful {
			return 0.0
		}
		return float64(info.Nutrition)*2.0 + info.Saturation*3.0
	}

	// Heuristic recognition for modded food dishes
	if strings.Contains(bare, "soup") || strings.Contains(bare, "stew") || strings.Contains(bare, "roast") ||
		strings.Contains(bare, "pasta") || strings.Contains(bare, "burger") || strings.Contains(bare, "sandwich") ||
		strings.Contains(bare, "pie") {
		return 75.0
	}

	return 0.0
}

// FindOptimalFood locates the slot of the highest scoring edible item in the agent's inventory.
func FindOptimalFood(inventory []*civquestv1.ItemStackProto) (slot int32, item *civquestv1.ItemStackProto, found bool) {
	bestScore := 0.0
	bestSlot := int32(-1)
	var bestItem *civquestv1.ItemStackProto

	for _, it := range inventory {
		if it == nil || it.Count <= 0 {
			continue
		}
		score := CalculateFoodScore(it.ItemId)
		if score > bestScore {
			bestScore = score
			bestSlot = it.Slot
			bestItem = it
		}
	}

	if bestSlot >= 0 && bestItem != nil {
		return bestSlot, bestItem, true
	}
	return -1, nil, false
}

// EvaluateHunger evaluates whether the agent should consume food based on food_level and resting state.
// Emits UseItemIntent when hunger is critical (<= 6) or resting (< 14) and food is present.
func EvaluateHunger(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState, restingOrIdle bool) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil {
		return nil
	}

	foodLevel := frame.Status.FoodLevel
	shouldEat := false
	if foodLevel <= 6 {
		shouldEat = true
	} else if restingOrIdle && foodLevel < 14 {
		shouldEat = true
	}

	if !shouldEat {
		return nil
	}

	// Respect 32-tick eating cooldown (1.6s)
	if state != nil {
		lastEat := state.GetLastEatingTick()
		if lastEat > 0 && frame.WorldTick >= lastEat && frame.WorldTick-lastEat < 32 {
			return nil
		}
	}

	slot, _, found := FindOptimalFood(frame.Status.Inventory)
	if !found {
		return nil
	}

	if state != nil {
		state.SetLastEatingTick(frame.WorldTick)
	}

	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_UseItem{
			UseItem: &civquestv1.UseItemIntent{
				InventorySlot:      slot,
				IsEatingOrDrinking: true,
			},
		},
	}
}

// EvaluateSleep evaluates bed navigation, sleeping in bed, and dawn awakening.
func EvaluateSleep(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil {
		return nil
	}

	// Auto-detect and record bed pos from interesting_blocks if not yet assigned
	if state != nil && state.GetAssignedBed() == nil {
		for _, b := range frame.InterestingBlocks {
			if b != nil && b.Pos != nil && strings.Contains(strings.ToLower(b.BlockId), "bed") {
				state.SetAssignedBed(b.Pos)
				break
			}
		}
	}

	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	isThunder := frame.Weather == civquestv1.Weather_WEATHER_THUNDER
	isNight := tod >= 13000 && tod < 23500
	isDawn := (tod >= 23500 || tod < 1000) && !isThunder

	// 1. Dawn awakening: if sleeping at dawn, wake up
	if isDawn {
		if frame.Status.IsSleeping {
			var bedPos *civquestv1.BlockPosProto
			if state != nil {
				bedPos = state.GetAssignedBed()
			}
			if bedPos == nil && frame.Status.Position != nil {
				bedPos = &civquestv1.BlockPosProto{
					X: int32(math.Floor(frame.Status.Position.X)),
					Y: int32(math.Floor(frame.Status.Position.Y)),
					Z: int32(math.Floor(frame.Status.Position.Z)),
				}
			}
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             bedPos,
						InteractionType: civquestv1.InteractBlockIntent_WAKE_UP,
						BlockOrItemId:   "minecraft:bed",
					},
				},
			}
		}
		return nil
	}

	// 2. Night or thunderstorm: navigate to assigned bed or sleep
	if isNight || isThunder {
		if frame.Status.IsSleeping {
			return nil // Already sleeping soundly
		}

		// When companion bond is active, do not force return to village bed
		if state != nil && state.IsCompanionActive() {
			return nil
		}

		var bedPos *civquestv1.BlockPosProto
		if state != nil {
			bedPos = state.GetAssignedBed()
		}
		if bedPos == nil {
			return nil // No bed assigned to navigate to
		}

		agentPos := frame.Status.Position
		if agentPos == nil {
			return nil
		}

		dx := float64(bedPos.X) + 0.5 - agentPos.X
		dy := float64(bedPos.Y) + 0.5 - agentPos.Y
		dz := float64(bedPos.Z) + 0.5 - agentPos.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

		if dist <= 3.0 {
			// In reach: enter bed with 40-tick interaction backoff to prevent packet spam
			if state != nil {
				lastSleep := state.GetLastSleepAttemptTick()
				if lastSleep > 0 && frame.WorldTick >= lastSleep && frame.WorldTick-lastSleep < 40 {
					return nil
				}
				state.SetLastSleepAttemptTick(frame.WorldTick)
			}
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             bedPos,
						InteractionType: civquestv1.InteractBlockIntent_SLEEP_IN_BED,
						BlockOrItemId:   "minecraft:bed",
					},
				},
			}
		}

		// Outside reach: navigate to bed location.
		// Baritone churn mitigation: if already pathing, do not re-emit NavigateIntent
		if frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
			return nil
		}
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: float64(bedPos.X) + 0.5,
						Y: float64(bedPos.Y),
						Z: float64(bedPos.Z) + 0.5,
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 1,
				},
			},
		}
	}

	return nil
}

// EvaluateBiologicalNeeds combines hunger and sleep evaluation, returning any required intents.
func EvaluateBiologicalNeeds(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState) []*civquestv1.AgentIntent {
	var intents []*civquestv1.AgentIntent
	if frame == nil || frame.Status == nil {
		return intents
	}

	isIdle := frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_IDLE ||
		frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_UNSPECIFIED
	if state != nil {
		routine := state.GetCurrentRoutine()
		if routine == RoutineTavernSocial || routine == RoutineDawnAwakening || routine == RoutineSleep {
			isIdle = true
		}
	}

	// 1. Hunger - eating takes priority over sleep in the current tick
	if eatIntent := EvaluateHunger(agentID, frame, state, isIdle); eatIntent != nil {
		intents = append(intents, eatIntent)
		return intents
	}

	// 2. Sleep
	if sleepIntent := EvaluateSleep(agentID, frame, state); sleepIntent != nil {
		intents = append(intents, sleepIntent)
	}

	return intents
}
