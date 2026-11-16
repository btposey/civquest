package system1

func init() {
	RegisterDefaultEvaluatorsAndSkills()
}

// RegisterDefaultEvaluatorsAndSkills registers standard built-in evaluators and skills into DefaultRegistry.
func RegisterDefaultEvaluatorsAndSkills() {
	DefaultRegistry.RegisterEvaluator(NewVitalsEvaluator())
	DefaultRegistry.RegisterEvaluator(NewThreatEvaluator())
	DefaultRegistry.RegisterEvaluator(NewArmoryMilitiaEvaluator())
	DefaultRegistry.RegisterEvaluator(NewCircadianEvaluator())
	DefaultRegistry.RegisterEvaluator(NewSurvivalKitEvaluator())
	DefaultRegistry.RegisterEvaluator(NewShelterEvaluator())
	DefaultRegistry.RegisterEvaluator(NewNavigationEvaluator())
	DefaultRegistry.RegisterEvaluator(NewInfrastructureEvaluator())
	DefaultRegistry.RegisterEvaluator(NewColonizeEvaluator())
	DefaultRegistry.RegisterEvaluator(NewBlueprintEvaluator())

	DefaultRegistry.RegisterSkill(NewCombatDefenseSkill())
	DefaultRegistry.RegisterSkill(NewBiologicalNeedsSkill())
	DefaultRegistry.RegisterSkill(NewArmoryCraftingSkill())
	DefaultRegistry.RegisterSkill(NewShelterConstructionSkill())
	DefaultRegistry.RegisterSkill(NewBootstrapCraftSkill())
	DefaultRegistry.RegisterSkill(NewBootstrapGatherSkill())
	DefaultRegistry.RegisterSkill(NewRoadPavingSkill())
	DefaultRegistry.RegisterSkill(NewCivicPatrolSkill())
	DefaultRegistry.RegisterSkill(NewFrontierScoutingSkill())
	DefaultRegistry.RegisterSkill(NewConstructionSkill())
}
