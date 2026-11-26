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
	var needsForaging bool = false

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
		needsForaging = (foodLevel <= 12 && !hasFood)
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
	graph.SetBool("vitals.needs_foraging", "vitals", needsForaging, fmt.Sprintf("Hungry (<= 12) without edible food: %t", needsForaging))

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

func isDrowningDamage(frame *civquestv1.TelemetryFrame) bool {
	if frame == nil || frame.LastDamageTaken == nil {
		return false
	}
	if frame.LastDamageTaken.SourceType == civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_DROWN {
		return true
	}
	if frame.Status != nil && frame.Status.IsInWater &&
		frame.LastDamageTaken.SourceType == civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_UNSPECIFIED &&
		frame.LastDamageTaken.AttackerEntityId == 0 &&
		frame.LastDamageTaken.AttackerName == "" {
		return true
	}
	return false
}

// IsImmediateHostileThreat returns true if visible entity e represents an immediate hostile
// threat within MaxPerceptionRadius (16m), matching ThreatEvaluator daytime/nighttime criteria.
func IsImmediateHostileThreat(frame *civquestv1.TelemetryFrame, e *civquestv1.VisibleEntity) bool {
	if frame == nil || frame.Status == nil || e == nil || !IsHostile(e) {
		return false
	}
	dist := float64(e.Distance)
	if dist <= 0 && e.Position != nil && frame.Status.Position != nil {
		dist = math.Hypot(e.Position.X-frame.Status.Position.X, e.Position.Z-frame.Status.Position.Z)
	}
	if dist > MaxPerceptionRadius {
		return false
	}
	selfHP := float64(frame.Status.Health)
	maxHP := float64(frame.Status.MaxHealth)
	if maxHP <= 0 {
		maxHP = 20.0
	}
	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	isDuskOrNight := tod >= 11000 && tod < 23500
	eType := strings.ToLower(e.EntityType)
	isCreeper := strings.Contains(eType, "creeper")
	isRanged := strings.Contains(eType, "skeleton") || strings.Contains(eType, "stray") || strings.Contains(eType, "pillager")
	return isCreeper || isRanged || dist <= 8.0 || isDuskOrNight || (selfHP <= LowHPThresholdRatio*maxHP)
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

	selfHP := 20.0
	maxHP := 20.0
	if frame != nil && frame.Status != nil {
		selfHP = float64(frame.Status.Health)
		if frame.Status.MaxHealth > 0 {
			maxHP = float64(frame.Status.MaxHealth)
		}
	}

	_, conf, hostiles := AssessImmediateThreats(frame, bravery)

	isDrowning := isDrowningDamage(frame) || sustainedDrowning(frame)
	damageTaken := false
	if frame != nil && frame.LastDamageTaken != nil && !isDrowning {
		if !IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) {
			damageTaken = true
		}
	}

	threatCount := 0
	hostilesPresent := false
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
				threatCount++
				if IsImmediateHostileThreat(frame, e) {
					hostilesPresent = true
				}
				if dist < minDist {
					minDist = dist
					nearestHostileID = int(e.EntityId)
					nearestHostileDist = dist
				}
			}
		}
	}

	creeperNearby := false
	for _, h := range hostiles {
		if strings.Contains(strings.ToLower(h), "creeper") {
			creeperNearby = true
			break
		}
	}

	shouldPanic := ShouldPanic(selfHP, maxHP, conf, hostilesPresent || damageTaken, creeperNearby)

	var inventory []*civquestv1.ItemStackProto
	if frame != nil && frame.Status != nil {
		inventory = frame.Status.Inventory
	}
	hasShield, hasWeapon, weaponSlot := ScanInventoryCombatItems(inventory)

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

	// Low HP with zero hostile threats or combat damage (e.g. starvation) is a recovery/foraging
	// concern, not a combat-flee EMERGENCY lock.
	isEmergency := hostilesPresent || damageTaken || isOnFire || isCriticalHunger || shouldPanic || isDrowning
	threatsCleared := !isEmergency

	threatType := "NONE"
	if isDrowning {
		threatType = "DROWNING"
	} else if hostilesPresent || damageTaken || (shouldPanic && (hostilesPresent || damageTaken || creeperNearby)) {
		threatType = "COMBAT"
	} else if isOnFire {
		threatType = "FIRE"
	}

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
	graph.SetBool("threat.is_drowning", "threat", isDrowning, fmt.Sprintf("Drowning emergency: %t", isDrowning))
	graph.SetString("threat.threatType", "threat", threatType, fmt.Sprintf("Threat type: %s", threatType))

	return nil
}
