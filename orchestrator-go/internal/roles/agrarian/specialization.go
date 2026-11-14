package agrarian

import (
	"context"
	"fmt"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Agrarian emergent profession constants.
const (
	ProfessionVintner = "VINTNER"
	ProfessionRancher = "RANCHER"
	ProfessionChef    = "CHEF"
	ProfessionFarmer  = "FARMER"
)

// SpecializationContext encapsulates the sensory and inventory snapshot evaluated for career transitions.
type SpecializationContext struct {
	AgentID           string
	CurrentProfession string
	Inventory         []*civquestv1.ItemStackProto
	InterestingBlocks []*civquestv1.VisibleBlock
	NearbyEntities    []*civquestv1.VisibleEntity
	WorldTick         int64
}

// SpecializationResult encapsulates the outcome of career discovery evaluation.
type SpecializationResult struct {
	Eligible          bool    `json:"eligible"`
	CurrentProfession string  `json:"current_profession"`
	TargetProfession  string  `json:"target_profession"`
	Confidence        float64 `json:"confidence"`
	TriggerReason     string  `json:"trigger_reason"`
	KeyTriggerItem    string  `json:"key_trigger_item"`
	SuggestedHobby    string  `json:"suggested_hobby"`
	Diction           string  `json:"diction"`
}

// Item / block triggers for each profession.
var vintnerKeywords = map[string]float64{
	"vinery:red_grape":           1.0,
	"vinery:white_grape":         1.0,
	"vinery:red_grape_seeds":     0.9,
	"vinery:savanna_grape_seeds": 0.9,
	"vinery:grape_juice":         0.8,
	"vinery:white_grape_juice":   0.8,
	"vinery:wine_press":          1.0,
	"vinery:fermentation_barrel": 1.0,
	"vinery:wine_bottle":         0.7,
	"vinery:apple_wine":          0.8,
	"vinery:claret_wine":         0.8,
	"vinery:noir_wine":           0.8,
	"vinery:solaris_wine":        0.8,
	"vinery:grapevine_stem":      0.9,
	"vinery:grape_bush":          0.9,
	"vinery:wild_grapes":         1.0,
}

var rancherKeywords = map[string]float64{
	"minecraft:lead":          1.0,
	"minecraft:saddle":        0.9,
	"minecraft:shears":        0.7,
	"minecraft:hay_block":     0.8,
	"minecraft:wheat":         0.6,
	"minecraft:golden_carrot": 0.7,
	"minecraft:milk_bucket":   0.8,
	"minecraft:leather":       0.6,
	"minecraft:cow":           0.8,
	"minecraft:sheep":         0.8,
	"minecraft:pig":           0.8,
	"minecraft:horse":         0.9,
	"minecraft:chicken":       0.6,
}

var chefKeywords = map[string]float64{
	"farmersdelight:cooking_pot":       1.0,
	"farmersdelight:skillet":           0.9,
	"farmersdelight:cutting_board":     0.8,
	"farmersdelight:stove":             1.0,
	"farmersdelight:tomato":            0.8,
	"farmersdelight:onion":             0.8,
	"farmersdelight:cabbage":           0.8,
	"farmersdelight:rice":              0.8,
	"farmersdelight:wheat_dough":       0.6,
	"farmersdelight:raw_pasta":         0.7,
	"farmersdelight:vegetable_noodles": 0.8,
	"farmersdelight:beef_stew":         0.8,
	"brewinandchewin:cheese_wheel":     0.8,
	"minecraft:bowl":                  0.4,
	"minecraft:mushroom_stew":         0.7,
	"minecraft:rabbit_stew":           0.7,
	"minecraft:smoker":                0.6,
}

// IsTransitionableProfession returns true if the current profession is open to emergent agrarian specialization.
func IsTransitionableProfession(prof string) bool {
	norm := strings.ToUpper(strings.TrimSpace(prof))
	switch norm {
	case "", "UNASSIGNED", "SETTLER", "VILLAGER", "FARMER", "EXPLORER":
		return true
	default:
		return false
	}
}

// EvaluateSpecialization analyzes inventory, discovered crops/seeds, and nearby entities to determine
// if an agent should emergently specialize into VINTNER, RANCHER, or CHEF.
func EvaluateSpecialization(ctx SpecializationContext) (SpecializationResult, bool) {
	currentNorm := strings.ToUpper(strings.TrimSpace(ctx.CurrentProfession))
	if !IsTransitionableProfession(currentNorm) {
		return SpecializationResult{
			Eligible:          false,
			CurrentProfession: currentNorm,
		}, false
	}

	var vintnerScore, rancherScore, chefScore float64
	var vintnerTrigger, rancherTrigger, chefTrigger string

	// 1. Scan Inventory
	for _, stack := range ctx.Inventory {
		if stack == nil || stack.Count <= 0 {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(stack.ItemId))
		if weight, ok := vintnerKeywords[id]; ok {
			vintnerScore += weight * float64(stack.Count)
			if vintnerTrigger == "" {
				vintnerTrigger = id
			}
		}
		if weight, ok := rancherKeywords[id]; ok {
			rancherScore += weight * float64(stack.Count)
			if rancherTrigger == "" {
				rancherTrigger = id
			}
		}
		if weight, ok := chefKeywords[id]; ok {
			chefScore += weight * float64(stack.Count)
			if chefTrigger == "" {
				chefTrigger = id
			}
		}
	}

	// 2. Scan Interesting Blocks (wild plants, workstations)
	for _, block := range ctx.InterestingBlocks {
		if block == nil {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(block.BlockId))
		for kw, weight := range vintnerKeywords {
			if strings.Contains(id, kw) || strings.Contains(id, "grape") || strings.Contains(id, "wine") {
				vintnerScore += weight
				if vintnerTrigger == "" {
					vintnerTrigger = id
				}
				break
			}
		}
		for kw, weight := range chefKeywords {
			if strings.Contains(id, kw) || strings.Contains(id, "cooking_pot") || strings.Contains(id, "stove") {
				chefScore += weight
				if chefTrigger == "" {
					chefTrigger = id
				}
				break
			}
		}
	}

	// 3. Scan Nearby Entities (livestock herds)
	for _, ent := range ctx.NearbyEntities {
		if ent == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(ent.DisplayName))
		typeStr := strings.ToLower(ent.EntityType)
		for kw, weight := range rancherKeywords {
			if strings.Contains(name, kw) || strings.Contains(typeStr, kw) {
				rancherScore += weight * 1.5
				if rancherTrigger == "" {
					rancherTrigger = kw
				}
				break
			}
		}
	}

	// Determine dominant calling
	type candidate struct {
		prof    string
		score   float64
		trigger string
		hobby   string
		diction string
	}

	candidates := []candidate{
		{
			prof:    ProfessionVintner,
			score:   vintnerScore,
			trigger: vintnerTrigger,
			hobby:   "VITICULTURIST",
			diction: "refined viticultural speech seasoned with grape varieties, fermentation, and vineyard lore",
		},
		{
			prof:    ProfessionRancher,
			score:   rancherScore,
			trigger: rancherTrigger,
			hobby:   "ANIMAL_BREEDER",
			diction: "steady livestock vernacular referencing herds, pastures, breeding, and husbandry",
		},
		{
			prof:    ProfessionChef,
			score:   chefScore,
			trigger: chefTrigger,
			hobby:   "CULINARY_ARTIST",
			diction: "aromatic culinary phrasing celebrating hearty stews, spices, savory roasts, and hearthcraft",
		},
	}

	var best candidate
	for _, c := range candidates {
		if c.score > best.score {
			best = c
		}
	}

	// Need a threshold score to trigger emergent transition
	const minThreshold = 0.5
	if best.score < minThreshold {
		return SpecializationResult{
			Eligible:          false,
			CurrentProfession: currentNorm,
		}, false
	}

	// Confidence bounded to [0.5, 0.99]
	confidence := 0.5 + (best.score/(best.score+5.0))*0.49

	return SpecializationResult{
		Eligible:          true,
		CurrentProfession: currentNorm,
		TargetProfession:  best.prof,
		Confidence:        confidence,
		TriggerReason:     fmt.Sprintf("Discovered and gathered %s, revealing aptitude for %s", best.trigger, best.prof),
		KeyTriggerItem:    best.trigger,
		SuggestedHobby:    best.hobby,
		Diction:           best.diction,
	}, true
}

// ApplySpecialization modifies the agent's identity configuration with the new emergent career.
func ApplySpecialization(identity *xdg.AgentIdentityConfig, res SpecializationResult) error {
	if identity == nil {
		return fmt.Errorf("identity config cannot be nil")
	}
	if !res.Eligible || res.TargetProfession == "" {
		return fmt.Errorf("specialization result is not eligible for transition")
	}

	identity.Profession = res.TargetProfession
	if res.SuggestedHobby != "" {
		identity.SecondaryHobby = res.SuggestedHobby
	}
	if res.Diction != "" {
		identity.Voice.Diction = res.Diction
	}
	return nil
}

// RecordSpecializationMemory writes a high-saliency episodic memory of the vocational epiphany to the agent's database.
func RecordSpecializationMemory(ctx context.Context, agentDB *db.AgentDB, agentID string, res SpecializationResult, worldTick int64) error {
	if agentDB == nil {
		return nil
	}
	if !res.Eligible {
		return nil
	}

	summary := fmt.Sprintf(
		"Experienced a vocational calling upon discovering %s: transitioned from %s to %s.",
		res.KeyTriggerItem, res.CurrentProfession, res.TargetProfession,
	)

	mem := db.EpisodicMemory{
		WorldTick:      worldTick,
		EventCategory:  "DISCOVERY",
		SaliencyScore:  0.85,
		ActorsJSON:     fmt.Sprintf(`["%s"]`, agentID),
		LocationCoords: "settlement_homestead",
		SummaryText:    summary,
		Chronicled:     false,
	}

	if _, err := agentDB.InsertEpisodicMemory(ctx, mem, nil); err != nil {
		return fmt.Errorf("inserting episodic memory: %w", err)
	}

	// Also record skill progression
	_ = agentDB.UpsertSkill(ctx, db.Skill{
		SkillName: res.TargetProfession,
		Category:  "PRIMARY",
		Level:     1,
		XP:        100,
	})

	return nil
}
