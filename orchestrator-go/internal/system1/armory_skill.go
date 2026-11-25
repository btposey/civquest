package system1

import (
	"fmt"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// ArmoryMilitiaEvaluator evaluates population N, carried inventory, and granary stock
// to populate armory status facts in the FactGraph.
type ArmoryMilitiaEvaluator struct{}

// NewArmoryMilitiaEvaluator creates an ArmoryMilitiaEvaluator instance.
func NewArmoryMilitiaEvaluator() *ArmoryMilitiaEvaluator {
	return &ArmoryMilitiaEvaluator{}
}

func (e *ArmoryMilitiaEvaluator) Name() string {
	return "armory_militia"
}

func (e *ArmoryMilitiaEvaluator) Category() string {
	return "Armory"
}

func (e *ArmoryMilitiaEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	var inventory []*civquestv1.ItemStackProto
	if ctx != nil && ctx.Frame != nil && ctx.Frame.Status != nil {
		inventory = ctx.Frame.Status.Inventory
	}

	pop := 1
	if ctx != nil {
		pop = DetermineSettlementPopulation(ctx.CivicDB)
	}

	quotas := ComputeArmoryQuotas(pop)
	stock := ScanArmoryStock(inventory, ctx.CivicDB)
	isSufficient, deficitCategory := CheckArmorySufficiencyWithQuotas(inventory, ctx.CivicDB, quotas)

	mats := inspectArmoryMaterials(inventory)
	canCraftArrows := mats.featherCount >= 1 && mats.stickCount >= 1 && mats.flintCount >= 1
	canCraftShield := mats.planksCount >= 1 && mats.ironCount >= 1
	canCraftIronSword := mats.ironCount >= 1 && mats.stickCount >= 1
	canCraftStoneSword := mats.cobbleCount >= 1 && mats.stickCount >= 1
	canCraftBow := mats.stringCount >= 3 && mats.stickCount >= 3
	canCraftWeapons := canCraftShield || canCraftIronSword || canCraftStoneSword || canCraftBow
	canCraftArmor := mats.ironCount >= 1 || mats.leatherCount >= 1
	canCraftTNT := mats.gunpowderCount >= 1 && mats.sandCount >= 1

	needsWeapons := stock.TotalWeapons() < (quotas.Swords+quotas.Bows) || (stock.Swords < quotas.Swords && stock.Bows < quotas.Bows)
	hasCompleteArmor := stock.Helmets >= quotas.Helmets && stock.Chestplates >= quotas.Chestplates &&
		stock.Leggings >= quotas.Leggings && stock.Boots >= quotas.Boots
	needsArmor := !hasCompleteArmor && stock.TotalArmor() < 4*quotas.Population
	needsArrows := stock.Arrows < quotas.Arrows
	needsTNT := stock.TNT < quotas.TNT

	hasMaterials := (needsArrows && canCraftArrows) ||
		(needsWeapons && canCraftWeapons) ||
		(needsArmor && canCraftArmor) ||
		(needsTNT && canCraftTNT)

	// Populate FactGraph
	// 1. armory.sufficient (bool)
	var suffEvidence string
	if isSufficient {
		suffEvidence = fmt.Sprintf("Militia quotas satisfied for population %d", pop)
	} else {
		switch deficitCategory {
		case CategoryArrows:
			suffEvidence = fmt.Sprintf("Deficit (need %d arrows)", quotas.Arrows-stock.Arrows)
		case CategoryWeapons:
			suffEvidence = fmt.Sprintf("Deficit (need %d weapons)", (quotas.Swords+quotas.Bows)-stock.TotalWeapons())
		case CategoryArmor:
			suffEvidence = fmt.Sprintf("Deficit (need %d armor pieces)", 4*quotas.Population-stock.TotalArmor())
		case CategoryTNT:
			suffEvidence = fmt.Sprintf("Deficit (need %d TNT)", quotas.TNT-stock.TNT)
		default:
			suffEvidence = fmt.Sprintf("Deficit (need %s)", deficitCategory)
		}
	}
	graph.SetBool("armory.sufficient", "Armory", isSufficient, suffEvidence)

	// 2. armory.population (int)
	graph.SetInt("armory.population", "Armory", pop, fmt.Sprintf("Settlement population: %d", pop))

	// 3. armory.deficit_category (string)
	var defEvidence string
	if deficitCategory != "" {
		defEvidence = fmt.Sprintf("Primary armory deficit category: %s", deficitCategory)
	} else {
		defEvidence = "No deficit category"
	}
	graph.SetString("armory.deficit_category", "Armory", deficitCategory, defEvidence)

	// 4. armory.has_materials (bool)
	var matEvidence string
	if hasMaterials {
		matEvidence = "Raw materials available for armory crafting"
	} else {
		matEvidence = "Insufficient raw materials for armory crafting"
	}
	graph.SetBool("armory.has_materials", "Armory", hasMaterials, matEvidence)

	return nil
}

// ArmoryCraftingSkill produces crafting intents to satisfy settlement armory quotas.
type ArmoryCraftingSkill struct{}

// NewArmoryCraftingSkill creates an ArmoryCraftingSkill instance.
func NewArmoryCraftingSkill() *ArmoryCraftingSkill {
	return &ArmoryCraftingSkill{}
}

func (s *ArmoryCraftingSkill) Name() string {
	return "armory_crafting"
}

func (s *ArmoryCraftingSkill) Category() string {
	return "Armory"
}

func (s *ArmoryCraftingSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	return !facts.GetBool("armory.sufficient") && facts.GetBool("armory.has_materials")
}

func (s *ArmoryCraftingSkill) Interruptible() bool {
	return true
}

func (s *ArmoryCraftingSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil {
		return nil, nil
	}
	intent := EvaluateArmoryMaintenance(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.CivicDB)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}

// OnComplete does not write facts: armory.sufficient is derived by the armory evaluator each tick.
func (s *ArmoryCraftingSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}
