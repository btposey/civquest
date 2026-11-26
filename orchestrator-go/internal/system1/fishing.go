package system1

// Fishing as a food source inside the foraging waterfall (bead cq-390.4, ADR-036 foraging frame).
//
// FishingActivity is not a separate frame: it is a bidder in the Foraging frame. Each tick it
// reports a fishing option with a utility (food yield discounted by distance) and the foraging
// waterfall takes it when it beats the best plant/land-animal option, or when a line is already
// cast (a cast line is never abandoned for another source). Two modes:
//
//	(a) no string / no rod: melee a visible cod or salmon from the shore (axe/sword/fists);
//	(b) rod: craft one (3 sticks + 2 string, 3x3 table recipe) when string is carried, cast at a
//	    surface-water cell, hold while the bobber floats, reel in on a bite. A bobber that stays
//	    out of the water is reeled in and recast, up to fishMaxDryCasts per frame.
//
// Cooking is not modelled (no furnace/campfire intent exists yet): raw fish are eaten under the
// ordinary hunger gate (EvaluateHunger), so "raw only when hungry" holds.

import (
	"fmt"
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// DESIGN BUDGET (cq-390.4): every number below is a tuned design choice, not a measurement.
const (
	// Utility = yield / (1 + dist/fishDistScale). Yields are in "food points per attempt", rod
	// fishing weighted highest: the user's direction is that fishing is a very good food choice.
	fishRodYield   = 8.0 // a rod gives a steady stream once cast
	fishMeleeYield = 4.0 // a swimming fish, hit by hand
	fishDistScale  = 10.0

	fishMeleeReach    = 3.5  // Java melee reach is 4.5
	fishSeekRadius    = 20.0 // fish farther than this are ignored
	fishWaterRadius   = 16.0 // surface water cells beyond this are not considered
	fishCastMin       = 2.0  // closer than this the bobber lands at our feet
	fishCastMax       = 6.0  // the bobber flies about this far
	fishStandoff      = 4.0  // stand this far back from the water edge when approaching to cast
	fishMeleeStandoff = 2.5  // approach a fish to this distance (stay on the shore)
	fishActionTicks   = int64(10)
	fishCraftTicks    = int64(40) // craft latency allowance before re-issuing a craft
	fishFlightTicks   = int64(40) // the bobber is airborne this long after a cast: never reel for "dry"
	fishDryTicks      = int64(20) // and must then stay out of the water this long (it bobs)
	fishMaxDryCasts   = 4         // give up rod fishing in this frame after this many misses
	fishTableReach    = 4.0
)

// fishLocal is the fishing cursor held in the Foraging frame Local.
type fishLocal struct {
	LastCraftTick int64
	LastCastTick  int64
	DrySince      int64 // first tick the cast bobber was seen out of water (0: floating)
	DryCasts      int   // casts that missed the water since the last bite
}

// fishingLineCast reports whether the agent's line is out.
func fishingLineCast(frame *civquestv1.TelemetryFrame) bool {
	return frame != nil && frame.Status != nil && frame.Status.Fishing != nil && frame.Status.Fishing.Cast
}

func isFishEntity(e *civquestv1.VisibleEntity) bool {
	if e == nil || e.Position == nil || IsHostile(e) {
		return false
	}
	switch bareID(e.EntityType) {
	case "cod", "salmon":
		return true
	}
	return false
}

func isSurfaceWaterBlock(b *civquestv1.VisibleBlock) bool {
	return b != nil && b.Pos != nil && bareID(b.BlockId) == "water"
}

// nearestSurfaceWater returns the nearest visible surface-water cell within fishWaterRadius.
func nearestSurfaceWater(frame *civquestv1.TelemetryFrame) (b *civquestv1.VisibleBlock, horiz float64) {
	pos := frame.Status.Position
	horiz = math.MaxFloat64
	for _, c := range frame.InterestingBlocks {
		if !isSurfaceWaterBlock(c) || blockDistance(pos, c) > fishWaterRadius {
			continue
		}
		h := math.Hypot(float64(c.Pos.X)+0.5-pos.X, float64(c.Pos.Z)+0.5-pos.Z)
		if h < horiz {
			b, horiz = c, h
		}
	}
	return
}

func nearestFish(frame *civquestv1.TelemetryFrame) (f *civquestv1.VisibleEntity, dist float64) {
	dist = math.MaxFloat64
	for _, e := range frame.VisibleEntities {
		if !isFishEntity(e) {
			continue
		}
		if d := entityDistance(frame.Status.Position, e); d <= fishSeekRadius && d < dist {
			f, dist = e, d
		}
	}
	return
}

func craftTableNear(frame *civquestv1.TelemetryFrame) bool {
	for _, b := range frame.InterestingBlocks {
		if b != nil && b.Pos != nil && bareID(b.BlockId) == "crafting_table" && blockDistance(frame.Status.Position, b) <= fishTableReach {
			return true
		}
	}
	return false
}

func fishingUtility(yield, dist float64) float64 { return yield / (1 + dist/fishDistScale) }

// fishingOption is what the fishing bidder offers this tick. A nil intent means "hold": stay on
// this option and emit nothing (a line floating, a cooldown, a path already under way).
type fishingOption struct {
	utility float64
	intent  *civquestv1.AgentIntent
}

// evaluateFishing computes the fishing option; ok=false means fishing is not possible now.
func evaluateFishing(agentID string, frame *civquestv1.TelemetryFrame, mem *forageLocal, speedMult float32, sprint bool) (fishingOption, bool) {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil {
		return fishingOption{}, false
	}
	fl := &mem.Fish
	pos := frame.Status.Position
	inv := frame.Status.Inventory
	navState := frame.Status.NavigationState
	hasRod := hasInventoryItem(inv, "fishing_rod")

	useItem := func(mode civquestv1.UseItemIntent_Mode, aim *civquestv1.Vec3) *civquestv1.AgentIntent {
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_UseItem{
			UseItem: &civquestv1.UseItemIntent{Mode: mode, AimAt: aim}}}
	}
	navigate := func(key string, x, y, z float64) *civquestv1.AgentIntent {
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && mem.LastTargetKey == key {
			return nil
		}
		mem.LastTargetKey = key
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target: &civquestv1.Vec3{X: x, Y: y, Z: z}, SpeedMultiplier: speedMult, Sprint: sprint, ArrivalTolerance: 1,
		}}}
	}
	// standOff is the point `back` blocks short of (tx, tz) on the line from the agent.
	standOff := func(tx, tz, back float64) (float64, float64) {
		dx, dz := tx-pos.X, tz-pos.Z
		d := math.Hypot(dx, dz)
		if d <= back || d < 1e-6 {
			return pos.X, pos.Z
		}
		return tx - dx/d*back, tz - dz/d*back
	}

	// A line is out: reel on a bite, reset a bobber that missed the water, otherwise hold.
	if fishingLineCast(frame) {
		fs := frame.Status.Fishing
		opt := fishingOption{utility: math.MaxFloat64}
		switch {
		case fs.Bite:
			fl.DrySince, fl.DryCasts = 0, 0
			opt.intent = useItem(civquestv1.UseItemIntent_REEL_FISHING_ROD, nil)
		case fs.InWater:
			fl.DrySince = 0
		case fl.DrySince == 0:
			fl.DrySince = frame.WorldTick
		case frame.WorldTick-fl.DrySince >= fishDryTicks && frame.WorldTick-fl.LastCastTick >= fishFlightTicks:
			fl.DrySince = 0
			fl.DryCasts++
			opt.intent = useItem(civquestv1.UseItemIntent_REEL_FISHING_ROD, nil)
		}
		return opt, true
	}

	water, waterDist := nearestSurfaceWater(frame)
	// Mode (b): rod in hand and water in view.
	if hasRod && water != nil && fl.DryCasts < fishMaxDryCasts {
		opt := fishingOption{utility: fishingUtility(fishRodYield, waterDist)}
		wx, wz := float64(water.Pos.X)+0.5, float64(water.Pos.Z)+0.5
		if waterDist >= fishCastMin && waterDist <= fishCastMax && !frame.Status.IsInWater {
			if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
				// Stand still first: a cast on the move walks off from the line.
				opt.intent = navigate("fish_stop", pos.X, pos.Y, pos.Z)
				return opt, true
			}
			if frame.WorldTick-fl.LastCastTick >= fishActionTicks {
				fl.LastCastTick = frame.WorldTick
				opt.intent = useItem(civquestv1.UseItemIntent_CAST_FISHING_ROD, &civquestv1.Vec3{X: wx, Y: float64(water.Pos.Y) + 0.5, Z: wz})
			}
			return opt, true
		}
		sx, sz := wx, wz
		if waterDist > fishCastMax {
			sx, sz = standOff(wx, wz, fishStandoff)
		} else {
			// Too close (or wading): back away from the water to casting distance.
			sx, sz = pos.X+(pos.X-wx), pos.Z+(pos.Z-wz)
			if d := math.Hypot(sx-pos.X, sz-pos.Z); d > 1e-6 {
				sx, sz = pos.X+(sx-pos.X)/d*fishStandoff, pos.Z+(sz-pos.Z)/d*fishStandoff
			}
		}
		opt.intent = navigate(fmt.Sprintf("fish_shore:%.0f,%.0f", sx, sz), sx, pos.Y, sz)
		return opt, true
	}

	// Mode (b'): string in hand, no rod: craft one beside a table (3 sticks + 2 string, 3x3).
	if !hasRod && water != nil && countInventoryItem(inv, "string") >= 2 && craftTableNear(frame) {
		opt := fishingOption{utility: fishingUtility(fishRodYield*0.8, waterDist)}
		if frame.WorldTick-fl.LastCraftTick < fishCraftTicks {
			return opt, true
		}
		recipe := ""
		switch {
		case countInventoryItem(inv, "stick") >= 3:
			recipe = "minecraft:fishing_rod"
		case planksCarried(inv) >= 2:
			recipe = "minecraft:stick"
		}
		if recipe != "" {
			fl.LastCraftTick = frame.WorldTick
			opt.intent = &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_CraftItem{
				CraftItem: &civquestv1.CraftItemIntent{RecipeId: recipe, BatchCount: 1}}}
			return opt, true
		}
	}

	// Mode (a): melee a fish from the shore.
	if fish, d := nearestFish(frame); fish != nil {
		opt := fishingOption{utility: fishingUtility(fishMeleeYield, d)}
		key := fmt.Sprintf("fish:%d", fish.EntityId)
		if d <= fishMeleeReach {
			if mem.LastTargetKey == key && mem.LastActionTick > 0 && frame.WorldTick >= mem.LastActionTick &&
				frame.WorldTick-mem.LastActionTick < forageActionCooldownTicks {
				return opt, true
			}
			mem.LastTargetKey, mem.LastActionTick = key, frame.WorldTick
			opt.intent = &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Attack{
				Attack: &civquestv1.AttackIntent{TargetEntityId: fish.EntityId}}}
			return opt, true
		}
		sx, sz := standOff(fish.Position.X, fish.Position.Z, fishMeleeStandoff)
		opt.intent = navigate("nav_"+key, sx, pos.Y, sz)
		return opt, true
	}
	return fishingOption{}, false
}

func planksCarried(inv []*civquestv1.ItemStackProto) int32 {
	var n int32
	for _, it := range inv {
		if it != nil && it.Count > 0 && strings.HasSuffix(bareID(it.ItemId), "_planks") {
			n += it.Count
		}
	}
	return n
}

// Land food option utilities (same units as the fishing yields; DESIGN BUDGET cq-390.4).
const (
	forageAnimalYield = 6.0
	foragePlantYield  = 4.0
	forageLeafYield   = 1.0
)
