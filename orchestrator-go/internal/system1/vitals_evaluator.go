package system1

import (
	"fmt"
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// VitalsEvaluator evaluates agent health, food level, starvation, fire, sleep status,
// and inventory food options into the FactGraph.
type VitalsEvaluator struct{}

// NewVitalsEvaluator creates a new VitalsEvaluator instance.
func NewVitalsEvaluator() *VitalsEvaluator {
	return &VitalsEvaluator{}
}

func (e *VitalsEvaluator) Name() string {
	return "vitals"
}

func (e *VitalsEvaluator) Category() string {
	return "vitals"
}

func (e *VitalsEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	var health float64 = 0.0
	var maxHealth float64 = 20.0
	var isHealthCritical bool = false
	var foodLevel int = 0
	var isCriticalHunger bool = false
	var hasEdibleFood bool = false
	var optimalFoodID string = ""
	var optimalFoodSlot int = -1
	var isOnFire bool = false
	var isSleeping bool = false
	var canEat bool = false

	if ctx != nil && ctx.Frame != nil && ctx.Frame.Status != nil {
		status := ctx.Frame.Status
		health = float64(status.Health)
		if status.MaxHealth > 0 {
			maxHealth = float64(status.MaxHealth)
		}
		isHealthCritical = health <= LowHPThresholdRatio*maxHealth

		foodLevel = int(status.FoodLevel)
		slot, item, hasFood := FindOptimalFood(status.Inventory)
		hasEdibleFood = hasFood
		if hasFood && item != nil {
			optimalFoodID = item.ItemId
			optimalFoodSlot = int(slot)
		}

		isCriticalHunger = (foodLevel <= 6 && hasFood)
		isOnFire = status.IsOnFire
		isSleeping = status.IsSleeping
		canEat = (foodLevel < 20 && hasFood)
	}

	graph.SetFloat("vitals.health", "vitals", health, fmt.Sprintf("Health: %.1f", health))
	graph.SetFloat("vitals.max_health", "vitals", maxHealth, fmt.Sprintf("Max health: %.1f", maxHealth))
	graph.SetBool("vitals.is_health_critical", "vitals", isHealthCritical, fmt.Sprintf("Health critical (<= 35%%): %t", isHealthCritical))
	graph.SetInt("vitals.food_level", "vitals", foodLevel, fmt.Sprintf("Food level: %d", foodLevel))
	graph.SetBool("vitals.is_critical_hunger", "vitals", isCriticalHunger, fmt.Sprintf("Critical hunger (<= 6 and has food): %t", isCriticalHunger))
	graph.SetBool("vitals.has_edible_food", "vitals", hasEdibleFood, fmt.Sprintf("Edible food available: %t", hasEdibleFood))
	graph.SetString("vitals.optimal_food_id", "vitals", optimalFoodID, fmt.Sprintf("Optimal food ID: %s", optimalFoodID))
	graph.SetInt("vitals.optimal_food_slot", "vitals", optimalFoodSlot, fmt.Sprintf("Optimal food inventory slot: %d", optimalFoodSlot))
	graph.SetBool("vitals.is_on_fire", "vitals", isOnFire, fmt.Sprintf("Agent on fire: %t", isOnFire))
	graph.SetBool("vitals.is_sleeping", "vitals", isSleeping, fmt.Sprintf("Agent sleeping: %t", isSleeping))
	graph.SetBool("vitals.can_eat", "vitals", canEat, fmt.Sprintf("Can eat (food < 20 and has edible food): %t", canEat))

	return nil
}

// ThreatEvaluator evaluates hostile mobs, panic status, combat equipment, and damage events
// into the FactGraph.
type ThreatEvaluator struct{}

// NewThreatEvaluator creates a new ThreatEvaluator instance.
func NewThreatEvaluator() *ThreatEvaluator {
	return &ThreatEvaluator{}
}

func (e *ThreatEvaluator) Name() string {
	return "threat"
}

func (e *ThreatEvaluator) Category() string {
	return "threat"
}

func (e *ThreatEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	bravery := 0.5
	if ctx != nil && ctx.Config != nil {
		bravery = ctx.Config.Traits.Bravery
	}

	var frame *civquestv1.TelemetryFrame
	if ctx != nil {
		frame = ctx.Frame
	}

	_, conf, hostiles := AssessImmediateThreats(frame, bravery)
	hostilesPresent := len(hostiles) > 0
	threatCount := len(hostiles)

	damageTaken := false
	if frame != nil && frame.LastDamageTaken != nil {
		damageTaken = true
	}

	nearestHostileID := -1
	nearestHostileDist := 0.0
	minDist := math.MaxFloat64

	var selfPos *civquestv1.Vec3
	if frame != nil && frame.Status != nil {
		selfPos = frame.Status.Position
	}

	if frame != nil {
		for _, e := range frame.VisibleEntities {
			if e == nil {
				continue
			}
			dist := float64(e.Distance)
			if dist <= 0 && e.Position != nil && selfPos != nil {
				dist = math.Hypot(e.Position.X-selfPos.X, e.Position.Z-selfPos.Z)
			}
			if dist > MaxPerceptionRadius {
				continue
			}
			if IsHostile(e) {
				if dist < minDist {
					minDist = dist
					nearestHostileID = int(e.EntityId)
					nearestHostileDist = dist
				}
			}
		}
	}

	selfHP := 20.0
	maxHP := 20.0
	if frame != nil && frame.Status != nil {
		selfHP = float64(frame.Status.Health)
		if frame.Status.MaxHealth > 0 {
			maxHP = float64(frame.Status.MaxHealth)
		}
	}

	creeperNearby := false
	for _, h := range hostiles {
		if strings.Contains(strings.ToLower(h), "creeper") {
			creeperNearby = true
			break
		}
	}

	shouldPanic := ShouldPanic(selfHP, maxHP, conf, hostilesPresent, creeperNearby)

	var inventory []*civquestv1.ItemStackProto
	if frame != nil && frame.Status != nil {
		inventory = frame.Status.Inventory
	}
	hasShield, hasWeapon, weaponSlot := ScanInventoryCombatItems(inventory)

	// Check vitals facts (or compute fallback from frame) for threats_cleared calculation
	isCriticalHealth := false
	if f, ok := graph.Get("vitals.is_health_critical"); ok {
		isCriticalHealth = f.Value.Bool
	} else if frame != nil && frame.Status != nil {
		isCriticalHealth = selfHP <= LowHPThresholdRatio*maxHP
	}

	isOnFire := false
	if f, ok := graph.Get("vitals.is_on_fire"); ok {
		isOnFire = f.Value.Bool
	} else if frame != nil && frame.Status != nil {
		isOnFire = frame.Status.IsOnFire
	}

	isCriticalHunger := false
	if f, ok := graph.Get("vitals.is_critical_hunger"); ok {
		isCriticalHunger = f.Value.Bool
	} else if frame != nil && frame.Status != nil && frame.Status.FoodLevel <= 6 {
		if _, _, hasFood := FindOptimalFood(inventory); hasFood {
			isCriticalHunger = true
		}
	}

	isEmergency := hostilesPresent || damageTaken || isCriticalHealth || isOnFire || isCriticalHunger || shouldPanic
	threatsCleared := !isEmergency

	graph.SetBool("threat.hostiles_present", "threat", hostilesPresent, fmt.Sprintf("Hostile threats present: %t", hostilesPresent))
	graph.SetInt("threat.immediate_threat_count", "threat", threatCount, fmt.Sprintf("Immediate threat count: %d", threatCount))
	graph.SetFloat("threat.confidence", "threat", conf, fmt.Sprintf("Combat confidence: %.2f", conf))
	graph.SetBool("threat.should_panic", "threat", shouldPanic, fmt.Sprintf("Combat panic triggered: %t", shouldPanic))
	graph.SetInt("threat.nearest_hostile_id", "threat", nearestHostileID, fmt.Sprintf("Nearest hostile entity ID: %d", nearestHostileID))
	graph.SetFloat("threat.nearest_hostile_dist", "threat", nearestHostileDist, fmt.Sprintf("Nearest hostile distance: %.2f", nearestHostileDist))
	graph.SetBool("threat.is_emergency", "threat", isEmergency, fmt.Sprintf("Immediate threat emergency: %t", isEmergency))
	graph.SetBool("threat.threats_cleared", "threat", threatsCleared, fmt.Sprintf("All threats cleared: %t", threatsCleared))
	graph.SetBool("threat.has_shield", "threat", hasShield, fmt.Sprintf("Shield equipped or in inventory: %t", hasShield))
	graph.SetBool("threat.has_weapon", "threat", hasWeapon, fmt.Sprintf("Weapon available: %t", hasWeapon))
	graph.SetInt("threat.weapon_slot", "threat", int(weaponSlot), fmt.Sprintf("Weapon inventory slot: %d", weaponSlot))
	graph.SetBool("threat.damage_taken", "threat", damageTaken, fmt.Sprintf("Recent damage taken: %t", damageTaken))

	return nil
}
