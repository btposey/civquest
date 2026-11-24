package system1

import (
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// FSMIntent encapsulates an emitted intent alongside its priority classification.
type FSMIntent struct {
	Intent   *civquestv1.AgentIntent
	Priority bool
}

// FSMContext encapsulates sensory frames, agent configuration, spatial state, and vitals for evaluation.
type FSMContext struct {
	AgentID        string
	Frame          *civquestv1.TelemetryFrame
	State          *AgentState
	Config         *xdg.AgentIdentityConfig
	SafeNodes      []db.MapNode
	CanEat         bool
	LastEatingTick int64
	PavingTracker  *RoadPavingTracker
	StuckTicks     int64
	TimeInState    int64
	HasShield      bool
	HasWeapon      bool
	WeaponSlot     int32
	CivicDB        *db.CivicRegistryDB
	Facts          *FactGraph
}

// HostilesPresent checks if any hostile mobs are within perception range.
func (c *FSMContext) HostilesPresent() bool {
	if c == nil {
		return false
	}
	if c.Facts != nil {
		return c.Facts.GetBool("threat.hostiles_present")
	}
	if c.Frame == nil {
		return false
	}
	for _, e := range c.Frame.VisibleEntities {
		if IsHostile(e) {
			return true
		}
	}
	return false
}

// DamageTaken checks if the agent took damage in the current frame.
func (c *FSMContext) DamageTaken() bool {
	if c == nil {
		return false
	}
	if c.Facts != nil {
		return c.Facts.GetBool("threat.damage_taken")
	}
	if c.Frame == nil || isDrowningDamage(c.Frame) {
		return false
	}
	return c.Frame.LastDamageTaken != nil && !IsPassiveOrEnvironmentalDamage(c.Frame.LastDamageTaken)
}

// IsDrowning checks if the agent is experiencing a drowning emergency.
func (c *FSMContext) IsDrowning() bool {
	if c == nil {
		return false
	}
	if c.Facts != nil {
		return c.Facts.GetBool("threat.is_drowning") || c.Facts.GetString("threat.threatType") == "DROWNING"
	}
	return isDrowningDamage(c.Frame)
}

// IsHealthCritical checks if HP <= 35% max HP.
func (c *FSMContext) IsHealthCritical() bool {
	if c == nil {
		return false
	}
	if c.Facts != nil {
		return c.Facts.GetBool("vitals.is_health_critical")
	}
	if c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	maxHP := float64(c.Frame.Status.MaxHealth)
	if maxHP <= 0 {
		maxHP = 20.0
	}
	return float64(c.Frame.Status.Health) <= LowHPThresholdRatio*maxHP
}

// IsOnFire checks if the agent is burning.
func (c *FSMContext) IsOnFire() bool {
	if c == nil {
		return false
	}
	if c.Facts != nil {
		return c.Facts.GetBool("vitals.is_on_fire")
	}
	if c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	return c.Frame.Status.IsOnFire
}

// IsCriticalHunger checks if hunger <= 6 and edible food exists.
func (c *FSMContext) IsCriticalHunger() bool {
	if c == nil {
		return false
	}
	if c.Facts != nil {
		return c.Facts.GetBool("vitals.is_critical_hunger")
	}
	if c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	if c.Frame.Status.FoodLevel <= 6 {
		if _, _, hasFood := FindOptimalFood(c.Frame.Status.Inventory); hasFood {
			return true
		}
	}
	return false
}

// ShouldPanic checks if combat panic rules trigger.
func (c *FSMContext) ShouldPanic() bool {
	if c == nil {
		return false
	}
	if c.Facts != nil {
		return c.Facts.GetBool("threat.should_panic")
	}
	if c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	bravery := 0.5
	if c.Config != nil {
		bravery = c.Config.Traits.Bravery
	}
	_, conf, hostiles := AssessImmediateThreats(c.Frame, bravery)
	selfHP := float64(c.Frame.Status.Health)
	maxHP := float64(c.Frame.Status.MaxHealth)
	if maxHP <= 0 {
		maxHP = 20.0
	}
	creeperNearby := false
	for _, h := range hostiles {
		if strings.Contains(strings.ToLower(h), "creeper") {
			creeperNearby = true
			break
		}
	}
	return ShouldPanic(selfHP, maxHP, conf, len(hostiles) > 0 || c.DamageTaken(), creeperNearby)
}

// IsEmergency evaluates whether any high-priority emergency condition is active.
func (c *FSMContext) IsEmergency() bool {
	if c != nil && c.Facts != nil {
		if _, exists := c.Facts.Get("threat.is_emergency"); exists {
			return c.Facts.GetBool("threat.is_emergency")
		}
		if _, exists := c.Facts.Get("threat.threats_cleared"); exists {
			return !c.Facts.GetBool("threat.threats_cleared")
		}
	}
	if c == nil {
		return false
	}
	return c.HostilesPresent() || c.DamageTaken() || c.IsOnFire() || c.IsCriticalHunger() || c.ShouldPanic() || c.IsDrowning()
}

// ThreatsCleared evaluates whether all hostiles, panic, fire, drowning, and critical hunger issues have cleared.
func (c *FSMContext) ThreatsCleared() bool {
	if c != nil && c.Facts != nil {
		if _, exists := c.Facts.Get("threat.threats_cleared"); exists {
			return c.Facts.GetBool("threat.threats_cleared")
		}
		if _, exists := c.Facts.Get("threat.is_emergency"); exists {
			return !c.Facts.GetBool("threat.is_emergency")
		}
	}
	if c == nil {
		return true
	}
	return !c.HostilesPresent() && !c.DamageTaken() && !c.IsOnFire() && !c.IsCriticalHunger() && !c.ShouldPanic() && !c.IsDrowning()
}

// HasSurvivalKit returns true if the agent holds an axe, weapon/sword, and light source.
func (c *FSMContext) HasSurvivalKit() bool {
	if c != nil && c.Facts != nil {
		if _, exists := c.Facts.Get("bootstrap.has_kit"); exists {
			return c.Facts.GetBool("bootstrap.has_kit")
		}
	}
	if c == nil || c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	return HasSurvivalKit(c.Frame.Status.Inventory)
}

// IsDuskOrNight returns true if current time is dusk, night, or thunderstorm.
func (c *FSMContext) IsDuskOrNight() bool {
	if c != nil && c.Facts != nil {
		if _, exists := c.Facts.Get("circadian.is_dusk_or_night"); exists {
			return c.Facts.GetBool("circadian.is_dusk_or_night")
		}
	}
	if c == nil || c.Frame == nil {
		return false
	}
	if c.Frame.Weather == civquestv1.Weather_WEATHER_THUNDER {
		return true
	}
	tod := ((c.Frame.TimeOfDay % 24000) + 24000) % 24000
	return tod >= 11000 && tod < 23500
}

// IsNight returns true if current time is deep night (13000-23500).
func (c *FSMContext) IsNight() bool {
	if c == nil || c.Frame == nil {
		return false
	}
	tod := ((c.Frame.TimeOfDay % 24000) + 24000) % 24000
	return tod >= 13000 && tod < 23500
}

// IsDawnOrDay returns true if daytime (23500 to 11000).
func (c *FSMContext) IsDawnOrDay() bool {
	return !c.IsDuskOrNight()
}

// HasAssignedBed returns true if state has an assigned bed.
func (c *FSMContext) HasAssignedBed() bool {
	if c != nil && c.Facts != nil {
		if _, exists := c.Facts.Get("shelter.has_bed"); exists {
			return c.Facts.GetBool("shelter.has_bed")
		}
	}
	if c == nil || c.State == nil {
		return false
	}
	return c.State.GetAssignedBed() != nil
}

// IsShelterComplete checks if all shelter walls have been placed or shelter pos secured.
func (c *FSMContext) IsShelterComplete() bool {
	if c != nil && c.Facts != nil {
		if _, exists := c.Facts.Get("shelter.complete"); exists {
			return c.Facts.GetBool("shelter.complete")
		}
	}
	if c == nil || c.State == nil {
		return false
	}
	return c.State.GetShelterBuildIndex() >= blueprints.CabinTotalBlocks ||
		c.State.GetShelterBlocksPlaced() >= blueprints.CabinTotalBlocks
}

// IsShelterOnCooldown checks if shelter construction is on backoff cooldown after a stuck timeout.
func (c *FSMContext) IsShelterOnCooldown() bool {
	if c != nil && c.Facts != nil {
		if _, exists := c.Facts.Get("shelter.on_cooldown"); exists {
			return c.Facts.GetBool("shelter.on_cooldown")
		}
	}
	if c == nil || c.State == nil || c.Frame == nil {
		return false
	}
	return c.State.IsShelterOnCooldown(c.Frame.WorldTick)
}

// IsBootstrapOnCooldown checks if bootstrap retry is on backoff cooldown after a timeout.
func (c *FSMContext) IsBootstrapOnCooldown() bool {
	if c != nil && c.Facts != nil {
		if _, exists := c.Facts.Get("bootstrap.on_cooldown"); exists {
			return c.Facts.GetBool("bootstrap.on_cooldown")
		}
	}
	if c == nil || c.State == nil || c.Frame == nil {
		return false
	}
	return c.State.IsBootstrapOnCooldown(c.Frame.WorldTick)
}

// IsEstablishingNewSettlement returns true if the agent is founding a new settlement node.
func (c *FSMContext) IsEstablishingNewSettlement() bool {
	if c != nil && c.Facts != nil {
		if _, exists := c.Facts.Get("colonize.is_establishing"); exists {
			return c.Facts.GetBool("colonize.is_establishing")
		}
	}
	if c == nil || c.State == nil {
		return false
	}
	return c.State.IsEstablishingNewSettlement()
}

// IsSleeping checks if agent status reports sleeping.
func (c *FSMContext) IsSleeping() bool {
	if c == nil {
		return false
	}
	if c.Facts != nil {
		return c.Facts.GetBool("vitals.is_sleeping")
	}
	if c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	return c.Frame.Status.IsSleeping
}

// IsStuckInState returns true if time in state or stuck ticks exceeds threshold.
func (c *FSMContext) IsStuckInState(maxTicks int64) bool {
	if c == nil {
		return false
	}
	// If active shelter construction, timber harvesting, or crafting is progressing, the agent is not stuck
	if c.State != nil && c.Frame != nil {
		wt := c.Frame.WorldTick
		if lastPlace := c.State.GetLastShelterPlaceTick(); lastPlace > 0 && wt >= lastPlace && wt-lastPlace < 100 {
			return false
		}
		if lastHarvest := c.State.GetLastHarvestTick(); lastHarvest > 0 && wt >= lastHarvest && wt-lastHarvest < 100 {
			return false
		}
		if lastCraft := c.State.GetLastCraftTick(); lastCraft > 0 && wt >= lastCraft && wt-lastCraft < 100 {
			return false
		}
	}
	return c.StuckTicks >= maxTicks || (maxTicks > 0 && c.TimeInState >= maxTicks)
}

// StateHandler defines the interface for each HFSM state lifecycle hooks.
type StateHandler interface {
	Name() State
	Guard(ctx *FSMContext) bool
	OnEntry(ctx *FSMContext, from State)
	OnTick(ctx *FSMContext) []FSMIntent
	OnExit(ctx *FSMContext, to State)
}

// StageTransitionRule defines a directed edge for Tier 1 macro stage transitions evaluated in priority order.
type StageTransitionRule struct {
	FromStage   Stage
	ToStage     Stage
	Condition   func(ctx *FSMContext) bool
	Description string
}

// TransitionRule is an alias for StageTransitionRule, preserving backward compatibility.
type TransitionRule = StageTransitionRule

func defaultTransitionRules() []StageTransitionRule {
	return []StageTransitionRule{
		// Priority 1: EMERGENCY Preemption from all non-emergency stages
		{
			FromStage: StageBootstrap,
			ToStage:   StageEmergency,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.is_emergency")
				}
				return c != nil && c.IsEmergency()
			},
			Description: "Hostile threat, damage, or critical vitals detected during bootstrap",
		},
		{
			FromStage: StageSettlement,
			ToStage:   StageEmergency,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.is_emergency")
				}
				return c != nil && c.IsEmergency()
			},
			Description: "Hostile threat, damage, or critical vitals detected during settlement living",
		},
		{
			FromStage: StageColonize,
			ToStage:   StageEmergency,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.is_emergency")
				}
				return c != nil && c.IsEmergency()
			},
			Description: "Hostile threat, damage, or critical vitals detected during colonization",
		},
		{
			FromStage: StateShelter,
			ToStage:   StageEmergency,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.is_emergency")
				}
				return c != nil && c.IsEmergency()
			},
			Description: "Hostile threat or damage detected; preempting shelter construction",
		},
		{
			FromStage: StateRest,
			ToStage:   StageEmergency,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.is_emergency")
				}
				return c != nil && c.IsEmergency()
			},
			Description: "Hostile threat or damage detected while resting; waking to defend or flee",
		},

		// Priority 2: Transitions OUT of EMERGENCY (Strictly guarded by ThreatsCleared)
		{
			FromStage: StageEmergency,
			ToStage:   StageColonize,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.threats_cleared") && c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.ThreatsCleared() && c.IsEstablishingNewSettlement()
			},
			Description: "Threats cleared; resuming settlement colonization",
		},
		{
			FromStage: StageEmergency,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.threats_cleared") && !c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && c.ThreatsCleared() && !c.HasSurvivalKit()
			},
			Description: "Threats cleared; resuming bootstrap tool gathering",
		},
		{
			FromStage: StageEmergency,
			ToStage:   StageSettlement,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.threats_cleared") && c.Facts.GetBool("bootstrap.has_kit") && !c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.ThreatsCleared() && c.HasSurvivalKit() && !c.IsEstablishingNewSettlement()
			},
			Description: "Threats cleared; resuming established settlement living",
		},

		// Priority 3: Transitions from BOOTSTRAP
		{
			FromStage: StageBootstrap,
			ToStage:   StageColonize,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("bootstrap.has_kit") && c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.HasSurvivalKit() && c.IsEstablishingNewSettlement()
			},
			Description: "Survival kit acquired; advancing to colonization",
		},
		{
			FromStage: StageBootstrap,
			ToStage:   StageSettlement,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("bootstrap.has_kit") && !c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.HasSurvivalKit() && !c.IsEstablishingNewSettlement()
			},
			Description: "Survival kit acquired (axe, sword, light); advancing to settlement living",
		},

		// Priority 4: Transitions from SETTLEMENT
		{
			FromStage: StageSettlement,
			ToStage:   StageColonize,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.IsEstablishingNewSettlement()
			},
			Description: "Founding new settlement node; advancing to colonization",
		},
		{
			FromStage: StageSettlement,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("circadian.is_dusk_or_night") && !c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && c.IsDawnOrDay() && !c.HasSurvivalKit()
			},
			Description: "Survival kit broken or missing; reverting to bootstrap",
		},

		// Priority 5: Transitions from COLONIZE
		{
			FromStage: StageColonize,
			ToStage:   StageSettlement,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("colonize.is_establishing") && c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && !c.IsEstablishingNewSettlement() && c.HasSurvivalKit()
			},
			Description: "Colonization complete and kit secured; transitioning to settlement living",
		},
		{
			FromStage: StageColonize,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("colonize.is_establishing") && !c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && !c.IsEstablishingNewSettlement() && !c.HasSurvivalKit()
			},
			Description: "Colony site cancelled or tools broken; reverting to bootstrap",
		},
		{
			FromStage: StageColonize,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("circadian.is_dusk_or_night") && !c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && c.IsDawnOrDay() && !c.HasSurvivalKit()
			},
			Description: "Survival kit broken or missing; reverting to bootstrap",
		},

		// Priority 6: Legacy transitions from SHELTER and REST (backward compatibility)
		{
			FromStage: StateShelter,
			ToStage:   StageSettlement,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("circadian.is_dusk_or_night") && c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && c.IsDawnOrDay() && c.HasSurvivalKit()
			},
			Description: "Dawn arrived; resuming settlement exploration and duties",
		},
		{
			FromStage: StateShelter,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("circadian.is_dusk_or_night") && !c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && c.IsDawnOrDay() && !c.HasSurvivalKit()
			},
			Description: "Dawn arrived; resuming bootstrap survival crafting",
		},
		{
			FromStage:   StateShelter,
			ToStage:     StageSettlement,
			Condition:   func(c *FSMContext) bool { return c != nil && c.IsStuckInState(600) },
			Description: "Shelter construction timed out/stuck; falling back to settlement",
		},
		{
			FromStage: StateRest,
			ToStage:   StageSettlement,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("circadian.is_dusk_or_night") && c.Facts.GetBool("bootstrap.has_kit") && !c.Facts.GetBool("vitals.is_sleeping")
				}
				return c != nil && c.IsDawnOrDay() && c.HasSurvivalKit() && !c.IsSleeping()
			},
			Description: "Dawn awakening; starting daily settlement duties",
		},
		{
			FromStage: StateRest,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("circadian.is_dusk_or_night") && !c.Facts.GetBool("bootstrap.has_kit") && !c.Facts.GetBool("vitals.is_sleeping")
				}
				return c != nil && c.IsDawnOrDay() && !c.HasSurvivalKit() && !c.IsSleeping()
			},
			Description: "Dawn awakening; starting survival kit bootstrap",
		},
	}
}

func tryForagingPreemption(ctx *FSMContext, skillCtx *SkillContext) ([]FSMIntent, bool) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return nil, false
	}
	if ctx.IsDuskOrNight() && ctx.Frame.Status.FoodLevel > 6 {
		return nil, false
	}
	var fSkill Skill = NewForagingSkill()
	if s, ok := DefaultRegistry.GetSkill("foraging"); ok && s != nil {
		fSkill = s
	}
	if !fSkill.Preconditions(ctx.Facts) {
		return nil, false
	}
	if ctx.State != nil {
		ctx.State.ClearExpeditionSurveyingIfActive()
		if ctx.State.HasActiveTask() {
			ctx.State.PauseActiveTask(ctx.Frame.WorldTick)
		}
	}
	si, err := fSkill.Execute(skillCtx, ctx.Facts)
	if err != nil {
		return nil, true
	}
	var out []FSMIntent
	for _, intent := range si {
		if intent != nil {
			out = append(out, FSMIntent{Intent: intent, Priority: false})
		}
	}
	return out, true
}

// BootstrapStateHandler handles survival kit bootstrap goals.
type BootstrapStateHandler struct{}

func (h *BootstrapStateHandler) Name() State { return StageBootstrap }
func (h *BootstrapStateHandler) Guard(ctx *FSMContext) bool {
	return true
}
func (h *BootstrapStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *BootstrapStateHandler) OnExit(ctx *FSMContext, to State)    {}
func (h *BootstrapStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	if ctx.Facts == nil {
		evalCtx := &EvaluatorContext{
			AgentID:       ctx.AgentID,
			Frame:         ctx.Frame,
			Config:        ctx.Config,
			State:         ctx.State,
			CivicDB:       ctx.CivicDB,
			SafeNodes:     ctx.SafeNodes,
			PavingTracker: ctx.PavingTracker,
		}
		ctx.Facts = DefaultRegistry.EvaluateAll(evalCtx)
	}

	// Biological needs (eating, sleep, wake) evaluated via DefaultRegistry / BiologicalNeedsSkill
	skillCtx := &SkillContext{
		AgentID:       ctx.AgentID,
		Frame:         ctx.Frame,
		Config:        ctx.Config,
		State:         ctx.State,
		CivicDB:       ctx.CivicDB,
		SafeNodes:     ctx.SafeNodes,
		PavingTracker: ctx.PavingTracker,
	}
	var hadAssignedBed bool
	if ctx.State != nil {
		hadAssignedBed = ctx.State.GetAssignedBed() != nil
	}
	var needsIntents []*civquestv1.AgentIntent
	if skill, ok := DefaultRegistry.GetSkill("biological_needs"); ok && skill != nil {
		if skill.Preconditions(ctx.Facts) {
			if si, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
				needsIntents = si
			}
		}
	} else {
		bioSkill := NewBiologicalNeedsSkill()
		if bioSkill.Preconditions(ctx.Facts) {
			if si, err := bioSkill.Execute(skillCtx, ctx.Facts); err == nil {
				needsIntents = si
			}
		} else {
			needsIntents = EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
		}
	}
	// If EvaluateSleep cleared an unreachable AssignedBed during this tick, refresh shelter.has_bed immediately.
	if hadAssignedBed && ctx.State != nil && ctx.State.GetAssignedBed() == nil && ctx.Facts != nil && ctx.Facts.GetBool("shelter.has_bed") {
		ctx.Facts.SetBool("shelter.has_bed", "shelter", false, "Assigned bed cleared as unreachable")
	}
	for _, ni := range needsIntents {
		if ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking {
			intents = append(intents, FSMIntent{Intent: ni, Priority: true})
		} else {
			intents = append(intents, FSMIntent{Intent: ni, Priority: false})
		}
	}

	// Road paving (suppressed while 360-degree horizon survey is active)
	if !ctx.Frame.Status.IsSleeping && ctx.PavingTracker != nil &&
		(ctx.State == nil || !ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick)) &&
		ctx.Frame.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_STUCK &&
		!ctx.Frame.Status.NavigationEnclosed {
		if ctx.State != nil && ctx.PavingTracker.GetHomeOrigin() == nil {
			if home := ctx.State.GetHomeOrigin(); home != nil {
				ctx.PavingTracker.SetHomeOrigin(home)
			}
		}
		if paveIntent := ctx.PavingTracker.EvaluatePaving(ctx.AgentID, ctx.Frame); paveIntent != nil {
			intents = append(intents, FSMIntent{Intent: paveIntent, Priority: false})
		}
	}

	// Hungry without food: preempt normal bootstrap/shelter to forage or hunt
	if forageIntents, preempted := tryForagingPreemption(ctx, skillCtx); preempted {
		intents = append(intents, forageIntents...)
		return intents
	}

	// In dusk/night without shelter or bed (ADR-032 §4): try shelter construction first if buildable,
	// otherwise fall through seamlessly to bootstrap gathering, crafting, directives, and exploration.
	// Once shelter is complete or an assigned bed exists, normal circadian evening/sleep routines apply.
	if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		shelterSatisfied := ctx.HasAssignedBed() || ctx.IsShelterComplete()
		isSleepRoutine := ctx.State != nil && ctx.State.Routine() == RoutineSleep
		isTavernRoutine := ctx.State != nil && ctx.State.Routine() == RoutineTavernSocial

		if !shelterSatisfied && ctx.IsDuskOrNight() && !ctx.IsShelterOnCooldown() {
			deferShelterForActiveHarvest := false
			if ctx.State != nil && ctx.State.HasActiveTask() {
				_, kind, _, _ := ctx.State.ActiveTaskInfo()
				if kind == "harvest_tree" || kind == "collect_items" {
					inv := inspectBootstrapInventory(ctx.Frame.Status.Inventory)
					if inv.buildingBlockCount == 0 && !inv.hasLight {
						deferShelterForActiveHarvest = true
					}
				}
			}

			if !deferShelterForActiveHarvest {
				var shelterIntents []FSMIntent
				if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "shelter"); ok && len(skillIntents) > 0 {
					for _, si := range skillIntents {
						if si != nil {
							shelterIntents = append(shelterIntents, FSMIntent{Intent: si, Priority: false})
						}
					}
				} else if shelterIntent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); shelterIntent != nil {
					shelterIntents = append(shelterIntents, FSMIntent{Intent: shelterIntent, Priority: false})
				}
				if len(shelterIntents) > 0 {
					intents = append(intents, shelterIntents...)
					return intents
				}
			}
		}

		if !shelterSatisfied || (!ctx.IsDuskOrNight() && !isSleepRoutine && !isTavernRoutine) {
			return runBootstrapActivity(ctx, skillCtx, intents)
		}
	}

	return intents
}

func runBootstrapActivity(ctx *FSMContext, skillCtx *SkillContext, intents []FSMIntent) []FSMIntent {
	if ctx.State != nil && ctx.State.HasActiveTask() {
		ctx.State.ResumeActiveTask(ctx.Frame.WorldTick)
		_, kind, _, _ := ctx.State.ActiveTaskInfo()
		if kind != "harvest_tree" && kind != "collect_items" {
			stepRes := ctx.State.StepActiveTask(ctx.AgentID, ctx.Frame)
			if stepRes.Intent != nil {
				intents = append(intents, FSMIntent{Intent: stepRes.Intent, Priority: false})
			}
			if stepRes.Active {
				return intents
			}
		} else {
			// Allow fresh or stuck-breaking non-GATHER_TIMBER System 2 directives to preempt harvest_tree.
			if kind == "harvest_tree" && ctx.Facts != nil && ctx.Facts.GetBool("system2.directive_active") &&
				(HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)) {
				dir, dirActive := ctx.State.ActiveStrategicDirective(ctx.Frame.WorldTick)
				task := ctx.State.GetActiveTask()
				if dirActive && task != nil && dir.Action != DirectiveActionGatherTimber &&
					(dir.Action == DirectiveActionScoutFrontier ||
						dir.Action == DirectiveActionMineStone ||
						dir.Action == DirectiveActionSocialize ||
						dir.Action == DirectiveActionBuildShelter ||
						(dir.Action == DirectiveActionForageFood && NeedsForaging(ctx.Frame))) {
					isStuckOrStalled := ctx.Frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK ||
						task.ConsecutiveFailures > 0 ||
						(ctx.Frame.WorldTick >= task.LastProgressTick && ctx.Frame.WorldTick-task.LastProgressTick >= 40)
					if dir.IssuedTick >= task.StartedTick || isStuckOrStalled {
						if dirIntent := EvaluateStrategicDirectiveWithID(ctx.AgentID, ctx.Frame, ctx.State, ctx.Config); dirIntent != nil {
							ctx.State.ClearActiveTask()
							intents = append(intents, FSMIntent{Intent: dirIntent, Priority: false})
							return intents
						} else if dir.Action == DirectiveActionScoutFrontier && ctx.State.IsExpeditionActive() {
							ctx.State.ClearActiveTask()
							return intents
						}
					}
				}
			}
			if bootstrapIntent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); bootstrapIntent != nil {
				intents = append(intents, FSMIntent{Intent: bootstrapIntent, Priority: false})
			}
			return intents
		}
	}
	if ctx.Facts != nil && ctx.Facts.GetBool("system2.directive_active") &&
		(HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)) {
		if dirIntent := EvaluateStrategicDirectiveWithID(ctx.AgentID, ctx.Frame, ctx.State, ctx.Config); dirIntent != nil {
			intents = append(intents, FSMIntent{Intent: dirIntent, Priority: false})
			return intents
		}
	}
	if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "bootstrap"); ok && len(skillIntents) > 0 {
		for _, si := range skillIntents {
			if si != nil {
				intents = append(intents, FSMIntent{Intent: si, Priority: false})
			}
		}
	} else if ctx.State == nil || !ctx.State.HasActiveTask() {
		if bootstrapIntent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); bootstrapIntent != nil {
			intents = append(intents, FSMIntent{Intent: bootstrapIntent, Priority: false})
		}
	}
	return intents
}

// SettlementStateHandler handles established settlement living, vocational duties, hobbies, and social routines.
type SettlementStateHandler struct{}

func (h *SettlementStateHandler) Name() State { return StageSettlement }
func (h *SettlementStateHandler) Guard(ctx *FSMContext) bool {
	return true
}
func (h *SettlementStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *SettlementStateHandler) OnExit(ctx *FSMContext, to State) {
	if ctx != nil && ctx.State != nil && ctx.Frame != nil && to == StageSettlement {
		ctx.State.SetLastShelterTimeoutTick(ctx.Frame.WorldTick)
	}
}
func (h *SettlementStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	if ctx.Facts == nil {
		evalCtx := &EvaluatorContext{
			AgentID:       ctx.AgentID,
			Frame:         ctx.Frame,
			Config:        ctx.Config,
			State:         ctx.State,
			CivicDB:       ctx.CivicDB,
			SafeNodes:     ctx.SafeNodes,
			PavingTracker: ctx.PavingTracker,
		}
		ctx.Facts = DefaultRegistry.EvaluateAll(evalCtx)
	}

	// Biological needs (eating, sleep, wake) evaluated via DefaultRegistry / BiologicalNeedsSkill
	skillCtx := &SkillContext{
		AgentID:       ctx.AgentID,
		Frame:         ctx.Frame,
		Config:        ctx.Config,
		State:         ctx.State,
		CivicDB:       ctx.CivicDB,
		SafeNodes:     ctx.SafeNodes,
		PavingTracker: ctx.PavingTracker,
	}
	var hadAssignedBed bool
	if ctx.State != nil {
		hadAssignedBed = ctx.State.GetAssignedBed() != nil
	}
	var needsIntents []*civquestv1.AgentIntent
	if skill, ok := DefaultRegistry.GetSkill("biological_needs"); ok && skill != nil {
		if skill.Preconditions(ctx.Facts) {
			if si, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
				needsIntents = si
			}
		}
	} else {
		bioSkill := NewBiologicalNeedsSkill()
		if bioSkill.Preconditions(ctx.Facts) {
			if si, err := bioSkill.Execute(skillCtx, ctx.Facts); err == nil {
				needsIntents = si
			}
		} else {
			needsIntents = EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
		}
	}
	// If EvaluateSleep cleared an unreachable AssignedBed during this tick, refresh shelter.has_bed immediately.
	if hadAssignedBed && ctx.State != nil && ctx.State.GetAssignedBed() == nil && ctx.Facts != nil && ctx.Facts.GetBool("shelter.has_bed") {
		ctx.Facts.SetBool("shelter.has_bed", "shelter", false, "Assigned bed cleared as unreachable")
	}
	for _, ni := range needsIntents {
		if ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking {
			intents = append(intents, FSMIntent{Intent: ni, Priority: true})
		} else {
			intents = append(intents, FSMIntent{Intent: ni, Priority: false})
		}
	}

	// Road paving (suppressed while 360-degree horizon survey is active)
	if ctx.State == nil || !ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick) {
		if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "infrastructure"); ok && len(skillIntents) > 0 {
			for _, si := range skillIntents {
				if si != nil {
					intents = append(intents, FSMIntent{Intent: si, Priority: false})
				}
			}
		} else if !ctx.Frame.Status.IsSleeping && ctx.PavingTracker != nil &&
			ctx.Frame.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_STUCK &&
			!ctx.Frame.Status.NavigationEnclosed {
			if ctx.State != nil && ctx.PavingTracker.GetHomeOrigin() == nil {
				if home := ctx.State.GetHomeOrigin(); home != nil {
					ctx.PavingTracker.SetHomeOrigin(home)
				}
			}
			if paveIntent := ctx.PavingTracker.EvaluatePaving(ctx.AgentID, ctx.Frame); paveIntent != nil {
				intents = append(intents, FSMIntent{Intent: paveIntent, Priority: false})
			}
		}
	}

	routine := RoutinePrimaryDuty
	if ctx.State != nil {
		routine = ctx.State.Routine()
	}

	// Hungry without food: preempt daily settlement duties (except in-inventory armory crafting) to forage/hunt
	if routine != RoutineArmoryMaintenance {
		if forageIntents, preempted := tryForagingPreemption(ctx, skillCtx); preempted {
			intents = append(intents, forageIntents...)
			return intents
		}
	}

	// In dusk/night without an assigned bed: prioritize outpost shelter construction
	if (ctx.IsDuskOrNight() || routine == RoutineSleep) && !ctx.HasAssignedBed() && !ctx.IsShelterComplete() && !ctx.IsShelterOnCooldown() {
		if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
			if shelterIntent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); shelterIntent != nil {
				intents = append(intents, FSMIntent{Intent: shelterIntent, Priority: false})
				return intents
			}
		}
	}

	// Execute any committed TaskSubroutine before falling back to circadian patrol/exploration
	if ctx.State != nil && ctx.State.HasActiveTask() && len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		ctx.State.ResumeActiveTask(ctx.Frame.WorldTick)
		stepRes := ctx.State.StepActiveTask(ctx.AgentID, ctx.Frame)
		if stepRes.Intent != nil {
			intents = append(intents, FSMIntent{Intent: stepRes.Intent, Priority: false})
		}
		if stepRes.Active {
			return intents
		}
	}

	// Execute active System 2 strategic directive during normal awake daytime settlement ticks (ADR-031)
	if routine != RoutineSleep && routine != RoutineArmoryMaintenance &&
		len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil && !ctx.IsDuskOrNight() {
		if ctx.Facts != nil && ctx.Facts.GetBool("system2.directive_active") {
			if dirIntent := EvaluateStrategicDirectiveWithID(ctx.AgentID, ctx.Frame, ctx.State, ctx.Config); dirIntent != nil {
				intents = append(intents, FSMIntent{Intent: dirIntent, Priority: false})
				return intents
			}
		}
	}

	// Hold steady while a 360-degree horizon survey is executing on the motor stack (ADR-032).
	if routine != RoutineSleep && routine != RoutineArmoryMaintenance &&
		ctx.State != nil && ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick) {
		return intents
	}

	switch routine {
	case RoutineSleep:
		// Sleeping in bed or resting inside shelter handled by biological needs
	case RoutinePrimaryDuty:
		if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
			if ctx.State != nil && ctx.State.GetExpeditionPhase() != ExpeditionIdle {
				if surveyIntent := EvaluateHorizonExplorer(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); surveyIntent != nil {
					intents = append(intents, FSMIntent{Intent: surveyIntent, Priority: false})
					return intents
				}
			}
			if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "security", "exploration"); ok && len(skillIntents) > 0 {
				for _, si := range skillIntents {
					if si != nil {
						intents = append(intents, FSMIntent{Intent: si, Priority: false})
					}
				}
			} else {
				prof := "UNASSIGNED"
				if ctx.Config != nil {
					prof = strings.ToUpper(strings.TrimSpace(ctx.Config.Profession))
				}
				var exploreIntent *civquestv1.AgentIntent
				if prof == "EXPLORER" || prof == "TRAILBLAZER" || prof == "SCOUT" {
					exploreIntent = EvaluateScouting(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State)
				} else {
					exploreIntent = EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State)
				}
				if exploreIntent != nil {
					intents = append(intents, FSMIntent{Intent: exploreIntent, Priority: false})
				}
			}
		}
	case RoutineCivicPatrol:
		if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
			if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "security", "exploration"); ok && len(skillIntents) > 0 {
				for _, si := range skillIntents {
					if si != nil {
						intents = append(intents, FSMIntent{Intent: si, Priority: false})
					}
				}
			} else if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
				intents = append(intents, FSMIntent{Intent: patrolIntent, Priority: false})
			}
		}
	case RoutineArmoryMaintenance:
		if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
			skillCtx := &SkillContext{
				AgentID: ctx.AgentID,
				Frame:   ctx.Frame,
				Config:  ctx.Config,
				State:   ctx.State,
				CivicDB: ctx.CivicDB,
			}
			if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "Armory"); ok && len(skillIntents) > 0 {
				for _, si := range skillIntents {
					if si != nil {
						intents = append(intents, FSMIntent{Intent: si, Priority: false})
					}
				}
			} else if armoryIntent := EvaluateArmoryMaintenance(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.CivicDB); armoryIntent != nil {
				intents = append(intents, FSMIntent{Intent: armoryIntent, Priority: false})
			} else {
				if ctx.State != nil {
					ctx.State.SetArmoryMaintenanceNeeded(false)
				}
				if forageIntents, preempted := tryForagingPreemption(ctx, skillCtx); preempted {
					intents = append(intents, forageIntents...)
					return intents
				}
				if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
					intents = append(intents, FSMIntent{Intent: patrolIntent, Priority: false})
				}
			}
		}
	case RoutineSecondaryHobby:
		if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
			if ctx.Config != nil && ctx.Config.EffectiveShift() == xdg.ShiftThird && ctx.State != nil && ctx.State.IsArmoryMaintenanceNeeded() {
				skillCtx := &SkillContext{
					AgentID: ctx.AgentID,
					Frame:   ctx.Frame,
					Config:  ctx.Config,
					State:   ctx.State,
					CivicDB: ctx.CivicDB,
				}
				if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "Armory"); ok && len(skillIntents) > 0 {
					for _, si := range skillIntents {
						if si != nil {
							intents = append(intents, FSMIntent{Intent: si, Priority: false})
						}
					}
					return intents
				}
				if armoryIntent := EvaluateArmoryMaintenance(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.CivicDB); armoryIntent != nil {
					intents = append(intents, FSMIntent{Intent: armoryIntent, Priority: false})
					return intents
				}
			}
			if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
				intents = append(intents, FSMIntent{Intent: patrolIntent, Priority: false})
			}
		}
	}

	return intents
}

// ExploreStateHandler is a backward-compatible alias for SettlementStateHandler.
type ExploreStateHandler = SettlementStateHandler

// ColonizeStateHandler handles establishing and building a new frontier settlement node.
type ColonizeStateHandler struct{}

func (h *ColonizeStateHandler) Name() State { return StageColonize }
func (h *ColonizeStateHandler) Guard(ctx *FSMContext) bool {
	return true
}
func (h *ColonizeStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *ColonizeStateHandler) OnExit(ctx *FSMContext, to State)    {}
func (h *ColonizeStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	if ctx.Facts == nil {
		evalCtx := &EvaluatorContext{
			AgentID:       ctx.AgentID,
			Frame:         ctx.Frame,
			Config:        ctx.Config,
			State:         ctx.State,
			CivicDB:       ctx.CivicDB,
			SafeNodes:     ctx.SafeNodes,
			PavingTracker: ctx.PavingTracker,
		}
		ctx.Facts = DefaultRegistry.EvaluateAll(evalCtx)
	}

	skillCtx := &SkillContext{
		AgentID:       ctx.AgentID,
		Frame:         ctx.Frame,
		Config:        ctx.Config,
		State:         ctx.State,
		CivicDB:       ctx.CivicDB,
		SafeNodes:     ctx.SafeNodes,
		PavingTracker: ctx.PavingTracker,
	}

	// Biological needs (eating, sleep, wake)
	needsIntents := EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
	for _, ni := range needsIntents {
		if ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking {
			intents = append(intents, FSMIntent{Intent: ni, Priority: true})
		} else {
			intents = append(intents, FSMIntent{Intent: ni, Priority: false})
		}
	}

	// Road paving (suppressed while 360-degree horizon survey is active)
	if ctx.State == nil || !ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick) {
		if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "infrastructure"); ok && len(skillIntents) > 0 {
			for _, si := range skillIntents {
				if si != nil {
					intents = append(intents, FSMIntent{Intent: si, Priority: false})
				}
			}
		} else if !ctx.Frame.Status.IsSleeping && ctx.PavingTracker != nil &&
			ctx.Frame.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_STUCK &&
			!ctx.Frame.Status.NavigationEnclosed {
			if ctx.State != nil && ctx.PavingTracker.GetHomeOrigin() == nil {
				if home := ctx.State.GetHomeOrigin(); home != nil {
					ctx.PavingTracker.SetHomeOrigin(home)
				}
			}
			if paveIntent := ctx.PavingTracker.EvaluatePaving(ctx.AgentID, ctx.Frame); paveIntent != nil {
				intents = append(intents, FSMIntent{Intent: paveIntent, Priority: false})
			}
		}
	}

	routine := RoutinePrimaryDuty
	if ctx.State != nil {
		routine = ctx.State.Routine()
	}

	if forageIntents, preempted := tryForagingPreemption(ctx, skillCtx); preempted {
		intents = append(intents, forageIntents...)
		return intents
	}

	if (ctx.IsDuskOrNight() || routine == RoutineSleep) && !ctx.HasAssignedBed() && !ctx.IsShelterComplete() && !ctx.IsShelterOnCooldown() {
		if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
			if shelterIntent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); shelterIntent != nil {
				intents = append(intents, FSMIntent{Intent: shelterIntent, Priority: false})
				return intents
			}
		}
	} else if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		if !ctx.IsDuskOrNight() && (ctx.State == nil || ctx.State.Routine() != RoutineSleep) {
			if ctx.Facts != nil && ctx.Facts.GetBool("system2.directive_active") &&
				(HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)) {
				if dirIntent := EvaluateStrategicDirectiveWithID(ctx.AgentID, ctx.Frame, ctx.State, ctx.Config); dirIntent != nil {
					intents = append(intents, FSMIntent{Intent: dirIntent, Priority: false})
					return intents
				}
			}
		}
		// Colonization duties: harvest timber, establish core starter shelter, scout perimeter
		if ctx.State != nil && ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick) {
			return intents
		}
		if bootstrapIntent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); bootstrapIntent != nil {
			intents = append(intents, FSMIntent{Intent: bootstrapIntent, Priority: false})
		} else if ctx.State != nil && ctx.State.HasActiveTask() {
			// Committed TaskSubroutine active (pathing/cooldown); suppress exploration
			return intents
		} else if surveyIntent := EvaluateHorizonExplorer(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); surveyIntent != nil {
			intents = append(intents, FSMIntent{Intent: surveyIntent, Priority: false})
		} else if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "exploration"); ok && len(skillIntents) > 0 {
			for _, si := range skillIntents {
				if si != nil {
					intents = append(intents, FSMIntent{Intent: si, Priority: false})
				}
			}
		} else if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
			intents = append(intents, FSMIntent{Intent: patrolIntent, Priority: false})
		}
	}

	return intents
}

// ShelterStateHandler handles dusk/night outpost shelter construction (legacy support).
type ShelterStateHandler struct{}

func (h *ShelterStateHandler) Name() State { return StateShelter }
func (h *ShelterStateHandler) Guard(ctx *FSMContext) bool {
	return true
}
func (h *ShelterStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *ShelterStateHandler) OnExit(ctx *FSMContext, to State) {
	if ctx != nil && ctx.State != nil && ctx.Frame != nil && to == StageSettlement {
		ctx.State.SetLastShelterTimeoutTick(ctx.Frame.WorldTick)
	}
}
func (h *ShelterStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	if ctx.Facts == nil {
		evalCtx := &EvaluatorContext{
			AgentID:       ctx.AgentID,
			Frame:         ctx.Frame,
			Config:        ctx.Config,
			State:         ctx.State,
			CivicDB:       ctx.CivicDB,
			SafeNodes:     ctx.SafeNodes,
			PavingTracker: ctx.PavingTracker,
		}
		ctx.Facts = DefaultRegistry.EvaluateAll(evalCtx)
	}

	skillCtx := &SkillContext{
		AgentID:       ctx.AgentID,
		Frame:         ctx.Frame,
		Config:        ctx.Config,
		State:         ctx.State,
		CivicDB:       ctx.CivicDB,
		SafeNodes:     ctx.SafeNodes,
		PavingTracker: ctx.PavingTracker,
	}

	// Biological needs (eating, sleep, wake) evaluated via DefaultRegistry / BiologicalNeedsSkill
	var needsIntents []*civquestv1.AgentIntent
	if skill, ok := DefaultRegistry.GetSkill("biological_needs"); ok && skill != nil {
		if skill.Preconditions(ctx.Facts) {
			if si, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
				needsIntents = si
			}
		}
	} else {
		needsIntents = EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
	}
	for _, ni := range needsIntents {
		if ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking {
			intents = append(intents, FSMIntent{Intent: ni, Priority: true})
		} else {
			intents = append(intents, FSMIntent{Intent: ni, Priority: false})
		}
	}

	// Shelter construction (only if not busy with chat/trade)
	if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		if skillIntents, _, ok := DefaultRegistry.SelectAndExecuteSkill(skillCtx, ctx.Facts, "shelter"); ok && len(skillIntents) > 0 {
			for _, si := range skillIntents {
				if si != nil {
					intents = append(intents, FSMIntent{Intent: si, Priority: false})
				}
			}
		} else if shelterIntent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); shelterIntent != nil {
			intents = append(intents, FSMIntent{Intent: shelterIntent, Priority: false})
		}
	}

	return intents
}

// EmergencyStateHandler handles reactive combat, fleeing, and survival reflexes.
type EmergencyStateHandler struct{}

func (h *EmergencyStateHandler) Name() State { return StageEmergency }
func (h *EmergencyStateHandler) Guard(ctx *FSMContext) bool {
	return true
}
func (h *EmergencyStateHandler) OnEntry(ctx *FSMContext, from State) {
	if ctx != nil && ctx.State != nil {
		ctx.State.ClearExpeditionSurveyingIfActive()
	}
}
func (h *EmergencyStateHandler) OnExit(ctx *FSMContext, to State) {}
func (h *EmergencyStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	if ctx.Facts == nil {
		evalCtx := &EvaluatorContext{
			AgentID:       ctx.AgentID,
			Frame:         ctx.Frame,
			Config:        ctx.Config,
			State:         ctx.State,
			CivicDB:       ctx.CivicDB,
			SafeNodes:     ctx.SafeNodes,
			PavingTracker: ctx.PavingTracker,
		}
		ctx.Facts = DefaultRegistry.EvaluateAll(evalCtx)
	}

	skillCtx := &SkillContext{
		AgentID:       ctx.AgentID,
		Frame:         ctx.Frame,
		Config:        ctx.Config,
		State:         ctx.State,
		CivicDB:       ctx.CivicDB,
		SafeNodes:     ctx.SafeNodes,
		PavingTracker: ctx.PavingTracker,
	}

	var skillIntents []*civquestv1.AgentIntent
	if skill, ok := DefaultRegistry.GetSkill("combat_defense"); ok && skill != nil {
		if skill.Preconditions(ctx.Facts) {
			si, err := skill.Execute(skillCtx, ctx.Facts)
			if err == nil {
				skillIntents = si
			}
		}
	}

	if len(skillIntents) == 0 {
		combatSkill := NewCombatDefenseSkill()
		if combatSkill.Preconditions(ctx.Facts) {
			si, err := combatSkill.Execute(skillCtx, ctx.Facts)
			if err == nil {
				skillIntents = si
			}
		} else {
			// Fallback direct execution
			si, err := combatSkill.Execute(skillCtx, ctx.Facts)
			if err == nil {
				skillIntents = si
			}
		}
	}

	for _, intent := range skillIntents {
		intents = append(intents, FSMIntent{
			Intent:   intent,
			Priority: true,
		})
	}

	return intents
}

// RestStateHandler handles sleeping in bed or safe resting through the night (legacy support).
type RestStateHandler struct{}

func (h *RestStateHandler) Name() State { return StateRest }
func (h *RestStateHandler) Guard(ctx *FSMContext) bool {
	// Guard Invariant: REST is strictly blocked when hostiles are present, damage is taken, health is critical, or hunger is critical!
	if ctx == nil {
		return true
	}
	if ctx.HostilesPresent() || ctx.DamageTaken() || ctx.IsHealthCritical() || ctx.IsOnFire() || ctx.IsCriticalHunger() || ctx.ShouldPanic() || ctx.IsDrowning() {
		return false
	}
	return true
}
func (h *RestStateHandler) OnEntry(ctx *FSMContext, from State) {
	if ctx != nil && ctx.State != nil {
		ctx.State.SetRoutine(RoutineSleep)
	}
}
func (h *RestStateHandler) OnExit(ctx *FSMContext, to State) {}
func (h *RestStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	// Biological needs (sleep / wake up intents)
	needsIntents := EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
	for _, ni := range needsIntents {
		if ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking {
			intents = append(intents, FSMIntent{Intent: ni, Priority: true})
		} else {
			intents = append(intents, FSMIntent{Intent: ni, Priority: false})
		}
	}

	return intents
}

// DeadStateHandler handles terminal permadeath state where all agent loops cease.
type DeadStateHandler struct{}

func (h *DeadStateHandler) Name() State                         { return StageDead }
func (h *DeadStateHandler) Guard(ctx *FSMContext) bool          { return true }
func (h *DeadStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *DeadStateHandler) OnExit(ctx *FSMContext, to State)    {}
func (h *DeadStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	return nil
}

// StateStackFrame represents a snapshot of an interrupted state context in the HTPDA pushdown automata stack.
type StateStackFrame struct {
	Stage       Stage             `json:"stage"`
	Routine     Routine           `json:"routine"`
	EnteredTick int64             `json:"entered_tick"`
	Reason      string            `json:"reason"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// FSMResult encapsulates the output of an FSM tick in the Two-Tier HFSM.
type FSMResult struct {
	Stage               Stage
	Routine             Routine
	PreviousStage       Stage
	PreviousRoutine     Routine
	StageTransitioned   bool
	RoutineTransitioned bool
	Transitioned        bool
	FlushPeriphery      bool
	State               State // Backward compatibility alias for Stage
	PreviousState       State // Backward compatibility alias for PreviousStage
	TransitionReason    string
	Intents             []FSMIntent
	GoalHierarchy       *GoalHierarchy
	CombatResult        *ReflexResult
	Facts               *FactGraph
}

// AgentFSM manages the formal Two-Tier Hierarchical Finite State Machine for System 1.
type AgentFSM struct {
	mu                  sync.RWMutex
	currentStage        Stage
	previousStage       Stage
	currentRoutine      Routine
	previousRoutine     Routine
	stateEnteredTick    int64
	routineEnteredTick  int64
	stuckTicks          int64
	lastPos             *civquestv1.Vec3
	handlers            map[Stage]StateHandler
	transitions         []StageTransitionRule
	onStageTransition   func(from, to Stage, reason string)
	onRoutineTransition func(from, to Routine, reason string)
	stack               []StateStackFrame
	maxStackDepth       int
	lastFacts           *FactGraph
}

// LastFacts returns the most recently evaluated FactGraph from Tick.
func (fsm *AgentFSM) LastFacts() *FactGraph {
	if fsm == nil {
		return nil
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.lastFacts
}

// AgentHFSM is an alias for AgentFSM representing the formal Two-Tier Hierarchical FSM.
type AgentHFSM = AgentFSM

// NewAgentFSM constructs an initialized AgentFSM with default state handlers and transition table.
func NewAgentFSM(initial State, onTransition func(from, to State, reason string)) *AgentFSM {
	if initial == "" {
		initial = StageBootstrap
	}
	initialRoutine := RoutineDawnPrep
	if initial == StageEmergency {
		initialRoutine = RoutineEmergency
	} else if initial == StageDead {
		initialRoutine = RoutineDead
	}
	fsm := &AgentFSM{
		currentStage:      initial,
		previousStage:     initial,
		currentRoutine:    initialRoutine,
		previousRoutine:   initialRoutine,
		maxStackDepth:     8,
		handlers:          make(map[Stage]StateHandler),
		transitions:       defaultTransitionRules(),
		onStageTransition: onTransition,
	}
	fsm.RegisterHandler(&BootstrapStateHandler{})
	fsm.RegisterHandler(&SettlementStateHandler{})
	fsm.RegisterHandler(&ColonizeStateHandler{})
	fsm.RegisterHandler(&EmergencyStateHandler{})
	fsm.RegisterHandler(&ShelterStateHandler{})
	fsm.RegisterHandler(&RestStateHandler{})
	fsm.RegisterHandler(&DeadStateHandler{})
	return fsm
}

func (fsm *AgentFSM) pushLocked(stage Stage, routine Routine, reason string, tick int64, metadata map[string]string) {
	if stage == "" {
		return
	}
	// Check duplicate top: ignore if duplicate top (same Stage and Routine)
	if n := len(fsm.stack); n > 0 {
		top := fsm.stack[n-1]
		if top.Stage == stage && top.Routine == routine {
			return
		}
	}

	maxDepth := fsm.maxStackDepth
	if maxDepth <= 0 {
		maxDepth = 8
	}

	// Drop oldest if overflow
	if len(fsm.stack) >= maxDepth {
		fsm.stack = fsm.stack[1:]
	}

	var metaCopy map[string]string
	if metadata != nil {
		metaCopy = make(map[string]string, len(metadata))
		for k, v := range metadata {
			metaCopy[k] = v
		}
	}

	fsm.stack = append(fsm.stack, StateStackFrame{
		Stage:       stage,
		Routine:     routine,
		EnteredTick: tick,
		Reason:      reason,
		Metadata:    metaCopy,
	})
}

func (fsm *AgentFSM) popLocked() (StateStackFrame, bool) {
	n := len(fsm.stack)
	if n == 0 {
		return StateStackFrame{}, false
	}
	frame := fsm.stack[n-1]
	fsm.stack = fsm.stack[:n-1]
	return frame, true
}

func (fsm *AgentFSM) peekLocked() (StateStackFrame, bool) {
	n := len(fsm.stack)
	if n == 0 {
		return StateStackFrame{}, false
	}
	return fsm.stack[n-1], true
}

func (fsm *AgentFSM) stackDepthLocked() int {
	return len(fsm.stack)
}

func (fsm *AgentFSM) clearStackLocked() {
	fsm.stack = nil
}

// Push pushes a state stack frame onto the HTPDA stack. Caps at maxStackDepth, dropping oldest on overflow, or ignoring if duplicate top.
func (fsm *AgentFSM) Push(stage Stage, routine Routine, reason string, tick int64, metadata map[string]string) {
	if fsm == nil {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.pushLocked(stage, routine, reason, tick, metadata)
}

// Pop pops the topmost state stack frame from the HTPDA stack. Returns false if empty.
func (fsm *AgentFSM) Pop() (StateStackFrame, bool) {
	if fsm == nil {
		return StateStackFrame{}, false
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	return fsm.popLocked()
}

// Peek inspects the topmost state stack frame without removing it. Returns false if empty.
func (fsm *AgentFSM) Peek() (StateStackFrame, bool) {
	if fsm == nil {
		return StateStackFrame{}, false
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.peekLocked()
}

// StackDepth returns the number of frames currently stored in the HTPDA state stack.
func (fsm *AgentFSM) StackDepth() int {
	if fsm == nil {
		return 0
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.stackDepthLocked()
}

// StackFrames returns a snapshot copy of the current HTPDA state stack frames from bottom to top.
func (fsm *AgentFSM) StackFrames() []StateStackFrame {
	if fsm == nil {
		return nil
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	if len(fsm.stack) == 0 {
		return nil
	}
	cpy := make([]StateStackFrame, len(fsm.stack))
	copy(cpy, fsm.stack)
	return cpy
}

// ClearStack empties the HTPDA state stack.
func (fsm *AgentFSM) ClearStack() {
	if fsm == nil {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.clearStackLocked()
}

// SetMaxStackDepth updates the maximum stack capacity.
func (fsm *AgentFSM) SetMaxStackDepth(depth int) {
	if fsm == nil {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if depth > 0 {
		fsm.maxStackDepth = depth
	}
}

// RegisterHandler registers a lifecycle handler for an FSM state.
func (fsm *AgentFSM) RegisterHandler(handler StateHandler) {
	if fsm == nil || handler == nil {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.handlers[handler.Name()] = handler
}

// CurrentStage returns the active Tier 1 macro strategic stage.
func (fsm *AgentFSM) CurrentStage() Stage {
	if fsm == nil {
		return StageBootstrap
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.currentStage
}

// PreviousStage returns the previous Tier 1 macro strategic stage.
func (fsm *AgentFSM) PreviousStage() Stage {
	if fsm == nil {
		return StageBootstrap
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.previousStage
}

// CurrentRoutine returns the active Tier 2 circadian operational routine.
func (fsm *AgentFSM) CurrentRoutine() Routine {
	if fsm == nil {
		return RoutineDawnPrep
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.currentRoutine
}

// PreviousRoutine returns the previous Tier 2 circadian operational routine.
func (fsm *AgentFSM) PreviousRoutine() Routine {
	if fsm == nil {
		return RoutineDawnPrep
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.previousRoutine
}

// CurrentState returns the active state (alias for CurrentStage for backward compatibility).
func (fsm *AgentFSM) CurrentState() State {
	return fsm.CurrentStage()
}

// PreviousState returns the previous state (alias for PreviousStage for backward compatibility).
func (fsm *AgentFSM) PreviousState() State {
	return fsm.PreviousStage()
}

// StateTuple returns the unified (Stage, Routine) tuple.
func (fsm *AgentFSM) StateTuple() StateTuple {
	if fsm == nil {
		return StateTuple{Stage: StageBootstrap, Routine: RoutineDawnPrep}
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return StateTuple{Stage: fsm.currentStage, Routine: fsm.currentRoutine}
}

// TransitionTo forces a state transition if the target state's guard allows it.
func (fsm *AgentFSM) TransitionTo(target State, reason string, ctx *FSMContext) bool {
	if fsm == nil {
		return false
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()

	// Invariant: Terminal Dead state cannot be transitioned out of
	if fsm.currentStage == StageDead {
		return false
	}

	if fsm.currentStage == target {
		return false
	}

	// Invariant: Non-emergency target is rejected if emergency conditions are active
	if target != StageEmergency && target != StageDead && ctx != nil && ctx.IsEmergency() {
		return false
	}

	targetHandler, ok := fsm.handlers[target]
	if ok && ctx != nil && !targetHandler.Guard(ctx) {
		return false
	}

	fromStage := fsm.currentStage

	// When transitioning into StageEmergency from any non-emergency stage,
	// automatically push the current (currentStage, currentRoutine, reason, tick, metadata) onto the stack!
	if target == StageEmergency && fromStage != StageEmergency && fromStage != StageDead {
		tick := int64(0)
		if ctx != nil && ctx.Frame != nil {
			tick = ctx.Frame.WorldTick
		}
		var taskMeta map[string]string
		if ctx != nil && ctx.State != nil && ctx.State.HasActiveTask() {
			taskMeta = ctx.State.HandleEmergencyPreempt(tick)
		}
		fsm.pushLocked(fromStage, fsm.currentRoutine, reason, tick, taskMeta)
	}

	// Terminal dead state: clear stack and any active task
	if target == StageDead {
		fsm.clearStackLocked()
		if ctx != nil && ctx.State != nil {
			ctx.State.ClearActiveTask()
		}
	}

	if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
		currentHandler.OnExit(ctx, target)
	}

	fsm.previousStage = fromStage
	fsm.currentStage = target
	if ctx != nil && ctx.Frame != nil {
		fsm.stateEnteredTick = ctx.Frame.WorldTick
	}
	fsm.stuckTicks = 0

	if target == StageEmergency {
		fsm.previousRoutine = fsm.currentRoutine
		fsm.currentRoutine = RoutineEmergency
	} else if target == StageDead {
		fsm.previousRoutine = fsm.currentRoutine
		fsm.currentRoutine = RoutineDead
	} else if ctx != nil && ctx.Frame != nil {
		newR := computeStageCircadianRoutine(target, ctx.Frame.TimeOfDay, ctx.Frame.Weather, ctx.Config, ctx.State, ctx)
		if newR != "" {
			fsm.previousRoutine = fsm.currentRoutine
			fsm.currentRoutine = newR
		}
	}

	if targetHandler != nil && ctx != nil {
		targetHandler.OnEntry(ctx, fromStage)
	}

	if fsm.onStageTransition != nil {
		fsm.onStageTransition(fromStage, target, reason)
	}

	if ctx != nil && ctx.State != nil {
		ctx.State.SetStageAndRoutine(target, fsm.currentRoutine)
	}

	return true
}

// Tick executes a single 5 Hz evaluation cycle of the formal Two-Tier HFSM.
func (fsm *AgentFSM) Tick(ctx *FSMContext) FSMResult {
	if fsm == nil {
		return FSMResult{Stage: StageBootstrap, Routine: RoutineDawnPrep, State: StageBootstrap}
	}

	if ctx != nil {
		evalCtx := &EvaluatorContext{
			AgentID:       ctx.AgentID,
			Frame:         ctx.Frame,
			Config:        ctx.Config,
			State:         ctx.State,
			CivicDB:       ctx.CivicDB,
			SafeNodes:     ctx.SafeNodes,
			PavingTracker: ctx.PavingTracker,
		}
		ctx.Facts = DefaultRegistry.EvaluateAll(evalCtx)
	}

	fsm.mu.Lock()
	defer fsm.mu.Unlock()

	if ctx != nil {
		fsm.lastFacts = ctx.Facts
	}

	worldTick := int64(0)
	tod := int64(0)
	var weather civquestv1.Weather
	if ctx != nil && ctx.Frame != nil {
		worldTick = ctx.Frame.WorldTick
		tod = ctx.Frame.TimeOfDay
		weather = ctx.Frame.Weather
		if ctx.State != nil && ctx.Frame.GetReceipt() != nil {
			ctx.State.IngestReceipt(ctx.Frame.GetReceipt())
		}
	}

	// 0. Terminal Dead State Check:
	// When ctx.Frame.Status.Health <= 0 or death detected:
	// - fsm.ClearStack()
	// - Transition to StageDead / RoutineDead.
	isDead := false
	if fsm.currentStage == StageDead {
		isDead = true
	} else if ctx != nil && ctx.Frame != nil {
		if ctx.Frame.DeathEvent != nil || (ctx.Frame.Status != nil && ctx.Frame.Status.Health <= 0) {
			isDead = true
		}
	}

	if isDead {
		fsm.clearStackLocked()
		if ctx != nil && ctx.State != nil {
			ctx.State.ClearActiveTask()
		}
		if fsm.currentStage != StageDead {
			fromStage := fsm.currentStage
			if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
				currentHandler.OnExit(ctx, StageDead)
			}
			fsm.previousStage = fromStage
			fsm.currentStage = StageDead
			fsm.stateEnteredTick = worldTick
			fsm.stuckTicks = 0
			if ctx != nil {
				ctx.TimeInState = 0
				ctx.StuckTicks = 0
			}
			prevR := fsm.currentRoutine
			fsm.previousRoutine = prevR
			fsm.currentRoutine = RoutineDead
			fsm.routineEnteredTick = worldTick

			if targetHandler, exists := fsm.handlers[StageDead]; exists && targetHandler != nil && ctx != nil {
				targetHandler.OnEntry(ctx, fromStage)
			}
			if fsm.onStageTransition != nil {
				fsm.onStageTransition(fromStage, StageDead, "terminal agent death detected")
			}
			if fsm.onRoutineTransition != nil {
				fsm.onRoutineTransition(prevR, RoutineDead, "terminal agent death detected")
			}
			if ctx != nil && ctx.State != nil {
				ctx.State.SetStageAndRoutine(StageDead, RoutineDead)
			}
			return FSMResult{
				Stage:               StageDead,
				Routine:             RoutineDead,
				PreviousStage:       fromStage,
				PreviousRoutine:     prevR,
				StageTransitioned:   true,
				RoutineTransitioned: true,
				Transitioned:        true,
				FlushPeriphery:      true,
				State:               StageDead,
				PreviousState:       fromStage,
				TransitionReason:    "terminal agent death detected",
				Facts:               fsm.lastFacts,
			}
		}

		if ctx != nil && ctx.State != nil {
			ctx.State.SetStageAndRoutine(StageDead, RoutineDead)
		}
		return FSMResult{
			Stage:           StageDead,
			Routine:         RoutineDead,
			PreviousStage:   fsm.previousStage,
			PreviousRoutine: fsm.previousRoutine,
			State:           StageDead,
			PreviousState:   fsm.previousStage,
			Facts:           fsm.lastFacts,
		}
	}

	// 1. Track position & stuckness
	if ctx != nil && ctx.Frame != nil && ctx.Frame.Status != nil && ctx.Frame.Status.Position != nil {
		pos := ctx.Frame.Status.Position
		if fsm.lastPos != nil {
			dx := pos.X - fsm.lastPos.X
			dy := pos.Y - fsm.lastPos.Y
			dz := pos.Z - fsm.lastPos.Z
			if dx*dx+dy*dy+dz*dz < 0.04 || ctx.Frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
				// If actively placing blocks, harvesting, or crafting, agent is actively engaged and not stuck
				activeProgress := false
				if ctx.State != nil {
					wt := ctx.Frame.WorldTick
					if (ctx.State.GetLastShelterPlaceTick() > 0 && wt-ctx.State.GetLastShelterPlaceTick() < 100) ||
						(ctx.State.GetLastHarvestTick() > 0 && wt-ctx.State.GetLastHarvestTick() < 100) ||
						(ctx.State.GetLastCraftTick() > 0 && wt-ctx.State.GetLastCraftTick() < 100) {
						activeProgress = true
					}
				}
				if activeProgress {
					fsm.stuckTicks = 0
					fsm.stateEnteredTick = worldTick
				} else {
					fsm.stuckTicks++
				}
			} else {
				fsm.stuckTicks = 0
			}
			fsm.lastPos.X = pos.X
			fsm.lastPos.Y = pos.Y
			fsm.lastPos.Z = pos.Z
		} else {
			fsm.lastPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		}
	}
	if ctx != nil {
		ctx.StuckTicks = fsm.stuckTicks
		if fsm.stateEnteredTick == 0 {
			fsm.stateEnteredTick = worldTick
		}
		ctx.TimeInState = worldTick - fsm.stateEnteredTick
		if ctx.TimeInState < 0 {
			ctx.TimeInState = 0
		}
	}

	// 2. Evaluate Tier 1 Macro Stage Transitions
	stageTransitioned := false
	routineTransitioned := false
	pdaRestored := false
	var transitionReason string

	// Priority 1: EMERGENCY Preemption from any non-emergency stage (PDA pushdown)
	if fsm.currentStage != StageEmergency && fsm.currentStage != StageDead && ctx != nil && ctx.IsEmergency() {
		fromStage := fsm.currentStage
		toStage := StageEmergency
		ruleDesc := "Hostile threat, damage, or critical vitals detected; preempting to emergency"

		var taskMeta map[string]string
		if ctx != nil && ctx.State != nil && ctx.State.HasActiveTask() {
			taskMeta = ctx.State.HandleEmergencyPreempt(worldTick)
		}
		// Automatically push current state onto the stack before switching to emergency
		fsm.pushLocked(fromStage, fsm.currentRoutine, ruleDesc, worldTick, taskMeta)

		if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
			currentHandler.OnExit(ctx, toStage)
		}

		fsm.previousStage = fromStage
		fsm.currentStage = toStage
		fsm.stateEnteredTick = worldTick
		fsm.stuckTicks = 0
		if ctx != nil {
			ctx.TimeInState = 0
			ctx.StuckTicks = 0
		}
		stageTransitioned = true
		transitionReason = ruleDesc

		if targetHandler, exists := fsm.handlers[toStage]; exists && targetHandler != nil && ctx != nil {
			targetHandler.OnEntry(ctx, fromStage)
		}

		if fsm.onStageTransition != nil {
			fsm.onStageTransition(fromStage, toStage, ruleDesc)
		}
	}

	// Pushdown Automata check:
	// In StageEmergency transition rules:
	// When c.ThreatsCleared() is true:
	// First check if frame, ok := fsm.Pop() succeeds. If ok, transition directly back to frame.Stage
	// (and restore frame.Routine in state)!
	// Only if the stack was empty does it fall back to the existing rules.
	if !stageTransitioned && fsm.currentStage == StageEmergency && ctx != nil && ctx.ThreatsCleared() {
		if frame, ok := fsm.popLocked(); ok {
			fromStage := fsm.currentStage
			toStage := frame.Stage

			if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
				currentHandler.OnExit(ctx, toStage)
			}

			fsm.previousStage = fromStage
			fsm.currentStage = toStage
			fsm.stateEnteredTick = worldTick
			fsm.stuckTicks = 0
			if ctx != nil {
				ctx.TimeInState = 0
				ctx.StuckTicks = 0
			}
			stageTransitioned = true
			transitionReason = "Threats cleared; restored previous state from PDA stack (" + string(toStage) + ")"

			if targetHandler, exists := fsm.handlers[toStage]; exists && targetHandler != nil && ctx != nil {
				targetHandler.OnEntry(ctx, fromStage)
			}

			if fsm.onStageTransition != nil {
				fsm.onStageTransition(fromStage, toStage, transitionReason)
			}

			// Restore frame.Routine directly from the stack
			prevR := fsm.currentRoutine
			fsm.previousRoutine = prevR
			fsm.currentRoutine = frame.Routine
			fsm.routineEnteredTick = worldTick
			if prevR != frame.Routine {
				routineTransitioned = true
				if fsm.onRoutineTransition != nil {
					fsm.onRoutineTransition(prevR, frame.Routine, "restored routine from PDA stack")
				}
			}
			pdaRestored = true

			if ctx.State != nil {
				ctx.State.ResumeActiveTask(worldTick)
				ctx.State.SetRoutine(frame.Routine)
				ctx.State.SetStageAndRoutine(toStage, frame.Routine)
			}
		}
	}

	if !stageTransitioned {
		for _, rule := range fsm.transitions {
			if rule.FromStage == fsm.currentStage && rule.Condition(ctx) {
				targetHandler, ok := fsm.handlers[rule.ToStage]
				if !ok || targetHandler.Guard(ctx) {
					fromStage := fsm.currentStage
					toStage := rule.ToStage

					// PDA State Stack: When transitioning into StageEmergency from any non-emergency stage,
					// automatically push the current (currentStage, currentRoutine, reason, tick, metadata) onto the stack!
					if toStage == StageEmergency && fromStage != StageEmergency && fromStage != StageDead {
						var taskMeta map[string]string
						if ctx != nil && ctx.State != nil && ctx.State.HasActiveTask() {
							taskMeta = ctx.State.HandleEmergencyPreempt(worldTick)
						}
						fsm.pushLocked(fromStage, fsm.currentRoutine, rule.Description, worldTick, taskMeta)
					}

					if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
						currentHandler.OnExit(ctx, toStage)
					}

					fsm.previousStage = fromStage
					fsm.currentStage = toStage
					fsm.stateEnteredTick = worldTick
					fsm.stuckTicks = 0
					if ctx != nil {
						ctx.TimeInState = 0
						ctx.StuckTicks = 0
					}
					stageTransitioned = true
					transitionReason = rule.Description

					if targetHandler != nil && ctx != nil {
						targetHandler.OnEntry(ctx, fromStage)
					}

					if fsm.onStageTransition != nil {
						fsm.onStageTransition(fromStage, toStage, rule.Description)
					}
					break
				}
			}
		}
	}

	// Capture pre-Step-3 routine in case a TaskSubroutine was installed externally before Tick.
	preStep3Routine := fsm.currentRoutine
	taskActiveBeforeStep3 := false

	// 3. Evaluate Tier 2 Circadian Operational Routine
	if !pdaRestored {
		var newRoutine Routine
		if fsm.currentStage == StageEmergency {
			newRoutine = RoutineEmergency
		} else if fsm.currentStage == StageDead {
			newRoutine = RoutineDead
		} else if fsm.currentStage == Stage("DIALOGUE") || fsm.currentRoutine == Routine("DIALOGUE") {
			newRoutine = fsm.currentRoutine
		} else if ctx != nil && ctx.State != nil {
			if active, _, _, taskRoutine := ctx.State.ActiveTaskInfo(); active && taskRoutine != "" {
				taskActiveBeforeStep3 = true
				newRoutine = taskRoutine
			} else {
				newRoutine = computeStageCircadianRoutine(fsm.currentStage, tod, weather, ctx.Config, ctx.State, ctx)
			}
		} else {
			var cfg *xdg.AgentIdentityConfig
			if ctx != nil {
				cfg = ctx.Config
			}
			newRoutine = computeStageCircadianRoutine(fsm.currentStage, tod, weather, cfg, nil, ctx)
		}

		if newRoutine != fsm.currentRoutine {
			prevR := fsm.currentRoutine
			fsm.previousRoutine = prevR
			fsm.currentRoutine = newRoutine
			fsm.routineEnteredTick = worldTick
			routineTransitioned = true
			if fsm.onRoutineTransition != nil {
				fsm.onRoutineTransition(prevR, newRoutine, "circadian schedule progression")
			}
		}
	}

	// 4. Synchronize AgentState atomically
	if ctx != nil && ctx.State != nil {
		ctx.State.SetStageAndRoutine(fsm.currentStage, fsm.currentRoutine)
	}

	preOnTickRoutine := fsm.currentRoutine

	// 5. Execute OnTick for the active Stage handler
	var intents []FSMIntent
	if handler, exists := fsm.handlers[fsm.currentStage]; exists && ctx != nil {
		intents = handler.OnTick(ctx)
	}

	// 5b. Synchronize active TaskSubroutine commitment with the PDA state stack
	if ctx != nil && ctx.State != nil && fsm.currentStage != StageEmergency && fsm.currentStage != StageDead {
		active, taskKind, _, taskRoutine := ctx.State.ActiveTaskInfo()
		topHasTaskFrame := len(fsm.stack) > 0 && fsm.stack[len(fsm.stack)-1].Metadata != nil &&
			fsm.stack[len(fsm.stack)-1].Metadata["task_frame"] == "true"

		if active {
			if !topHasTaskFrame && !ctx.State.IsActiveTaskPushedToPDA() {
				parentR := preOnTickRoutine
				if taskActiveBeforeStep3 {
					parentR = preStep3Routine
				}
				if parentR == "" || parentR == RoutineHarvestTree || parentR == RoutineCollectItems ||
					parentR == RoutineEmergency || parentR == RoutineDead {
					parentR = computeStageCircadianRoutine(fsm.currentStage, tod, weather, ctx.Config, ctx.State, ctx)
				}
				meta := ctx.State.GetActiveTaskMetadata()
				fsm.pushLocked(fsm.currentStage, parentR, "task subroutine committed: "+taskKind, worldTick, meta)
				ctx.State.SetActiveTaskPushedToPDA(true)
			}
			if taskRoutine != "" && fsm.currentRoutine != taskRoutine {
				prevR := fsm.currentRoutine
				fsm.previousRoutine = prevR
				fsm.currentRoutine = taskRoutine
				fsm.routineEnteredTick = worldTick
				routineTransitioned = true
				if fsm.onRoutineTransition != nil {
					fsm.onRoutineTransition(prevR, taskRoutine, "task subroutine active: "+taskKind)
				}
				ctx.State.SetStageAndRoutine(fsm.currentStage, fsm.currentRoutine)
			}
		} else if topHasTaskFrame {
			if popped, ok := fsm.popLocked(); ok {
				restoredR := popped.Routine
				if restoredR == "" || restoredR == RoutineHarvestTree || restoredR == RoutineCollectItems ||
					restoredR == RoutineEmergency || restoredR == RoutineDead {
					restoredR = computeStageCircadianRoutine(fsm.currentStage, tod, weather, ctx.Config, ctx.State, ctx)
				}
				if restoredR != fsm.currentRoutine {
					prevR := fsm.currentRoutine
					fsm.previousRoutine = prevR
					fsm.currentRoutine = restoredR
					fsm.routineEnteredTick = worldTick
					routineTransitioned = true
					if fsm.onRoutineTransition != nil {
						fsm.onRoutineTransition(prevR, restoredR, "task subroutine completed/popped")
					}
				}
				ctx.State.SetStageAndRoutine(fsm.currentStage, fsm.currentRoutine)
			}
		}
	}

	// 6. Evaluate Goals
	var goals GoalHierarchy
	if ctx != nil {
		goals = EvaluateGoals(ctx.Frame, ctx.Config, ctx.State)
	}

	stageTransitioned = stageTransitioned && fsm.previousStage != fsm.currentStage
	if !stageTransitioned {
		transitionReason = ""
	}

	return FSMResult{
		Stage:               fsm.currentStage,
		Routine:             fsm.currentRoutine,
		PreviousStage:       fsm.previousStage,
		PreviousRoutine:     fsm.previousRoutine,
		StageTransitioned:   stageTransitioned,
		RoutineTransitioned: routineTransitioned,
		Transitioned:        stageTransitioned,
		FlushPeriphery:      routineTransitioned || stageTransitioned,
		State:               fsm.currentStage,
		PreviousState:       fsm.previousStage,
		TransitionReason:    transitionReason,
		Intents:             intents,
		GoalHierarchy:       &goals,
		Facts:               fsm.lastFacts,
	}
}

func computeStageCircadianRoutine(
	stage Stage,
	tod int64,
	weather civquestv1.Weather,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
	ctx *FSMContext,
) Routine {
	routine := ComputeCircadianRoutine(tod, weather, cfg, state)
	shelterSatisfied := isStateShelterOrBedSatisfied(state)
	if !shelterSatisfied && ctx != nil {
		shelterSatisfied = ctx.HasAssignedBed() || ctx.IsShelterComplete()
	}
	return gateBootstrapCircadianRoutine(stage, routine, state, shelterSatisfied)
}
