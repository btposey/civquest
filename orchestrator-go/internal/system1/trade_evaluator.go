package system1

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// System1TradeDecision encapsulates a <1ms deterministic quid-pro-quo trade evaluation.
type System1TradeDecision struct {
	Decision       string // "ACCEPT", "COUNTER_OFFER", or "REJECT"
	AcceptDeal     bool
	OfferedValue   float64
	RequestedValue float64
	RequiredRatio  float64
	CounterOffer   []*civquestv1.ItemStackProto
	SpokenReply    string
	PrestigeDelta  float64
	MemorySummary  string
	TradeIntent    *civquestv1.RespondTradeIntent
}

// DefaultTradeValuations returns the baseline quid-pro-quo item valuation table for an agent,
// weighted by the agent's primary profession.
func DefaultTradeValuations(profession string) map[string]db.TradeValuation {
	base := map[string]float64{
		// Precious & Currency
		"minecraft:emerald":      10.0,
		"minecraft:diamond":      30.0,
		"minecraft:gold_ingot":   8.0,
		"minecraft:iron_ingot":   5.0,
		"minecraft:copper_ingot": 2.0,
		"minecraft:coal":         1.5,
		"minecraft:charcoal":     1.5,
		"minecraft:flint":        1.0,

		// Weapons & Shields
		"minecraft:netherite_sword": 45.0,
		"minecraft:diamond_sword":   32.0,
		"minecraft:iron_sword":      14.0,
		"minecraft:stone_sword":     6.0,
		"minecraft:wooden_sword":    3.0,
		"minecraft:shield":          12.0,
		"minecraft:bow":             10.0,
		"minecraft:crossbow":        12.0,
		"minecraft:arrow":           0.5,

		// Tools
		"minecraft:diamond_pickaxe": 35.0,
		"minecraft:iron_pickaxe":    15.0,
		"minecraft:stone_pickaxe":   6.0,
		"minecraft:wooden_pickaxe":  3.0,
		"minecraft:diamond_axe":     35.0,
		"minecraft:iron_axe":        15.0,
		"minecraft:stone_axe":       6.0,
		"minecraft:wooden_axe":      3.0,
		"minecraft:iron_shovel":     10.0,
		"minecraft:stone_shovel":    4.0,
		"minecraft:iron_hoe":        10.0,
		"minecraft:stone_hoe":       4.0,

		// Armor
		"minecraft:iron_chestplate": 24.0,
		"minecraft:iron_leggings":   20.0,
		"minecraft:iron_helmet":     15.0,
		"minecraft:iron_boots":      12.0,

		// Food & Survival
		"minecraft:golden_apple":    25.0,
		"minecraft:cooked_beef":     4.0,
		"minecraft:cooked_porkchop": 4.0,
		"minecraft:cooked_mutton":   3.5,
		"minecraft:cooked_chicken":  3.0,
		"minecraft:cooked_salmon":   3.5,
		"minecraft:cooked_cod":      3.0,
		"minecraft:bread":           2.5,
		"minecraft:baked_potato":    2.5,
		"minecraft:apple":           2.0,
		"minecraft:carrot":          1.2,
		"minecraft:potato":          1.0,
		"minecraft:wheat":           1.0,
		"minecraft:wheat_seeds":     0.3,
		"minecraft:sweet_berries":   1.0,

		// Exploration, Cartography & Chronicling
		"minecraft:torch":         0.8,
		"minecraft:lantern":       4.0,
		"minecraft:compass":       12.0,
		"minecraft:map":           8.0,
		"minecraft:filled_map":    15.0,
		"minecraft:book":          4.0,
		"minecraft:writable_book": 8.0,
		"minecraft:written_book":  12.0,
		"minecraft:paper":         1.0,
		"minecraft:ink_sac":       1.5,
		"minecraft:feather":       1.0,

		// Building & Materials
		"minecraft:oak_log":     1.0,
		"minecraft:spruce_log":  1.0,
		"minecraft:birch_log":   1.0,
		"minecraft:oak_planks":  0.3,
		"minecraft:cobblestone": 0.25,
		"minecraft:stone":       0.4,
		"minecraft:dirt":        0.1,
		"minecraft:stick":       0.15,

		// Mob Drops
		"minecraft:leather":      2.0,
		"minecraft:string":       1.2,
		"minecraft:bone":         1.0,
		"minecraft:gunpowder":    2.5,
		"minecraft:ender_pearl":  8.0,
		"minecraft:rotten_flesh": 0.2,
		"minecraft:spider_eye":   0.8,
	}

	profUpper := strings.ToUpper(strings.TrimSpace(profession))
	table := make(map[string]db.TradeValuation, len(base))
	for itemID, val := range base {
		mult := 1.0
		rationale := "baseline frontier valuation"
		switch profUpper {
		case "EXPLORER", "TRAILBLAZER":
			if strings.Contains(itemID, "sword") || strings.Contains(itemID, "axe") || itemID == "minecraft:shield" ||
				strings.Contains(itemID, "boots") || itemID == "minecraft:torch" || itemID == "minecraft:compass" ||
				strings.Contains(itemID, "map") || itemID == "minecraft:bread" || strings.Contains(itemID, "cooked_") {
				mult = 1.4
				rationale = "explorer frontier survival and scouting priority"
			}
		case "CARTOGRAPHER":
			if strings.Contains(itemID, "map") || itemID == "minecraft:paper" || itemID == "minecraft:compass" || itemID == "minecraft:ink_sac" {
				mult = 1.6
				rationale = "cartographer map compilation priority"
			}
		case "CHRONICLER", "HISTORIAN":
			if strings.Contains(itemID, "book") || itemID == "minecraft:paper" || itemID == "minecraft:ink_sac" || itemID == "minecraft:feather" {
				mult = 1.5
				rationale = "chronicler archival priority"
			}
		case "FARMER":
			if strings.Contains(itemID, "hoe") || strings.Contains(itemID, "seeds") || itemID == "minecraft:bone" || itemID == "minecraft:wheat" {
				mult = 1.4
				rationale = "farmer agricultural priority"
			}
		case "BLACKSMITH", "WEAPONSMITH", "ARMORER":
			if strings.Contains(itemID, "ingot") || itemID == "minecraft:coal" || itemID == "minecraft:diamond" {
				mult = 1.4
				rationale = "blacksmith metallurgy priority"
			}
		}

		table[itemID] = db.TradeValuation{
			ItemID:         itemID,
			BaseValue:      val,
			NeedMultiplier: mult,
			MinAcceptRatio: 0.85,
			Rationale:      rationale,
		}
	}
	return table
}

// LookupItemUnitValue returns the effective unit value for an item ID using the persisted/default valuation table
// and dynamic scarcity multipliers from the agent's current inventory and vitals.
func LookupItemUnitValue(
	itemID string,
	table map[string]db.TradeValuation,
	inventory []*civquestv1.ItemStackProto,
	foodLevel int32,
	forIncomingOffer bool,
) float64 {
	norm := strings.ToLower(strings.TrimSpace(itemID))
	if !strings.Contains(norm, ":") {
		norm = "minecraft:" + norm
	}

	baseVal := 1.0
	needMult := 1.0
	if v, ok := table[norm]; ok {
		baseVal = v.BaseValue
		needMult = v.NeedMultiplier
	} else {
		switch {
		case strings.Contains(norm, "diamond"):
			baseVal = 25.0
		case strings.Contains(norm, "iron"):
			baseVal = 10.0
		case strings.Contains(norm, "stone_") || strings.Contains(norm, "wooden_"):
			baseVal = 4.0
		case strings.Contains(norm, "cooked_") || strings.Contains(norm, "bread"):
			baseVal = 3.0
		case strings.Contains(norm, "log"):
			baseVal = 1.0
		}
	}

	if !forIncomingOffer {
		return baseVal * needMult
	}

	// Dynamic scarcity multiplier when evaluating items offered TO the agent
	scarcityMult := 1.0
	isWeaponOrTool := strings.Contains(norm, "_sword") || strings.Contains(norm, "_axe") || strings.Contains(norm, "_pickaxe")
	isShield := norm == "minecraft:shield"
	isFood := strings.Contains(norm, "cooked_") || norm == "minecraft:bread" || norm == "minecraft:apple" ||
		norm == "minecraft:baked_potato" || norm == "minecraft:carrot" || norm == "minecraft:golden_apple"

	if isWeaponOrTool && CalculateGearMultiplier(inventory) <= 0.6 {
		// Unarmed agent strongly values their first weapon or tool
		scarcityMult = 2.0
	} else if isShield && !hasItemInInventory(inventory, "minecraft:shield") {
		scarcityMult = 1.8
	} else if isFood && foodLevel <= 14 {
		scarcityMult = 1.75
	}

	return baseVal * needMult * scarcityMult
}

func hasItemInInventory(inv []*civquestv1.ItemStackProto, targetID string) bool {
	for _, it := range inv {
		if it != nil && it.Count > 0 && strings.EqualFold(it.ItemId, targetID) {
			return true
		}
	}
	return false
}

// EvaluateTradeQuidProQuo executes a deterministic (<1ms, zero-token) System 1 trade evaluation
// using the agent's persisted valuation table, inventory scarcity, and player disposition.
func EvaluateTradeQuidProQuo(
	req *civquestv1.TradeRequest,
	frame *civquestv1.TelemetryFrame,
	profession string,
	dispositionScore float64,
	valuations map[string]db.TradeValuation,
) System1TradeDecision {
	if len(valuations) == 0 {
		valuations = DefaultTradeValuations(profession)
	}

	var inv []*civquestv1.ItemStackProto
	var foodLevel int32 = 20
	if frame != nil && frame.Status != nil {
		inv = frame.Status.Inventory
		foodLevel = frame.Status.FoodLevel
	}

	if req == nil || len(req.PlayerOfferedItems) == 0 {
		return System1TradeDecision{
			Decision:      "REJECT",
			AcceptDeal:    false,
			SpokenReply:   "You have not offered anything on the table yet, friend.",
			PrestigeDelta: 0.0,
			MemorySummary: "Player opened trade without offering items.",
			TradeIntent: &civquestv1.RespondTradeIntent{
				AcceptDeal:     false,
				SpokenResponse: "You have not offered anything on the table yet, friend.",
			},
		}
	}

	playerName := strings.TrimSpace(req.PlayerName)
	if playerName == "" {
		playerName = "traveler"
	}

	// 1. Compute total incoming offered value
	var offeredVal float64
	var offeredDesc []string
	minRatio := 0.85
	for _, stack := range req.PlayerOfferedItems {
		if stack == nil || stack.Count <= 0 {
			continue
		}
		unitVal := LookupItemUnitValue(stack.ItemId, valuations, inv, foodLevel, true)
		offeredVal += unitVal * float64(stack.Count)
		shortName := strings.ReplaceAll(strings.TrimPrefix(strings.ToLower(stack.ItemId), "minecraft:"), "_", " ")
		offeredDesc = append(offeredDesc, fmt.Sprintf("%dx %s", stack.Count, shortName))
		if v, ok := valuations[strings.ToLower(stack.ItemId)]; ok && v.MinAcceptRatio > 0 {
			minRatio = v.MinAcceptRatio
		}
	}

	// Adjust required quid-pro-quo threshold by relationship disposition (-100 to +100)
	// Friendly players get up to 25% discount; disliked players need up to 40% premium.
	dispositionFactor := 1.0 - (dispositionScore / 250.0)
	requiredRatio := math.Max(0.55, math.Min(1.35, minRatio*dispositionFactor))

	proposalLower := strings.ToLower(strings.TrimSpace(req.SpokenProposal))
	isExplicitGift := strings.Contains(proposalLower, "gift") || strings.Contains(proposalLower, "free") ||
		strings.Contains(proposalLower, "for you") || strings.Contains(proposalLower, "keep it") ||
		strings.Contains(proposalLower, "take this")

	// 2. Select fair counter-offer from agent's surplus inventory (unless it's a gift)
	var counterOffer []*civquestv1.ItemStackProto
	var counterVal float64
	var counterDesc []string

	if !isExplicitGift {
		targetReturnVal := offeredVal / math.Max(0.75, requiredRatio)
		for _, stack := range inv {
			if stack == nil || stack.Count <= 0 {
				continue
			}
			normID := strings.ToLower(stack.ItemId)
			// Never trade away the agent's primary weapon, shield, or minimum food reserve
			if (strings.Contains(normID, "_sword") || normID == "minecraft:shield") && stack.Count == 1 {
				continue
			}
			unitVal := LookupItemUnitValue(normID, valuations, inv, foodLevel, false)
			if unitVal <= 0 {
				unitVal = 0.5
			}
			maxGive := int(math.Floor((targetReturnVal - counterVal) / unitVal))
			if maxGive <= 0 {
				continue
			}
			if maxGive > int(stack.Count) {
				maxGive = int(stack.Count)
			}
			if maxGive > 16 {
				maxGive = 16
			}
			if maxGive > 0 {
				counterOffer = append(counterOffer, &civquestv1.ItemStackProto{
					Slot:   stack.Slot,
					ItemId: stack.ItemId,
					Count:  int32(maxGive),
				})
				counterVal += unitVal * float64(maxGive)
				shortName := strings.ReplaceAll(strings.TrimPrefix(normID, "minecraft:"), "_", " ")
				counterDesc = append(counterDesc, fmt.Sprintf("%dx %s", maxGive, shortName))
				if counterVal >= targetReturnVal*0.8 {
					break
				}
			}
		}
	}

	// 3. Quid-pro-quo threshold check
	if offeredVal <= 0.05 {
		reply := fmt.Sprintf("I have no use for %s right now, %s.", strings.Join(offeredDesc, ", "), playerName)
		return System1TradeDecision{
			Decision:       "REJECT",
			AcceptDeal:     false,
			OfferedValue:   offeredVal,
			RequestedValue: counterVal,
			RequiredRatio:  requiredRatio,
			SpokenReply:    reply,
			PrestigeDelta:  0.0,
			MemorySummary:  fmt.Sprintf("Declined low-value trade offer (%s) from %s.", strings.Join(offeredDesc, ", "), playerName),
			TradeIntent: &civquestv1.RespondTradeIntent{
				PlayerEntityId: req.PlayerEntityId,
				AcceptDeal:     false,
				SpokenResponse: reply,
			},
		}
	}

	var reply string
	var memSummary string
	if len(counterDesc) > 0 {
		reply = fmt.Sprintf("Fair trade, %s—I will take your %s in exchange for %s.",
			playerName, strings.Join(offeredDesc, ", "), strings.Join(counterDesc, ", "))
		memSummary = fmt.Sprintf("Completed fair barter with %s: received %s in exchange for %s.",
			playerName, strings.Join(offeredDesc, ", "), strings.Join(counterDesc, ", "))
	} else {
		reply = fmt.Sprintf("Thank you kindly for the %s, %s—I will put it to good use!",
			strings.Join(offeredDesc, ", "), playerName)
		memSummary = fmt.Sprintf("Accepted %s from %s to aid frontier survival.",
			strings.Join(offeredDesc, ", "), playerName)
	}

	prestigeDelta := math.Min(6.0, math.Max(1.5, math.Round((offeredVal/5.0)*10)/10))

	return System1TradeDecision{
		Decision:       "ACCEPT",
		AcceptDeal:     true,
		OfferedValue:   offeredVal,
		RequestedValue: counterVal,
		RequiredRatio:  requiredRatio,
		CounterOffer:   counterOffer,
		SpokenReply:    reply,
		PrestigeDelta:  prestigeDelta,
		MemorySummary:  memSummary,
		TradeIntent: &civquestv1.RespondTradeIntent{
			PlayerEntityId:    req.PlayerEntityId,
			AcceptDeal:        true,
			AgentCounterOffer: counterOffer,
			SpokenResponse:    reply,
		},
	}
}
