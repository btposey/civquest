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
	DefaultRegistry.RegisterSkill(NewCombatDefenseSkill())
	DefaultRegistry.RegisterSkill(NewBiologicalNeedsSkill())
	DefaultRegistry.RegisterSkill(NewArmoryCraftingSkill())
}
