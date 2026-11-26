package system1

import (
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// FSMIntent encapsulates an emitted intent alongside its priority classification.
type FSMIntent struct {
	Intent   *civquestv1.AgentIntent
	Priority bool
	// Frame is the activity frame that emitted the intent (0 = not frame-owned, e.g. biological
	// needs). The runtime stamps it onto the outgoing IntentBatch.frame_id (ADR-036 §5).
	Frame uint64
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
	Registry       *EngineRegistry
}

func resolveContextRegistry(ctx *FSMContext) *EngineRegistry {
	if ctx != nil && ctx.Registry != nil {
		return ctx.Registry
	}
	reg := NewDefaultEngineRegistry()
	if ctx != nil {
		ctx.Registry = reg
	}
	return reg
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
	return isDrowningDamage(c.Frame) || sustainedDrowning(c.Frame)
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

// HasAssignedBed returns true if the frozen shelter.has_bed fact (or, without facts, state) records a bed.
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
	return c.State.GetCabinBuilt() >= blueprints.CabinTotalBlocks
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
