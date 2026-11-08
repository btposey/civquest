package system1

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Threat weight constants as specified in doc/ai/04-system1-utility-engine.md.
const (
	ThreatZombie   = 15.0
	ThreatSkeleton = 20.0
	ThreatCreeper  = 35.0
	ThreatPillager = 25.0
	ThreatWarden   = 500.0
	ThreatDefault  = 15.0

	MaxPerceptionRadius = 16.0
	CreeperPanicRadius  = 5.0
	LowHPThresholdRatio = 0.35
	DefaultFleeDistance = 16.0
)

// MobThreat returns the deterministic threat constant for a given Minecraft entity type.
func MobThreat(entityType string) float64 {
	t := strings.ToLower(entityType)
	if idx := strings.Index(t, ":"); idx != -1 {
		t = t[idx+1:]
	}
	switch {
	case strings.Contains(t, "warden"):
		return ThreatWarden
	case strings.Contains(t, "creeper"):
		return ThreatCreeper
	case strings.Contains(t, "pillager") || strings.Contains(t, "vindicator") ||
		strings.Contains(t, "evoker") || strings.Contains(t, "ravager") || strings.Contains(t, "witch"):
		return ThreatPillager
	case strings.Contains(t, "skeleton") || strings.Contains(t, "stray") || strings.Contains(t, "wither_skeleton"):
		return ThreatSkeleton
	case strings.Contains(t, "zombie") || strings.Contains(t, "husk") || strings.Contains(t, "drowned") ||
		strings.Contains(t, "zombified_piglin") || strings.Contains(t, "zombie_villager"):
		return ThreatZombie
	default:
		return ThreatDefault
	}
}

// IsHostile determines whether a visible entity represents a hostile threat.
func IsHostile(entity *civquestv1.VisibleEntity) bool {
	if entity == nil {
		return false
	}
	if entity.IsHostile {
		return true
	}
	t := strings.ToLower(entity.EntityType)
	if idx := strings.Index(t, ":"); idx != -1 {
		t = t[idx+1:]
	}
	switch {
	case strings.Contains(t, "creeper"),
		strings.Contains(t, "skeleton"),
		strings.Contains(t, "stray"),
		strings.Contains(t, "wither_skeleton"),
		strings.Contains(t, "zombie"),
		strings.Contains(t, "husk"),
		strings.Contains(t, "drowned"),
		strings.Contains(t, "pillager"),
		strings.Contains(t, "vindicator"),
		strings.Contains(t, "evoker"),
		strings.Contains(t, "ravager"),
		strings.Contains(t, "witch"),
		strings.Contains(t, "warden"),
		strings.Contains(t, "spider"),
		strings.Contains(t, "cave_spider"),
		strings.Contains(t, "enderman"),
		strings.Contains(t, "blaze"),
		strings.Contains(t, "ghast"),
		strings.Contains(t, "phantom"),
		strings.Contains(t, "slime"),
		strings.Contains(t, "magma_cube"),
		strings.Contains(t, "silverfish"),
		strings.Contains(t, "endermite"),
		strings.Contains(t, "hoglin"),
		strings.Contains(t, "piglin_brute"):
		return true
	default:
		return false
	}
}

// IsAlly determines whether a visible entity is a friendly player, settler, or tamed companion.
func IsAlly(entity *civquestv1.VisibleEntity) bool {
	if entity == nil || IsHostile(entity) {
		return false
	}
	t := strings.ToLower(entity.EntityType)
	if idx := strings.Index(t, ":"); idx != -1 {
		t = t[idx+1:]
	}
	if strings.Contains(t, "player") || entity.IsTamed ||
		strings.Contains(t, "iron_golem") || strings.Contains(t, "snow_golem") {
		return true
	}
	return false
}

// CalculateGearMultiplier evaluates the equipped and inventory armor, weapons, and shield,
// returning a deterministic gear multiplier M_gear bounded within [0.5, 2.5].
func CalculateGearMultiplier(inventory []*civquestv1.ItemStackProto) float64 {
	var bestHelmet, bestChestplate, bestLeggings, bestBoots float64
	var bestWeapon float64
	var hasShield float64

	for _, item := range inventory {
		if item == nil || item.Count <= 0 {
			continue
		}
		id := strings.ToLower(item.ItemId)
		if idx := strings.Index(id, ":"); idx != -1 {
			id = id[idx+1:]
		}

		if strings.Contains(id, "shield") {
			hasShield = 0.5
		}

		if wScore := weaponScore(id); wScore > bestWeapon {
			bestWeapon = wScore
		}

		if aScore, slotType := armorScore(id); aScore > 0 {
			switch slotType {
			case "helmet":
				if aScore > bestHelmet {
					bestHelmet = aScore
				}
			case "chestplate":
				if aScore > bestChestplate {
					bestChestplate = aScore
				}
			case "leggings":
				if aScore > bestLeggings {
					bestLeggings = aScore
				}
			case "boots":
				if aScore > bestBoots {
					bestBoots = aScore
				}
			}
		}
	}

	total := 0.5 + bestWeapon + hasShield + bestHelmet + bestChestplate + bestLeggings + bestBoots
	if total < 0.5 {
		return 0.5
	}
	if total > 2.5 {
		return 2.5
	}
	return total
}

func weaponScore(id string) float64 {
	switch {
	case strings.Contains(id, "netherite_sword") || strings.Contains(id, "netherite_axe"):
		return 1.0
	case strings.Contains(id, "diamond_sword") || strings.Contains(id, "diamond_axe"):
		return 0.85
	case strings.Contains(id, "iron_sword") || strings.Contains(id, "iron_axe") || strings.Contains(id, "trident"):
		return 0.65
	case strings.Contains(id, "stone_sword") || strings.Contains(id, "stone_axe") ||
		strings.Contains(id, "golden_sword") || strings.Contains(id, "golden_axe") ||
		strings.Contains(id, "bow") || strings.Contains(id, "crossbow"):
		return 0.45
	case strings.Contains(id, "wooden_sword") || strings.Contains(id, "wooden_axe"):
		return 0.30
	default:
		return 0.0
	}
}

func armorScore(id string) (score float64, slotType string) {
	switch {
	case strings.Contains(id, "helmet"):
		slotType = "helmet"
	case strings.Contains(id, "chestplate"):
		slotType = "chestplate"
	case strings.Contains(id, "leggings"):
		slotType = "leggings"
	case strings.Contains(id, "boots"):
		slotType = "boots"
	default:
		return 0, ""
	}

	switch {
	case strings.Contains(id, "netherite"):
		score = 0.25
	case strings.Contains(id, "diamond"):
		score = 0.20
	case strings.Contains(id, "iron") || strings.Contains(id, "turtle"):
		score = 0.15
	case strings.Contains(id, "chainmail"):
		score = 0.10
	case strings.Contains(id, "golden") || strings.Contains(id, "leather"):
		score = 0.05
	default:
		score = 0.05
	}
	return score, slotType
}

// CalculateConfidence evaluates the fight-or-flight confidence score:
// Confidence = ((HP_self + sum(HP_allies)) / sum(Threat_hostiles)) * M_gear * (0.5 + Bravery_trait).
func CalculateConfidence(selfHP float64, alliesHP float64, threatHostiles float64, gearMultiplier float64, braveryTrait float64) float64 {
	if threatHostiles <= 0 {
		return 999.0
	}
	if braveryTrait < 0.0 {
		braveryTrait = 0.0
	} else if braveryTrait > 1.0 {
		braveryTrait = 1.0
	}
	if gearMultiplier < 0.5 {
		gearMultiplier = 0.5
	} else if gearMultiplier > 2.5 {
		gearMultiplier = 2.5
	}
	hpTotal := selfHP + alliesHP
	if hpTotal < 0 {
		hpTotal = 0
	}
	return (hpTotal / threatHostiles) * gearMultiplier * (0.5 + braveryTrait)
}

// ShouldPanic checks the SURVIVAL_PANIC override rules:
// 1. HP_self <= 0.35 * HP_max
// 2. Confidence < 1.0 (when hostiles present within R=16)
// 3. Creeper within 5 blocks has fuse_ticks > 0.
func ShouldPanic(selfHP float64, maxHP float64, confidence float64, hostilesPresent bool, creeperHissingNearby bool) bool {
	if maxHP <= 0 {
		maxHP = 20.0
	}
	if selfHP <= LowHPThresholdRatio*maxHP {
		return true
	}
	if creeperHissingNearby {
		return true
	}
	if hostilesPresent && confidence < 1.0 {
		return true
	}
	return false
}

// SelectPriorityHostile selects the most critical hostile threat:
// Creeper with fuse_ticks > 0 has top priority, otherwise the hostile with the minimum distance.
func SelectPriorityHostile(hostiles []*civquestv1.VisibleEntity) *civquestv1.VisibleEntity {
	if len(hostiles) == 0 {
		return nil
	}
	for _, h := range hostiles {
		if h == nil {
			continue
		}
		t := strings.ToLower(h.EntityType)
		if strings.Contains(t, "creeper") && h.FuseTicks > 0 {
			return h
		}
	}

	var closest *civquestv1.VisibleEntity
	minDist := float32(math.MaxFloat32)
	for _, h := range hostiles {
		if h == nil {
			continue
		}
		if closest == nil || h.Distance < minDist {
			closest = h
			minDist = h.Distance
		}
	}
	return closest
}

// IsRangedAttacker checks whether the target hostile or last damage event indicates a ranged attacker.
func IsRangedAttacker(entity *civquestv1.VisibleEntity, lastDamage *civquestv1.DamageEvent) bool {
	if lastDamage != nil && lastDamage.SourceType == civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_ARROW {
		return true
	}
	if entity == nil {
		return false
	}
	held := strings.ToLower(entity.HeldItemId)
	if strings.Contains(held, "bow") || strings.Contains(held, "crossbow") {
		return true
	}
	t := strings.ToLower(entity.EntityType)
	if strings.Contains(t, "skeleton") || strings.Contains(t, "stray") || strings.Contains(t, "pillager") {
		return true
	}
	return false
}

// FoodScore ranks food items by saturation, restorative value, and safety.
func FoodScore(itemID string) int {
	id := strings.ToLower(itemID)
	if idx := strings.Index(id, ":"); idx != -1 {
		id = id[idx+1:]
	}
	switch {
	case id == "enchanted_golden_apple":
		return 100
	case id == "golden_apple":
		return 95
	case id == "golden_carrot":
		return 90
	case id == "cooked_porkchop" || id == "cooked_beef" || id == "steak":
		return 80
	case id == "cooked_mutton" || id == "cooked_salmon":
		return 70
	case id == "cooked_chicken" || id == "cooked_rabbit" || id == "rabbit_stew":
		return 65
	case strings.Contains(id, "soup") || strings.Contains(id, "stew") || strings.Contains(id, "roast") ||
		strings.Contains(id, "pasta") || strings.Contains(id, "burger") || strings.Contains(id, "sandwich"):
		return 75
	case id == "bread" || id == "baked_potato" || id == "cooked_cod" || id == "pumpkin_pie":
		return 50
	case id == "apple" || id == "carrot" || id == "sweet_berries" || id == "glow_berries" || id == "melon_slice" || id == "cookie":
		return 30
	case id == "raw_beef" || id == "raw_porkchop" || id == "raw_chicken" || id == "raw_mutton" || id == "raw_salmon" || id == "raw_cod":
		return 15
	default:
		return 0
	}
}

// FindBestFoodItem locates the slot of the highest scoring food item in the agent's inventory.
func FindBestFoodItem(inventory []*civquestv1.ItemStackProto) (slot int32, found bool) {
	bestScore := 0
	bestSlot := int32(-1)
	for _, item := range inventory {
		if item == nil || item.Count <= 0 {
			continue
		}
		score := FoodScore(item.ItemId)
		if score > bestScore {
			bestScore = score
			bestSlot = item.Slot
		}
	}
	if bestSlot >= 0 {
		return bestSlot, true
	}
	return -1, false
}

// ComputeFleeTarget computes a 3D escape position oriented away from the centroid of hostiles,
// prioritizing safe nodes from map_graph.db when available.
func ComputeFleeTarget(selfPos *civquestv1.Vec3, hostiles []*civquestv1.VisibleEntity, fleeDist float64, safeNodes []db.MapNode) *civquestv1.Vec3 {
	if selfPos == nil {
		return &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
	}
	if fleeDist <= 0 {
		fleeDist = DefaultFleeDistance
	}

	var sumX, sumZ float64
	var count float64
	for _, h := range hostiles {
		if h == nil || h.Position == nil {
			continue
		}
		sumX += h.Position.X
		sumZ += h.Position.Z
		count++
	}

	if count == 0 {
		return &civquestv1.Vec3{
			X: selfPos.X - fleeDist,
			Y: selfPos.Y,
			Z: selfPos.Z,
		}
	}

	centroidX := sumX / count
	centroidZ := sumZ / count

	// Evaluate safe nodes in map_graph.db if provided
	if len(safeNodes) > 0 {
		curDistToCentroid := math.Hypot(selfPos.X-centroidX, selfPos.Z-centroidZ)
		var bestNode *db.MapNode
		bestScore := -1.0

		for i := range safeNodes {
			node := &safeNodes[i]
			nodeDistToCentroid := math.Hypot(node.X-centroidX, node.Z-centroidZ)
			if nodeDistToCentroid > curDistToCentroid {
				distFromSelf := math.Hypot(node.X-selfPos.X, node.Z-selfPos.Z)
				if distFromSelf <= 48.0 {
					combinedScore := node.SafetyScore*1.5 + (nodeDistToCentroid / (distFromSelf + 1.0))
					if combinedScore > bestScore {
						bestScore = combinedScore
						bestNode = node
					}
				}
			}
		}

		if bestNode != nil {
			return &civquestv1.Vec3{
				X: bestNode.X,
				Y: bestNode.Y,
				Z: bestNode.Z,
			}
		}
	}

	// Centroid avoidance vector
	dx := selfPos.X - centroidX
	dz := selfPos.Z - centroidZ
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		dx = -1.0
		dz = 0.0
		dist = 1.0
	}
	ux := dx / dist
	uz := dz / dist

	return &civquestv1.Vec3{
		X: selfPos.X + ux*fleeDist,
		Y: selfPos.Y,
		Z: selfPos.Z + uz*fleeDist,
	}
}

// ReflexContext encapsulates sensory and environmental state for System 1 evaluation.
type ReflexContext struct {
	AgentID   string
	Bravery   float64
	SafeNodes []db.MapNode
	Frame     *civquestv1.TelemetryFrame
	CanEat    bool
}

// ReflexResult encapsulates the deterministic output and queued intents from System 1 evaluation.
type ReflexResult struct {
	Evaluated       bool
	ShouldAct       bool
	IsPanic         bool
	Confidence      float64
	GearMultiplier  float64
	SelfHP          float64
	MaxHP           float64
	HostilesCount   int
	AlliesCount     int
	PriorityHostile *civquestv1.VisibleEntity
	FleeTarget      *civquestv1.Vec3
	EatenFoodSlot   int32
	Intents         []*civquestv1.AgentIntent
}

// EvaluateReflexes executes the deterministic System 1 Fight-or-Flight evaluation in <2ms on host CPU.
// Emits AttackIntent, NavigateIntent, and UseItemIntent without invoking LLM tokens.
func EvaluateReflexes(ctx ReflexContext) ReflexResult {
	res := ReflexResult{
		EatenFoodSlot: -1,
	}
	if ctx.Frame == nil || ctx.Frame.Status == nil {
		return res
	}

	status := ctx.Frame.Status
	selfHP := float64(status.Health)
	maxHP := float64(status.MaxHealth)
	if maxHP <= 0 {
		maxHP = 20.0
	}
	foodLevel := status.FoodLevel
	inventory := status.Inventory
	selfPos := status.Position
	if selfPos == nil {
		selfPos = &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
	}

	res.Evaluated = true
	res.SelfHP = selfHP
	res.MaxHP = maxHP

	var hostiles []*civquestv1.VisibleEntity
	var allies []*civquestv1.VisibleEntity
	var sumHostileThreat float64
	var sumAlliesHP float64
	var creeperHissingNearby bool

	for _, e := range ctx.Frame.VisibleEntities {
		if e == nil {
			continue
		}
		dist := float64(e.Distance)
		if dist <= 0 && e.Position != nil {
			dist = math.Hypot(e.Position.X-selfPos.X, e.Position.Z-selfPos.Z)
		}

		if dist > MaxPerceptionRadius {
			continue
		}

		if IsHostile(e) {
			hostiles = append(hostiles, e)
			threat := MobThreat(e.EntityType)
			sumHostileThreat += threat

			tLower := strings.ToLower(e.EntityType)
			if strings.Contains(tLower, "creeper") && e.FuseTicks > 0 && dist <= CreeperPanicRadius {
				creeperHissingNearby = true
			}
		} else if IsAlly(e) {
			allies = append(allies, e)
			aHP := float64(e.Health)
			if aHP <= 0 {
				aHP = 20.0
			}
			sumAlliesHP += aHP
		}
	}

	// If no visible hostiles are present but agent took damage, account for unseen attacker
	if len(hostiles) == 0 && ctx.Frame.LastDamageTaken != nil {
		threat := ThreatDefault
		switch ctx.Frame.LastDamageTaken.SourceType {
		case civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_ARROW:
			threat = ThreatSkeleton
		case civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_EXPLOSION:
			threat = ThreatCreeper
		}
		sumHostileThreat += threat
	}

	res.HostilesCount = len(hostiles)
	res.AlliesCount = len(allies)

	gearMult := CalculateGearMultiplier(inventory)
	res.GearMultiplier = gearMult

	confidence := CalculateConfidence(selfHP, sumAlliesHP, sumHostileThreat, gearMult, ctx.Bravery)
	res.Confidence = confidence

	hasThreats := len(hostiles) > 0 || ctx.Frame.LastDamageTaken != nil
	isPanic := ShouldPanic(selfHP, maxHP, confidence, hasThreats, creeperHissingNearby)
	res.IsPanic = isPanic

	if isPanic {
		res.ShouldAct = true
		priorityHostile := SelectPriorityHostile(hostiles)
		res.PriorityHostile = priorityHostile

		targetID := int32(0)
		if priorityHostile != nil {
			targetID = priorityHostile.EntityId
		} else if ctx.Frame.LastDamageTaken != nil {
			targetID = ctx.Frame.LastDamageTaken.AttackerEntityId
		}

		fleeTarget := ComputeFleeTarget(selfPos, hostiles, DefaultFleeDistance, ctx.SafeNodes)
		res.FleeTarget = fleeTarget

		// 1. Raise shield and flee immediately
		attackIntent := &civquestv1.AgentIntent{
			AgentId: ctx.AgentID,
			Action: &civquestv1.AgentIntent_Attack{
				Attack: &civquestv1.AttackIntent{
					TargetEntityId:  targetID,
					RaiseShield:     true,
					FleeImmediately: true,
				},
			},
		}
		res.Intents = append(res.Intents, attackIntent)

		// 2. Navigate away from threat
		navIntent := &civquestv1.AgentIntent{
			AgentId: ctx.AgentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target:           fleeTarget,
					SpeedMultiplier:  1.25,
					Sprint:           true,
					ArrivalTolerance: 2,
				},
			},
		}
		res.Intents = append(res.Intents, navIntent)

		// 3. Eat food while fleeing if health or food is depleted
		if (foodLevel < 20 || selfHP < maxHP) && ctx.CanEat {
			if slot, found := FindBestFoodItem(inventory); found {
				res.EatenFoodSlot = slot
				useItemIntent := &civquestv1.AgentIntent{
					AgentId: ctx.AgentID,
					Action: &civquestv1.AgentIntent_UseItem{
						UseItem: &civquestv1.UseItemIntent{
							InventorySlot:      slot,
							IsEatingOrDrinking: true,
						},
					},
				}
				res.Intents = append(res.Intents, useItemIntent)
			}
		}
	} else if len(hostiles) > 0 {
		// Confident Stand (Confidence >= 1.0)
		res.ShouldAct = true
		priorityHostile := SelectPriorityHostile(hostiles)
		res.PriorityHostile = priorityHostile
		isRanged := IsRangedAttacker(priorityHostile, ctx.Frame.LastDamageTaken)

		attackIntent := &civquestv1.AgentIntent{
			AgentId: ctx.AgentID,
			Action: &civquestv1.AgentIntent_Attack{
				Attack: &civquestv1.AttackIntent{
					TargetEntityId:   priorityHostile.EntityId,
					RaiseShield:      isRanged,
					KeepDistanceKite: true,
					FleeImmediately:  false,
				},
			},
		}
		res.Intents = append(res.Intents, attackIntent)
	} else {
		// No immediate combat threat; check emergency hunger consumption
		if foodLevel <= 6 && ctx.CanEat {
			if slot, found := FindBestFoodItem(inventory); found {
				res.ShouldAct = true
				res.EatenFoodSlot = slot
				useItemIntent := &civquestv1.AgentIntent{
					AgentId: ctx.AgentID,
					Action: &civquestv1.AgentIntent_UseItem{
						UseItem: &civquestv1.UseItemIntent{
							InventorySlot:      slot,
							IsEatingOrDrinking: true,
						},
					},
				}
				res.Intents = append(res.Intents, useItemIntent)
			}
		}
	}

	return res
}
