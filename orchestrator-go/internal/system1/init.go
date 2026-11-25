package system1

// NewDefaultEngineRegistry constructs a fresh, isolated EngineRegistry with all
// built-in evaluators and skills registered.
func NewDefaultEngineRegistry() *EngineRegistry {
	reg := NewEngineRegistry()
	RegisterDefaultEvaluatorsAndSkills(reg)
	return reg
}

// RegisterDefaultEvaluatorsAndSkills registers standard built-in evaluators and skills into reg.
func RegisterDefaultEvaluatorsAndSkills(reg *EngineRegistry) {
	if reg == nil {
		return
	}
	reg.RegisterEvaluator(NewVitalsEvaluator())
	reg.RegisterEvaluator(NewThreatEvaluator())
	reg.RegisterEvaluator(NewArmoryMilitiaEvaluator())
	reg.RegisterEvaluator(NewCircadianEvaluator())
	reg.RegisterEvaluator(NewSurvivalKitEvaluator())
	reg.RegisterEvaluator(NewShelterEvaluator())
	reg.RegisterEvaluator(NewNavigationEvaluator())
	reg.RegisterEvaluator(NewInfrastructureEvaluator())
	reg.RegisterEvaluator(NewColonizeEvaluator())
	reg.RegisterEvaluator(NewBlueprintEvaluator())
	reg.RegisterEvaluator(NewDirectiveEvaluator())

	reg.RegisterSkill(NewCombatDefenseSkill())
	reg.RegisterSkill(NewBiologicalNeedsSkill())
	reg.RegisterSkill(NewArmoryCraftingSkill())
	reg.RegisterSkill(NewShelterConstructionSkill())
	reg.RegisterSkill(NewBootstrapCraftSkill())
	reg.RegisterSkill(NewBootstrapGatherSkill())
	reg.RegisterSkill(NewRoadPavingSkill())
	reg.RegisterSkill(NewCivicPatrolSkill())
	reg.RegisterSkill(NewFrontierScoutingSkill())
	reg.RegisterSkill(NewConstructionSkill())
	reg.RegisterSkill(NewForagingSkill())
}
