package system1

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// BiologicalNeedsSkill manages eating to resolve hunger and sleeping/waking routines.
type BiologicalNeedsSkill struct{}

// NewBiologicalNeedsSkill creates a BiologicalNeedsSkill instance.
func NewBiologicalNeedsSkill() *BiologicalNeedsSkill {
	return &BiologicalNeedsSkill{}
}

func (s *BiologicalNeedsSkill) Name() string {
	return "biological_needs"
}

func (s *BiologicalNeedsSkill) Category() string {
	return "vitals"
}

func (s *BiologicalNeedsSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	if facts.GetBool("vitals.can_eat") {
		return true
	}
	if facts.GetBool("vitals.is_sleeping") {
		return true
	}
	routine := facts.GetString("circadian.routine")
	if routine == string(RoutineSleep) || routine == string(RoutineDawnAwakening) {
		return true
	}
	if tod, ok := facts.GetInt("circadian.time_of_day"); ok {
		if tod >= 13000 && tod < 23500 {
			return true
		}
	}
	return false
}

func (s *BiologicalNeedsSkill) Interruptible() bool {
	return true
}

func (s *BiologicalNeedsSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, nil
	}
	intents := EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
	return intents, nil
}

func (s *BiologicalNeedsSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}

// CombatDefenseSkill executes deterministic fight-or-flight reflexes during emergencies.
type CombatDefenseSkill struct{}

// NewCombatDefenseSkill creates a CombatDefenseSkill instance.
func NewCombatDefenseSkill() *CombatDefenseSkill {
	return &CombatDefenseSkill{}
}

func (s *CombatDefenseSkill) Name() string {
	return "combat_defense"
}

func (s *CombatDefenseSkill) Category() string {
	return "combat"
}

func (s *CombatDefenseSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	return facts.GetBool("threat.hostiles_present") ||
		facts.GetBool("threat.should_panic") ||
		facts.GetBool("threat.damage_taken") ||
		facts.GetBool("threat.is_drowning") ||
		facts.GetString("threat.threatType") == "DROWNING" ||
		facts.GetBool("vitals.is_on_fire")
}

func (s *CombatDefenseSkill) Interruptible() bool {
	return false
}

func (s *CombatDefenseSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, nil
	}

	bravery := 0.5
	if ctx.Config != nil {
		bravery = ctx.Config.Traits.Bravery
	}

	hasShield := false
	hasWeapon := false
	var weaponSlot int32 = -1
	canEat := false

	if facts != nil {
		hasShield = facts.GetBool("threat.has_shield")
		hasWeapon = facts.GetBool("threat.has_weapon")
		if ws, ok := facts.GetInt("threat.weapon_slot"); ok {
			weaponSlot = int32(ws)
		}
		canEat = facts.GetBool("vitals.can_eat")
	} else {
		hasShield, hasWeapon, weaponSlot = ScanInventoryCombatItems(ctx.Frame.Status.Inventory)
		_, _, hasFood := FindOptimalFood(ctx.Frame.Status.Inventory)
		canEat = ctx.Frame.Status.FoodLevel < 20 && hasFood
	}

	rCtx := ReflexContext{
		AgentID:    ctx.AgentID,
		Bravery:    bravery,
		SafeNodes:  ctx.SafeNodes,
		Frame:      ctx.Frame,
		CanEat:     canEat,
		HasShield:  hasShield,
		HasWeapon:  hasWeapon,
		WeaponSlot: weaponSlot,
	}

	combatResult := EvaluateReflexes(rCtx)
	if !combatResult.ShouldAct {
		return nil, nil
	}
	return combatResult.Intents, nil
}

func (s *CombatDefenseSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}
