package system1

import (
	"fmt"
	"math"
	"strings"
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// ForagingGoalText is the ImmediateGoal recorded when an agent is actively foraging or hunting for food.
	ForagingGoalText = "FORAGE_FOOD: Gather wild food or hunt livestock to relieve hunger"

	forageHungerThreshold      = 12
	forageBlockReach           = 4.5
	forageMeleeReach           = 3.5
	forageDropPickupReach      = 1.5
	forageActionCooldownTicks  = int64(10)
	forageExploreCooldownTicks = int64(20)
	forageExploreStepDist      = 24.0
)

type forageAgentMemory struct {
	lastActionTick       int64
	lastTargetKey        string
	lastExploreTick      int64
	exploreHeadingRad    float64
	exploreTarget        *civquestv1.Vec3
	visitedContainerPOIs map[string]int64
}

var (
	forageMu       sync.Mutex
	forageMemories = make(map[string]*forageAgentMemory)
)

func getForageMemory(agentID string) *forageAgentMemory {
	key := agentID
	if key == "" {
		key = "_default"
	}
	m, ok := forageMemories[key]
	if !ok {
		m = &forageAgentMemory{
			visitedContainerPOIs: make(map[string]int64),
		}
		forageMemories[key] = m
	}
	return m
}

func init() {
	DefaultRegistry.RegisterSkill(NewForagingSkill())
}

// ForagingSkill implements deterministic, zero-token food gathering and hunting
// when an agent is hungry (FoodLevel <= 12) and lacks edible food in inventory.
type ForagingSkill struct{}

// NewForagingSkill creates a ForagingSkill instance.
func NewForagingSkill() *ForagingSkill {
	return &ForagingSkill{}
}

func (s *ForagingSkill) Name() string {
	return "foraging"
}

func (s *ForagingSkill) Category() string {
	return "vitals"
}

// Preconditions returns true when the agent is hungry without edible food and
// no active combat/survival threat requires immediate combat defense.
func (s *ForagingSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	if facts.GetBool("threat.hostiles_present") ||
		facts.GetBool("threat.should_panic") ||
		facts.GetBool("threat.damage_taken") ||
		facts.GetBool("threat.is_drowning") ||
		facts.GetString("threat.threatType") == "DROWNING" ||
		facts.GetBool("vitals.is_on_fire") {
		return false
	}
	if facts.GetBool("vitals.is_sleeping") {
		return false
	}
	if facts.GetBool("vitals.has_edible_food") {
		return false
	}
	if facts.GetBool("vitals.needs_foraging") {
		return true
	}
	if fl, ok := facts.GetInt("vitals.food_level"); ok {
		return fl <= forageHungerThreshold
	}
	return false
}

func (s *ForagingSkill) Interruptible() bool {
	return true
}

func (s *ForagingSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, nil
	}
	intent := EvaluateForaging(ctx.AgentID, ctx.Frame, ctx.State)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}

func (s *ForagingSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}

func bareID(raw string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	if idx := strings.LastIndex(id, ":"); idx >= 0 {
		id = id[idx+1:]
	}
	return id
}

// isEdiblePlantBlock classifies visible plant/crop blocks into foraging tiers:
// tier 2 = prime wild/crop food block, tier 1 = fallback apple-bearing leaves, 0 = not food.
func isEdiblePlantBlock(b *civquestv1.VisibleBlock) int {
	if b == nil || b.Pos == nil {
		return 0
	}
	id := bareID(b.BlockId)
	stateStr := strings.ToLower(b.BlockStateString)

	switch id {
	case "sweet_berry_bush":
		// Age 0 or 1 yields no berries; age 2/3 or unspecified state is harvestable.
		if strings.Contains(stateStr, "age=0") || strings.Contains(stateStr, "age=1") {
			return 0
		}
		return 2
	case "melon", "pumpkin", "hay_block", "brown_mushroom", "red_mushroom",
		"carrots", "potatoes", "beetroots", "wheat",
		"wild_cabbages", "wild_tomatoes", "wild_onions", "wild_carrots",
		"wild_potatoes", "wild_beetroots", "cabbages", "tomatoes", "onions", "rice":
		return 2
	case "oak_leaves", "dark_oak_leaves":
		return 1
	default:
		return 0
	}
}

func isCropMatureHint(b *civquestv1.VisibleBlock) bool {
	if b == nil {
		return false
	}
	stateStr := strings.ToLower(b.BlockStateString)
	if stateStr == "" {
		return true
	}
	id := bareID(b.BlockId)
	switch id {
	case "sweet_berry_bush", "beetroots":
		return strings.Contains(stateStr, "age=3") || strings.Contains(stateStr, "age=2")
	case "wheat", "carrots", "potatoes":
		return strings.Contains(stateStr, "age=7")
	default:
		return true
	}
}

func isPassiveFoodAnimal(e *civquestv1.VisibleEntity) bool {
	if e == nil || e.IsTamed || IsHostile(e) || IsAlly(e) {
		return false
	}
	switch bareID(e.EntityType) {
	case "cow", "mooshroom", "pig", "sheep", "chicken", "rabbit", "salmon", "cod":
		return true
	default:
		return false
	}
}

func isFoodDropEntity(e *civquestv1.VisibleEntity) bool {
	if !isDroppedItemEntity(e) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(e.DisplayName))
	if name == "" {
		return false
	}
	norm := strings.ReplaceAll(name, " ", "_")
	if CalculateFoodScore(norm) > 0 {
		return true
	}
	for _, kw := range []string{
		"beef", "steak", "porkchop", "mutton", "chicken", "rabbit",
		"salmon", "cod", "berries", "berry", "apple", "carrot",
		"potato", "beetroot", "melon", "bread", "mushroom", "wheat",
		"tomato", "cabbage", "onion",
	} {
		if strings.Contains(norm, kw) {
			return true
		}
	}
	return false
}

func entityDistance(pos *civquestv1.Vec3, e *civquestv1.VisibleEntity) float64 {
	if e == nil {
		return math.MaxFloat64
	}
	if e.Position != nil && pos != nil {
		dx := e.Position.X - pos.X
		dy := e.Position.Y - pos.Y
		dz := e.Position.Z - pos.Z
		return math.Sqrt(dx*dx + dy*dy + dz*dz)
	}
	if e.Distance > 0 {
		return float64(e.Distance)
	}
	return math.MaxFloat64
}

func blockDistance(pos *civquestv1.Vec3, b *civquestv1.VisibleBlock) float64 {
	if pos == nil || b == nil || b.Pos == nil {
		return math.MaxFloat64
	}
	dx := float64(b.Pos.X) + 0.5 - pos.X
	dy := float64(b.Pos.Y) + 0.5 - pos.Y
	dz := float64(b.Pos.Z) + 0.5 - pos.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func setForagingGoal(state *AgentState) {
	if state == nil {
		return
	}
	g := state.GetGoals()
	g.ImmediateGoal = ForagingGoalText
	state.SetGoals(g)
}

// EvaluateForaging deterministically selects a food acquisition intent (harvest edible plants,
// hunt passive livestock, pick up dropped food, inspect known crop/container POIs, or scout
// outward) when FoodLevel <= 12 and no edible food is in inventory.
func EvaluateForaging(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState) *civquestv1.AgentIntent {
	return evaluateForagingInternal(agentID, frame, state, false)
}

// EvaluateForagingDirective executes deterministic food gathering/hunting when directed by System 2
// even if FoodLevel is above the reactive hunger threshold, while still yielding to sleep and threats.
func EvaluateForagingDirective(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState) *civquestv1.AgentIntent {
	return evaluateForagingInternal(agentID, frame, state, true)
}

func evaluateForagingInternal(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState, force bool) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil {
		return nil
	}
	if frame.Status.IsSleeping || (!force && !NeedsForaging(frame)) {
		return nil
	}

	// Yield to active hostile threats or combat damage.
	for _, e := range frame.VisibleEntities {
		if e != nil && IsHostile(e) {
			dist := entityDistance(frame.Status.Position, e)
			if dist <= MaxPerceptionRadius {
				return nil
			}
		}
	}
	if frame.LastDamageTaken != nil && !IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) {
		return nil
	}

	setForagingGoal(state)

	pos := frame.Status.Position
	navState := frame.Status.NavigationState
	canSprint := frame.Status.FoodLevel > 6
	speedMult := float32(1.05)
	if frame.Status.FoodLevel <= 6 {
		speedMult = float32(1.15)
	}

	forageMu.Lock()
	mem := getForageMemory(agentID)
	defer forageMu.Unlock()

	// 0. Pick up nearby dropped food items (e.g. raw meat or berries from recent kills/harvests).
	var bestDrop *civquestv1.VisibleEntity
	bestDropDist := math.MaxFloat64
	for _, e := range frame.VisibleEntities {
		if !isFoodDropEntity(e) || e.Position == nil {
			continue
		}
		d := entityDistance(pos, e)
		if d <= 12.0 && d > forageDropPickupReach && d < bestDropDist {
			bestDropDist = d
			bestDrop = e
		}
	}
	if bestDrop != nil {
		if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING || mem.lastTargetKey != fmt.Sprintf("drop:%d", bestDrop.EntityId) {
			mem.lastTargetKey = fmt.Sprintf("drop:%d", bestDrop.EntityId)
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{
							X: bestDrop.Position.X,
							Y: bestDrop.Position.Y,
							Z: bestDrop.Position.Z,
						},
						SpeedMultiplier:  speedMult,
						Sprint:           canSprint,
						ArrivalTolerance: 1,
					},
				},
			}
		}
		return nil
	}

	// 1. Scan visible edible plant blocks (Tier 2 primary plants, Tier 1 fallback leaves).
	var bestPrimePlant *civquestv1.VisibleBlock
	bestPrimeDist := math.MaxFloat64
	bestPrimeMature := false

	var bestFallbackPlant *civquestv1.VisibleBlock
	bestFallbackDist := math.MaxFloat64

	for _, b := range frame.InterestingBlocks {
		tier := isEdiblePlantBlock(b)
		if tier == 0 {
			continue
		}
		if float64(b.Pos.Y)-pos.Y > 4.0 {
			continue
		}
		if state != nil && state.IsTargetUnreachable(float64(b.Pos.X)+0.5, float64(b.Pos.Y), float64(b.Pos.Z)+0.5, frame.WorldTick) {
			continue
		}
		d := blockDistance(pos, b)
		if tier == 2 {
			mature := isCropMatureHint(b)
			if bestPrimePlant == nil ||
				(mature && !bestPrimeMature) ||
				(mature == bestPrimeMature && d < bestPrimeDist) {
				bestPrimePlant = b
				bestPrimeDist = d
				bestPrimeMature = mature
			}
		} else if tier == 1 && d < bestFallbackDist {
			bestFallbackPlant = b
			bestFallbackDist = d
		}
	}

	// 2. Scan visible passive food animals.
	var bestAnimal *civquestv1.VisibleEntity
	bestAnimalDist := math.MaxFloat64
	for _, e := range frame.VisibleEntities {
		if !isPassiveFoodAnimal(e) {
			continue
		}
		d := entityDistance(pos, e)
		if d < bestAnimalDist {
			bestAnimal = e
			bestAnimalDist = d
		}
	}

	// Immediate action within reach takes top priority (plant harvest or animal hunt).
	if bestPrimePlant != nil && bestPrimeDist <= forageBlockReach {
		targetKey := fmt.Sprintf("block:%d,%d,%d", bestPrimePlant.Pos.X, bestPrimePlant.Pos.Y, bestPrimePlant.Pos.Z)
		if mem.lastTargetKey == targetKey && mem.lastActionTick > 0 &&
			frame.WorldTick >= mem.lastActionTick && frame.WorldTick-mem.lastActionTick < forageActionCooldownTicks {
			return nil
		}
		mem.lastTargetKey = targetKey
		mem.lastActionTick = frame.WorldTick
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_InteractBlock{
				InteractBlock: &civquestv1.InteractBlockIntent{
					Pos:             cloneBlockPos(bestPrimePlant.Pos),
					InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
					BlockOrItemId:   bestPrimePlant.BlockId,
				},
			},
		}
	}

	if bestAnimal != nil && bestAnimalDist <= forageMeleeReach {
		targetKey := fmt.Sprintf("entity:%d", bestAnimal.EntityId)
		if mem.lastTargetKey == targetKey && mem.lastActionTick > 0 &&
			frame.WorldTick >= mem.lastActionTick && frame.WorldTick-mem.lastActionTick < forageActionCooldownTicks {
			return nil
		}
		mem.lastTargetKey = targetKey
		mem.lastActionTick = frame.WorldTick
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Attack{
				Attack: &civquestv1.AttackIntent{
					TargetEntityId:  bestAnimal.EntityId,
					RaiseShield:     false,
					FleeImmediately: false,
				},
			},
		}
	}

	// Choose closer between prime plant block and passive food animal outside reach.
	navigateToPlant := func(b *civquestv1.VisibleBlock) *civquestv1.AgentIntent {
		targetKey := fmt.Sprintf("nav_block:%d,%d,%d", b.Pos.X, b.Pos.Y, b.Pos.Z)
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && mem.lastTargetKey == targetKey {
			return nil
		}
		mem.lastTargetKey = targetKey
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: float64(b.Pos.X) + 0.5,
						Y: float64(b.Pos.Y),
						Z: float64(b.Pos.Z) + 0.5,
					},
					SpeedMultiplier:  speedMult,
					Sprint:           canSprint,
					ArrivalTolerance: 2,
				},
			},
		}
	}

	navigateToAnimal := func(e *civquestv1.VisibleEntity) *civquestv1.AgentIntent {
		if e.Position == nil {
			return nil
		}
		targetKey := fmt.Sprintf("nav_entity:%d", e.EntityId)
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && mem.lastTargetKey == targetKey {
			return nil
		}
		mem.lastTargetKey = targetKey
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: e.Position.X,
						Y: e.Position.Y,
						Z: e.Position.Z,
					},
					SpeedMultiplier:  speedMult,
					Sprint:           canSprint,
					ArrivalTolerance: 2,
				},
			},
		}
	}

	if bestPrimePlant != nil && (bestAnimal == nil || bestPrimeDist <= bestAnimalDist) {
		return navigateToPlant(bestPrimePlant)
	}
	if bestAnimal != nil {
		return navigateToAnimal(bestAnimal)
	}
	if bestFallbackPlant != nil {
		if bestFallbackDist <= forageBlockReach {
			targetKey := fmt.Sprintf("block:%d,%d,%d", bestFallbackPlant.Pos.X, bestFallbackPlant.Pos.Y, bestFallbackPlant.Pos.Z)
			if mem.lastTargetKey == targetKey && mem.lastActionTick > 0 &&
				frame.WorldTick >= mem.lastActionTick && frame.WorldTick-mem.lastActionTick < forageActionCooldownTicks {
				return nil
			}
			mem.lastTargetKey = targetKey
			mem.lastActionTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             cloneBlockPos(bestFallbackPlant.Pos),
						InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
						BlockOrItemId:   bestFallbackPlant.BlockId,
					},
				},
			}
		}
		return navigateToPlant(bestFallbackPlant)
	}

	// 3. Check SpatialIndex for known CROP or CONTAINER/GRANARY POIs when nothing edible is in frustum.
	if state != nil {
		if si := state.SpatialIndex(); si != nil {
			for _, cropPOI := range si.FindPOIsInRadius(pos, POICategoryCrop, 48.0) {
				if cropPOI == nil || isSaplingItemID(cropPOI.BlockID) {
					continue
				}
				acc := poiAccessTarget(cropPOI)
				if state.IsTargetUnreachable(cropPOI.X, cropPOI.Y, cropPOI.Z, frame.WorldTick) ||
					(acc != nil && state.IsTargetUnreachable(acc.X, acc.Y, acc.Z, frame.WorldTick)) {
					continue
				}
				d := math.Hypot(cropPOI.X-pos.X, cropPOI.Z-pos.Z)
				if d > 4.0 {
					targetKey := fmt.Sprintf("poi:%s", cropPOI.POIID)
					if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING || mem.lastTargetKey != targetKey {
						mem.lastTargetKey = targetKey
						return &civquestv1.AgentIntent{
							AgentId: agentID,
							Action: &civquestv1.AgentIntent_Navigate{
								Navigate: &civquestv1.NavigateIntent{
									Target:           acc,
									SpeedMultiplier:  speedMult,
									Sprint:           canSprint,
									ArrivalTolerance: 2,
								},
							},
						}
					}
					return nil
				}
			}
			skipContainers := force && frame.Status.FoodLevel > forageHungerThreshold &&
				(state.GetLatestSurvey() == nil || state.IsExpeditionActive())
			for _, contPOI := range si.FindPOIsInRadius(pos, POICategoryContainer, 48.0) {
				if skipContainers {
					break
				}
				if contPOI == nil {
					continue
				}
				acc := poiAccessTarget(contPOI)
				if state.IsTargetUnreachable(contPOI.X, contPOI.Y, contPOI.Z, frame.WorldTick) ||
					(acc != nil && state.IsTargetUnreachable(acc.X, acc.Y, acc.Z, frame.WorldTick)) {
					continue
				}
				lastVisit := mem.visitedContainerPOIs[contPOI.POIID]
				d := math.Hypot(contPOI.X-pos.X, contPOI.Z-pos.Z)
				if d <= 4.0 {
					mem.visitedContainerPOIs[contPOI.POIID] = frame.WorldTick
				} else if lastVisit == 0 || frame.WorldTick-lastVisit > 400 {
					targetKey := fmt.Sprintf("poi:%s", contPOI.POIID)
					if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING || mem.lastTargetKey != targetKey {
						mem.lastTargetKey = targetKey
						return &civquestv1.AgentIntent{
							AgentId: agentID,
							Action: &civquestv1.AgentIntent_Navigate{
								Navigate: &civquestv1.NavigateIntent{
									Target:           acc,
									SpeedMultiplier:  speedMult,
									Sprint:           canSprint,
									ArrivalTolerance: 2,
								},
							},
						}
					}
					return nil
				}
			}
		}
	}

	// Well-fed agents following a System 2 FORAGE_FOOD directive should yield to normal
	// exploration/survey rather than blind random foraging walks when no reachable food exists.
	if force && frame.Status.FoodLevel > forageHungerThreshold {
		return nil
	}

	// 4. Outward foraging exploration when no food blocks, animals, or reachable food POIs are visible.
	needNewExplore := mem.exploreTarget == nil ||
		navState == civquestv1.NavigationState_NAVIGATION_STATE_IDLE ||
		navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED ||
		navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK

	if !needNewExplore && mem.exploreTarget != nil {
		distToTarget := math.Hypot(pos.X-mem.exploreTarget.X, pos.Z-mem.exploreTarget.Z)
		if distToTarget <= 3.0 {
			needNewExplore = true
		}
	}

	if !needNewExplore && navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
		return nil
	}

	if mem.lastExploreTick > 0 && frame.WorldTick >= mem.lastExploreTick &&
		frame.WorldTick-mem.lastExploreTick < forageExploreCooldownTicks &&
		navState != civquestv1.NavigationState_NAVIGATION_STATE_STUCK &&
		mem.exploreTarget != nil {
		return nil
	}

	if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
		mem.exploreHeadingRad += 2.2
	} else {
		mem.exploreHeadingRad += 0.9
	}
	mem.exploreHeadingRad = math.Atan2(math.Sin(mem.exploreHeadingRad), math.Cos(mem.exploreHeadingRad))

	target := &civquestv1.Vec3{
		X: math.Round((pos.X+math.Cos(mem.exploreHeadingRad)*forageExploreStepDist)*10) / 10,
		Y: pos.Y,
		Z: math.Round((pos.Z+math.Sin(mem.exploreHeadingRad)*forageExploreStepDist)*10) / 10,
	}
	mem.exploreTarget = target
	mem.lastExploreTick = frame.WorldTick
	mem.lastTargetKey = fmt.Sprintf("forage_explore:%.1f,%.1f", target.X, target.Z)

	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target:           target,
				SpeedMultiplier:  speedMult,
				Sprint:           canSprint,
				ArrivalTolerance: 3,
			},
		},
	}
}
